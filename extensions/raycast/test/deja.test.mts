import assert from "node:assert/strict";
import test from "node:test";
import { parseLastJSON, parseResumeOutput, parseSearchJSON, parseShowJSON, quoteConversationMarkdown } from "../src/lib/deja.ts";
import { expandTilde, locateDeja } from "../src/lib/locate.ts";

test("parseSearchJSON separates strict hits from relevance fallback", () => {
  const env = parseSearchJSON(
    JSON.stringify({
      schema_version: 2,
      tier: "relevance",
      strict: 1,
      hits: [
        { harness: "claude", id: "s1", project: "app", strict: true },
        { harness: "codex", id: "s2", project: "app" },
      ],
    }),
  );
  assert.equal(env.tier, "relevance");
  assert.equal(env.strictHits.length, 1);
  assert.equal(env.strictHits[0].id, "s1");
  assert.equal(env.nearestHits.length, 1);
  assert.equal(env.nearestHits[0].id, "s2");
});

test("parseSearchJSON handles legacy bare array and exact tier", () => {
  const legacy = parseSearchJSON(JSON.stringify([{ harness: "cursor", id: "c1", project: "web" }]));
  assert.equal(legacy.strictHits.length, 1);
  assert.equal(legacy.nearestHits.length, 0);
});

test("parseLastJSON reads envelope and array", () => {
  assert.equal(parseLastJSON(JSON.stringify({ sessions: [{ harness: "claude", id: "a", project: "p" }] })).length, 1);
  assert.equal(parseLastJSON(JSON.stringify([{ harness: "claude", id: "a", project: "p" }])).length, 1);
});

test("parseShowJSON and quoteConversationMarkdown escape embedded fences safely", () => {
  const s = parseShowJSON(
    JSON.stringify({
      schema_version: 2,
      session: {
        harness: "claude",
        id: "abc",
        project: "deja-vu",
        messages: [{ role: "assistant", text: "```sh\nrm -rf /\n```\n# Heading", time: "2026-10-01T00:00:00Z" }],
      },
    }),
  );
  const md = quoteConversationMarkdown(s);
  assert.ok(md.includes("> ```sh"));
  assert.ok(md.includes("> # Heading"));
});

test("parseResumeOutput extracts POSIX-quoted cd directory", () => {
  const got = parseResumeOutput("deja: wrote session back\ncd '/Users/me/my project' && claude --resume abc-123\n");
  assert.equal(got.command, "cd '/Users/me/my project' && claude --resume abc-123");
  assert.equal(got.cwd, "/Users/me/my project");
});

test("locateDeja checks preference first, then PATH and standard locations", () => {
  assert.equal(expandTilde("~/bin/deja", "/Users/test"), "/Users/test/bin/deja");
  const found = locateDeja("~/custom/deja", "/usr/bin:/bin", "/Users/test", (p) => p === "/Users/test/custom/deja");
  assert.equal(found, "/Users/test/custom/deja");
  const fallback = locateDeja("", "/usr/bin", "/Users/test", (p) => p === "/opt/homebrew/bin/deja");
  assert.equal(fallback, "/opt/homebrew/bin/deja");
});
