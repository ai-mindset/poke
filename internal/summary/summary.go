// Package summary creates source-cited summaries without accessing local notes or tasks.
package summary

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ai-mindset/poke/internal/model"
)

const MaxPromptBytes = 256 * 1024

type Evidence struct {
	SourceID string `json:"source_id"`
	Quote    string `json:"quote"`
}

type Claim struct {
	Text     string     `json:"text"`
	Evidence []Evidence `json:"evidence"`
}

type Draft struct {
	Context       []Claim `json:"context"`
	KeyPoints     []Claim `json:"key_points"`
	Decisions     []Claim `json:"decisions"`
	OpenQuestions []Claim `json:"open_questions"`
	NextActions   []Claim `json:"next_actions"`
}

type Source struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Author    string `json:"author"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type Result struct {
	Draft
	Scope           string   `json:"scope"`
	IssueURL        string   `json:"issue_url"`
	IssueTitle      string   `json:"issue_title"`
	IssueState      string   `json:"issue_state"`
	ThreadFetchedAt string   `json:"thread_fetched_at"`
	GeneratedAt     string   `json:"generated_at"`
	Provider        string   `json:"provider"`
	RequestedModel  string   `json:"requested_model"`
	Model           string   `json:"model"`
	SourceHash      string   `json:"source_hash"`
	Sources         []Source `json:"sources"`
}

type sourceText struct {
	Source
	Text string `json:"text"`
}

type Completion struct{ Content, Model string }

type Generator interface {
	Generate(context.Context, string) (Completion, error)
}

// Summarize validates input before generation and verifies every citation and
// excerpt afterwards. This establishes provenance, not semantic entailment.
func Summarize(ctx context.Context, thread model.Thread, generator Generator, provider, requestedModel string) (Result, error) {
	sources, err := collectSources(thread)
	if err != nil {
		return Result{}, err
	}
	payload, err := json.Marshal(struct {
		Scope   string       `json:"scope"`
		State   string       `json:"issue_state"`
		Sources []sourceText `json:"sources"`
	}{thread.Scope, thread.Issue.State, sources})
	if err != nil {
		return Result{}, err
	}
	if len(payload)+len(systemMessage()) > MaxPromptBytes {
		return Result{}, fmt.Errorf("Thread exceeds the %d-byte single-request limit. No source text was truncated or sent to the model.", MaxPromptBytes)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	completion, err := generator.Generate(ctx, string(payload))
	if err != nil {
		return Result{}, err
	}
	var draft Draft
	if err := decodeJSON([]byte(completion.Content), &draft); err != nil {
		return Result{}, fmt.Errorf("Model returned an invalid summary JSON object; no summary was accepted.")
	}
	used, err := validateDraft(draft, sources)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(completion.Model) == "" {
		return Result{}, fmt.Errorf("Model response omitted its model identity.")
	}
	// Exclude retrieval time from the digest so identical source content has the
	// same identity across refreshes. Canonicalize comment order without mutating input.
	comments := append([]model.Comment{}, thread.Comments...)
	slices.SortFunc(comments, func(a, b model.Comment) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	content, err := json.Marshal(struct {
		Scope    string            `json:"scope"`
		Issue    model.ThreadIssue `json:"issue"`
		Comments []model.Comment   `json:"comments"`
	}{thread.Scope, thread.Issue, comments})
	if err != nil {
		return Result{}, err
	}
	hash := sha256.Sum256(content)
	return Result{Draft: draft, Scope: thread.Scope, IssueURL: thread.Issue.URL, IssueTitle: thread.Issue.Title,
		IssueState: thread.Issue.State, ThreadFetchedAt: thread.FetchedAt, GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Provider: provider, RequestedModel: requestedModel, Model: completion.Model, SourceHash: hex.EncodeToString(hash[:]), Sources: used}, nil
}

func collectSources(thread model.Thread) ([]sourceText, error) {
	if thread.Scope != model.ThreadScope || !thread.Complete || len(thread.Warnings) != 0 {
		return nil, fmt.Errorf("Summarization requires a complete issue-body-and-comments transcript without coverage warnings. Capture the thread again.")
	}
	ref, err := model.ParseThreadReference(thread.Issue.URL)
	if err != nil || ref.Number != thread.Issue.Number || thread.Issue.NodeID == "" || (thread.Issue.State != "open" && thread.Issue.State != "closed") {
		return nil, fmt.Errorf("Transcript has invalid issue identity or state.")
	}
	if _, err := time.Parse(time.RFC3339, thread.FetchedAt); err != nil {
		return nil, fmt.Errorf("Transcript has an invalid retrieval timestamp.")
	}
	if thread.Issue.CommentCount != len(thread.Comments) {
		return nil, fmt.Errorf("Transcript comment count does not match its captured comments.")
	}
	sources := []sourceText{{Source: Source{ID: "issue", URL: thread.Issue.URL, Author: thread.Issue.Author,
		CreatedAt: thread.Issue.CreatedAt, UpdatedAt: thread.Issue.UpdatedAt}, Text: thread.Issue.Title + "\n\n" + thread.Issue.Body}}
	seen := map[int64]bool{}
	comments := slices.Clone(thread.Comments)
	slices.SortFunc(comments, func(a, b model.Comment) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	for _, comment := range comments {
		if comment.ID < 1 || seen[comment.ID] || comment.URL != thread.Issue.URL+"#issuecomment-"+strconv.FormatInt(comment.ID, 10) {
			return nil, fmt.Errorf("Transcript contains duplicate comment IDs or an invalid comment source URL.")
		}
		seen[comment.ID] = true
		sources = append(sources, sourceText{Source: Source{ID: "comment:" + strconv.FormatInt(comment.ID, 10), URL: comment.URL,
			Author: comment.Author, CreatedAt: comment.CreatedAt, UpdatedAt: comment.UpdatedAt}, Text: comment.Body})
	}
	return sources, nil
}

func validateDraft(draft Draft, sources []sourceText) ([]Source, error) {
	byID := map[string]sourceText{}
	for _, source := range sources {
		byID[source.ID] = source
	}
	used := map[string]bool{}
	sections := [][]Claim{draft.Context, draft.KeyPoints, draft.Decisions, draft.OpenQuestions, draft.NextActions}
	if len(draft.Context) == 0 {
		return nil, fmt.Errorf("Model omitted the summary context.")
	}
	for _, section := range sections {
		if section == nil || len(section) > 6 {
			return nil, fmt.Errorf("Model omitted a required summary section or exceeded its six-item limit.")
		}
		for _, claim := range section {
			if strings.TrimSpace(claim.Text) == "" || utf8.RuneCountInString(claim.Text) > 600 || len(claim.Evidence) < 1 || len(claim.Evidence) > 3 {
				return nil, fmt.Errorf("Model returned an empty, overlong, or uncited claim.")
			}
			for _, evidence := range claim.Evidence {
				source, ok := byID[evidence.SourceID]
				if !ok || strings.TrimSpace(evidence.Quote) == "" || utf8.RuneCountInString(evidence.Quote) > 500 || !strings.Contains(source.Text, evidence.Quote) {
					return nil, fmt.Errorf("Model cited an unknown source or an excerpt that does not exactly occur in that source. No summary was accepted.")
				}
				used[evidence.SourceID] = true
			}
		}
	}
	result := []Source{}
	for _, source := range sources {
		if used[source.ID] {
			result = append(result, source.Source)
		}
	}
	return result, nil
}

func decodeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("Expected a single JSON object.")
	}
	return nil
}
