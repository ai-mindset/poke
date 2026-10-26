package model

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const ThreadScope = "issue_body_and_comments"

// Thread is a source transcript, independent of assigned-issue and private task data.
type Thread struct {
	Scope     string      `json:"scope"`
	Issue     ThreadIssue `json:"issue"`
	Comments  []Comment   `json:"comments"`
	FetchedAt string      `json:"fetched_at"`
	Complete  bool        `json:"complete"`
	Warnings  []string    `json:"warnings"`
}

type ThreadIssue struct {
	NodeID       string `json:"node_id"`
	Number       int64  `json:"number"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	URL          string `json:"url"`
	Author       string `json:"author"`
	State        string `json:"state"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	CommentCount int    `json:"comment_count"`
}

type Comment struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	URL       string `json:"url"`
	Author    string `json:"author"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

var threadRepository = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ParseThreadReference restricts network retrieval to github.com. Unlike cached
// lookups, silently dropping a URL's hostname would fetch a different source.
func ParseThreadReference(value string) (Reference, error) {
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil {
			return Reference{}, fmt.Errorf("Thread URLs must use https://github.com/OWNER/REPO/issues/NUMBER.")
		}
	}
	ref, err := ParseReference(value)
	if err != nil {
		return Reference{}, err
	}
	parts := strings.Split(ref.Repository, "/")
	if ref.Number < 1 || !threadRepository.MatchString(ref.Repository) || parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return Reference{}, fmt.Errorf("Invalid thread reference \"%s\". Use owner/repo#123 or an HTTPS GitHub issue URL.", value)
	}
	return ref, nil
}
