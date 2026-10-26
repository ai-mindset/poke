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
