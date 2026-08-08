import { deepStrictEqual, equal, strictEqual } from "node:assert/strict";
import type { GitHubAccount, GitHubIssue } from "../src/models.ts";
import { PokeStore } from "../src/store.ts";

const account: GitHubAccount = {
  host: "github.com",
  login: "octocat",
  nodeId: "USER_1",
};

function issue(
  nodeId: string,
  number: number,
  title = `Issue ${number}`,
): GitHubIssue {
  return {
    nodeId,
    number,
    title,
    url: `https://github.com/octo/repo/issues/${number}`,
    repository: "octo/repo",
    state: "open",
    updatedAt: "2026-08-08T10:00:00Z",
    assignees: ["octocat"],
  };
}

Deno.test("sync keeps annotations when a cached issue becomes stale", () => {
  const store = new PokeStore();
  try {
    store.syncIssues(account, [issue("ISSUE_1", 1)]);
    store.addTask(account.nodeId, "ISSUE_1", "Investigate");
    store.setNote(account.nodeId, "ISSUE_1", "Private context");

    store.syncIssues(account, []);

    const cached = store.listIssues(account.nodeId, {
      includeClosed: true,
      includeStale: true,
    });
    strictEqual(cached.length, 1);
    strictEqual(cached[0].stale, true);
    strictEqual(cached[0].note, "Private context");
    strictEqual(cached[0].openTaskCount, 1);
  } finally {
    store.close();
  }
});

Deno.test("partial sync does not mark unseen issues stale", () => {
  const store = new PokeStore();
  try {
    store.syncIssues(account, [issue("ISSUE_1", 1)]);
    store.syncIssues(account, [issue("ISSUE_2", 2)], { complete: false });

    const cached = store.listIssues(account.nodeId, {
      includeClosed: true,
      includeStale: true,
    });
    equal(cached.length, 2);
    deepStrictEqual(cached.map((item) => item.stale), [false, false]);
  } finally {
    store.close();
  }
});

Deno.test("tasks can be completed, renamed, reordered, and deleted", () => {
  const store = new PokeStore();
  try {
    store.syncIssues(account, [issue("ISSUE_1", 1)]);
    const first = store.addTask(account.nodeId, "ISSUE_1", "First");
    const second = store.addTask(account.nodeId, "ISSUE_1", "Second");
    const third = store.addTask(account.nodeId, "ISSUE_1", "Third");

    store.setTaskCompleted(account.nodeId, first.id, true);
    store.renameTask(account.nodeId, second.id, "Renamed");
    store.moveTask(account.nodeId, third.id, 1);
    store.deleteTask(account.nodeId, first.id);

    const tasks = store.getTasks(account.nodeId, "ISSUE_1");
    deepStrictEqual(tasks.map((task) => task.title), ["Third", "Renamed"]);
    deepStrictEqual(tasks.map((task) => task.position), [1, 2]);
    deepStrictEqual(tasks.map((task) => task.completed), [false, false]);
  } finally {
    store.close();
  }
});

Deno.test("next view includes only current open work with local tasks", () => {
  const store = new PokeStore();
  try {
    const closed = { ...issue("ISSUE_2", 2), state: "closed" as const };
    store.syncIssues(account, [issue("ISSUE_1", 1), closed]);
    store.addTask(account.nodeId, "ISSUE_1", "Open next action");
    store.addTask(account.nodeId, "ISSUE_2", "Closed issue action");

    const next = store.listNext(account.nodeId);
    deepStrictEqual(next.map((item) => item.nodeId), ["ISSUE_1"]);
  } finally {
    store.close();
  }
});
