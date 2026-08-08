import { dirname, join } from "node:path";
import { GitHubClient } from "./github.ts";
import type { CachedIssue, GitHubAccount } from "./models.ts";
import { parseIssueReference } from "./models.ts";
import { PokeStore } from "./store.ts";

interface GlobalOptions {
  dbPath?: string;
  org?: string;
  help: boolean;
  args: string[];
}

export async function main(args: string[]): Promise<void> {
  try {
    await run(args);
  } catch (error) {
    console.error(`Error: ${(error as Error).message}`);
    Deno.exitCode = 1;
  }
}

export async function run(args: string[]): Promise<void> {
  const options = parseGlobalOptions(args);
  if (options.help || options.args[0] === "help") {
    printHelp();
    return;
  }

  const dbPath = options.dbPath ?? getDefaultDatabasePath();
  if (dbPath !== ":memory:") {
    await Deno.mkdir(dirname(dbPath), { recursive: true });
  }

  const store = new PokeStore(dbPath);
  try {
    const command = options.args[0];
    if (!command) {
      const account = await sync(store, options.org);
      printIssues(store.listIssues(account.nodeId));
      return;
    }

    switch (command) {
      case "sync": {
        await sync(store, options.org);
        return;
      }
      case "issues": {
        const account = requireAccount(store);
        printIssues(store.listIssues(account.nodeId, {
          includeClosed: options.args.includes("--all"),
          includeStale: options.args.includes("--stale"),
        }));
        return;
      }
      case "next": {
        const account = requireAccount(store);
        printNext(store, account);
        return;
      }
      case "show": {
        const account = requireAccount(store);
        const issue = requireIssue(
          store,
          account,
          requireArgument(options.args, 1),
        );
        printIssue(store, issue);
        return;
      }
      case "task": {
        handleTask(store, requireAccount(store), options.args.slice(1));
        return;
      }
      case "note": {
        handleNote(store, requireAccount(store), options.args.slice(1));
        return;
      }
      default: {
        // Preserve the old `poke my-org` shorthand while making account-wide
        // synchronization the default.
        if (options.args.length === 1 && !command.startsWith("-")) {
          const account = await sync(store, command);
          printIssues(store.listIssues(account.nodeId));
          return;
        }
        throw new Error(`Unknown command "${command}". Run poke help.`);
      }
    }
  } finally {
    store.close();
  }
}

async function sync(store: PokeStore, org?: string): Promise<GitHubAccount> {
  const token = await resolveToken();
  const client = new GitHubClient(token);
  const account = await client.getAccount();
  const result = await client.fetchAssignedIssues(org);
  store.syncIssues(account, result.issues, { complete: result.complete });

  console.log(
    `Synced ${result.issues.length} assigned issues for @${account.login}${
      org ? ` in ${org}` : ""
    }.`,
  );
  if (!result.complete) {
    console.warn(
      "GitHub returned a partial result. Existing cached issues were retained as current.",
    );
  }
  return account;
}

function handleTask(
  store: PokeStore,
  account: GitHubAccount,
  args: string[],
): void {
  const action = requireArgument(args, 0);
  switch (action) {
    case "add": {
      const issue = requireIssue(store, account, requireArgument(args, 1));
      const title = requireText(args, 2, "task title");
      const task = store.addTask(account.nodeId, issue.nodeId, title);
      console.log(`Added task ${task.id} to ${formatReference(issue)}.`);
      return;
    }
    case "done":
    case "reopen": {
      const taskId = parsePositiveInteger(requireArgument(args, 1), "task ID");
      const task = store.setTaskCompleted(
        account.nodeId,
        taskId,
        action === "done",
      );
      console.log(
        `${action === "done" ? "Completed" : "Reopened"} task ${task.id}.`,
      );
      return;
    }
    case "rename": {
      const taskId = parsePositiveInteger(requireArgument(args, 1), "task ID");
      const task = store.renameTask(
        account.nodeId,
        taskId,
        requireText(args, 2, "task title"),
      );
      console.log(`Renamed task ${task.id}.`);
      return;
    }
    case "move": {
      const taskId = parsePositiveInteger(requireArgument(args, 1), "task ID");
      const position = parsePositiveInteger(
        requireArgument(args, 2),
        "position",
      );
      const task = store.moveTask(account.nodeId, taskId, position);
      console.log(`Moved task ${task.id} to position ${task.position}.`);
      return;
    }
    case "delete": {
      const taskId = parsePositiveInteger(requireArgument(args, 1), "task ID");
      store.deleteTask(account.nodeId, taskId);
      console.log(`Deleted task ${taskId}.`);
      return;
    }
    default:
      throw new Error(`Unknown task action "${action}". Run poke help.`);
  }
}

function handleNote(
  store: PokeStore,
  account: GitHubAccount,
  args: string[],
): void {
  const action = requireArgument(args, 0);
  const issue = requireIssue(store, account, requireArgument(args, 1));

  if (action === "set") {
    store.setNote(account.nodeId, issue.nodeId, requireText(args, 2, "note"));
    console.log(`Saved note for ${formatReference(issue)}.`);
    return;
  }
  if (action === "show") {
    console.log(issue.note ?? "No note saved.");
    return;
  }
  throw new Error(`Unknown note action "${action}". Run poke help.`);
}

function printIssues(issues: CachedIssue[]): void {
  if (issues.length === 0) {
    console.log("No matching cached issues.");
    return;
  }
  for (const issue of issues) {
    const taskSummary = issue.openTaskCount > 0
      ? ` | ${issue.openTaskCount} next`
      : "";
    const noteMarker = issue.note ? " | note" : "";
    const staleMarker = issue.stale ? " | stale" : "";
    console.log(
      `${
        formatReference(issue)
      } ${issue.title} [${issue.state}${taskSummary}${noteMarker}${staleMarker}]`,
    );
  }
}

function printIssue(store: PokeStore, issue: CachedIssue): void {
  console.log(`${formatReference(issue)} ${issue.title}`);
  console.log(`${issue.state}${issue.stale ? " | stale" : ""} | ${issue.url}`);
  console.log(`\nNote:\n${issue.note ?? "(none)"}`);
  console.log("\nTasks:");
  const tasks = store.getTasks(issue.accountNodeId, issue.nodeId);
  if (tasks.length === 0) {
    console.log("(none)");
    return;
  }
  for (const task of tasks) {
    console.log(`${task.completed ? "[x]" : "[ ]"} ${task.id}: ${task.title}`);
  }
}

function printNext(store: PokeStore, account: GitHubAccount): void {
  const issues = store.listNext(account.nodeId);
  if (issues.length === 0) {
    console.log("No open local tasks.");
    return;
  }

  for (const issue of issues) {
    const task = store.getTasks(account.nodeId, issue.nodeId).find((item) =>
      !item.completed
    );
    if (task) {
      console.log(`${task.id}: ${task.title} (${formatReference(issue)})`);
    }
  }
}

function requireAccount(store: PokeStore): GitHubAccount {
  const account = store.getLastAccount();
  if (!account) throw new Error("No synced account. Run poke sync first.");
  return account;
}

function requireIssue(
  store: PokeStore,
  account: GitHubAccount,
  value: string,
): CachedIssue {
  const reference = parseIssueReference(value);
  const issue = store.findIssue(account.nodeId, reference);
  if (!issue) {
    throw new Error(
      `${reference.repository}#${reference.number} is not in the local cache. Run poke sync first.`,
    );
  }
  return issue;
}

function formatReference(issue: CachedIssue): string {
  return `${issue.repository}#${issue.number}`;
}

function parseGlobalOptions(args: string[]): GlobalOptions {
  const result: GlobalOptions = { help: false, args: [] };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === "--help" || arg === "-h") {
      result.help = true;
    } else if (arg === "--") {
      continue;
    } else if (arg === "--db" || arg === "--org") {
      const value = args[index + 1];
      if (!value) throw new Error(`${arg} requires a value.`);
      if (arg === "--db") result.dbPath = value;
      else result.org = value;
      index += 1;
    } else if (arg.startsWith("--db=")) {
      result.dbPath = arg.slice("--db=".length);
    } else if (arg.startsWith("--org=")) {
      result.org = arg.slice("--org=".length);
    } else {
      result.args.push(arg);
    }
  }
  return result;
}

function requireArgument(args: string[], index: number): string {
  const value = args[index];
  if (!value) throw new Error("Missing command argument. Run poke help.");
  return value;
}

function requireText(args: string[], index: number, label: string): string {
  const value = args.slice(index).join(" ").trim();
  if (!value) throw new Error(`Missing ${label}.`);
  return value;
}

function parsePositiveInteger(value: string, label: string): number {
  const number = Number(value);
  if (!Number.isInteger(number) || number < 1) {
    throw new Error(`${label} must be a positive integer.`);
  }
  return number;
}

function getDefaultDatabasePath(): string {
  const override = Deno.env.get("POKE_DB_PATH");
  if (override) return override;

  if (Deno.build.os === "windows") {
    const base = Deno.env.get("LOCALAPPDATA") ?? Deno.env.get("USERPROFILE");
    if (!base) throw new Error("Unable to determine the local data directory.");
    return join(base, "poke", "poke.db");
  }

  const home = Deno.env.get("HOME");
  if (!home) throw new Error("HOME is not set.");
  return join(
    Deno.env.get("XDG_DATA_HOME") ?? join(home, ".local", "share"),
    "poke",
    "poke.db",
  );
}

async function resolveToken(): Promise<string> {
  const environmentToken = Deno.env.get("GITHUB_TOKEN");
  if (environmentToken) return environmentToken;

  const legacyToken = await readLegacyToken();
  if (legacyToken) {
    console.warn(
      "Using the legacy plaintext Poke token file. Prefer GITHUB_TOKEN.",
    );
    return legacyToken;
  }

  throw new Error(
    "GitHub authentication required. Set GITHUB_TOKEN to a personal access token.",
  );
}

async function readLegacyToken(): Promise<string | null> {
  const home = Deno.env.get("HOME") ?? Deno.env.get("USERPROFILE");
  const localAppData = Deno.env.get("LOCALAPPDATA");
  const candidates = [
    home ? join(home, ".config", "poke", ".env") : null,
    localAppData ? join(localAppData, "poke", ".env") : null,
  ].filter((path): path is string => path !== null);

  for (const path of candidates) {
    try {
      const contents = await Deno.readTextFile(path);
      const match = contents.match(/^GITHUB_TOKEN=(.+)$/m);
      if (match?.[1]) return match[1].trim().replace(/^['"]|['"]$/g, "");
    } catch (error) {
      if (!(error instanceof Deno.errors.NotFound)) throw error;
    }
  }
  return null;
}

function printHelp(): void {
  console.log(`Poke: a local action layer for assigned GitHub issues

Usage:
  poke                              Sync and list open assigned issues
  poke sync [--org ORG]            Refresh the local issue cache
  poke issues [--all] [--stale]    List cached issues
  poke show OWNER/REPO#NUMBER       Show an issue, note, and tasks
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

ISSUE may be OWNER/REPO#NUMBER or a GitHub issue URL.`);
}
