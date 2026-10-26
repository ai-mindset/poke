package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-mindset/poke/internal/model"
)

type fakeThreadClient struct {
	result model.Thread
	err    error
	ref    model.Reference
}

func (f *fakeThreadClient) Thread(ctx context.Context, ref model.Reference) (model.Thread, error) {
	f.ref = ref
	if err := ctx.Err(); err != nil {
		return model.Thread{}, err
	}
	return f.result, f.err
}

func transcriptFixture() model.Thread {
	return model.Thread{Scope: model.ThreadScope, Issue: model.ThreadIssue{NodeID: "ISSUE_42", Number: 42, Title: "A discussion", Body: "Original **context**", URL: "https://github.com/octo/repo/issues/42", Author: "alice", State: "open", CreatedAt: "2026-08-08T10:00:00Z", UpdatedAt: "2026-08-09T10:00:00Z", CommentCount: 1},
		Comments: []model.Comment{{ID: 7, Body: "An attributed response", URL: "https://github.com/octo/repo/issues/42#issuecomment-7", CreatedAt: "2026-08-08T11:00:00Z", UpdatedAt: "2026-08-09T11:00:00Z"}}, FetchedAt: "2026-10-24T12:00:00Z", Complete: true, Warnings: []string{}}
}

func TestThreadCommandWithoutDatabase(t *testing.T) {
	for _, tc := range []struct {
		name                string
		flags               []string
		jsonOutput          bool
		envToken, wantToken string
	}{
		{"token transcript", nil, false, "token", "token"},
		{"token JSON", []string{"--json"}, true, "token", "token"},
		{"CLI fallback", []string{"--json"}, true, "", ""},
		{"explicit CLI", []string{"--gh"}, false, "token", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			c := New(&out, &errOut)
			c.getenv = func(key string) string {
				if key != "GITHUB_TOKEN" {
					t.Fatalf("unexpected environment access: %s", key)
				}
				if strings.Contains(tc.name, "explicit") {
					t.Fatal("explicit gh unexpectedly consulted token")
				}
				return tc.envToken
			}
			c.lookupEnv = func(string) (string, bool) { t.Fatal("thread accessed database-path configuration"); return "", false }
			fake := &fakeThreadClient{result: transcriptFixture()}
			c.newThreadClient = func(token string) threadClient {
				if token != tc.wantToken {
					t.Errorf("token=%q, want %q", token, tc.wantToken)
				}
				return fake
			}
			directory := filepath.Join(t.TempDir(), "must-not-exist")
			args := []string{"--db", filepath.Join(directory, "poke.db"), "thread", "https://github.com/octo/repo/issues/42#issuecomment-7"}
			if err := c.Run(context.Background(), append(args, tc.flags...)); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("thread created a database directory")
			}
			if fake.ref.Repository != "octo/repo" || fake.ref.Number != 42 || errOut.Len() != 0 {
				t.Fatalf("wrong request: %+v, stderr=%q", fake.ref, &errOut)
			}
			if tc.jsonOutput {
				var got model.Thread
				if err := json.Unmarshal(out.Bytes(), &got); err != nil || !got.Complete || got.Comments[0].ID != 7 || got.Issue.Body != "Original **context**" {
					t.Fatalf("JSON: %s, %v", &out, err)
				}
			} else {
				for _, want := range []string{"Original **context**", "@alice", "An attributed response", "#issuecomment-7", "deleted or unavailable user", "2026-08-09T11:00:00Z", "Timeline events"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("transcript missing %q", want)
					}
				}
			}
		})
	}
}

func TestThreadErrorsAndCoverage(t *testing.T) {
	var out, errOut bytes.Buffer
	c := New(&out, &errOut)
	c.getenv = func(string) string { return "token" }
	fake := &fakeThreadClient{result: transcriptFixture()}
	c.newThreadClient = func(string) threadClient { return fake }
	fake.result.Complete = false
	fake.result.Warnings = []string{"The issue changed during retrieval."}
	if err := c.Run(context.Background(), []string{"thread", "--json", "octo/repo#42"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"complete": false`) || !strings.Contains(errOut.String(), "Warning: The issue changed") {
		t.Fatalf("partial coverage hidden: %q %q", &out, &errOut)
	}
	fake.err = errors.New("A later page failed")
	out.Reset()
	errOut.Reset()
	if err := c.Run(context.Background(), []string{"thread", "octo/repo#42"}); err == nil || out.Len() != 0 {
		t.Fatalf("failed retrieval emitted data: %q %v", &out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Run(ctx, []string{"thread", "octo/repo#42"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	for _, args := range [][]string{{"thread"}, {"thread", "--json"}, {"thread", "octo/repo#42", "--wat"}, {"thread", "octo/repo#42", "extra"}, {"thread", "https://evil.example/octo/repo/issues/42"}, {"thread", "octo/repo#0"}} {
		c.getenv = func(string) string { t.Fatal("invalid request consulted authentication"); return "" }
		if err := c.Run(context.Background(), args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestThreadTerminalControls(t *testing.T) {
	var out, errOut bytes.Buffer
	c := New(&out, &errOut)
	c.getenv = func(string) string { return "token" }
	fake := &fakeThreadClient{result: transcriptFixture()}
	fake.result.Comments[0].Body = "Preserve 日本語\n\tcode\x1b[2J\rhidden"
	c.newThreadClient = func(string) threadClient { return fake }
	if err := c.Run(context.Background(), []string{"thread", "octo/repo#42"}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\r") || !strings.Contains(out.String(), `\x1b[2J\rhidden`) {
		t.Fatalf("unsafe terminal output: %q", &out)
	}
	out.Reset()
	if err := c.Run(context.Background(), []string{"thread", "octo/repo#42", "--json"}); err != nil {
		t.Fatal(err)
	}
	var got model.Thread
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Comments[0].Body != fake.result.Comments[0].Body {
		t.Fatal("JSON changed source text")
	}
}
