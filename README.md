# Poke

Poke is a local action layer for GitHub issues. It synchronizes issues assigned
to the authenticated account, then lets you keep private notes and lightweight
tasks under each issue without adding noise to the shared GitHub history.

GitHub remains authoritative for issue metadata. Notes, task order, completion,
and focus state remain in a local SQLite database.

## Features

- Account-wide assigned-issue synchronization, optionally limited to one
  organization
- Persistent private notes per issue
- Ordered, completable local tasks per issue
- A consolidated `next` view across open issues
- Offline access to previously synchronized work
- Retention of annotations when an issue closes, is unassigned, or becomes
  inaccessible
- No GitHub mutations: synchronization is read-only

## Quick start

### 1. Install Poke

Linux and macOS (x86-64 or ARM64):

```bash
curl -fsSL https://raw.githubusercontent.com/ai-mindset/poke/main/install.sh | bash
export PATH="$HOME/.local/bin:$PATH"
poke --help
```

Windows x86-64 (PowerShell):

```powershell
irm https://raw.githubusercontent.com/ai-mindset/poke/main/install.ps1 | iex
poke --help
```

The installers download the executable from the latest GitHub release. Deno is
not required. They verify the executable against the release's `SHA256SUMS`
before installing it. If you prefer not to pipe a script into a shell, download
and inspect the installer first, or download the matching executable and
`SHA256SUMS` directly from the
[latest release](https://github.com/ai-mindset/poke/releases/latest).

### 2. Create a GitHub token

Create a [fine-grained personal access token](https://github.com/settings/personal-access-tokens/new)
with:

- **Resource owner:** the organization whose private repositories you need
- **Expiration:** the shortest period that is practical for you
- **Repository access:** **All repositories** if listing them individually is
  impractical
- **Repository permissions:** **Issues: Read-only**; GitHub adds
  **Metadata: Read-only** automatically
- **Account and organization permissions:** none

This token can read public repositories and the selected organization's private
repositories, subject to that organization's approval and SSO policies. It
cannot modify issues, notes, or repository content. A fine-grained token has one
resource owner; use a classic PAT with the broader `repo` scope only if you must
cross several resource owners that cannot be covered by fine-grained tokens.
Poke does not need the `notifications` scope and does not use the GitHub CLI.

### 3. Keep the token out of files and shell history

On Linux desktops, store it in Secret Service (GNOME Keyring, KWallet, or a
compatible keyring):

```bash
secret-tool store \
  --label="Poke GitHub PAT" \
  application poke \
  host github.com
```

At the `Password:` prompt, paste the PAT and press Enter. Then synchronize with:

```bash
GITHUB_TOKEN="$(
  secret-tool lookup application poke host github.com
)" poke sync
```

`secret-tool` is supplied by the `libsecret-tools` package on Debian and Ubuntu.
Only synchronization needs the token; `issues`, `show`, `task`, `note`, and
`next` use the local database.

For convenient synchronization from zsh, add this to `~/.zshrc`:

```zsh
export PATH="$HOME/.local/bin:$PATH"

poke-sync() {
  local token
  if ! token="$(secret-tool lookup application poke host github.com)"; then
    print -u2 "Could not read the Poke GitHub PAT from Secret Service."
    return 1
  fi
  if [[ -z "$token" ]]; then
    print -u2 "No Poke GitHub PAT was found in Secret Service."
    return 1
  fi
  GITHUB_TOKEN="$token" command poke sync "$@"
}
```

Run `source ~/.zshrc` once, then use `poke-sync` or
`poke-sync --org example-org`. The token exists only in the environment of that
one Poke process.

On macOS or Windows, store the PAT in your system keychain or password manager
and expose it only to the synchronization process. For a non-persistent,
history-free macOS session:

```bash
printf "GitHub PAT: "
stty -echo
IFS= read -r GITHUB_TOKEN
stty echo
printf "\n"
GITHUB_TOKEN="$GITHUB_TOKEN" poke sync
unset GITHUB_TOKEN
```

For a non-persistent, history-free PowerShell session:

```powershell
$secureToken = Read-Host "GitHub PAT" -AsSecureString
$env:GITHUB_TOKEN = [Net.NetworkCredential]::new("", $secureToken).Password
poke sync
Remove-Item Env:\GITHUB_TOKEN
$secureToken = $null
```

Avoid saving the PAT in `.env`, shell-profile, or PowerShell-profile files.
Legacy Poke plaintext token files are still read for compatibility, but emit a
warning.

### 4. Start working

The first synchronization fetches open issues assigned to your GitHub account:

```bash
# Linux with Secret Service
GITHUB_TOKEN="$(secret-tool lookup application poke host github.com)" poke sync

# Or, when GITHUB_TOKEN is already present in this shell
poke sync
```

Limit a synchronization to one organization when desired:

```bash
poke sync --org example-org
```

After synchronizing, the following commands work offline.

## Usage

List or inspect cached issues:

```bash
poke issues
poke issues --all
poke issues --all --stale
poke show owner/repository#42
poke show https://github.com/owner/repository/issues/42
```

Add and manage local tasks:

```bash
poke task add owner/repository#42 "Reproduce the failure"
poke task add owner/repository#42 "Add a regression test"
poke task done 1
poke task reopen 1
poke task rename 1 "Reproduce on Linux"
poke task move 2 1
poke task delete 1
poke next
```

Save or read the issue's private note:

```bash
poke note set owner/repository#42 "Waiting for staging credentials"
poke note show owner/repository#42
```

Run `poke help` for the complete command summary.

## Troubleshooting

- `No items found` means the query found no open issues assigned to the
  authenticated user. Try `poke sync` without `--org`.
- Private repositories missing from results usually mean the organization has
  not approved the token, SSO authorization is incomplete, or the token's
  repository selection is too narrow.
- If `poke` is not found after installation, start a new terminal or add
  `~/.local/bin` (Linux/macOS) or `%USERPROFILE%\.local\bin` (Windows) to
  `PATH`.
- Use `poke issues --all --stale` to inspect cached issues that closed, became
  unassigned, or became inaccessible. Local notes and tasks are retained.

## Local data

The default database is:

- Linux/macOS: `$XDG_DATA_HOME/poke/poke.db`, or
  `~/.local/share/poke/poke.db`
- Windows: `%LOCALAPPDATA%\poke\poke.db`

Override it with `POKE_DB_PATH` or `--db PATH`. The database is private only
to the extent that the operating system account and disk are protected; it is
not encrypted by Poke.

Each cached issue is keyed by the authenticated GitHub account and GitHub's
global node ID. A complete synchronization marks missing issues stale rather
than deleting them, preserving all local notes and tasks. If GitHub reports a
partial search result, Poke does not mark unseen issues stale.

## Development

```bash
deno task dev -- help
deno task test
deno task check
```

Package a self-contained QuickJS executable for the current platform (requires
Deno 2.9.5 or newer):

```bash
deno task build:current
./dist/poke --help
```

QuickJS substantially reduces executable size and startup overhead, but the
backend is experimental and Deno warns that it does not receive the same
security updates as V8. Build the larger V8 variant when that tradeoff is not
acceptable:

```bash
deno task build:current:v8
./dist/poke-v8 --help
```

Cross-compile all five release targets with QuickJS:

```bash
deno task build
```

This produces Linux x86-64/ARM64, macOS x86-64/ARM64, and Windows x86-64
executables in `dist/`. Deno supports cross-compiling these targets from any
host.

## Releases

[`release.yml`](.github/workflows/release.yml) runs checks and builds each
supported target independently with Deno 2.9.5. A manual workflow run makes the
executables available as workflow artifacts. Supplying an existing `v*` tag in
the manual run publishes that tag, which provides a recovery path if its push
event was missed. Pushing a `v*` tag also creates a GitHub release containing all
five executables, generated release notes, and `SHA256SUMS`; the release is
created only after every build succeeds.

The source is split into:

- `src/github.ts`: read-only GitHub API client and pagination
- `src/store.ts`: SQLite schema, migrations, and local operations
- `src/cli.ts`: command parsing and presentation
- `src/models.ts`: shared domain types and issue-reference parsing

## Current boundary

Poke deliberately does not yet publish tasks or notes to GitHub, synchronize
between devices, provide shared boards, or implement sprints and estimation.
Those features can be added later without changing the local data boundary.

## License

MIT
