# Pingh

A minimal CLI tool for GitHub that intelligently filters through notification noise, showing you exactly what needs your attention: PRs for review, assigned issues, and @ mentions.

## Problem

GitHub notifications are overwhelming. You get spammed with every comment, issue update, and workflow run. The important stuff (PRs you need to review, comments that mention you) gets buried.

## Solution

Ping filters GitHub items to show exactly what matters:

- Pull requests awaiting your personal or team review
- Issues assigned to you (open and recently closed)
- PRs you've recently reviewed
- Direct mentions (`@username`) and team mentions

## Install

**Linux/macOS:**

```bash
curl -fsSL https://raw.githubusercontent.com/ai-mindset/pingh/main/install.sh | bash
```

**Windows (PowerShell):**

```powershell
iwr https://raw.githubusercontent.com/ai-mindset/pingh/main/install.ps1 | iex
```

## Setup

1. Create GitHub Personal Access Token (classic):
   - Go to [GitHub Settings → Tokens](https://github.com/settings/tokens)
   - Generate a classic token with the following scopes:
     - `repo` (required for searching private repositories)
     - `notifications` (required for notification access)

2. Edit the config file created by the installer:
   ```bash
   # Linux/macOS
   nano ~/.config/pingh/.env

   # Windows
   notepad %USERPROFILE%\AppData\Local\pingh\.env
   ```

3. Add your GitHub details to the config:
   ```
   GITHUB_TOKEN=ghp_your_token_here
   GITHUB_USERNAME=your_username
   WORK_ORGS=Your-Organization
   WORK_TEAMS=team1,team2,team3
   ```

## Usage

**Check notifications:**

```bash
pingh
```

**Filter for a specific organization:**

```bash
pingh my-organization
```

**Control how many notifications to display:**

```bash
pingh --notifications=15
```

**Sample output:**

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

**Automate with cron (optional):**

```bash
# Check every 15 minutes
*/15 * * * * ~/.local/bin/pingh
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

3. Sends desktop notifications with the most important items

**Result:** Signal without noise. See what needs your attention, ignore the rest.

## Development

**Requirements:** Deno 2.0+

**Run from source:**

```bash
deno run --allow-env --allow-net --allow-read --allow-run pingh.ts
```

**Build binaries:**

```bash
deno task build
```

## License

MIT
