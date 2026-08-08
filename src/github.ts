import type { GitHubAccount, GitHubIssue, SyncResult } from "./models.ts";

interface GitHubUserResponse {
  login: string;
  node_id: string;
}

interface GitHubIssueResponse {
  node_id: string;
  number: number;
  title: string;
  html_url: string;
  repository_url: string;
  state: "open" | "closed";
  updated_at: string;
  assignees: Array<{ login: string }>;
}

interface SearchResponse {
  total_count: number;
  incomplete_results: boolean;
  items: GitHubIssueResponse[];
}

export type Fetcher = (
  input: string | URL | Request,
  init?: RequestInit,
) => Promise<Response>;

export class GitHubClient {
  readonly #token: string;
  readonly #fetch: Fetcher;
  readonly #apiBase: string;

  constructor(
    token: string,
    options: { fetch?: Fetcher; apiBase?: string } = {},
  ) {
    this.#token = token;
    this.#fetch = options.fetch ?? fetch;
    this.#apiBase = options.apiBase ?? "https://api.github.com";
  }

  async getAccount(): Promise<GitHubAccount> {
    const user = await this.#request<GitHubUserResponse>("/user");
    return {
      host: new URL(this.#apiBase).host,
      login: user.login,
      nodeId: user.node_id,
    };
  }

  async fetchAssignedIssues(org?: string): Promise<SyncResult> {
    const queries = [
      this.#buildQuery("open", org),
      this.#buildQuery("closed", org),
    ];
    const issuesById = new Map<string, GitHubIssue>();
    let complete = true;

    // Keep search requests serial to reduce secondary rate-limit pressure.
    for (const query of queries) {
      const result = await this.#search(query);
      complete &&= result.complete;
      for (const issue of result.issues) {
        issuesById.set(issue.nodeId, issue);
      }
    }

    return { issues: [...issuesById.values()], complete };
  }

  #buildQuery(state: "open" | "closed", org?: string): string {
    const terms = [
      "is:issue",
      `is:${state}`,
      "assignee:@me",
      "sort:updated-desc",
    ];
    if (org) terms.push(`org:${org}`);
    return terms.join(" ");
  }

  async #search(
    query: string,
  ): Promise<{ issues: GitHubIssue[]; complete: boolean }> {
    const issues: GitHubIssue[] = [];
    let page = 1;
    let totalCount = 0;
    let incomplete = false;

    do {
      const params = new URLSearchParams({
        q: query,
        per_page: "100",
        page: String(page),
      });
      const data = await this.#request<SearchResponse>(
        `/search/issues?${params}`,
      );
      totalCount = data.total_count;
      incomplete ||= data.incomplete_results;
      issues.push(...data.items.map(mapIssue));
      page += 1;
    } while (issues.length < Math.min(totalCount, 1000) && page <= 10);

    return {
      issues,
      complete: !incomplete && totalCount <= 1000 &&
        issues.length >= totalCount,
    };
  }

  async #request<T>(path: string): Promise<T> {
    for (let attempt = 0; attempt < 2; attempt += 1) {
      const response = await this.#fetch(`${this.#apiBase}${path}`, {
        headers: {
          "Accept": "application/vnd.github+json",
          "Authorization": `Bearer ${this.#token}`,
          "User-Agent": "poke",
          "X-GitHub-Api-Version": "2022-11-28",
        },
      });

      if (response.ok) return await response.json() as T;

      const retryDelay = getRetryDelay(response);
      if (retryDelay !== null && attempt === 0) {
        await new Promise((resolve) => setTimeout(resolve, retryDelay));
        continue;
      }

      const details = await response.text();
      throw new Error(
        `GitHub API error ${response.status}: ${
          details || response.statusText
        }`,
      );
    }

    throw new Error("GitHub API request failed after retrying.");
  }
}

function getRetryDelay(response: Response): number | null {
  const retryAfter = response.headers.get("Retry-After");
  if (retryAfter !== null) {
    const seconds = Number(retryAfter);
    if (Number.isFinite(seconds) && seconds >= 0) {
      return Math.min(seconds * 1000, 60_000);
    }
  }

  if (response.status === 429) return 1000;
  if (response.headers.get("X-RateLimit-Remaining") === "0") {
    const resetAt = Number(response.headers.get("X-RateLimit-Reset")) * 1000;
    if (Number.isFinite(resetAt)) {
      return Math.min(Math.max(resetAt - Date.now(), 0), 60_000);
    }
  }
  return null;
}

function mapIssue(issue: GitHubIssueResponse): GitHubIssue {
  return {
    nodeId: issue.node_id,
    number: issue.number,
    title: issue.title,
    url: issue.html_url,
    repository: new URL(issue.repository_url).pathname.replace(
      /^\/repos\//,
      "",
    ),
    state: issue.state,
    updatedAt: issue.updated_at,
    assignees: issue.assignees.map((assignee) => assignee.login),
  };
}
