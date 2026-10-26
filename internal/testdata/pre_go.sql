-- Frozen synthetic SQLite schema/data from the TypeScript implementation at d5d35bc.
-- Captured before the Go CI cutover; no runtime TypeScript dependency.
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
BEGIN TRANSACTION;
CREATE TABLE accounts (
        node_id TEXT PRIMARY KEY,
        host TEXT NOT NULL,
        login TEXT NOT NULL,
        synced_at TEXT NOT NULL
      ) STRICT;
INSERT INTO "accounts" VALUES('USER_1','api.github.com','octocat','2026-08-08T10:00:00.000Z');
INSERT INTO "accounts" VALUES('USER_2','api.github.com','other','2026-08-07T10:00:00.000Z');
CREATE TABLE issues_cache (
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
INSERT INTO "issues_cache" VALUES('USER_1','ISSUE_1',1,'Issue 1 日本語','https://github.com/octo/repo/issues/1','octo/repo','open','["octocat"]','2026-08-08T10:00:00.000Z','2026-08-08T10:00:00.000Z',0);
INSERT INTO "issues_cache" VALUES('USER_1','ISSUE_2',2,'Issue 2 日本語','https://github.com/octo/repo/issues/2','octo/repo','closed','["octocat"]','2026-08-08T10:00:00.000Z','2026-08-08T10:00:00.000Z',0);
INSERT INTO "issues_cache" VALUES('USER_1','ISSUE_3',3,'Issue 3 日本語','https://github.com/octo/repo/issues/3','octo/repo','open','["octocat"]','2026-08-08T10:00:00.000Z','2026-08-08T10:00:00.000Z',1);
INSERT INTO "issues_cache" VALUES('USER_2','ISSUE_1',1,'Issue 1 日本語','https://github.com/octo/repo/issues/1','octo/repo','open','["octocat"]','2026-08-08T10:00:00.000Z','2026-08-07T10:00:00.000Z',0);
CREATE TABLE notes (
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
INSERT INTO "notes" VALUES('USER_1','ISSUE_1','Private 日本語','2026-08-08T10:00:00.000Z','2026-08-08T10:00:00.000Z');
INSERT INTO "notes" VALUES('USER_1','ISSUE_3','Retained stale context','2026-08-08T10:00:00.000Z','2026-08-08T10:00:00.000Z');
INSERT INTO "notes" VALUES('USER_2','ISSUE_1','Other account','2026-08-08T10:00:00.000Z','2026-08-08T10:00:00.000Z');
CREATE TABLE sync_state (
        account_node_id TEXT NOT NULL,
        key TEXT NOT NULL,
        value TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        PRIMARY KEY (account_node_id, key),
        FOREIGN KEY (account_node_id) REFERENCES accounts(node_id)
      ) STRICT;
INSERT INTO "sync_state" VALUES('USER_1','issues:last_sync','2026-08-08T10:00:00.000Z','2026-08-08T10:00:00.000Z');
INSERT INTO "sync_state" VALUES('USER_2','issues:last_sync','2026-08-07T10:00:00.000Z','2026-08-07T10:00:00.000Z');
CREATE TABLE tasks (
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
INSERT INTO "tasks" VALUES(1,'USER_1','ISSUE_1','First action',1,2,'2026-08-08T10:00:00.000Z','2026-08-08T10:00:00.000Z');
INSERT INTO "tasks" VALUES(2,'USER_1','ISSUE_1','Second action',0,1,'2026-08-08T10:00:00.000Z','2026-08-08T10:00:00.000Z');
INSERT INTO "tasks" VALUES(3,'USER_1','ISSUE_3','Stale action',0,1,'2026-08-08T10:00:00.000Z','2026-08-08T10:00:00.000Z');
CREATE INDEX tasks_issue_position
        ON tasks(account_node_id, issue_node_id, position);
DELETE FROM "sqlite_sequence";
INSERT INTO "sqlite_sequence" VALUES('tasks',3);
COMMIT;
