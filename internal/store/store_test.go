package store

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/ai-mindset/poke/internal/model"
)

var account = model.Account{Host: "github.com", Login: "octocat", NodeID: "USER_1"}

func TestTemporaryDatabases(t *testing.T) {
	for _, path := range []string{"", ":memory:"} {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("Open(%q): %v", path, err)
		}
		syncTest(t, s, account, []model.Issue{fixtureIssue(1, "Alpha")}, true, "")
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		last, err := s.LastAccount()
		if err != nil || last != nil {
			t.Fatalf("temporary database persisted: %+v, %v", last, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func fixtureIssue(id int64, owner string) model.Issue {
	return model.Issue{NodeID: owner, Number: id, Title: "Issue", URL: "https://github.com/" + owner + "/repo/issues/1",
		Repository: owner + "/repo", State: "open", UpdatedAt: "2026-08-08T10:00:00Z", Assignees: []string{"octocat"}}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	name := "poke ?# ü.db"
	if runtime.GOOS == "windows" {
		name = "poke # ü.db"
	}
	s, err := Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func syncTest(t *testing.T, s *Store, user model.Account, issues []model.Issue, complete bool, org string) {
	t.Helper()
	if err := s.Sync(context.Background(), user, issues, SyncOptions{Complete: complete, Org: org}); err != nil {
		t.Fatal(err)
	}
}

func TestSyncScopeAndAnnotationRetention(t *testing.T) {
	s := openTestStore(t)
	a, b := fixtureIssue(1, "Alpha"), fixtureIssue(2, "Beta")
	syncTest(t, s, account, []model.Issue{a, b}, true, "")
	if _, err := s.AddTask(account.NodeID, a.NodeID, "Investigate"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNote(account.NodeID, a.NodeID, "Private context 日本語"); err != nil {
		t.Fatal(err)
	}
	syncTest(t, s, account, nil, true, "aLPHa")
	issues, err := s.ListIssues(account.NodeID, true, true)
	if err != nil || len(issues) != 2 {
		t.Fatalf("issues: %+v, %v", issues, err)
	}
	for _, issue := range issues {
		if issue.NodeID == a.NodeID {
			if !issue.Stale || issue.Note == nil || *issue.Note != "Private context 日本語" || issue.OpenTaskCount != 1 {
				t.Fatalf("lost annotations: %+v", issue)
			}
		} else if issue.Stale {
			t.Fatal("organization sync marked another organization stale")
		}
	}
	syncTest(t, s, account, nil, false, "Beta")
	current, _ := s.ListIssues(account.NodeID, false, false)
	if len(current) != 1 || current[0].NodeID != b.NodeID {
		t.Fatalf("partial sync changed staleness: %+v", current)
	}
	syncTest(t, s, account, nil, true, "")
	current, _ = s.ListIssues(account.NodeID, true, false)
	if len(current) != 0 {
		t.Fatal("account-wide sync must mark all missing issues stale")
	}
	// Reappearance and closure refresh metadata while retaining annotations.
	a.State = "closed"
	syncTest(t, s, account, []model.Issue{a}, true, "Alpha")
	closed, err := s.FindIssue(account.NodeID, model.Reference{Repository: "ALPHA/REPO", Number: 1})
	if err != nil || closed.Stale || closed.State != "closed" || closed.Note == nil || closed.OpenTaskCount != 1 {
		t.Fatalf("closed issue: %+v, %v", closed, err)
	}
}

func TestTaskLifecycleAndNext(t *testing.T) {
	s := openTestStore(t)
	issue := fixtureIssue(1, "Alpha")
	closed := fixtureIssue(2, "Beta")
	closed.State = "closed"
	syncTest(t, s, account, []model.Issue{issue, closed}, true, "")
	var ids []int64
	for _, title := range []string{"First", "Second", "Third"} {
		task, err := s.AddTask(account.NodeID, issue.NodeID, title)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, task.ID)
	}
	if _, err := s.SetCompleted(account.NodeID, ids[0], true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameTask(account.NodeID, ids[1], " Renamed "); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveTask(account.NodeID, ids[2], 1); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTask(account.NodeID, ids[0]); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.Tasks(account.NodeID, issue.NodeID)
	if err != nil || len(tasks) != 2 || tasks[0].Title != "Third" || tasks[1].Title != "Renamed" || tasks[0].Position != 1 || tasks[1].Position != 2 {
		t.Fatalf("tasks: %+v, %v", tasks, err)
	}
	before := tasks[0].UpdatedAt
	moved, err := s.MoveTask(account.NodeID, ids[2], 99)
	if err != nil || moved.Position != 2 || moved.UpdatedAt != before {
		t.Fatalf("move: %+v, %v", moved, err)
	}
	if _, err := s.SetCompleted(account.NodeID, ids[1], true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetCompleted(account.NodeID, ids[1], false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTask(account.NodeID, closed.NodeID, "Closed action"); err != nil {
		t.Fatal(err)
	}
	next, err := s.ListNext(account.NodeID)
	if err != nil || len(next) != 1 || next[0].NodeID != issue.NodeID {
		t.Fatalf("next: %+v, %v", next, err)
	}
	for _, id := range ids[1:] {
		if _, err := s.SetCompleted(account.NodeID, id, true); err != nil {
			t.Fatal(err)
		}
	}
	next, _ = s.ListNext(account.NodeID)
	if len(next) != 0 {
		t.Fatal("completed tasks appeared in next")
	}
	if _, err := s.AddTask(account.NodeID, issue.NodeID, " "); err == nil {
		t.Fatal("accepted empty task")
	}
	if err := s.SetNote(account.NodeID, issue.NodeID, " "); err == nil {
		t.Fatal("accepted empty note")
	}
	if _, err := s.MoveTask(account.NodeID, ids[1], 0); err == nil {
		t.Fatal("accepted zero position")
	}
}

func TestAccountsAndAtomicSync(t *testing.T) {
	s := openTestStore(t)
	issue := fixtureIssue(1, "Alpha")
	syncTest(t, s, account, []model.Issue{issue}, true, "")
	task, err := s.AddTask(account.NodeID, issue.NodeID, "Private")
	if err != nil {
		t.Fatal(err)
	}
	other := model.Account{Host: "github.com", Login: "other", NodeID: "USER_2"}
	syncTest(t, s, other, []model.Issue{issue}, true, "")
	if _, err := s.Task(other.NodeID, task.ID); err == nil {
		t.Fatal("cross-account task access")
	}
	if err := s.DeleteTask(other.NodeID, task.ID); err == nil {
		t.Fatal("cross-account task deletion")
	}
	if _, err := s.RenameTask(other.NodeID, task.ID, "stolen"); err == nil {
		t.Fatal("cross-account rename")
	}
	if _, err := s.SetCompleted(other.NodeID, task.ID, true); err == nil {
		t.Fatal("cross-account completion")
	}
	if _, err := s.MoveTask(other.NodeID, task.ID, 1); err == nil {
		t.Fatal("cross-account move")
	}
	syncTest(t, s, other, nil, true, "")
	before, _ := s.ListIssues(account.NodeID, true, true)
	broken := fixtureIssue(2, "Broken")
	broken.State = "invalid"
	if err := s.Sync(context.Background(), account, []model.Issue{broken}, SyncOptions{Complete: true}); err == nil {
		t.Fatal("invalid sync succeeded")
	}
	after, _ := s.ListIssues(account.NodeID, true, true)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed sync changed cache")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Sync(ctx, account, nil, SyncOptions{Complete: true}); err == nil {
		t.Fatal("cancelled sync succeeded")
	}
	after, _ = s.ListIssues(account.NodeID, true, true)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("cancelled sync changed cache")
	}
	var enabled int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("foreign keys: %d, %v", enabled, err)
	}
	if _, err := s.AddTask(account.NodeID, "missing", "orphan"); err == nil {
		t.Fatal("foreign key not enforced")
	}
}
