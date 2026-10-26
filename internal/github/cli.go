package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/ai-mindset/poke/internal/model"
)

// CLI uses GitHub CLI's existing authentication without extracting its token.
type CLI struct {
	run func(context.Context, ...string) ([]byte, error)
	now func() time.Time
}

func NewCLI() *CLI { return &CLI{run: runGH, now: time.Now} }

func runGH(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "gh", args...)
	command.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1")
	output, err := command.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if _, ok := err.(*exec.Error); ok {
			return nil, fmt.Errorf("GitHub CLI was not found. Install gh and run gh auth login, or set GITHUB_TOKEN.")
		}
		// Do not echo subprocess diagnostics, which can include debugging output.
		return nil, fmt.Errorf("gh api failed (%v). Check gh auth status or use GITHUB_TOKEN.", err)
	}
	return output, nil
}

func (c *CLI) api(ctx context.Context, path string, paginate bool, target any) error {
	args := []string{"api", "--hostname", "github.com", "--method", "GET",
		"--header", "Accept: application/vnd.github+json", "--header", "X-GitHub-Api-Version: 2022-11-28"}
	if paginate {
		args = append(args, "--paginate", "--slurp")
	}
	args = append(args, path)
	output, err := c.run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(output, target); err != nil {
		return fmt.Errorf("Cannot decode GitHub CLI response: %w", err)
	}
	return nil
}

func (c *CLI) request(ctx context.Context, path string, target any) error {
	return c.api(ctx, path, false, target)
}

func (c *CLI) comments(ctx context.Context, path string) ([]apiComment, error) {
	var pages [][]apiComment
	if err := c.api(ctx, path, true, &pages); err != nil {
		return nil, err
	}
	items := []apiComment{}
	for _, page := range pages {
		items = append(items, page...)
	}
	return items, nil
}

func (c *CLI) Thread(ctx context.Context, ref model.Reference) (model.Thread, error) {
	return fetchThread(ctx, ref, c.request, c.comments, c.now)
}
