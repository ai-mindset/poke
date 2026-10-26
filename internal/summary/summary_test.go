package summary

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ai-mindset/poke/internal/model"
)

type fakeGenerator struct {
	completion Completion
	err        error
	calls      int
	input      string
}

func (f *fakeGenerator) Generate(_ context.Context, input string) (Completion, error) {
	f.calls++
	f.input = input
	return f.completion, f.err
}

func testThread() model.Thread {
	data, err := os.ReadFile("testdata/decision_reversal.json")
	if err != nil {
		panic(err)
	}
	var thread model.Thread
	if err := json.Unmarshal(data, &thread); err != nil {
		panic(err)
	}
	return thread
}

func testDraft() Draft {
	claim := func(text, source, quote string) Claim {
		return Claim{Text: text, Evidence: []Evidence{{SourceID: source, Quote: quote}}}
	}
	return Draft{
		Context: []Claim{claim("Requests return stale cached values.", "issue", "Requests currently return stale values.")},
		KeyPoints: []Claim{{Text: "A full-cache clear was proposed; another participant objected to load spikes.", Evidence: []Evidence{
			{SourceID: "comment:1", Quote: "I propose clearing the entire cache on every write."},
			{SourceID: "comment:2", Quote: "I disagree: global clears cause load spikes."}}}},
		Decisions:     []Claim{claim("The per-key decision was superseded by per-tenant invalidation.", "comment:4", "the earlier decision is superseded. Decision: use per-tenant invalidation.")},
		OpenQuestions: []Claim{claim("TTL remains unresolved.", "comment:3", "TTL remains open.")},
		NextActions:   []Claim{claim("Alice offered to add a regression test.", "comment:3", "I'll add a regression test.")},
	}
}

func generatorFor(t *testing.T, draft Draft) *fakeGenerator {
	t.Helper()
	data, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeGenerator{completion: Completion{Content: string(data), Model: "test-model"}}
}

func TestCitedSummary(t *testing.T) {
	thread := testThread()
	generator := generatorFor(t, testDraft())
	result, err := Summarize(context.Background(), thread, generator, "ollama", OllamaModel)
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "ollama" || result.RequestedModel != OllamaModel || result.Model != "test-model" || result.Scope != model.ThreadScope || len(result.SourceHash) != 64 || result.ThreadFetchedAt != thread.FetchedAt || len(result.Sources) != 5 {
		t.Fatalf("provenance missing: %+v", result)
	}
	if generator.calls != 1 || len(result.Decisions) != 1 || !strings.Contains(result.Decisions[0].Text, "superseded") {
		t.Fatal("summary lost decision reversal")
	}
	for _, comment := range thread.Comments {
		var request struct {
			Sources []sourceText `json:"sources"`
		}
		if err := json.Unmarshal([]byte(generator.input), &request); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, source := range request.Sources {
			if source.Text == comment.Body {
				found = true
			}
		}
		if !found {
			t.Fatalf("source omitted: %d", comment.ID)
		}
	}
	for _, source := range result.Sources {
		wantAuthor := "alice"
		if source.ID == "comment:2" || source.ID == "comment:4" {
			wantAuthor = "bob"
		}
		if source.Author != wantAuthor || !strings.HasPrefix(source.URL, thread.Issue.URL) {
			t.Fatalf("invented source metadata: %+v", source)
		}
	}
}

func TestRejectsInvalidEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Draft)
	}{
		{"unknown source", func(d *Draft) { d.Context[0].Evidence[0].SourceID = "comment:999" }},
		{"fabricated excerpt", func(d *Draft) { d.Decisions[0].Evidence[0].Quote = "Everyone unanimously approved this." }},
		{"altered excerpt", func(d *Draft) { d.Context[0].Evidence[0].Quote = "Requests currently return fresh values." }},
		{"empty excerpt", func(d *Draft) { d.Context[0].Evidence[0].Quote = "  " }},
		{"missing citation", func(d *Draft) { d.Context[0].Evidence = nil }},
		{"empty context", func(d *Draft) { d.Context = []Claim{} }},
		{"missing section", func(d *Draft) { d.NextActions = nil }},
		{"empty claim", func(d *Draft) { d.Context[0].Text = " " }},
		{"overlong claim", func(d *Draft) { d.Context[0].Text = strings.Repeat("x", 601) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := testDraft()
			tc.change(&draft)
			_, err := Summarize(context.Background(), testThread(), generatorFor(t, draft), "ollama", OllamaModel)
			if err == nil {
				t.Fatal("invalid summary accepted")
			}
		})
	}
	for _, content := range []string{"not JSON", "{}", "null", `{"context":[],"unexpected":"field"}`, "```json\n{}\n```", "{} {}"} {
		generator := &fakeGenerator{completion: Completion{Content: content, Model: "test-model"}}
		if _, err := Summarize(context.Background(), testThread(), generator, "ollama", OllamaModel); err == nil {
			t.Errorf("accepted invalid output %q", content)
		}
	}
}

func TestRejectsIncompleteInputBeforeGeneration(t *testing.T) {
	for _, change := range []func(*model.Thread){
		func(t *model.Thread) { t.Complete = false }, func(t *model.Thread) { t.Warnings = []string{"changed during capture"} },
		func(t *model.Thread) { t.Scope = "unknown" }, func(t *model.Thread) { t.Issue.CommentCount++ },
		func(t *model.Thread) { t.Comments[1].ID = t.Comments[0].ID }, func(t *model.Thread) { t.Comments[0].URL = "https://evil.example/comment" },
		func(t *model.Thread) { t.FetchedAt = "not a date" }, func(t *model.Thread) { t.Issue.URL = "https://evil.example/octo/repo/issues/42" },
		func(t *model.Thread) { t.Issue.Body = strings.Repeat("x", MaxPromptBytes) },
	} {
		thread := testThread()
		change(&thread)
		generator := generatorFor(t, testDraft())
		if _, err := Summarize(context.Background(), thread, generator, "ollama", OllamaModel); err == nil || generator.calls != 0 {
			t.Fatalf("invalid input reached model: calls=%d error=%v", generator.calls, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	generator := generatorFor(t, testDraft())
	if _, err := Summarize(ctx, testThread(), generator, "ollama", OllamaModel); !errors.Is(err, context.Canceled) || generator.calls != 0 {
		t.Fatalf("cancellation: %v calls=%d", err, generator.calls)
	}
}

func TestSourceDigestTracksContent(t *testing.T) {
	thread := testThread()
	first, err := Summarize(context.Background(), thread, generatorFor(t, testDraft()), "ollama", OllamaModel)
	if err != nil {
		t.Fatal(err)
	}
	thread.FetchedAt = "2026-10-26T12:00:00Z"
	second, err := Summarize(context.Background(), thread, generatorFor(t, testDraft()), "mistral", MistralModel)
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceHash != second.SourceHash {
		t.Fatal("retrieval time or provider changed source digest")
	}
	thread.Comments[0].Body += " Added context."
	third, err := Summarize(context.Background(), thread, generatorFor(t, testDraft()), "ollama", OllamaModel)
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceHash == third.SourceHash {
		t.Fatal("source edit did not change digest")
	}
}

func TestEmptySectionsAndProviderFailure(t *testing.T) {
	draft := testDraft()
	draft.Decisions = []Claim{}
	draft.OpenQuestions = []Claim{}
	draft.NextActions = []Claim{}
	if _, err := Summarize(context.Background(), testThread(), generatorFor(t, draft), "ollama", OllamaModel); err != nil {
		t.Fatal(err)
	}
	generator := generatorFor(t, draft)
	generator.err = errors.New("provider failed")
	result, err := Summarize(context.Background(), testThread(), generator, "ollama", OllamaModel)
	if err == nil || result.IssueURL != "" {
		t.Fatal("provider failure produced a summary")
	}
}
