import { deepStrictEqual, strictEqual } from "node:assert/strict";
import { run } from "../src/cli.ts";
import type { GitHubAccount, GitHubIssue } from "../src/models.ts";
import { PokeStore } from "../src/store.ts";

Deno.test("offline CLI manages notes and tasks in a file-backed database", async () => {
  const tempDir = await Deno.makeTempDir({ prefix: "poke-test-" });
  const dbPath = `${tempDir}/poke.db`;
  const account: GitHubAccount = {
    host: "github.com",
    login: "octocat",
    nodeId: "USER_1",
  };
  const issue: GitHubIssue = {
    nodeId: "ISSUE_1",
    number: 42,
    title: "Test the CLI",
    url: "https://github.com/octo/repo/issues/42",
    repository: "octo/repo",
    state: "open",
    updatedAt: "2026-08-08T10:00:00Z",
    assignees: ["octocat"],
  };

  try {
    const seed = new PokeStore(dbPath);
    seed.syncIssues(account, [issue]);
    seed.close();

    await run(["--db", dbPath, "note", "set", "octo/repo#42", "Context"]);
    await run([
      "--db",
      dbPath,
      "task",
      "add",
      "octo/repo#42",
      "First action",
    ]);

    const verify = new PokeStore(dbPath);
    const cached = verify.findIssue(account.nodeId, {
      repository: "octo/repo",
      number: 42,
    });
    strictEqual(cached?.note, "Context");
    deepStrictEqual(
      verify.getTasks(account.nodeId, issue.nodeId).map((task) => task.title),
      ["First action"],
    );
    verify.close();
  } finally {
    await Deno.remove(tempDir, { recursive: true });
  }
});
