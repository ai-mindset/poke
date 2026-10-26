package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ai-mindset/poke/internal/summary"
)

type fakeSummaryGenerator struct {
	calls   int
	input   string
	content string
	err     error
}

func (f *fakeSummaryGenerator) Generate(ctx context.Context, input string) (summary.Completion, error) {
	f.calls++
	f.input = input
	if err := ctx.Err(); err != nil {
		return summary.Completion{}, err
	}
	return summary.Completion{Content: f.content, Model: "test-model-revision"}, f.err
}

func summaryGenerator(t *testing.T) *fakeSummaryGenerator {
	t.Helper()
	draft := summary.Draft{Context: []summary.Claim{{Text: "The issue describes original context.", Evidence: []summary.Evidence{{SourceID: "issue", Quote: "Original **context**"}}}},
		KeyPoints: []summary.Claim{{Text: "A participant supplied an attributed response.", Evidence: []summary.Evidence{{SourceID: "comment:7", Quote: "An attributed response"}}}},
		Decisions: []summary.Claim{}, OpenQuestions: []summary.Claim{}, NextActions: []summary.Claim{}}
	data, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeSummaryGenerator{content: string(data)}
}

func TestSummarizeProvidersAndInputModes(t *testing.T) {
	for _, tc := range []struct {
		name, provider, input string
		flags                 []string
		jsonOutput            bool
	}{
		{"default local from GitHub", "ollama", "", nil, false},
		{"explicit local via gh", "ollama", "", []string{"--provider=ollama", "--gh", "--context-tokens=16384"}, false},
		{"local from stdin", "ollama", "-", nil, false},
		{"Mistral from file", "mistral", "file", []string{"--provider", "mistral", "--json"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			c := New(&out, &errOut)
			c.lookupEnv = func(string) (string, bool) { t.Fatal("summarize accessed database configuration"); return "", false }
			c.getenv = func(key string) string {
				switch key {
				case "MISTRAL_API_KEY":
					if tc.provider != "mistral" {
						t.Fatal("local provider consulted hosted credentials")
					}
					return "hosted-key"
				case "POKE_OLLAMA_URL":
					if tc.provider != "ollama" {
						t.Fatal("Mistral consulted local endpoint")
					}
					return "http://127.0.0.1:11434"
				case "GITHUB_TOKEN":
					if tc.input != "" || strings.Contains(tc.name, "via gh") {
						t.Fatal("unexpected GitHub token lookup")
					}
					return "github-key"
				default:
					t.Fatalf("unexpected environment lookup: %s", key)
					return ""
				}
			}
			thread := transcriptFixture()
			c.newThreadClient = func(token string) threadClient {
				if tc.input != "" {
					t.Fatal("saved input attempted GitHub access")
				}
				if strings.Contains(tc.name, "via gh") && token != "" {
					t.Fatal("explicit gh used token client")
				}
				return &fakeThreadClient{result: thread}
			}
			fake := summaryGenerator(t)
			c.newSummaryClient = func(config summary.Config) (summary.Generator, error) {
				if config.Provider != tc.provider {
					t.Fatalf("provider=%s", config.Provider)
				}
				if _, err := summary.NewClient(config); err != nil {
					return nil, err
				}
				if tc.provider == "mistral" && config.APIKey != "hosted-key" {
					t.Fatal("missing Mistral credentials")
				}
				return fake, nil
			}
			directory := filepath.Join(t.TempDir(), "must-not-exist")
			args := []string{"--db", filepath.Join(directory, "poke.db"), "summarize"}
			if tc.input == "" {
				args = append(args, "octo/repo#42")
			} else {
				data, err := json.Marshal(thread)
				if err != nil {
					t.Fatal(err)
				}
				path := tc.input
				if path == "-" {
					c.In = bytes.NewReader(data)
				} else {
					path = filepath.Join(t.TempDir(), "thread.json")
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				args = append(args, "--input", path)
			}
			if err := c.Run(context.Background(), append(args, tc.flags...)); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("summary created a database directory")
			}
			if fake.calls != 1 || errOut.Len() != 0 || strings.Contains(fake.input, "hosted-key") || strings.Contains(fake.input, "github-key") {
				t.Fatalf("unsafe model request: calls=%d stderr=%q", fake.calls, &errOut)
			}
			if tc.jsonOutput {
				var got summary.Result
				if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Provider != "mistral" || got.RequestedModel != summary.MistralModel || got.Model != "test-model-revision" || len(got.Sources) != 2 || len(got.SourceHash) != 64 {
					t.Fatalf("summary JSON: %s %v", &out, err)
				}
			} else {
				for _, part := range []string{"Context:", "Key points:", "Decisions:\n(none identified)", "Open questions:", "Next actions:", "Sources:", "[comment:7]", "#issuecomment-7"} {
					if !strings.Contains(out.String(), part) {
						t.Errorf("summary missing %q", part)
					}
				}
			}
		})
	}
}

func TestSummarizeRejectsIncompleteAndInvalidOutput(t *testing.T) {
	var out, errOut bytes.Buffer
	c := New(&out, &errOut)
	c.getenv = func(string) string { return "" }
	thread := transcriptFixture()
	thread.Complete = false
	c.newThreadClient = func(string) threadClient { return &fakeThreadClient{result: thread} }
	fake := summaryGenerator(t)
	c.newSummaryClient = func(config summary.Config) (summary.Generator, error) {
		if config.Provider != "ollama" {
			t.Fatal("automatic provider fallback")
		}
		return fake, nil
	}
	if err := c.Run(context.Background(), []string{"summarize", "octo/repo#42"}); err == nil || fake.calls != 0 || out.Len() != 0 {
		t.Fatalf("incomplete input reached model: %v", err)
	}
	thread.Complete = true
	fake.content = `{"context":[]}`
	if err := c.Run(context.Background(), []string{"summarize", "octo/repo#42"}); err == nil || out.Len() != 0 {
		t.Fatal("invalid output was printed")
	}
	fake.err = errors.New("provider failed")
	if err := c.Run(context.Background(), []string{"summarize", "octo/repo#42"}); err == nil || out.Len() != 0 {
		t.Fatal("provider failure was ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Run(ctx, []string{"summarize", "octo/repo#42"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestMistralRequiresKeyBeforeFetching(t *testing.T) {
	var out, errOut bytes.Buffer
	c := New(&out, &errOut)
	c.getenv = func(key string) string {
		if key != "MISTRAL_API_KEY" {
			t.Fatalf("unexpected lookup %s", key)
		}
		return ""
	}
	c.newThreadClient = func(string) threadClient { t.Fatal("missing key fetched an issue"); return nil }
	if err := c.Run(context.Background(), []string{"summarize", "octo/repo#42", "--provider", "mistral"}); err == nil || !strings.Contains(err.Error(), "MISTRAL_API_KEY") {
		t.Fatalf("missing key: %v", err)
	}
}

func TestSummaryInputValidation(t *testing.T) {
	for _, args := range [][]string{
		{"summarize"}, {"summarize", "octo/repo#42", "extra"}, {"summarize", "octo/repo#42", "--input", "file"},
		{"summarize", "octo/repo#42", "--wat"}, {"summarize", "octo/repo#42", "--provider"},
		{"summarize", "octo/repo#42", "--provider=unknown"}, {"summarize", "--input=-", "--gh"},
		{"summarize", "--input=-", "--provider=mistral", "--context-tokens=32768"},
		{"summarize", "octo/repo#42", "--context-tokens=0"}, {"summarize", "octo/repo#42", "--json=true"},
		{"summarize", "https://evil.example/octo/repo/issues/42"},
	} {
		var out, errOut bytes.Buffer
		c := New(&out, &errOut)
		c.getenv = func(string) string { t.Fatal("invalid arguments consulted credentials"); return "" }
		if err := c.Run(context.Background(), args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	for _, data := range []string{"not JSON", "{} {}", `{"unexpected":"field"}`, strings.Repeat("x", 1024*1024+1)} {
		var out, errOut bytes.Buffer
		c := New(&out, &errOut)
		c.In = strings.NewReader(data)
		if _, err := c.readTranscript("-"); err == nil {
			t.Fatal("invalid or oversized transcript accepted")
		}
	}
}

func TestSummaryTerminalEscapes(t *testing.T) {
	var out, errOut bytes.Buffer
	c := New(&out, &errOut)
	c.printSummary(summary.Result{Draft: summary.Draft{Context: []summary.Claim{{Text: "Unsafe \x1b[2J\rdisplay", Evidence: []summary.Evidence{{SourceID: "issue", Quote: "source"}}}}}, Sources: []summary.Source{{ID: "issue", URL: "https://github.com/octo/repo/issues/42", Author: "alice"}}})
	if strings.ContainsAny(out.String(), "\x1b\r") || !strings.Contains(out.String(), `\x1b[2J\rdisplay`) {
		t.Fatalf("unsafe summary display: %q", &out)
	}
}

func TestReleaseSummary(t *testing.T) {
	binary := os.Getenv("POKE_TEST_BINARY")
	if binary == "" {
		t.Skip("set POKE_TEST_BINARY to test a packaged executable")
	}
	fixture, err := filepath.Abs("../summary/testdata/decision_reversal.json")
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requests.Add(1)
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != summary.OllamaModel || r.Header.Get("Authorization") != "" {
			t.Error("packaged request used the wrong model or credentials")
		}
		draft := summary.Draft{Context: []summary.Claim{{Text: "Requests return stale values.", Evidence: []summary.Evidence{{SourceID: "issue", Quote: "Requests currently return stale values."}}}},
			KeyPoints: []summary.Claim{}, Decisions: []summary.Claim{}, OpenQuestions: []summary.Claim{}, NextActions: []summary.Claim{}}
		if count > 1 {
			draft.Context[0].Evidence[0].Quote = "A fabricated excerpt."
		}
		content, err := json.Marshal(draft)
		if err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode(map[string]any{"model": summary.OllamaModel, "done": true, "done_reason": "stop", "message": map[string]string{"content": string(content)}})
	}))
	defer server.Close()
	for attempt := 1; attempt <= 2; attempt++ {
		command := exec.Command(binary, "summarize", "--input", fixture, "--json")
		command.Env = append(os.Environ(), "POKE_OLLAMA_URL="+server.URL)
		var out, errOut bytes.Buffer
		command.Stdout = &out
		command.Stderr = &errOut
		err := command.Run()
		if attempt == 1 {
			var result summary.Result
			if err != nil || json.Unmarshal(out.Bytes(), &result) != nil || len(result.Context) != 1 || result.Provider != "ollama" || result.RequestedModel != summary.OllamaModel || result.Context[0].Text != "Requests return stale values." {
				t.Fatalf("packaged summary: stdout=%q stderr=%q error=%v", &out, &errOut, err)
			}
		} else if err == nil || command.ProcessState.ExitCode() != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "excerpt") {
			t.Fatalf("invalid evidence escaped packaged validation: stdout=%q stderr=%q error=%v", &out, &errOut, err)
		}
	}
	if requests.Load() != 2 {
		t.Fatal("packaged summary retried automatically")
	}
}
