package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/ai-mindset/poke/internal/model"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := ":memory:"
	if path == "" {
		// SQLite treats an empty filename as an unnamed temporary database.
		dsn = "file:"
	} else if path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		// Encode paths so characters such as '?' never become driver options.
		u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
		if !strings.HasPrefix(u.Path, "/") {
			u.Path = "/" + u.Path
		}
		dsn = u.String()
	}
	dsn += "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func timestamp() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

type SyncOptions struct {
	Complete bool
	Org      string
	SyncedAt string
}

func (s *Store) Sync(ctx context.Context, account model.Account, issues []model.Issue, options SyncOptions) error {
	now := options.SyncedAt
	if now == "" {
		now = timestamp()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO accounts (node_id, host, login, synced_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
		  host = excluded.host, login = excluded.login, synced_at = excluded.synced_at`,
		account.NodeID, account.Host, account.Login, now); err != nil {
		return err
	}
	if options.Complete {
		query := "UPDATE issues_cache SET stale = 1 WHERE account_node_id = ?"
		args := []any{account.NodeID}
		if options.Org != "" {
			query += " AND lower(substr(repository, 1, instr(repository, '/') - 1)) = lower(?)"
			args = append(args, options.Org)
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	upsert, err := tx.PrepareContext(ctx, `
		INSERT INTO issues_cache (
		  account_node_id, node_id, number, title, url, repository, state,
		  assignees, updated_at, last_seen_at, stale
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(account_node_id, node_id) DO UPDATE SET
		  number = excluded.number, title = excluded.title, url = excluded.url,
		  repository = excluded.repository, state = excluded.state,
		  assignees = excluded.assignees, updated_at = excluded.updated_at,
		  last_seen_at = excluded.last_seen_at, stale = 0`)
	if err != nil {
		return err
	}
	defer upsert.Close()
	for _, issue := range issues {
		assignees := issue.Assignees
		if assignees == nil {
			assignees = []string{}
		}
		encoded, err := json.Marshal(assignees)
		if err != nil {
			return err
		}
		if _, err := upsert.ExecContext(ctx, account.NodeID, issue.NodeID, issue.Number,
			issue.Title, issue.URL, issue.Repository, issue.State, string(encoded), issue.UpdatedAt, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sync_state (account_node_id, key, value, updated_at)
		VALUES (?, 'issues:last_sync', ?, ?)
		ON CONFLICT(account_node_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		account.NodeID, now, now); err != nil {
		return err
	}
	return tx.Commit()
}

const issueSelect = `
  SELECT i.account_node_id, i.node_id, i.number, i.title, i.url, i.repository,
    i.assignees, i.state, i.updated_at, i.last_seen_at, i.stale, n.body,
    COUNT(CASE WHEN t.completed = 0 THEN 1 END),
    COUNT(CASE WHEN t.completed = 1 THEN 1 END)
  FROM issues_cache i
  LEFT JOIN notes n ON n.account_node_id = i.account_node_id AND n.issue_node_id = i.node_id
  LEFT JOIN tasks t ON t.account_node_id = i.account_node_id AND t.issue_node_id = i.node_id
`

type scanner interface{ Scan(...any) error }

func scanIssue(row scanner) (model.CachedIssue, error) {
	var issue model.CachedIssue
	var assignees string
	err := row.Scan(&issue.AccountNodeID, &issue.NodeID, &issue.Number, &issue.Title,
		&issue.URL, &issue.Repository, &assignees, &issue.State, &issue.UpdatedAt,
		&issue.LastSeenAt, &issue.Stale, &issue.Note, &issue.OpenTaskCount, &issue.CompletedTaskCount)
	if err == nil {
		err = json.Unmarshal([]byte(assignees), &issue.Assignees)
	}
	return issue, err
}

func (s *Store) queryIssues(query string, args ...any) ([]model.CachedIssue, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	issues := []model.CachedIssue{}
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

func (s *Store) ListIssues(account string, includeClosed, includeStale bool) ([]model.CachedIssue, error) {
	query := issueSelect + " WHERE i.account_node_id = ?"
	if !includeClosed {
		query += " AND i.state = 'open'"
	}
	if !includeStale {
		query += " AND i.stale = 0"
	}
	query += " GROUP BY i.account_node_id, i.node_id ORDER BY i.stale ASC, i.updated_at DESC"
	return s.queryIssues(query, account)
}

func (s *Store) ListNext(account string) ([]model.CachedIssue, error) {
	return s.queryIssues(issueSelect+`
		WHERE i.account_node_id = ? AND i.stale = 0 AND i.state = 'open'
		AND EXISTS (SELECT 1 FROM tasks next_task
		  WHERE next_task.account_node_id = i.account_node_id
		    AND next_task.issue_node_id = i.node_id AND next_task.completed = 0)
		GROUP BY i.account_node_id, i.node_id
		ORDER BY MIN(CASE WHEN t.completed = 0 THEN t.position END), i.updated_at DESC`, account)
}

func (s *Store) FindIssue(account string, ref model.Reference) (*model.CachedIssue, error) {
	issue, err := scanIssue(s.db.QueryRow(issueSelect+`
		WHERE i.account_node_id = ? AND lower(i.repository) = lower(?) AND i.number = ?
		GROUP BY i.account_node_id, i.node_id`, account, ref.Repository, ref.Number))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

func (s *Store) LastAccount() (*model.Account, error) {
	var account model.Account
	err := s.db.QueryRow("SELECT host, login, node_id FROM accounts ORDER BY synced_at DESC LIMIT 1").Scan(
		&account.Host, &account.Login, &account.NodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &account, nil
}

const taskSelect = "SELECT id, account_node_id, issue_node_id, title, completed, position, created_at, updated_at FROM tasks"

func scanTask(row scanner) (model.Task, error) {
	var task model.Task
	err := row.Scan(&task.ID, &task.AccountNodeID, &task.IssueNodeID, &task.Title,
		&task.Completed, &task.Position, &task.CreatedAt, &task.UpdatedAt)
	return task, err
}

func (s *Store) Tasks(account, issue string) ([]model.Task, error) {
	rows, err := s.db.Query(taskSelect+" WHERE account_node_id = ? AND issue_node_id = ? ORDER BY position, id", account, issue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []model.Task{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func taskNotFound(id int64) error { return fmt.Errorf("Task %d was not found.", id) }

func (s *Store) Task(account string, id int64) (model.Task, error) {
	task, err := scanTask(s.db.QueryRow(taskSelect+" WHERE account_node_id = ? AND id = ?", account, id))
	if errors.Is(err, sql.ErrNoRows) {
		return task, taskNotFound(id)
	}
	return task, err
}

func (s *Store) AddTask(account, issue, title string) (model.Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return model.Task{}, errors.New("Task title cannot be empty.")
	}
	now := timestamp()
	result, err := s.db.Exec(`
		INSERT INTO tasks (account_node_id, issue_node_id, title, completed, position, created_at, updated_at)
		SELECT ?, ?, ?, 0, COALESCE(MAX(position), 0) + 1, ?, ?
		FROM tasks WHERE account_node_id = ? AND issue_node_id = ?`, account, issue, title, now, now, account, issue)
	if err != nil {
		return model.Task{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.Task{}, err
	}
	return s.Task(account, id)
}

func (s *Store) updateTask(account string, id int64, query string, args ...any) (model.Task, error) {
	args = append(args, account, id)
	result, err := s.db.Exec(query+" WHERE account_node_id = ? AND id = ?", args...)
	if err != nil {
		return model.Task{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return model.Task{}, err
	}
	if count == 0 {
		return model.Task{}, taskNotFound(id)
	}
	return s.Task(account, id)
}

func (s *Store) SetCompleted(account string, id int64, completed bool) (model.Task, error) {
	return s.updateTask(account, id, "UPDATE tasks SET completed = ?, updated_at = ?", completed, timestamp())
}

func (s *Store) RenameTask(account string, id int64, title string) (model.Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return model.Task{}, errors.New("Task title cannot be empty.")
	}
	return s.updateTask(account, id, "UPDATE tasks SET title = ?, updated_at = ?", title, timestamp())
}

func (s *Store) MoveTask(account string, id, position int64) (model.Task, error) {
	if position < 1 {
		return model.Task{}, errors.New("Task position must be a positive integer.")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return model.Task{}, err
	}
	defer tx.Rollback()
	task, err := scanTask(tx.QueryRow(taskSelect+" WHERE account_node_id = ? AND id = ?", account, id))
	if errors.Is(err, sql.ErrNoRows) {
		return task, taskNotFound(id)
	}
	if err != nil {
		return task, err
	}
	var count int64
	if err := tx.QueryRow("SELECT COUNT(*) FROM tasks WHERE account_node_id = ? AND issue_node_id = ?", account, task.IssueNodeID).Scan(&count); err != nil {
		return task, err
	}
	target := min(position, count)
	if target < task.Position {
		_, err = tx.Exec(`UPDATE tasks SET position = position + 1
		  WHERE account_node_id = ? AND issue_node_id = ? AND position >= ? AND position < ?`, account, task.IssueNodeID, target, task.Position)
	} else if target > task.Position {
		_, err = tx.Exec(`UPDATE tasks SET position = position - 1
		  WHERE account_node_id = ? AND issue_node_id = ? AND position > ? AND position <= ?`, account, task.IssueNodeID, task.Position, target)
	}
	if err != nil {
		return task, err
	}
	if _, err := tx.Exec("UPDATE tasks SET position = ? WHERE account_node_id = ? AND id = ?", target, account, id); err != nil {
		return task, err
	}
	if err := tx.Commit(); err != nil {
		return task, err
	}
	task.Position = target
	return task, nil
}

func (s *Store) DeleteTask(account string, id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	task, err := scanTask(tx.QueryRow(taskSelect+" WHERE account_node_id = ? AND id = ?", account, id))
	if errors.Is(err, sql.ErrNoRows) {
		return taskNotFound(id)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM tasks WHERE account_node_id = ? AND id = ?", account, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET position = position - 1
	  WHERE account_node_id = ? AND issue_node_id = ? AND position > ?`, account, task.IssueNodeID, task.Position); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetNote(account, issue, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return errors.New("Note cannot be empty.")
	}
	now := timestamp()
	_, err := s.db.Exec(`INSERT INTO notes (account_node_id, issue_node_id, body, created_at, updated_at)
	  VALUES (?, ?, ?, ?, ?) ON CONFLICT(account_node_id, issue_node_id)
	  DO UPDATE SET body = excluded.body, updated_at = excluded.updated_at`, account, issue, body, now, now)
	return err
}
