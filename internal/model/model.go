package model

import (
	"fmt"
	"regexp"
	"strconv"
)

type Account struct {
	Host, Login, NodeID string
}

type Issue struct {
	NodeID     string
	Number     int64
	Title      string
	URL        string
	Repository string
	State      string
	UpdatedAt  string
	Assignees  []string
}

type CachedIssue struct {
	Issue
	AccountNodeID      string
	LastSeenAt         string
	Stale              bool
	Note               *string
	OpenTaskCount      int
	CompletedTaskCount int
}

type Task struct {
	ID, Position               int64
	AccountNodeID, IssueNodeID string
	Title                      string
	Completed                  bool
	CreatedAt, UpdatedAt       string
}

type Reference struct {
	Repository string
	Number     int64
}

type SyncResult struct {
	Issues   []Issue
	Complete bool
}

var (
	issueURL   = regexp.MustCompile(`^https?://[^/]+/([^/]+/[^/]+)/issues/(\d+)(?:[/?#].*)?$`)
	issueShort = regexp.MustCompile(`^([^/\s]+/[^/#\s]+)#(\d+)$`)
)

func ParseReference(value string) (Reference, error) {
	match := issueURL.FindStringSubmatch(value)
	if match == nil {
		match = issueShort.FindStringSubmatch(value)
	}
	if match != nil {
		number, err := strconv.ParseInt(match[2], 10, 64)
		if err == nil {
			return Reference{Repository: match[1], Number: number}, nil
		}
	}
	return Reference{}, fmt.Errorf("Invalid issue reference \"%s\". Use owner/repo#123 or an issue URL.", value)
}
