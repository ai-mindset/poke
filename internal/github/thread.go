package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ai-mindset/poke/internal/model"
)

type apiAuthor struct {
	Login string `json:"login"`
}

type apiThreadIssue struct {
	NodeID      string          `json:"node_id"`
	Number      int64           `json:"number"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	URL         string          `json:"html_url"`
	User        apiAuthor       `json:"user"`
	State       string          `json:"state"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	Comments    *int            `json:"comments"`
	PullRequest json.RawMessage `json:"pull_request"`
}

type apiComment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	URL       string    `json:"html_url"`
	User      apiAuthor `json:"user"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}

type jsonRequest func(context.Context, string, any) error
type commentRequest func(context.Context, string) ([]apiComment, error)

var canonicalComments = regexp.MustCompile(`^/repositories/[0-9]+(/issues/[0-9]+/comments)$`)

func (c *Client) Thread(ctx context.Context, ref model.Reference) (model.Thread, error) {
	return fetchThread(ctx, ref, c.request, c.comments, c.now)
}

func fetchThread(ctx context.Context, ref model.Reference, request jsonRequest, comments commentRequest, now func() time.Time) (model.Thread, error) {
	path := fmt.Sprintf("/repos/%s/issues/%d", ref.Repository, ref.Number)
	var before, after apiThreadIssue
	if err := request(ctx, path, &before); err != nil {
		return model.Thread{}, err
	}
	if len(before.PullRequest) > 0 && string(before.PullRequest) != "null" {
		return model.Thread{}, fmt.Errorf("This reference is a pull request. Thread capture currently supports issues and their issue comments.")
	}
	if before.NodeID == "" || before.Number < 1 || before.URL == "" || before.Comments == nil || *before.Comments < 0 {
		return model.Thread{}, fmt.Errorf("GitHub returned incomplete issue metadata; thread coverage cannot be verified.")
	}
	items, err := comments(ctx, path+"/comments?per_page=100")
	if err != nil {
		return model.Thread{}, err
	}
	if err := request(ctx, path, &after); err != nil {
		return model.Thread{}, err
	}
	result := model.Thread{
		Scope: model.ThreadScope,
		Issue: model.ThreadIssue{NodeID: before.NodeID, Number: before.Number, Title: before.Title, Body: before.Body,
			URL: before.URL, Author: before.User.Login, State: before.State, CreatedAt: before.CreatedAt,
			UpdatedAt: before.UpdatedAt, CommentCount: *before.Comments},
		Comments: []model.Comment{}, FetchedAt: now().UTC().Format(time.RFC3339), Complete: true, Warnings: []string{},
	}
	warn := func(message string) { result.Complete = false; result.Warnings = append(result.Warnings, message) }
	if after.Comments == nil || after.NodeID != before.NodeID || after.UpdatedAt != before.UpdatedAt || after.Body != before.Body || after.Title != before.Title || after.State != before.State || *after.Comments != *before.Comments {
		warn("The issue changed during retrieval. Refresh the transcript before relying on its coverage.")
	}
	seen := map[int64]bool{}
	duplicates := false
	for _, item := range items {
		if item.ID < 1 || item.URL == "" {
			return model.Thread{}, fmt.Errorf("GitHub returned a comment without its identity or source URL.")
		}
		if seen[item.ID] {
			duplicates = true
			continue
		}
		seen[item.ID] = true
		result.Comments = append(result.Comments, model.Comment{ID: item.ID, Body: item.Body, URL: item.URL,
			Author: item.User.Login, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt})
	}
	if duplicates {
		warn("Repeated comment IDs were returned during pagination; coverage may have changed.")
	}
	if len(result.Comments) != *before.Comments {
		warn(fmt.Sprintf("Retrieved %d unique comments; GitHub reported %d at the start.", len(result.Comments), *before.Comments))
	}
	slices.SortFunc(result.Comments, func(a, b model.Comment) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return result, nil
}

func (c *Client) comments(ctx context.Context, path string) ([]apiComment, error) {
	items := []apiComment{}
	seen := map[string]bool{}
	for path != "" {
		if seen[path] {
			return nil, fmt.Errorf("GitHub repeated a comment pagination link; retrieval stopped.")
		}
		seen[path] = true
		var page []apiComment
		headers, err := c.requestPage(ctx, path, &page)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		path, err = nextCommentPage(headers.Get("Link"), c.BaseURL, path)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func nextCommentPage(link, baseURL, current string) (string, error) {
	base, err := url.Parse(baseURL + current)
	if err != nil {
		return "", err
	}
	for _, part := range strings.Split(link, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		next := false
		for _, field := range fields[1:] {
			if strings.TrimSpace(field) == `rel="next"` {
				next = true
			}
		}
		if !next {
			continue
		}
		target := strings.TrimSpace(fields[0])
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			return "", fmt.Errorf("Invalid GitHub comment pagination link.")
		}
		u, err := url.Parse(strings.TrimSuffix(strings.TrimPrefix(target, "<"), ">"))
		if err != nil {
			return "", err
		}
		u = base.ResolveReference(u)
		sameEndpoint := u.Path == base.Path
		// GitHub's Link headers canonicalize owner/name paths to repository IDs.
		// Once canonical, subsequent pages must retain that same repository ID.
		if !sameEndpoint && strings.HasPrefix(base.Path, "/repos/") {
			if match := canonicalComments.FindStringSubmatch(u.Path); match != nil && strings.HasSuffix(base.Path, match[1]) {
				sameEndpoint = true
			}
		}
		if u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || !sameEndpoint || u.Fragment != "" {
			return "", fmt.Errorf("GitHub returned a comment pagination link outside the requested endpoint.")
		}
		return u.RequestURI(), nil
	}
	return "", nil
}
