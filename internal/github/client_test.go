package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := New("secret-token")
	c.BaseURL = server.URL
	return c
}

func apiFixture(n int) map[string]any {
	return map[string]any{"node_id": fmt.Sprintf("ISSUE_%d", n), "number": n, "title": "Issue", "html_url": "https://github.com/octo/repo/issues/1",
		"repository_url": "https://api.github.com/repos/octo/repo", "state": "open", "updated_at": "2026-08-08T10:00:00Z", "assignees": []map[string]string{{"login": "octocat"}}}
}

func TestSearchPaginationAndCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name       string
		total      int
		incomplete bool
		want       int
		complete   bool
	}{
		{"empty", 0, false, 0, true}, {"pagination", 101, false, 101, true},
		{"cap", 1001, false, 1000, false}, {"incomplete", 1, true, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer secret-token" || r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" || r.Header.Get("User-Agent") != "poke" {
					t.Error("incorrect request headers or method")
				}
				q := r.URL.Query()
				query := q.Get("q")
				requests = append(requests, query)
				if !strings.Contains(query, "is:issue") || !strings.Contains(query, "assignee:@me") || !strings.Contains(query, "org:Acme") || q.Get("per_page") != "100" {
					t.Errorf("incorrect query: %v", q)
				}
				total := tc.total
				if strings.Contains(query, "is:closed") {
					total = 0
				}
				page, _ := strconv.Atoi(q.Get("page"))
				items := []map[string]any{}
				for n := (page-1)*100 + 1; n <= min(page*100, total); n++ {
					items = append(items, apiFixture(n))
				}
				json.NewEncoder(w).Encode(map[string]any{"total_count": total, "incomplete_results": tc.incomplete, "items": items})
			})
			result, err := c.AssignedIssues(context.Background(), "Acme")
			if err != nil || len(result.Issues) != tc.want || result.Complete != tc.complete {
				t.Fatalf("result: %+v, %v", result, err)
			}
			if len(result.Issues) > 0 && (result.Issues[0].Repository != "octo/repo" || result.Issues[0].Assignees[0] != "octocat") {
				t.Fatal("mapping failed")
			}
			if len(requests) < 2 || !strings.Contains(requests[len(requests)-1], "is:closed") {
				t.Fatal("did not search both states")
			}
		})
	}
}

func TestDeduplicationAndAccount(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			fmt.Fprint(w, `{"login":"octocat","node_id":"USER_1"}`)
			return
		}
		item := apiFixture(1)
		if strings.Contains(r.URL.Query().Get("q"), "is:closed") {
			item["state"] = "closed"
		}
		json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "items": []any{item}})
	})
	account, err := c.Account(context.Background())
	if err != nil || account.Login != "octocat" || account.NodeID != "USER_1" || account.Host == "" {
		t.Fatalf("account: %+v, %v", account, err)
	}
	result, err := c.AssignedIssues(context.Background(), "")
	if err != nil || len(result.Issues) != 1 || result.Issues[0].State != "closed" {
		t.Fatalf("deduplication: %+v, %v", result, err)
	}
}

func TestRateLimits(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, tc := range []struct {
		name      string
		status    int
		headers   map[string]string
		body      string
		wantWait  time.Duration
		attempts  int
		wantError bool
	}{
		{"retry once", 429, map[string]string{"Retry-After": "0"}, "limited", 0, 2, false},
		{"primary", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1800000020"}, "limited", 20 * time.Second, 2, false},
		{"secondary", 403, nil, "secondary rate limit", time.Minute, 2, false},
		{"429 fallback", 429, nil, "limited", time.Minute, 2, false},
		{"long retry", 429, map[string]string{"Retry-After": "120"}, "limited", 0, 1, true},
		{"long reset", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1800000120"}, "limited", 0, 1, true},
		{"later reset", 403, map[string]string{"Retry-After": "0", "X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1800000120"}, "limited", 0, 1, true},
		{"HTTP date", 429, map[string]string{"Retry-After": now.Add(30 * time.Second).UTC().Format(http.TimeFormat)}, "limited", 30 * time.Second, 2, false},
		{"forbidden", 403, nil, "Forbidden secret-token", 0, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts, waits := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				attempts++
				if attempts == 1 {
					for key, value := range tc.headers {
						w.Header().Set(key, value)
					}
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
					return
				}
				fmt.Fprint(w, `{"login":"octocat","node_id":"USER_1"}`)
			})
			c.now = func() time.Time { return now }
			c.wait = func(_ context.Context, delay time.Duration) error {
				waits++
				if delay != tc.wantWait {
					t.Errorf("wait = %v, want %v", delay, tc.wantWait)
				}
				return nil
			}
			_, err := c.Account(context.Background())
			if (err != nil) != tc.wantError || attempts != tc.attempts {
				t.Fatalf("attempts=%d, error=%v", attempts, err)
			}
			if tc.attempts == 1 && waits != 0 {
				t.Fatal("waited before failing")
			}
			if err != nil && strings.Contains(err.Error(), "secret-token") {
				t.Fatal("token leaked")
			}
			if tc.name == "long retry" && !strings.Contains(err.Error(), "2m0s") {
				t.Fatalf("missing actual wait: %v", err)
			}
		})
	}
}

func TestRetryLimitAndCancellation(t *testing.T) {
	var attempts atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(429)
	})
	if _, err := c.Account(context.Background()); err == nil || attempts.Load() != 2 {
		t.Fatalf("attempts=%d, error=%v", attempts.Load(), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.wait = func(ctx context.Context, _ time.Duration) error { cancel(); return wait(ctx, time.Minute) }
	if _, err := c.Account(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait cancellation: %v", err)
	}
}

func TestRequestTimeoutAndCancellation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	if c.HTTP.Timeout != 30*time.Second {
		t.Fatal("missing production timeout")
	}
	c.HTTP.Timeout = 20 * time.Millisecond
	if _, err := c.Account(context.Background()); err == nil {
		t.Fatal("request did not time out")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Account(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancellation: %v", err)
	}
}
