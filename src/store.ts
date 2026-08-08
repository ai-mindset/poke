import { DatabaseSync } from "node:sqlite";
import type {
  CachedIssue,
  GitHubAccount,
  GitHubIssue,
  IssueReference,
  LocalTask,
} from "./models.ts";

interface IssueRow extends Record<string, unknown> {
  account_node_id: string;
  node_id: string;
  number: number;
  title: string;
  url: string;
  repository: string;
  assignees: string;
  state: "open" | "closed";
  updated_at: string;
  last_seen_at: string;
  stale: number;
  note: string | null;
  open_task_count: number;
  completed_task_count: number;
}

interface TaskRow extends Record<string, unknown> {
  id: number;
  account_node_id: string;
  issue_node_id: string;
  title: string;
  completed: number;
  position: number;
  created_at: string;
  updated_at: string;
}

const ISSUE_SELECT = `
  SELECT
    i.account_node_id,
    i.node_id,
    i.number,
    i.title,
    i.url,
    i.repository,
    i.assignees,
    i.state,
    i.updated_at,
    i.last_seen_at,
    i.stale,
    n.body AS note,
    COUNT(CASE WHEN t.completed = 0 THEN 1 END) AS open_task_count,
    COUNT(CASE WHEN t.completed = 1 THEN 1 END) AS completed_task_count
  FROM issues_cache i
  LEFT JOIN notes n
    ON n.account_node_id = i.account_node_id
    AND n.issue_node_id = i.node_id
  LEFT JOIN tasks t
    ON t.account_node_id = i.account_node_id
    AND t.issue_node_id = i.node_id
`;

export class PokeStore {
  readonly #db: DatabaseSync;

  constructor(path = ":memory:") {
    this.#db = new DatabaseSync(path);
    this.#migrate();
  }

  close(): void {
    this.#db.close();
  }

  syncIssues(
    account: GitHubAccount,
    issues: GitHubIssue[],
    options: { complete?: boolean; syncedAt?: string } = {},
  ): void {
    const syncedAt = options.syncedAt ?? new Date().toISOString();
    const complete = options.complete ?? true;

    this.#db.exec("BEGIN IMMEDIATE");
    try {
      this.#db.prepare(`
        INSERT INTO accounts (node_id, host, login, synced_at)
        VALUES (?, ?, ?, ?)
        ON CONFLICT(node_id) DO UPDATE SET
          host = excluded.host,
          login = excluded.login,
          synced_at = excluded.synced_at
      `).run(account.nodeId, account.host, account.login, syncedAt);

      if (complete) {
        this.#db.prepare(`
          UPDATE issues_cache SET stale = 1 WHERE account_node_id = ?
        `).run(account.nodeId);
      }

      const upsert = this.#db.prepare(`
        INSERT INTO issues_cache (
          account_node_id, node_id, number, title, url, repository, state,
          assignees, updated_at, last_seen_at, stale
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
        ON CONFLICT(account_node_id, node_id) DO UPDATE SET
          number = excluded.number,
          title = excluded.title,
          url = excluded.url,
          repository = excluded.repository,
          state = excluded.state,
          assignees = excluded.assignees,
          updated_at = excluded.updated_at,
          last_seen_at = excluded.last_seen_at,
          stale = 0
      `);

      for (const issue of issues) {
        upsert.run(
          account.nodeId,
          issue.nodeId,
          issue.number,
          issue.title,
          issue.url,
          issue.repository,
          issue.state,
          JSON.stringify(issue.assignees),
          issue.updatedAt,
          syncedAt,
        );
      }

      this.#db.prepare(`
        INSERT INTO sync_state (account_node_id, key, value, updated_at)
        VALUES (?, 'issues:last_sync', ?, ?)
        ON CONFLICT(account_node_id, key) DO UPDATE SET
          value = excluded.value,
          updated_at = excluded.updated_at
      `).run(account.nodeId, syncedAt, syncedAt);
      this.#db.exec("COMMIT");
    } catch (error) {
      this.#db.exec("ROLLBACK");
      throw error;
    }
  }

  listIssues(
    accountNodeId: string,
    options: { includeClosed?: boolean; includeStale?: boolean } = {},
  ): CachedIssue[] {
    const conditions = ["i.account_node_id = ?"];
    const parameters: Array<string | number> = [accountNodeId];

    if (!options.includeClosed) conditions.push("i.state = 'open'");
    if (!options.includeStale) conditions.push("i.stale = 0");

    const rows = this.#db.prepare(`
      ${ISSUE_SELECT}
      WHERE ${conditions.join(" AND ")}
      GROUP BY i.account_node_id, i.node_id
      ORDER BY i.stale ASC, i.updated_at DESC
    `).all(...parameters) as IssueRow[];

    return rows.map(mapIssueRow);
  }

  listNext(accountNodeId: string): CachedIssue[] {
    const rows = this.#db.prepare(`
      ${ISSUE_SELECT}
      WHERE i.account_node_id = ?
        AND i.stale = 0
        AND i.state = 'open'
        AND EXISTS (
          SELECT 1 FROM tasks next_task
          WHERE next_task.account_node_id = i.account_node_id
            AND next_task.issue_node_id = i.node_id
            AND next_task.completed = 0
        )
      GROUP BY i.account_node_id, i.node_id
      ORDER BY MIN(CASE WHEN t.completed = 0 THEN t.position END), i.updated_at DESC
    `).all(accountNodeId) as IssueRow[];

    return rows.map(mapIssueRow);
  }

  findIssue(
    accountNodeId: string,
    reference: IssueReference,
  ): CachedIssue | null {
    const row = this.#db.prepare(`
      ${ISSUE_SELECT}
      WHERE i.account_node_id = ?
        AND lower(i.repository) = lower(?)
        AND i.number = ?
      GROUP BY i.account_node_id, i.node_id
    `).get(accountNodeId, reference.repository, reference.number) as
      | IssueRow
      | undefined;

    return row ? mapIssueRow(row) : null;
  }

  addTask(
    accountNodeId: string,
    issueNodeId: string,
    title: string,
    now = new Date().toISOString(),
  ): LocalTask {
    const normalizedTitle = title.trim();
    if (!normalizedTitle) throw new Error("Task title cannot be empty.");

    const position = this.#db.prepare(`
      SELECT COALESCE(MAX(position), 0) + 1 AS position
      FROM tasks WHERE account_node_id = ? AND issue_node_id = ?
    `).get(accountNodeId, issueNodeId) as { position: number };

    const result = this.#db.prepare(`
      INSERT INTO tasks (
        account_node_id, issue_node_id, title, completed, position,
        created_at, updated_at
      ) VALUES (?, ?, ?, 0, ?, ?, ?)
    `).run(
      accountNodeId,
      issueNodeId,
      normalizedTitle,
      position.position,
      now,
      now,
    );

    return this.getTask(accountNodeId, Number(result.lastInsertRowid));
  }

  getTasks(accountNodeId: string, issueNodeId: string): LocalTask[] {
    const rows = this.#db.prepare(`
      SELECT * FROM tasks
      WHERE account_node_id = ? AND issue_node_id = ?
      ORDER BY position, id
    `).all(accountNodeId, issueNodeId) as TaskRow[];
    return rows.map(mapTaskRow);
  }

  getTask(accountNodeId: string, taskId: number): LocalTask {
    const row = this.#db.prepare(`
      SELECT * FROM tasks WHERE account_node_id = ? AND id = ?
    `).get(accountNodeId, taskId) as TaskRow | undefined;
    if (!row) throw new Error(`Task ${taskId} was not found.`);
    return mapTaskRow(row);
  }

  setTaskCompleted(
    accountNodeId: string,
    taskId: number,
    completed: boolean,
    now = new Date().toISOString(),
  ): LocalTask {
    const result = this.#db.prepare(`
      UPDATE tasks SET completed = ?, updated_at = ?
      WHERE account_node_id = ? AND id = ?
    `).run(completed ? 1 : 0, now, accountNodeId, taskId);
    if (result.changes === 0) throw new Error(`Task ${taskId} was not found.`);
    return this.getTask(accountNodeId, taskId);
  }

  renameTask(
    accountNodeId: string,
    taskId: number,
    title: string,
    now = new Date().toISOString(),
  ): LocalTask {
    const normalizedTitle = title.trim();
    if (!normalizedTitle) throw new Error("Task title cannot be empty.");

    const result = this.#db.prepare(`
      UPDATE tasks SET title = ?, updated_at = ?
      WHERE account_node_id = ? AND id = ?
    `).run(normalizedTitle, now, accountNodeId, taskId);
    if (result.changes === 0) throw new Error(`Task ${taskId} was not found.`);
    return this.getTask(accountNodeId, taskId);
  }

  moveTask(accountNodeId: string, taskId: number, position: number): LocalTask {
    if (!Number.isInteger(position) || position < 1) {
      throw new Error("Task position must be a positive integer.");
    }

    const task = this.getTask(accountNodeId, taskId);
    const countRow = this.#db.prepare(`
      SELECT COUNT(*) AS count FROM tasks
      WHERE account_node_id = ? AND issue_node_id = ?
    `).get(accountNodeId, task.issueNodeId) as { count: number };
    const target = Math.min(position, countRow.count);
    if (target === task.position) return task;

    this.#db.exec("BEGIN IMMEDIATE");
    try {
      if (target < task.position) {
        this.#db.prepare(`
          UPDATE tasks SET position = position + 1
          WHERE account_node_id = ? AND issue_node_id = ?
            AND position >= ? AND position < ?
        `).run(accountNodeId, task.issueNodeId, target, task.position);
      } else {
        this.#db.prepare(`
          UPDATE tasks SET position = position - 1
          WHERE account_node_id = ? AND issue_node_id = ?
            AND position > ? AND position <= ?
        `).run(accountNodeId, task.issueNodeId, task.position, target);
      }
      this.#db.prepare(`
        UPDATE tasks SET position = ? WHERE account_node_id = ? AND id = ?
      `).run(target, accountNodeId, taskId);
      this.#db.exec("COMMIT");
    } catch (error) {
      this.#db.exec("ROLLBACK");
      throw error;
    }

    return this.getTask(accountNodeId, taskId);
  }

  deleteTask(accountNodeId: string, taskId: number): void {
    const task = this.getTask(accountNodeId, taskId);
    this.#db.exec("BEGIN IMMEDIATE");
    try {
      this.#db.prepare(`
        DELETE FROM tasks WHERE account_node_id = ? AND id = ?
      `).run(accountNodeId, taskId);
      this.#db.prepare(`
        UPDATE tasks SET position = position - 1
        WHERE account_node_id = ? AND issue_node_id = ? AND position > ?
      `).run(accountNodeId, task.issueNodeId, task.position);
      this.#db.exec("COMMIT");
    } catch (error) {
      this.#db.exec("ROLLBACK");
      throw error;
    }
  }

  setNote(
    accountNodeId: string,
    issueNodeId: string,
    body: string,
    now = new Date().toISOString(),
  ): void {
    const normalizedBody = body.trim();
    if (!normalizedBody) throw new Error("Note cannot be empty.");

    this.#db.prepare(`
      INSERT INTO notes (
        account_node_id, issue_node_id, body, created_at, updated_at
      ) VALUES (?, ?, ?, ?, ?)
      ON CONFLICT(account_node_id, issue_node_id) DO UPDATE SET
        body = excluded.body,
        updated_at = excluded.updated_at
    `).run(accountNodeId, issueNodeId, normalizedBody, now, now);
  }

  getLastAccount(): GitHubAccount | null {
    const row = this.#db.prepare(`
      SELECT host, login, node_id FROM accounts
      ORDER BY synced_at DESC LIMIT 1
    `).get() as
      | { host: string; login: string; node_id: string }
      | undefined;

    return row
      ? { host: row.host, login: row.login, nodeId: row.node_id }
      : null;
  }

  #migrate(): void {
    this.#db.exec(`
      PRAGMA journal_mode = WAL;
      PRAGMA foreign_keys = ON;

      CREATE TABLE IF NOT EXISTS accounts (
        node_id TEXT PRIMARY KEY,
        host TEXT NOT NULL,
        login TEXT NOT NULL,
        synced_at TEXT NOT NULL
      ) STRICT;

      CREATE TABLE IF NOT EXISTS issues_cache (
        account_node_id TEXT NOT NULL,
        node_id TEXT NOT NULL,
        number INTEGER NOT NULL,
        title TEXT NOT NULL,
        url TEXT NOT NULL,
        repository TEXT NOT NULL,
        state TEXT NOT NULL CHECK (state IN ('open', 'closed')),
        assignees TEXT NOT NULL DEFAULT '[]',
        updated_at TEXT NOT NULL,
        last_seen_at TEXT NOT NULL,
        stale INTEGER NOT NULL DEFAULT 0 CHECK (stale IN (0, 1)),
        PRIMARY KEY (account_node_id, node_id),
        UNIQUE (account_node_id, repository, number),
        FOREIGN KEY (account_node_id) REFERENCES accounts(node_id)
      ) STRICT;

      CREATE TABLE IF NOT EXISTS tasks (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        account_node_id TEXT NOT NULL,
        issue_node_id TEXT NOT NULL,
        title TEXT NOT NULL,
        completed INTEGER NOT NULL DEFAULT 0 CHECK (completed IN (0, 1)),
        position INTEGER NOT NULL,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        FOREIGN KEY (account_node_id, issue_node_id)
          REFERENCES issues_cache(account_node_id, node_id)
          ON DELETE RESTRICT
      ) STRICT;

      CREATE INDEX IF NOT EXISTS tasks_issue_position
        ON tasks(account_node_id, issue_node_id, position);

      CREATE TABLE IF NOT EXISTS notes (
        account_node_id TEXT NOT NULL,
        issue_node_id TEXT NOT NULL,
        body TEXT NOT NULL,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        PRIMARY KEY (account_node_id, issue_node_id),
        FOREIGN KEY (account_node_id, issue_node_id)
          REFERENCES issues_cache(account_node_id, node_id)
          ON DELETE RESTRICT
      ) STRICT;

      CREATE TABLE IF NOT EXISTS sync_state (
        account_node_id TEXT NOT NULL,
        key TEXT NOT NULL,
        value TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        PRIMARY KEY (account_node_id, key),
        FOREIGN KEY (account_node_id) REFERENCES accounts(node_id)
      ) STRICT;
    `);
  }
}

function mapIssueRow(row: IssueRow): CachedIssue {
  return {
    accountNodeId: row.account_node_id,
    nodeId: row.node_id,
    number: row.number,
    title: row.title,
    url: row.url,
    repository: row.repository,
    state: row.state,
    updatedAt: row.updated_at,
    assignees: JSON.parse(row.assignees) as string[],
    lastSeenAt: row.last_seen_at,
    stale: row.stale === 1,
    note: row.note,
    openTaskCount: row.open_task_count,
    completedTaskCount: row.completed_task_count,
  };
}

function mapTaskRow(row: TaskRow): LocalTask {
  return {
    id: row.id,
    accountNodeId: row.account_node_id,
    issueNodeId: row.issue_node_id,
    title: row.title,
    completed: row.completed === 1,
    position: row.position,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  };
}
