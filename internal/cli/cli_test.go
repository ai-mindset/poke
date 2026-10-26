package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-mindset/poke/internal/model"
	"github.com/ai-mindset/poke/internal/store"
)

var testAccount = model.Account{Host: "api.github.com", Login: "octocat", NodeID: "USER_1"}

func testIssue() model.Issue {
	return model.Issue{NodeID: "ISSUE_1", Number: 42, Title: "Test the CLI", URL: "https://github.com/octo/repo/issues/42",
		Repository: "octo/repo", State: "open", UpdatedAt: "2026-08-08T10:00:00Z", Assignees: []string{"octocat"}}
}

func seed(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "poke.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Sync(context.Background(), testAccount, []model.Issue{testIssue()}, store.SyncOptions{Complete: true}); err != nil {
		t.Fatal(err)
	}
	return path
}

func offlineCLI(t *testing.T, out, errOut *bytes.Buffer) *CLI {
	t.Helper()
	c := New(out, errOut)
	c.newClient = func(string) githubClient { t.Fatal("offline command attempted network access"); return nil }
	c.getenv = func(key string) string { t.Fatalf("unexpected environment read: %s", key); return "" }
	return c
}

func TestOfflineCommands(t *testing.T) {
	path := seed(t)
	var out, errOut bytes.Buffer
	c := offlineCLI(t, &out, &errOut)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"issues"}, "octo/repo#42 Test the CLI [open]\n"},
		{[]string{"next"}, "No open local tasks.\n"},
		{[]string{"note", "show", "octo/repo#42"}, "No note saved.\n"},
		{[]string{"show", "https://github.com/octo/repo/issues/42#issuecomment-1"}, "octo/repo#42 Test the CLI\nopen | https://github.com/octo/repo/issues/42\n\nNote:\n(none)\n\nTasks:\n(none)\n"},
		{[]string{"note", "set", "OCTO/REPO#42", " Context 日本語 "}, "Saved note for octo/repo#42.\n"},
		{[]string{"note", "show", "octo/repo#42"}, "Context 日本語\n"},
		{[]string{"task", "add", "octo/repo#42", "First", "action"}, "Added task 1 to octo/repo#42.\n"},
		{[]string{"task", "add", "octo/repo#42", "Second"}, "Added task 2 to octo/repo#42.\n"},
		{[]string{"issues", "--all", "--stale"}, "octo/repo#42 Test the CLI [open | 2 next | note]\n"},
		{[]string{"task", "done", "1"}, "Completed task 1.\n"},
		{[]string{"next"}, "2: Second (octo/repo#42)\n"},
		{[]string{"task", "reopen", "1"}, "Reopened task 1.\n"},
		{[]string{"task", "rename", "1", " Renamed "}, "Renamed task 1.\n"},
		{[]string{"task", "move", "2", "1"}, "Moved task 2 to position 1.\n"},
		{[]string{"next"}, "2: Second (octo/repo#42)\n"},
		{[]string{"task", "done", "2"}, "Completed task 2.\n"},
		{[]string{"show", "octo/repo#42"}, "octo/repo#42 Test the CLI\nopen | https://github.com/octo/repo/issues/42\n\nNote:\nContext 日本語\n\nTasks:\n[x] 2: Second\n[ ] 1: Renamed\n"},
		{[]string{"task", "delete", "1"}, "Deleted task 1.\n"},
		{[]string{"next"}, "No open local tasks.\n"},
	} {
		out.Reset()
		errOut.Reset()
		args := append(append([]string{}, tc.args...), "--db", path)
		if err := c.Run(context.Background(), args); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if out.String() != tc.want || errOut.Len() != 0 {
			t.Errorf("%v: stdout=%q stderr=%q, want %q", tc.args, &out, &errOut, tc.want)
		}
	}
}

func TestErrors(t *testing.T) {
	path := seed(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"show"}, "Missing command argument. Run poke help."},
		{[]string{"show", "#42"}, "Invalid issue reference \"#42\". Use owner/repo#123 or an issue URL."},
		{[]string{"show", "octo/repo#99"}, "octo/repo#99 is not in the local cache. Run poke sync first."},
		{[]string{"task"}, "Missing command argument. Run poke help."},
		{[]string{"task", "wat"}, "Unknown task action \"wat\". Run poke help."},
		{[]string{"task", "add", "octo/repo#42", " "}, "Missing task title."},
		{[]string{"task", "done", "0"}, "task ID must be a positive integer."},
		{[]string{"task", "move", "1", "-1"}, "position must be a positive integer."},
		{[]string{"task", "done", "999"}, "Task 999 was not found."},
		{[]string{"note", "set", "octo/repo#42"}, "Missing note."},
		{[]string{"note", "wat", "octo/repo#42"}, "Unknown note action \"wat\". Run poke help."},
		{[]string{"--unknown"}, "Unknown command \"--unknown\". Run poke help."},
		{[]string{"bogus", "extra"}, "Unknown command \"bogus\". Run poke help."},
	} {
		var out, errOut bytes.Buffer
		c := offlineCLI(t, &out, &errOut)
		err := c.Run(context.Background(), append([]string{"--db", path}, tc.args...))
		if err == nil || err.Error() != tc.want {
			t.Errorf("%v: %v, want %q", tc.args, err, tc.want)
		}
	}
	for _, flag := range []string{"--db", "--org"} {
		if _, err := parseOptions([]string{flag}); err == nil || err.Error() != flag+" requires a value." {
			t.Errorf("%s: %v", flag, err)
		}
	}
}

func TestEmptyCache(t *testing.T) {
	for _, args := range [][]string{{"--db=", "issues"}, {"--db=:memory:", "next"}} {
		var out, errOut bytes.Buffer
		c := offlineCLI(t, &out, &errOut)
		if err := c.Run(context.Background(), args); err == nil || err.Error() != "No synced account. Run poke sync first." {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestHelpAndOptions(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}, {"task", "--help"}} {
		var out, errOut bytes.Buffer
		c := offlineCLI(t, &out, &errOut)
		path := filepath.Join(t.TempDir(), "never-created", "poke.db")
		if err := c.Run(context.Background(), append(args, "--db", path)); err != nil {
			t.Fatal(err)
		}
		if out.String() != help+"\n" {
			t.Fatal("wrong help")
		}
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Fatal("help created database directory")
		}
	}
	for _, args := range [][]string{{"--db=x", "issues", "--org=A"}, {"issues", "--db", "x", "--org", "A"}, {"--", "--db=x", "issues", "--org=A"}} {
		got, err := parseOptions(args)
		if err != nil || got.dbPath == nil || *got.dbPath != "x" || got.org != "A" || len(got.args) != 1 || got.args[0] != "issues" {
			t.Errorf("%v: %+v, %v", args, got, err)
		}
	}
	for _, value := range []string{"1", "1.0", "1e0", "0x1", "0b1", "0o1", " 1 "} {
		if n, err := positiveInteger(value, "ID"); err != nil || n != 1 {
			t.Errorf("%q: %d %v", value, n, err)
		}
	}
}

type fakeGitHub struct {
	result model.SyncResult
	err    error
	org    string
}

func (f *fakeGitHub) Account(context.Context) (model.Account, error) { return testAccount, nil }
func (f *fakeGitHub) AssignedIssues(ctx context.Context, org string) (model.SyncResult, error) {
	f.org = org
	if err := ctx.Err(); err != nil {
		return model.SyncResult{}, err
	}
	return f.result, f.err
}

func TestSyncCommandsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		args []string
		org  string
		list bool
	}{
		{nil, "", true}, {[]string{"sync"}, "", false}, {[]string{"sync", "--org=Acme"}, "Acme", false}, {[]string{"Acme"}, "Acme", true},
	} {
		var out, errOut bytes.Buffer
		c := New(&out, &errOut)
		c.getenv = func(key string) string {
			if key == "GITHUB_TOKEN" {
				return "token"
			}
			t.Fatalf("unexpected read: %s", key)
			return ""
		}
		fake := &fakeGitHub{result: model.SyncResult{Issues: []model.Issue{testIssue()}, Complete: false}}
		c.newClient = func(token string) githubClient {
			if token != "token" {
				t.Fatal("wrong token")
			}
			return fake
		}
		path := filepath.Join(t.TempDir(), "poke.db")
		if err := c.Run(context.Background(), append([]string{"--db", path}, tc.args...)); err != nil {
			t.Fatal(err)
		}
		if fake.org != tc.org || strings.Contains(out.String(), "[open]") != tc.list || !strings.Contains(errOut.String(), "partial result") {
			t.Fatalf("sync: %q %q org=%s", &out, &errOut, fake.org)
		}
	}
	path := seed(t)
	var out, errOut bytes.Buffer
	c := New(&out, &errOut)
	c.getenv = func(string) string { return "" }
	c.newClient = func(string) githubClient { t.Fatal("missing token constructed client"); return nil }
	if err := c.Run(context.Background(), []string{"--db", path, "sync"}); err == nil || !strings.Contains(err.Error(), "Set GITHUB_TOKEN") {
		t.Fatalf("missing auth: %v", err)
	}
	c.getenv = func(string) string { return "token" }
	c.newClient = func(string) githubClient { return &fakeGitHub{err: errors.New("API failure")} }
	if err := c.Run(context.Background(), []string{"--db", path, "sync"}); err == nil {
		t.Fatal("failure ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Run(ctx, []string{"--db", path, "sync"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	issues, err := s.ListIssues(testAccount.NodeID, false, false)
	if err != nil || len(issues) != 1 {
		t.Fatal("failed sync changed cache")
	}
}

func TestDatabasePaths(t *testing.T) {
	for _, tc := range []struct {
		os        string
		env       map[string]string
		want      string
		wantError bool
	}{
		{"linux", map[string]string{"HOME": "/home/test"}, filepath.Join("/home/test", ".local", "share", "poke", "poke.db"), false},
		{"darwin", map[string]string{"HOME": "/home/test", "XDG_DATA_HOME": "/data"}, filepath.Join("/data", "poke", "poke.db"), false},
		{"windows", map[string]string{"LOCALAPPDATA": "local", "USERPROFILE": "user"}, filepath.Join("local", "poke", "poke.db"), false},
		{"windows", map[string]string{"USERPROFILE": "user"}, filepath.Join("user", "poke", "poke.db"), false},
		{"linux", map[string]string{"POKE_DB_PATH": "override"}, "override", false},
		{"linux", nil, "", true}, {"windows", nil, "", true},
	} {
		get := func(key string) string { return tc.env[key] }
		lookup := func(key string) (string, bool) { value, ok := tc.env[key]; return value, ok }
		got, err := defaultDatabasePath(tc.os, get, lookup)
		if got != tc.want || (err != nil) != tc.wantError {
			t.Errorf("%s %v: %q, %v", tc.os, tc.env, got, err)
		}
	}
}

func TestReleaseExecutable(t *testing.T) {
	binary := os.Getenv("POKE_TEST_BINARY")
	if binary == "" {
		t.Skip("set POKE_TEST_BINARY to test a packaged executable")
	}
	path := seed(t)
	for _, tc := range []struct {
		args    []string
		want    string
		failure bool
	}{
		{[]string{"--help"}, help + "\n", false},
		{[]string{"--db", path, "issues"}, "octo/repo#42 Test the CLI [open]\n", false},
		{[]string{"--db", path, "task", "add", "octo/repo#42", "Packaged action"}, "Added task 1 to octo/repo#42.\n", false},
		{[]string{"--db", path, "next"}, "1: Packaged action (octo/repo#42)\n", false},
		{[]string{"--db", path, "show", "#42"}, "Error: Invalid issue reference \"#42\". Use owner/repo#123 or an issue URL.\n", true},
	} {
		command := exec.Command(binary, tc.args...)
		output, err := command.CombinedOutput()
		if string(output) != tc.want || (err != nil) != tc.failure {
			t.Errorf("%v: %q, %v", tc.args, output, err)
		}
		if tc.failure && command.ProcessState.ExitCode() != 1 {
			t.Errorf("wrong error exit code: %v", command.ProcessState)
		}
	}
}
