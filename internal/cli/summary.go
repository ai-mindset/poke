package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/ai-mindset/poke/internal/model"
	"github.com/ai-mindset/poke/internal/summary"
)

type summaryOptions struct {
	issue, input, provider        string
	contextTokens                 int
	contextSet, jsonOutput, viaGH bool
}

func parseSummaryOptions(args []string) (summaryOptions, error) {
	options := summaryOptions{provider: "ollama", contextTokens: summary.DefaultContextTokens}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--provider", "--input", "--context-tokens":
			if !hasValue {
				if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
					return options, fmt.Errorf("%s requires a value.", name)
				}
				index++
				value = args[index]
			}
			if value == "" {
				return options, fmt.Errorf("%s requires a value.", name)
			}
			switch name {
			case "--provider":
				options.provider = value
			case "--input":
				options.input = value
			case "--context-tokens":
				number, err := strconv.Atoi(value)
				if err != nil || number < 1024 || number > 1048576 {
					return options, fmt.Errorf("Ollama context size must be between 1024 and 1048576 tokens.")
				}
				options.contextTokens = number
				options.contextSet = true
			}
		case "--json":
			if hasValue {
				return options, fmt.Errorf("--json takes no value.")
			}
			options.jsonOutput = true
		case "--gh":
			if hasValue {
				return options, fmt.Errorf("--gh takes no value.")
			}
			options.viaGH = true
		default:
			if strings.HasPrefix(arg, "-") {
				return options, fmt.Errorf("Unknown summarize option \"%s\". Run poke help.", arg)
			}
			if options.issue != "" {
				return options, fmt.Errorf("Specify one issue or --input FILE.")
			}
			options.issue = arg
		}
	}
	if (options.issue == "") == (options.input == "") {
		return options, fmt.Errorf("Specify one issue or --input FILE (use - for standard input).")
	}
	if options.input != "" && options.viaGH {
		return options, fmt.Errorf("--gh applies only when fetching an issue, not with --input.")
	}
	if options.provider != "ollama" && options.contextSet {
		return options, fmt.Errorf("--context-tokens applies only to Ollama.")
	}
	return options, nil
}

func (c *CLI) summarize(ctx context.Context, args []string) error {
	options, err := parseSummaryOptions(args)
	if err != nil {
		return err
	}
	requestedModel, err := summary.ModelFor(options.provider)
	if err != nil {
		return err
	}
	var ref model.Reference
	if options.issue != "" {
		ref, err = model.ParseThreadReference(options.issue)
		if err != nil {
			return err
		}
	}
	config := summary.Config{Provider: options.provider, ContextTokens: options.contextTokens}
	if options.provider == "mistral" {
		config.APIKey = c.getenv("MISTRAL_API_KEY")
	} else {
		config.OllamaURL = c.getenv("POKE_OLLAMA_URL")
	}
	generator, err := c.newSummaryClient(config)
	if err != nil {
		return err
	}
	var thread model.Thread
	if options.input != "" {
		thread, err = c.readTranscript(options.input)
	} else {
		thread, err = c.fetchThread(ctx, ref, options.viaGH)
	}
	if err != nil {
		return err
	}
	result, err := summary.Summarize(ctx, thread, generator, options.provider, requestedModel)
	if err != nil {
		return err
	}
	if options.jsonOutput {
		encoder := json.NewEncoder(c.Out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	c.printSummary(result)
	return nil
}

func (c *CLI) readTranscript(path string) (model.Thread, error) {
	reader := c.In
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return model.Thread{}, err
		}
		defer file.Close()
		reader = file
	}
	if reader == nil {
		return model.Thread{}, fmt.Errorf("No standard input reader is available.")
	}
	const limit = 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return model.Thread{}, err
	}
	if len(data) > limit {
		return model.Thread{}, fmt.Errorf("Transcript exceeds the 1 MiB input-file limit; no model request was sent.")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var thread model.Thread
	if err := decoder.Decode(&thread); err != nil {
		return model.Thread{}, fmt.Errorf("Input must be a poke thread --json transcript.")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return model.Thread{}, fmt.Errorf("Input must contain exactly one transcript JSON object.")
	}
	return thread, nil
}

func (c *CLI) printSummary(result summary.Result) {
	fmt.Fprintf(c.Out, "Summary: %s\n%s\n%s | %s/%s\nSource fetched: %s | Summary generated: %s\nScope: issue body and issue comments.\n",
		terminalText(result.IssueTitle), terminalText(result.IssueURL), terminalText(result.IssueState),
		terminalText(result.Provider), terminalText(result.Model), terminalText(result.ThreadFetchedAt), terminalText(result.GeneratedAt))
	for _, section := range []struct {
		name   string
		claims []summary.Claim
	}{
		{"Context", result.Context}, {"Key points", result.KeyPoints}, {"Decisions", result.Decisions},
		{"Open questions", result.OpenQuestions}, {"Next actions", result.NextActions},
	} {
		fmt.Fprintf(c.Out, "\n%s:\n", section.name)
		if len(section.claims) == 0 {
			fmt.Fprintln(c.Out, "(none identified)")
		}
		for _, claim := range section.claims {
			ids := []string{}
			seen := map[string]bool{}
			for _, evidence := range claim.Evidence {
				if !seen[evidence.SourceID] {
					seen[evidence.SourceID] = true
					ids = append(ids, evidence.SourceID)
				}
			}
			fmt.Fprintf(c.Out, "- %s [%s]\n", terminalText(claim.Text), strings.Join(ids, ", "))
		}
	}
	fmt.Fprintln(c.Out, "\nSources:")
	for _, source := range result.Sources {
		fmt.Fprintf(c.Out, "[%s] %s | %s\n  Created: %s | Updated: %s\n", source.ID, author(source.Author), terminalText(source.URL), terminalText(source.CreatedAt), terminalText(source.UpdatedAt))
	}
}
