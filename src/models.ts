export interface GitHubAccount {
  host: string;
  login: string;
  nodeId: string;
}

export interface GitHubIssue {
  nodeId: string;
  number: number;
  title: string;
  url: string;
  repository: string;
  state: "open" | "closed";
  updatedAt: string;
  assignees: string[];
}

export interface CachedIssue extends GitHubIssue {
  accountNodeId: string;
  lastSeenAt: string;
  stale: boolean;
  note: string | null;
  openTaskCount: number;
  completedTaskCount: number;
}

export interface LocalTask {
  id: number;
  accountNodeId: string;
  issueNodeId: string;
  title: string;
  completed: boolean;
  position: number;
  createdAt: string;
  updatedAt: string;
}

export interface IssueReference {
  repository: string;
  number: number;
}

export interface SyncResult {
  issues: GitHubIssue[];
  complete: boolean;
}

export function parseIssueReference(value: string): IssueReference {
  const urlMatch = value.match(
    /^https?:\/\/[^/]+\/([^/]+\/[^/]+)\/issues\/(\d+)(?:[/?#].*)?$/,
  );
  const shortMatch = value.match(/^([^/\s]+\/[^/#\s]+)#(\d+)$/);
  const match = urlMatch ?? shortMatch;

  if (!match) {
    throw new Error(
      `Invalid issue reference "${value}". Use owner/repo#123 or an issue URL.`,
    );
  }

  return {
    repository: match[1],
    number: Number.parseInt(match[2], 10),
  };
}
