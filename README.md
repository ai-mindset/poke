# Poke 👉

A minimal CLI tool for GitHub that intelligently filters through notification noise, showing you exactly what needs your attention: PRs for review, assigned issues, and @ mentions.

## Problem

GitHub notifications are overwhelming. You get spammed with every comment, issue update, and workflow run. The important stuff (PRs you need to review, comments that mention you) gets buried.

## Solution

Poke filters GitHub items to show exactly what matters:

- Pull requests awaiting your personal or team review
- Issues assigned to you (open and recently closed)
- PRs you've recently reviewed
- Direct mentions (`@username`) and team mentions
- To-do list functionality to track issue progress and add notes

## Install

**Linux/macOS:**

```bash
curl -fsSL https://raw.githubusercontent.com/ai-mindset/poke/main/install.sh | bash
```

**Windows (PowerShell):**

```powershell
iwr https://raw.githubusercontent.com/ai-mindset/poke/main/install.ps1 | iex
```

## Setup

1. Create GitHub Personal Access Token (classic):
   - Go to [GitHub Settings → Tokens](https://github.com/settings/tokens)
   - Generate a classic token with the following scopes:
     - `repo` (required for searching private repositories and updating labels)
     - `notifications` (required for notification access)

2. Edit the config file created by the installer:
   ```bash
   # Linux/macOS
   nano ~/.config/poke/.env

   # Windows
   notepad %USERPROFILE%\AppData\Local\poke\.env
   ```

3. Add your GitHub details to the config:
   ```
   GITHUB_TOKEN=ghp_your_token_here
   WORK_ORGS=Your-Organization
   WORK_TEAMS=team1,team2,team3
   ```

4. Required environment variables:
   - `GITHUB_TOKEN`: Your GitHub personal access token (mandatory)
   - `WORK_ORGS`: Comma-separated list of your organizations (optional)
   - `WORK_TEAMS`: Comma-separated list of your teams (optional)

## Usage

**Check notifications:**

```bash
poke
```

**Filter for a specific organization:**

```bash
poke my-organization
```

**Control how many notifications to display:**

```bash
poke --notifications=15
```

**Use to-do list functionality:**

```bash
# View your to-do list (both assigned issues and manually added items)
poke --todo


# Update issue status (backlog, in-progress, blocked, review)
poke --update=org/repo#123 --status=in-progress

# Add notes to an issue (automatically fetches issue title and timestamps entries)
poke --update=org/repo#123 --note="Working on authentication fix"
# Notes accumulate with timestamps instead of overwriting

# Update both status and note
poke --update=org/repo#123 --status=blocked --note="Waiting for API access"

# Update and immediately show your to-do list
poke --update=org/repo#123 --note="Fixed bug" --show
```

**Standard view output:**

```
🔥 3 Pull Requests need your review:

✅ [org/repo] Add new authentication feature
   your review requested • https://github.com/org/repo/pull/123

✅ [org/repo] Fix bug in API response
   team review requested • https://github.com/org/repo/pull/456

🔄 [org/repo] Update documentation
   review_requested • https://github.com/org/repo/pull/789

📋 2 Issues assigned to you:

📝 [org/repo] Implement search feature
   #42 • todo • https://github.com/org/repo/issues/42

📝 [org/repo] Fix performance issue in dashboard
   #43 • todo • https://github.com/org/repo/issues/43
```

**To-do list view output:**

```
📋 Your To-Do List (4 issues):

🟨 In Progress (1):

🟨 [org/repo] Implement search feature
   #42 • in-progress • https://github.com/org/repo/issues/42
   📝 Note: Working on authentication integration

🟥 Blocked (1):

🟥 [org/repo] Fix performance issue in dashboard
   #43 • blocked • https://github.com/org/repo/issues/43
   📝 Note: Waiting for API access from team

⬜ Backlog (2):

⬜ [org/repo] Update documentation
   #44 • backlog • https://github.com/org/repo/issues/44

⬜ [org/repo] Add unit tests
   #45 • backlog • https://github.com/org/repo/issues/45

Commands:
  Update status: poke --update=org/repo#123 --status=in-progress
  Add note:      poke --update=org/repo#123 --note="Working on this now"
```

**Automate with cron (optional):**

```bash
# Check every 15 minutes
*/15 * * * * ~/.local/bin/poke
```

## How It Works

1. Uses the GitHub Search API to find:
   - PRs waiting for your review (personal or team)
   - Open issues assigned to you
   - Recently closed issues you were assigned to
   - PRs you've recently reviewed

2. Prioritizes items by importance:
   - Work organization items are boosted to the top
   - PRs needing review come first
   - Then open issues assigned to you
   - Followed by recently completed work

3. Tracks issue progress with a to-do system:
   - Updates GitHub issue labels to show status (visible to your team)
   - Stores your personal notes locally in `~/.poke/todo.json`
   - Notes accumulate with timestamps instead of overwriting
   - Automatically fetches and stores issue titles for context
   - Provides a unified view of issues grouped by status
   - Works with both assigned issues and manual entries

4. Sends desktop notifications with the most important items

**Result:** Signal without noise. See what needs your attention, ignore the rest, and track your progress effectively.

## Development

**Requirements:** Deno 2.0+

**Run from source:**

```bash
deno run --allow-env --allow-net --allow-read --allow-run poke.ts --notifications=12 # choose a number of notifications
deno run --allow-env --allow-net --allow-read --allow-run poke.ts --todo # run in to-do list mode
```

**Run using tasks:**

```bash
deno task dev         # Run in dev mode with debug output
deno task todo        # Run in to-do list mode
```

**Build binaries:**

```bash
deno task build
```

## License

MIT
