package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/ai-mindset/poke/internal/github"
	"github.com/ai-mindset/poke/internal/model"
	"github.com/ai-mindset/poke/internal/store"
	"github.com/ai-mindset/poke/internal/summary"
)

type githubClient interface {
	Account(context.Context) (model.Account, error)
	AssignedIssues(context.Context, string) (model.SyncResult, error)
}

type threadClient interface {
	Thread(context.Context, model.Reference) (model.Thread, error)
}

type CLI struct {
	In               io.Reader
	Out, Err         io.Writer
	getenv           func(string) string
	lookupEnv        func(string) (string, bool)
	newClient        func(string) githubClient
	newThreadClient  func(string) threadClient
	newSummaryClient func(summary.Config) (summary.Generator, error)
}

func New(out, errOut io.Writer) *CLI {
	return &CLI{In: os.Stdin, Out: out, Err: errOut, getenv: os.Getenv, lookupEnv: os.LookupEnv,
		newSummaryClient: func(config summary.Config) (summary.Generator, error) { return summary.NewClient(config) },
		newClient:        func(token string) githubClient { return github.New(token) },
		newThreadClient: func(token string) threadClient {
			if token == "" {
				return github.NewCLI()
			}
			return github.New(token)
		}}
}

type options struct {
	dbPath *string
	org    string
	help   bool
	args   []string
}

func parseOptions(args []string) (options, error) {
	var result options
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--help" || arg == "-h":
			result.help = true
		case arg == "--": // Preserve the existing global-option parser.
		case arg == "--db" || arg == "--org":
			if index+1 >= len(args) || args[index+1] == "" {
				return result, fmt.Errorf("%s requires a value.", arg)
			}
			index++
			if arg == "--db" {
				value := args[index]
				result.dbPath = &value
			} else {
				result.org = args[index]
			}
		case strings.HasPrefix(arg, "--db="):
			value := strings.TrimPrefix(arg, "--db=")
			result.dbPath = &value
		case strings.HasPrefix(arg, "--org="):
			result.org = strings.TrimPrefix(arg, "--org=")
		default:
			result.args = append(result.args, arg)
		}
	}
	return result, nil
}

func (c *CLI) Run(ctx context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	if opts.help || (len(opts.args) > 0 && opts.args[0] == "help") {
		_, err := fmt.Fprintln(c.Out, help)
		return err
	}
	if len(opts.args) > 0 && opts.args[0] == "thread" {
		return c.thread(ctx, opts.args[1:])
	}
	if len(opts.args) > 0 && opts.args[0] == "summarize" {
		return c.summarize(ctx, opts.args[1:])
	}
	var path string
	if opts.dbPath != nil {
		path = *opts.dbPath
	} else {
		path, err = defaultDatabasePath(runtime.GOOS, c.getenv, c.lookupEnv)
		if err != nil {
			return err
		}
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
	}
	s, err := store.Open(path)
	if err != nil {
		return err
	}
	defer s.Close()
	command := ""
	if len(opts.args) > 0 {
		command = opts.args[0]
	}
	if command == "" || command == "sync" {
		account, err := c.sync(ctx, s, opts.org)
		if err != nil {
			return err
		}
		if command == "" {
			return c.printIssues(s, account.NodeID, false, false)
		}
		return nil
	}
	if !slices.Contains([]string{"issues", "next", "show", "task", "note"}, command) {
		if len(opts.args) == 1 && !strings.HasPrefix(command, "-") {
			account, err := c.sync(ctx, s, command)
			if err != nil {
				return err
			}
			return c.printIssues(s, account.NodeID, false, false)
		}
		return fmt.Errorf("Unknown command \"%s\". Run poke help.", command)
	}
	account, err := s.LastAccount()
	if err != nil {
		return err
	}
	if account == nil {
		return errors.New("No synced account. Run poke sync first.")
	}
	switch command {
	case "issues":
		return c.printIssues(s, account.NodeID, slices.Contains(opts.args, "--all"), slices.Contains(opts.args, "--stale"))
	case "next":
		return c.printNext(s, account.NodeID)
	case "show":
		value, err := argument(opts.args, 1)
		if err != nil {
			return err
		}
		issue, err := requireIssue(s, account.NodeID, value)
		if err != nil {
			return err
		}
		return c.printIssue(s, issue)
	case "task":
		return c.task(s, account.NodeID, opts.args[1:])
	case "note":
		return c.note(s, account.NodeID, opts.args[1:])
	}
	return nil
}

func (c *CLI) sync(ctx context.Context, s *store.Store, org string) (model.Account, error) {
	token := c.getenv("GITHUB_TOKEN")
	if token == "" {
		return model.Account{}, errors.New("GitHub authentication required. Set GITHUB_TOKEN to a personal access token.")
	}
	client := c.newClient(token)
	account, err := client.Account(ctx)
	if err != nil {
		return account, err
	}
	result, err := client.AssignedIssues(ctx, org)
	if err != nil {
		return account, err
	}
	if err := s.Sync(ctx, account, result.Issues, store.SyncOptions{Complete: result.Complete, Org: org}); err != nil {
		return account, err
	}
	suffix := ""
	if org != "" {
		suffix = " in " + org
	}
	fmt.Fprintf(c.Out, "Synced %d assigned issues for @%s%s.\n", len(result.Issues), account.Login, suffix)
	if !result.Complete {
		fmt.Fprintln(c.Err, "GitHub returned a partial result. Existing cached issues were retained as current.")
	}
	return account, nil
}

func requireIssue(s *store.Store, account, value string) (*model.CachedIssue, error) {
	ref, err := model.ParseReference(value)
	if err != nil {
		return nil, err
	}
	issue, err := s.FindIssue(account, ref)
	if err != nil {
		return nil, err
	}
	if issue == nil {
		return nil, fmt.Errorf("%s#%d is not in the local cache. Run poke sync first.", ref.Repository, ref.Number)
	}
	return issue, nil
}

func reference(issue *model.CachedIssue) string {
	return fmt.Sprintf("%s#%d", issue.Repository, issue.Number)
}

func (c *CLI) task(s *store.Store, account string, args []string) error {
	action, err := argument(args, 0)
	if err != nil {
		return err
	}
	if action == "add" {
		value, err := argument(args, 1)
		if err != nil {
			return err
		}
		issue, err := requireIssue(s, account, value)
		if err != nil {
			return err
		}
		title, err := text(args, 2, "task title")
		if err != nil {
			return err
		}
		task, err := s.AddTask(account, issue.NodeID, title)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "Added task %d to %s.\n", task.ID, reference(issue))
		return nil
	}
	if !slices.Contains([]string{"done", "reopen", "rename", "move", "delete"}, action) {
		return fmt.Errorf("Unknown task action \"%s\". Run poke help.", action)
	}
	value, err := argument(args, 1)
	if err != nil {
		return err
	}
	id, err := positiveInteger(value, "task ID")
	if err != nil {
		return err
	}
	switch action {
	case "done", "reopen":
		if _, err := s.SetCompleted(account, id, action == "done"); err != nil {
			return err
		}
		verb := "Completed"
		if action == "reopen" {
			verb = "Reopened"
		}
		fmt.Fprintf(c.Out, "%s task %d.\n", verb, id)
	case "rename":
		title, err := text(args, 2, "task title")
		if err != nil {
			return err
		}
		if _, err := s.RenameTask(account, id, title); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "Renamed task %d.\n", id)
	case "move":
		value, err := argument(args, 2)
		if err != nil {
			return err
		}
		position, err := positiveInteger(value, "position")
		if err != nil {
			return err
		}
		task, err := s.MoveTask(account, id, position)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "Moved task %d to position %d.\n", task.ID, task.Position)
	case "delete":
		if err := s.DeleteTask(account, id); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "Deleted task %d.\n", id)
	}
	return nil
}

func (c *CLI) note(s *store.Store, account string, args []string) error {
	action, err := argument(args, 0)
	if err != nil {
		return err
	}
	value, err := argument(args, 1)
	if err != nil {
		return err
	}
	issue, err := requireIssue(s, account, value)
	if err != nil {
		return err
	}
	switch action {
	case "set":
		body, err := text(args, 2, "note")
		if err != nil {
			return err
		}
		if err := s.SetNote(account, issue.NodeID, body); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "Saved note for %s.\n", reference(issue))
	case "show":
		body := "No note saved."
		if issue.Note != nil {
			body = *issue.Note
		}
		fmt.Fprintln(c.Out, body)
	default:
		return fmt.Errorf("Unknown note action \"%s\". Run poke help.", action)
	}
	return nil
}

func (c *CLI) printIssues(s *store.Store, account string, closed, stale bool) error {
	issues, err := s.ListIssues(account, closed, stale)
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		fmt.Fprintln(c.Out, "No matching cached issues.")
	}
	for _, issue := range issues {
		markers := issue.State
		if issue.OpenTaskCount > 0 {
			markers += fmt.Sprintf(" | %d next", issue.OpenTaskCount)
		}
		if issue.Note != nil && *issue.Note != "" {
			markers += " | note"
		}
		if issue.Stale {
			markers += " | stale"
		}
		fmt.Fprintf(c.Out, "%s %s [%s]\n", reference(&issue), issue.Title, markers)
	}
	return nil
}

func (c *CLI) printIssue(s *store.Store, issue *model.CachedIssue) error {
	fmt.Fprintf(c.Out, "%s %s\n", reference(issue), issue.Title)
	state := issue.State
	if issue.Stale {
		state += " | stale"
	}
	fmt.Fprintf(c.Out, "%s | %s\n", state, issue.URL)
	note := "(none)"
	if issue.Note != nil {
		note = *issue.Note
	}
	fmt.Fprintf(c.Out, "\nNote:\n%s\n\nTasks:\n", note)
	tasks, err := s.Tasks(issue.AccountNodeID, issue.NodeID)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		fmt.Fprintln(c.Out, "(none)")
	}
	for _, task := range tasks {
		marker := " "
		if task.Completed {
			marker = "x"
		}
		fmt.Fprintf(c.Out, "[%s] %d: %s\n", marker, task.ID, task.Title)
	}
	return nil
}

func (c *CLI) printNext(s *store.Store, account string) error {
	issues, err := s.ListNext(account)
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		fmt.Fprintln(c.Out, "No open local tasks.")
	}
	for _, issue := range issues {
		tasks, err := s.Tasks(account, issue.NodeID)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if !task.Completed {
				fmt.Fprintf(c.Out, "%d: %s (%s)\n", task.ID, task.Title, reference(&issue))
				break
			}
		}
	}
	return nil
}

func argument(args []string, index int) (string, error) {
	if index >= len(args) || args[index] == "" {
		return "", errors.New("Missing command argument. Run poke help.")
	}
	return args[index], nil
}

func text(args []string, index int, label string) (string, error) {
	var value string
	if index < len(args) {
		value = strings.TrimSpace(strings.Join(args[index:], " "))
	}
	if value == "" {
		return "", fmt.Errorf("Missing %s.", label)
	}
	return value, nil
}

func positiveInteger(value, label string) (int64, error) {
	value = strings.TrimSpace(value)
	// Match Number(value) for the numeric forms accepted by the old CLI.
	if len(value) > 2 && value[0] == '0' && strings.ContainsRune("xXoObB", rune(value[1])) {
		if number, err := strconv.ParseInt(value, 0, 64); err == nil && number > 0 {
			return number, nil
		}
	} else if number, err := strconv.ParseFloat(value, 64); err == nil && number > 0 && number < math.MaxInt64 && math.Trunc(number) == number {
		return int64(number), nil
	}
	return 0, fmt.Errorf("%s must be a positive integer.", label)
}

func defaultDatabasePath(goos string, getenv func(string) string, lookup func(string) (string, bool)) (string, error) {
	if path := getenv("POKE_DB_PATH"); path != "" {
		return path, nil
	}
	if goos == "windows" {
		base, set := lookup("LOCALAPPDATA")
		if !set {
			base = getenv("USERPROFILE")
		}
		if base == "" {
			return "", errors.New("Unable to determine the local data directory.")
		}
		return filepath.Join(base, "poke", "poke.db"), nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("HOME is not set.")
	}
	base, set := lookup("XDG_DATA_HOME")
	if !set {
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "poke", "poke.db"), nil
}

const help = `Poke: a local action layer for assigned GitHub issues

Usage:
  poke                              Sync and list open assigned issues
  poke sync [--org ORG]            Refresh the local issue cache
  poke issues [--all] [--stale]    List cached issues
  poke show OWNER/REPO#NUMBER       Show an issue, note, and tasks
  poke thread ISSUE [--json] [--gh] Capture an issue body and all comments
  poke summarize ISSUE [--provider ollama|mistral] [--json] [--gh]
  poke summarize --input FILE [--provider ollama|mistral] [--json]
                                    Summarize a full thread with cited evidence
                                    Ollama defaults to qwen3.8:27b on loopback
                                    --context-tokens N sets Ollama context (32768)
                                    Mistral uses mistral-medium-latest and MISTRAL_API_KEY
  poke next                         Show the first open task per issue
  poke task add ISSUE TITLE         Add a local task
  poke task done ID                 Complete a task
  poke task reopen ID               Reopen a task
  poke task rename ID TITLE         Rename a task
  poke task move ID POSITION        Reorder a task
  poke task delete ID               Delete a task
  poke note set ISSUE TEXT          Save or replace a private note
  poke note show ISSUE              Print a private note

Global options:
  --db PATH                         Override the SQLite database path
  --org ORG                         Limit GitHub synchronization to an organization
  -h, --help                        Show this help

ISSUE may be OWNER/REPO#NUMBER or a GitHub issue URL.`
