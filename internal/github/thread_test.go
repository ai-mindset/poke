package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-mindset/poke/internal/model"
)

var threadRef = model.Reference{Repository: "octo/repo", Number: 42}

func threadFixture(count int) map[string]any {
	return map[string]any{"node_id": "ISSUE_42", "number": 42, "title": "A discussion 日本語", "body": "Original **context**",
		"html_url": "https://github.com/octo/repo/issues/42", "user": map[string]string{"login": "alice"},
		"state": "open", "created_at": "2026-08-08T10:00:00Z", "updated_at": "2026-08-09T10:00:00Z", "comments": count}
}

func commentFixture(id int) map[string]any {
	return map[string]any{"id": id, "body": fmt.Sprintf("Comment %d\n```go\ncode()\n```", id),
		"html_url": fmt.Sprintf("https://github.com/octo/repo/issues/42#issuecomment-%d", id),
		"user":     map[string]string{"login": "bob"}, "created_at": "2026-08-08T11:00:00Z", "updated_at": "2026-08-09T11:00:00Z"}
}

func TestThreadPagination(t *testing.T) {
	for _, count := range []int{0, 101, 1001} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			issueReads, pageReads := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("thread capture must be read-only")
				}
				if r.URL.Path == "/repos/octo/repo/issues/42" {
					issueReads++
					json.NewEncoder(w).Encode(threadFixture(count))
					return
				}
				if r.URL.Path != "/repos/octo/repo/issues/42/comments" {
					t.Errorf("unexpected endpoint %s", r.URL)
					w.WriteHeader(404)
					return
				}
				pageReads++
				if r.URL.Query().Get("per_page") != "100" {
					t.Error("missing page size")
				}
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if page == 0 {
					page = 1
				}
				items := []map[string]any{}
				for n := (page-1)*100 + 1; n <= min(page*100, count); n++ {
					items = append(items, commentFixture(n))
				}
				slices.Reverse(items)
				if page*100 < count {
					w.Header().Set("Link", fmt.Sprintf(`</repos/octo/repo/issues/42/comments?per_page=100&page=%d>; rel="next", </repos/octo/repo/issues/42/comments?per_page=100&page=1>; rel="first"`, page+1))
				}
				json.NewEncoder(w).Encode(items)
			})
			c.now = func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) }
			got, err := c.Thread(context.Background(), threadRef)
			if err != nil || !got.Complete || len(got.Comments) != count || issueReads != 2 || pageReads != max(1, (count+99)/100) {
				t.Fatalf("capture: count=%d complete=%v issueReads=%d pageReads=%d error=%v", len(got.Comments), got.Complete, issueReads, pageReads, err)
			}
			if got.Issue.Body != "Original **context**" || got.Issue.Author != "alice" || got.FetchedAt != "2026-10-24T12:00:00Z" {
				t.Fatalf("metadata lost: %+v", got.Issue)
			}
			for index, comment := range got.Comments {
				if comment.ID != int64(index+1) || comment.Author != "bob" || !strings.Contains(comment.Body, "```go") || comment.UpdatedAt == "" {
					t.Fatalf("comment lost: %+v", comment)
				}
			}
		})
	}
}

func TestThreadCoverage(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		count                      int
		duplicate, changed, failed bool
	}{
		{"missing comments", 2, false, false, false}, {"duplicate comments", 1, true, false, false},
		{"changed issue", 1, false, true, false}, {"failed page", 1, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/repos/octo/repo/issues/42" {
					reads++
					issue := threadFixture(tc.count)
					if reads == 2 && tc.changed {
						issue["body"] = "Edited during retrieval"
					}
					json.NewEncoder(w).Encode(issue)
					return
				}
				if tc.failed {
					w.WriteHeader(403)
					fmt.Fprint(w, "Forbidden")
					return
				}
				comment := commentFixture(1)
				comment["user"] = nil
				items := []any{comment}
				if tc.duplicate {
					items = append(items, comment)
				}
				json.NewEncoder(w).Encode(items)
			})
			got, err := c.Thread(context.Background(), threadRef)
			if tc.failed {
				if err == nil || got.Issue.NodeID != "" {
					t.Fatalf("partial thread escaped error: %+v %v", got, err)
				}
				return
			}
			if err != nil || got.Complete || len(got.Warnings) == 0 || len(got.Comments) != 1 || got.Comments[0].Author != "" {
				t.Fatalf("coverage: %+v %v", got, err)
			}
		})
	}
}

func TestThreadRejectsPullRequestsAndMalformedData(t *testing.T) {
	for _, mutation := range []func(map[string]any){
		func(m map[string]any) {
			m["pull_request"] = map[string]any{"url": "https://api.github.com/repos/octo/repo/pulls/42"}
		},
		func(m map[string]any) { delete(m, "comments") },
		func(m map[string]any) { m["comments"] = -1 },
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			data := threadFixture(1)
			mutation(data)
			json.NewEncoder(w).Encode(data)
		})
		if _, err := c.Thread(context.Background(), threadRef); err == nil {
			t.Fatal("accepted unsupported or unverifiable thread")
		}
	}
}

func TestCommentPaginationLinks(t *testing.T) {
	for _, tc := range []struct {
		link, want string
		fail       bool
	}{
		{`<https://api.github.com/repos/octo/repo/issues/42/comments?page=2>; rel="next"`, "/repos/octo/repo/issues/42/comments?page=2", false},
		{`<?page=2>; rel="next"`, "/repos/octo/repo/issues/42/comments?page=2", false},
		{`<https://api.github.com/repositories/23096959/issues/42/comments?page=2>; rel="next"`, "/repositories/23096959/issues/42/comments?page=2", false},
		{`<https://api.github.com/repositories/23096959/issues/43/comments?page=2>; rel="next"`, "", true},
		{`<https://evil.example/repos/octo/repo/issues/42/comments?page=2>; rel="next"`, "", true},
		{`<http://api.github.com/repos/octo/repo/issues/42/comments?page=2>; rel="next"`, "", true},
		{`</repos/other/repo/issues/42/comments?page=2>; rel="next"`, "", true},
		{`garbage; rel="next"`, "", true}, {"", "", false},
	} {
		got, err := nextCommentPage(tc.link, "https://api.github.com", "/repos/octo/repo/issues/42/comments?per_page=100")
		if got != tc.want || (err != nil) != tc.fail {
			t.Errorf("%s: %q %v", tc.link, got, err)
		}
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `</repos/octo/repo/issues/42/comments?per_page=100>; rel="next"`)
		fmt.Fprint(w, "[]")
	})
	if _, err := c.comments(context.Background(), "/repos/octo/repo/issues/42/comments?per_page=100"); err == nil {
		t.Fatal("pagination cycle not rejected")
	}
}

func TestGitHubCLIThread(t *testing.T) {
	c := NewCLI()
	var calls [][]string
	c.run = func(ctx context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if !slices.Contains(args, "GET") || !slices.Contains(args, "github.com") {
			t.Fatal("missing method or hostname")
		}
		if strings.Contains(args[len(args)-1], "/comments?") {
			if !slices.Contains(args, "--paginate") || !slices.Contains(args, "--slurp") {
				t.Fatal("comment pagination missing")
			}
			return json.Marshal([]any{[]any{commentFixture(2)}, []any{commentFixture(1)}})
		}
		return json.Marshal(threadFixture(2))
	}
	got, err := c.Thread(context.Background(), threadRef)
	if err != nil || !got.Complete || len(calls) != 3 || len(got.Comments) != 2 || got.Comments[0].ID != 1 {
		t.Fatalf("gh capture: %+v %v", got, err)
	}
	c.run = func(context.Context, ...string) ([]byte, error) { return []byte("[partial"), errors.New("CLI failed") }
	if got, err := c.Thread(context.Background(), threadRef); err == nil || got.Issue.NodeID != "" {
		t.Fatal("failed CLI produced transcript")
	}
	c.run = func(context.Context, ...string) ([]byte, error) { return []byte("not JSON"), nil }
	if _, err := c.Thread(context.Background(), threadRef); err == nil {
		t.Fatal("accepted human-formatted CLI output")
	}
}

func TestRunGHMissingAndCancelled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := runGH(context.Background(), "api", "/user"); err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Fatalf("missing gh: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runGH(ctx, "api", "/user"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
