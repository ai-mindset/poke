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
- Issue-body and comment transcripts with source links, JSON export, and
  optional GitHub CLI authentication
- Cited summaries using local Ollama or an explicitly selected Mistral API
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

The installers download the executable from the latest GitHub release. Go and
Deno are not required. They verify the executable against the release's `SHA256SUMS`
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
Poke does not need the `notifications` scope. Synchronization uses the token
directly; thread capture also supports GitHub CLI authentication.

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
Synchronization needs the token; thread capture can use it or GitHub CLI
authentication. `issues`, `show`, `task`, `note`, and `next` use the local database.

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
and expose it only to the Poke process that needs it. For a non-persistent,
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
The Go executable requires `GITHUB_TOKEN` for synchronization and no longer reads
legacy plaintext token files.

### 4. Start working

Synchronization fetches open and closed issues assigned to your GitHub account;
the default listing shows current open issues:

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

Capture an issue's body and all accessible issue comments, even when the issue
is not assigned to you or present in the local cache:

```bash
poke thread owner/repository#42
poke thread https://github.com/owner/repository/issues/42 --json > thread.json
```

When `GITHUB_TOKEN` is present, capture uses the read-only REST API client.
Otherwise, it uses your existing GitHub CLI authentication (`gh auth login`).
Use `--gh` to explicitly select GitHub CLI. No token is extracted from its
credential store. GitHub CLI's own environment-token precedence still applies.
Thread capture does not open the local database or include private notes/tasks.

The transcript retains raw Markdown, authors, creation/edit timestamps, comment
IDs, and source URLs. Direct API capture follows comment pagination links;
GitHub CLI capture uses [`gh api --paginate --slurp`](https://cli.github.com/manual/gh_api).
Each CLI subprocess has a two-minute timeout and can be interrupted.
Readable transcripts escape terminal control characters; JSON preserves the
original source text.

`--json` emits the `issue_body_and_comments` scope, an issue object, comment
array, retrieval time, `complete`, and
`warnings`. Coverage is checked against GitHub's comment count, duplicate IDs,
and issue metadata before/after retrieval. Changes or mismatches are explicitly
marked incomplete. API calls are not an atomic snapshot; concurrent edits may
escape these checks. Failed pages produce an error without a partial transcript.

This increment covers github.com issue bodies and issue comments. Timeline
events, linked discussions, pull request reviews, and attachment contents are
outside its scope. Pull requests and other hosts are rejected explicitly.
Summarize a complete issue thread with local Ollama, using `qwen3.8:27b`:

```bash
poke summarize owner/repository#42
poke summarize owner/repository#42 --gh --json
```

Ollama must already serve that model. Poke uses `http://127.0.0.1:11434` by
default; set `POKE_OLLAMA_URL` for a different loopback port. Only HTTP(S)
loopback URLs are accepted. Poke does not download models or fall back to a
hosted provider. To disable Ollama's own cloud features, configure its server
with [`OLLAMA_NO_CLOUD=1`](https://docs.ollama.com/faq#how-do-i-disable-ollamas-cloud-features).

Select Mistral explicitly to use `mistral-medium-latest`:

```bash
# Supply MISTRAL_API_KEY to this process from your secret manager.
poke summarize owner/repository#42 --provider mistral
```

Selecting Mistral sends the requested issue body, comments, and attribution
metadata to `https://api.mistral.ai/v1/chat/completions`, including private issue
content when you select a private issue. `MISTRAL_API_KEY` is read from the
environment; no credential files are read. The prompt contains captured thread
data; Poke does not add GitHub credentials or local notes/tasks. Model HTTP requests have a five-minute timeout,
do not follow redirects, and are not automatically retried.

Use a saved capture for repeatable comparisons, or `--input -` for standard input:

```bash
poke thread owner/repository#42 --json > thread.json
poke summarize --input thread.json
poke summarize --input thread.json --provider mistral --json > summary.json
```

Summaries contain context, key points, explicit decisions, open questions, and
next actions. Each claim requires a known source ID and an exact supporting
excerpt from that source. Poke validates those excerpts and resolves their URLs
and authors from the transcript. Readable output shows source references;
`--json` also includes each excerpt, the provider, requested and returned model
names, source retrieval time, summary time, and a SHA-256 digest of source
content. This digest excludes retrieval time and does not authenticate an imported
file against GitHub. Summaries are printed, not stored in the task database.

Both providers receive the same schema and instructions to preserve disagreement,
qualify proposals, and recognize superseded decisions. The adapters use
[Ollama structured output](https://docs.ollama.com/capabilities/structured-outputs)
and [Mistral JSON schema output](https://docs.mistral.ai/studio/conversations/structured-output/custom).
Matching source excerpts verifies provenance; it cannot establish that a claim
is logically supported, correctly classified, or complete. Review consequential
decisions against their sources before relying on them.

The first version uses one complete prompt, with no text truncation or chunking
in Poke. It rejects incomplete captures, inconsistent source IDs/counts, input
files over 1 MiB, and serialized prompt content over 256 KiB. Context-limit errors,
incomplete output, invalid JSON, and invalid citations fail without accepting a
summary. The prompt asks for 200-400 words; each of the five sections has at most
six claims. Output generation is capped at 4096 tokens.

Ollama requests use temperature zero, thinking disabled, and 32768 context tokens.
Increase context explicitly when your model and available memory support it:

```bash
poke summarize owner/repository#42 --context-tokens 65536
```

Poke sends `truncate: false` and `shift: false` to Ollama; use a server that
supports these controls. Poke cannot verify how an arbitrary local server handles
them. There is no automatic move to Mistral when local inference fails. Long-thread
chunking and automatic refresh are separate future increments.

## Troubleshooting

- `No matching cached issues.` means the local listing found no matching issues.
  Try `poke sync` without `--org`, or `poke issues --all --stale`.
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
Organization-limited synchronization marks missing issues stale only within that
organization. Existing Deno databases work directly with Go; no conversion is
needed. Notes, task IDs, completion, order, and timestamps are retained.

GitHub requests time out after 30 seconds per attempt. Rate-limited requests are
retried at most once, respecting GitHub's wait headers. If the required wait
exceeds 60 seconds, synchronization fails with a retry delay instead of retrying
early. Failed or cancelled synchronization does not replace the cached work.

## Development

Use Go 1.27.3 or newer:

```bash
go run ./cmd/poke help
go vet ./...
go test ./...
gofmt -l cmd internal
```

Build a self-contained executable without a C compiler:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/poke ./cmd/poke
./dist/poke --help
```

Cross-compile by setting `GOOS` and `GOARCH`, for example:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/poke-linux-aarch64 ./cmd/poke
```

Supported combinations are Linux and macOS (`amd64`, `arm64`) and Windows
(`amd64`). The only direct Go module dependency is the
[CGO-free SQLite driver](https://pkg.go.dev/modernc.org/sqlite).

Go tests are self-contained: CLI regression tests use output captured from the
pre-Go implementation, and storage compatibility tests load its frozen synthetic
SQLite schema/data snapshot. They check that opening an existing database preserves
its schema and data, then independently query Go's writes for notes, task IDs,
ordering, timestamps, account isolation, and retained stale work. CI requires only Go:

```bash
go vet ./...
go test ./...
```

Set `POKE_TEST_BINARY` to an absolute executable path to include packaged-binary
help, database, and exit-code smoke tests. Tests use synthetic data and temporary
databases; no GitHub token is needed.

Go rewrite baseline measurements on Linux amd64, before thread capture was
added, against the checksum-verified v2.0.0 release:

| Measurement                      |     v2.0.0 |         Go |
| -------------------------------- | ---------: | ---------: |
| Executable bytes                 | 61,801,504 | 11,227,296 |
| Median `--help` wall time        |    81.0 ms |     2.1 ms |
| Median cached `issues` wall time |    90.3 ms |     3.4 ms |

Timing used 30 subprocess runs per command after three warmups, with randomized
interleaving on the same machine and identical synthetic issue data. Go used
the build flags above. These are local measurements, not cross-platform or
cold-cache guarantees.

## Releases

[`release.yml`](.github/workflows/release.yml) runs checks and builds each
supported target independently with Go 1.27.3 and CGO disabled. Each target runs
tests and executable smoke tests on a matching runner. CI does not install Deno
or invoke the retained TypeScript implementation. A manual workflow run makes the
executables available as workflow artifacts. Supplying an existing `v*` tag in
the manual run publishes that tag, which provides a recovery path if its push
event was missed. Pushing a `v*` tag also creates a GitHub release containing all
five executables, generated release notes, and `SHA256SUMS`; the release is
created only after every build succeeds.

The source is split into:

- `cmd/poke`: executable entrypoint and interruption handling
- `internal/github`: read-only GitHub API client and pagination
- `internal/store`: existing SQLite schema and local operations
- `internal/cli`: command parsing and presentation
- `internal/model`: domain types and issue-reference parsing
- `internal/summary`: model adapters, summary schema, prompts, and evidence validation

The retained `src/` implementation and Deno tasks are available for manual
reference. Go builds, tests, and releases do not depend on them.

## Current boundary

Poke deliberately does not yet publish tasks or notes to GitHub, synchronize
between devices, provide shared boards, or implement sprints and estimation.
Those features can be added later without changing the local data boundary.

The `summarize` command supports local `qwen3.8:27b` and hosted
`mistral-medium-latest`. Evaluate semantic accuracy on representative captured
threads before adding automatic refresh or cross-issue synthesis. The automated
tests use synthetic transcripts and model responses; they do not measure the
selected models' factual accuracy. Browser extensions and shared services remain
deferred.

The synthetic fixture below provides a repeatable first manual model comparison:

```bash
poke summarize --input internal/summary/testdata/decision_reversal.json
poke summarize --input internal/summary/testdata/decision_reversal.json --provider mistral
```

Check that both retain the full-cache proposal and objection, identify per-tenant
invalidation as the latest stated decision, mark the per-key decision as
superseded, leave TTL unresolved, and attribute the regression-test offer to
Alice. The fixture is synthetic; its GitHub URLs are source identifiers, not
links to a real discussion. Repeat evaluation on representative real threads
before relying on model output.

## License

MIT
