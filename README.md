# Ping

A minimal CLI tool to filter GitHub notification noise, showing only PRs and @ mentions that need your attention.

## Problem

GitHub notifications are overwhelming. You get spammed with every comment, issue update, and workflow run. The important stuff (PRs you need to review, comments that mention you) gets buried.

## Solution

Ping filters notifications to show only what matters:

- Pull requests
- Direct mentions (`@username`)
- Team mentions
- Review requests
- Assignments

Uses GitHub's `participating=true` filter to eliminate 80% of noise upfront.

## Install

**Linux/macOS:**

```bash
curl -fsSL https://raw.githubusercontent.com/ai-mindset/ping/main/install.sh | bash
```

**Windows (PowerShell):**

```powershell
iwr https://raw.githubusercontent.com/ai-mindset/ping/main/install.ps1 | iex
```

## Setup

1. Create GitHub Personal Access Token (classic):
   - Go to [GitHub Settings → Tokens](https://github.com/settings/tokens)
   - Generate a classic token with "Notifications" read permission (fine-grained tokens don't support the notifications API)

2. Create `.env` file:
   ```bash
   [[ ! -d ~/.config/ping ]] && mkdir -p ~/.config/ping
   echo "GITHUB_TOKEN=ghp_your_token_here" > ~/.config/ping/.env
   chmod 600 .env  # Secure the file
   ```

## Usage

**Check notifications:**

```bash
ping
```

**Sample output:**

```
3 important notification(s):

🔄 [owner/repo] Fix authentication bug
   review_requested • https://github.com/owner/repo/pull/123

💬 [team/project] Discussion about feature X
   mention • https://github.com/team/project/issues/456
```

**Automate with cron (optional):**

```bash
# Check every 15 minutes
*/15 * * * * cd /path/to/project && /path/to/ping
```

## How It Works

1. Fetches GitHub notifications with `participating=true` (you're directly involved)
2. Filters for PRs and priority reasons (mentions, reviews, assignments)
3. Displays unread notifications only
4. Converts API URLs to web URLs for easy clicking

**Result:** Signal without noise. See what needs your attention, ignore the rest.

## Development

**Requirements:** Deno 2.0+

**Run from source:**

```bash
deno run --allow-env --allow-net --allow-read ping.ts
```

**Build binaries:**

```bash
deno run --allow-run --allow-write build.ts
```

## License

MIT
