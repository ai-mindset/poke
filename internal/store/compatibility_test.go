package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ai-mindset/poke/internal/model"
)

func databaseSnapshot(t *testing.T, db *sql.DB) map[string][][]any {
	t.Helper()
	result := map[string][][]any{}
	queries := map[string]string{
		"schema": "SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY type, name",
	}
	for _, table := range []string{"accounts", "issues_cache", "notes", "tasks", "sync_state", "sqlite_sequence"} {
		queries[table] = "SELECT * FROM " + table + " ORDER BY rowid"
	}
	for name, query := range queries {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			destinations := make([]any, len(columns))
			for index := range values {
				destinations[index] = &values[index]
			}
			if err := rows.Scan(destinations...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			result[name] = append(result[name], values)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestPreGoDatabaseCompatibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "poke.db")
	snapshot, err := os.ReadFile("../testdata/pre_go.sql")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	// Seed with the frozen original schema, without invoking the Go store's
	// schema creation or data-writing code.
	if _, err := raw.Exec(string(snapshot)); err != nil {
		t.Fatal(err)
	}
	before := databaseSnapshot(t, raw)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after := databaseSnapshot(t, raw)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("opening with Go altered the original schema or data")
	}
	user, err := s.LastAccount()
	if err != nil || user == nil || user.NodeID != "USER_1" {
		t.Fatalf("account: %+v, %v", user, err)
	}
	issue, err := s.FindIssue("USER_1", model.Reference{Repository: "OCTO/REPO", Number: 1})
	if err != nil || issue == nil || issue.Note == nil || *issue.Note != "Private 日本語" {
		t.Fatalf("original issue: %+v, %v", issue, err)
	}
	if err := s.SetNote("USER_1", issue.NodeID, "Updated in Go"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameTask("USER_1", 1, "Renamed in Go"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetCompleted("USER_1", 1, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveTask("USER_1", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTask("USER_1", 2); err != nil {
		t.Fatal(err)
	}
	added, err := s.AddTask("USER_1", issue.NodeID, "Added in Go")
	if err != nil || added.ID != 4 {
		t.Fatalf("autoincrement: %+v, %v", added, err)
	}
	updated := issue.Issue
	updated.Title = "Updated title"
	if err := s.Sync(context.Background(), *user, []model.Issue{updated}, SyncOptions{Complete: false}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Verify writes through independent SQL queries, including retained
	// timestamps, stale annotations, account isolation, and task IDs.
	var note, title, createdAt string
	var completed, position int
	if err := raw.QueryRow("SELECT body FROM notes WHERE account_node_id = 'USER_1' AND issue_node_id = 'ISSUE_1'").Scan(&note); err != nil || note != "Updated in Go" {
		t.Fatalf("note=%q: %v", note, err)
	}
	if err := raw.QueryRow("SELECT title, completed, position, created_at FROM tasks WHERE id = 1").Scan(&title, &completed, &position, &createdAt); err != nil || title != "Renamed in Go" || completed != 0 || position != 1 || createdAt != "2026-08-08T10:00:00.000Z" {
		t.Fatalf("task: %q %d %d %q: %v", title, completed, position, createdAt, err)
	}
	if err := raw.QueryRow("SELECT title, position FROM tasks WHERE id = 4").Scan(&title, &position); err != nil || title != "Added in Go" || position != 2 {
		t.Fatalf("new task: %q %d: %v", title, position, err)
	}
	if err := raw.QueryRow("SELECT body FROM notes WHERE account_node_id = 'USER_2' AND issue_node_id = 'ISSUE_1'").Scan(&note); err != nil || note != "Other account" {
		t.Fatalf("other account note: %q: %v", note, err)
	}
	if err := raw.QueryRow("SELECT title FROM issues_cache WHERE account_node_id = 'USER_1' AND node_id = 'ISSUE_1'").Scan(&title); err != nil || title != "Updated title" {
		t.Fatalf("issue title: %q: %v", title, err)
	}
	var stale, openTasks int
	if err := raw.QueryRow(`SELECT i.stale, n.body,
		(SELECT COUNT(*) FROM tasks WHERE account_node_id = i.account_node_id AND issue_node_id = i.node_id AND completed = 0)
		FROM issues_cache i JOIN notes n ON n.account_node_id = i.account_node_id AND n.issue_node_id = i.node_id
		WHERE i.account_node_id = 'USER_1' AND i.node_id = 'ISSUE_3'`).Scan(&stale, &note, &openTasks); err != nil || stale != 1 || note != "Retained stale context" || openTasks != 1 {
		t.Fatalf("stale work: %d %q %d: %v", stale, note, openTasks, err)
	}
}
