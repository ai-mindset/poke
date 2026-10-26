package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/ai-mindset/poke/internal/model"
)

func (c *CLI) thread(ctx context.Context, args []string) error {
	var issue string
	jsonOutput, viaGH := false, false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		case "--gh":
			viaGH = true
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("Unknown thread option \"%s\". Run poke help.", arg)
			}
			if issue != "" {
				return fmt.Errorf("Use poke thread ISSUE [--json] [--gh].")
			}
			issue = arg
		}
	}
	if issue == "" {
		return fmt.Errorf("Missing issue reference. Use poke thread OWNER/REPO#NUMBER.")
	}
	ref, err := model.ParseThreadReference(issue)
	if err != nil {
		return err
	}
	result, err := c.fetchThread(ctx, ref, viaGH)
	if err != nil {
		return err
	}
	for _, warning := range result.Warnings {
		fmt.Fprintln(c.Err, "Warning:", warning)
	}
	if jsonOutput {
		encoder := json.NewEncoder(c.Out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	fmt.Fprintf(c.Out, "%s#%d %s\n%s\n", ref.Repository, result.Issue.Number, terminalText(result.Issue.Title), terminalText(result.Issue.URL))
	coverage := "pagination completed; comment count matched"
	if !result.Complete {
		coverage = "incomplete or changed during retrieval"
	}
	fmt.Fprintf(c.Out, "Fetched: %s | %d comments | %s\n", result.FetchedAt, len(result.Comments), coverage)
	fmt.Fprintln(c.Out, "Scope: issue body and issue comments. Timeline events, linked threads, PR reviews, and attachment contents are outside this transcript.")
	fmt.Fprintf(c.Out, "\nIssue by %s | %s | %s\nUpdated: %s\n\n%s\n", author(result.Issue.Author), terminalText(result.Issue.CreatedAt),
		terminalText(result.Issue.State), terminalText(result.Issue.UpdatedAt), terminalText(result.Issue.Body))
	for _, comment := range result.Comments {
		fmt.Fprintf(c.Out, "\n---\nComment %d by %s | %s\n%s\nUpdated: %s\n\n%s\n", comment.ID,
			author(comment.Author), terminalText(comment.CreatedAt), terminalText(comment.URL), terminalText(comment.UpdatedAt), terminalText(comment.Body))
	}
	return nil
}

func (c *CLI) fetchThread(ctx context.Context, ref model.Reference, viaGH bool) (model.Thread, error) {
	token := ""
	if !viaGH {
		token = c.getenv("GITHUB_TOKEN")
	}
	return c.newThreadClient(token).Thread(ctx, ref)
}

func author(login string) string {
	if login == "" {
		return "(deleted or unavailable user)"
	}
	return "@" + terminalText(login)
}

func terminalText(value string) string {
	var text strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			quoted := strconv.QuoteRuneToASCII(r)
			text.WriteString(quoted[1 : len(quoted)-1])
		} else {
			text.WriteRune(r)
		}
	}
	return text.String()
}
