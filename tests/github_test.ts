import { equal, match, rejects } from "node:assert/strict";
import { GitHubClient } from "../src/github.ts";

function response(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function apiIssue(number: number): Record<string, unknown> {
  return {
    node_id: `ISSUE_${number}`,
    number,
    title: `Issue ${number}`,
    html_url: `https://github.com/octo/repo/issues/${number}`,
    repository_url: "https://api.github.com/repos/octo/repo",
    state: "open",
    updated_at: "2026-08-08T10:00:00Z",
    assignees: [{ login: "octocat" }],
  };
}

Deno.test("GitHubClient paginates account-wide assigned issue searches", async () => {
  const fetcher = (input: string | URL | Request): Promise<Response> => {
    const url = new URL(String(input));
    const query = url.searchParams.get("q") ?? "";
    const page = Number(url.searchParams.get("page"));

    if (query.includes("is:closed")) {
      return Promise.resolve(response({
        total_count: 0,
        incomplete_results: false,
        items: [],
      }));
    }

    const start = (page - 1) * 100 + 1;
    const count = page === 1 ? 100 : 1;
    return Promise.resolve(response({
      total_count: 101,
      incomplete_results: false,
      items: Array.from(
        { length: count },
        (_, index) => apiIssue(start + index),
      ),
    }));
  };

  const client = new GitHubClient("token", { fetch: fetcher });
  const result = await client.fetchAssignedIssues();
  equal(result.issues.length, 101);
  equal(result.complete, true);
  equal(result.issues[100].repository, "octo/repo");
});

Deno.test("GitHubClient marks searches over GitHub's result cap partial", async () => {
  const fetcher = (input: string | URL | Request): Promise<Response> => {
    const url = new URL(String(input));
    const query = url.searchParams.get("q") ?? "";
    const page = Number(url.searchParams.get("page"));
    const isOpen = query.includes("is:open");
    return Promise.resolve(response({
      total_count: isOpen ? 1001 : 0,
      incomplete_results: false,
      items: isOpen
        ? Array.from(
          { length: 100 },
          (_, index) => apiIssue((page - 1) * 100 + index + 1),
        )
        : [],
    }));
  };

  const client = new GitHubClient("token", { fetch: fetcher });
  const result = await client.fetchAssignedIssues();
  equal(result.issues.length, 1000);
  equal(result.complete, false);
});

Deno.test("GitHubClient reports API failures without leaking the token", async () => {
  const client = new GitHubClient("secret-token", {
    fetch: () => Promise.resolve(response({ message: "Forbidden" }, 403)),
  });

  await rejects(
    () => client.getAccount(),
    (error: Error) => {
      match(error.message, /GitHub API error 403/);
      equal(error.message.includes("secret-token"), false);
      return true;
    },
  );
});

Deno.test("GitHubClient retries a rate-limited request once", async () => {
  let attempts = 0;
  const client = new GitHubClient("token", {
    fetch: () => {
      attempts += 1;
      if (attempts === 1) {
        return Promise.resolve(
          new Response("limited", {
            status: 429,
            headers: { "Retry-After": "0" },
          }),
        );
      }
      return Promise.resolve(response({ login: "octocat", node_id: "USER_1" }));
    },
  });

  const account = await client.getAccount();
  equal(attempts, 2);
  equal(account.login, "octocat");
});
