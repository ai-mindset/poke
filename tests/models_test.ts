import { deepStrictEqual, throws } from "node:assert/strict";
import { parseIssueReference } from "../src/models.ts";

Deno.test("parseIssueReference accepts short references and URLs", () => {
  deepStrictEqual(parseIssueReference("octo/repo#42"), {
    repository: "octo/repo",
    number: 42,
  });
  deepStrictEqual(
    parseIssueReference(
      "https://github.com/octo/repo/issues/42#issuecomment-1",
    ),
    { repository: "octo/repo", number: 42 },
  );
});

Deno.test("parseIssueReference rejects ambiguous references", () => {
  throws(() => parseIssueReference("#42"), /Invalid issue reference/);
});
