// The plugin's pure helpers, tested without dsh.
//
// index.js registers itself against a live host on import, so the parts worth
// testing are copied here as the file states them. That is a real risk — a
// change in index.js will not fail these tests — so each case exists to pin a
// rule that was wrong once, not to prove the file is correct.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const source = readFileSync(new URL("../index.js", import.meta.url), "utf8");

test("the recall limit is bounded on both sides", () => {
  const clamp = (asked) => Math.min(20, Math.max(1, asked));
  assert.equal(clamp(0), 1);
  assert.equal(clamp(-3), 1);
  assert.equal(clamp(7), 7);
  assert.equal(clamp(9999), 20);
});

test("tool output declares a plain JSON schema", () => {
  // A schemastery instance is rejected by the host with "schema must be a
  // value schema object", and the profile then fails to load at all.
  assert.match(source, /schema: \{ type: "string" \}/);
  assert.doesNotMatch(source, /schema: z\./);
});

test("automatic recall does not use the pre-step waterfall", () => {
  // A message spliced there is dropped by a later listener before the request
  // is built, with nothing reported.
  // The comment explaining why names the event, so the check is on the
  // registration rather than on the words.
  assert.doesNotMatch(source, /ctx\.on\("agent\/pre-step"/);
  assert.match(source, /ctx\.systemPrompt\.context\(/);
});

test("the patch names the package the host has to load", () => {
  const patch = readFileSync(new URL("../cordis.patch.yml", import.meta.url), "utf8");
  const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
  assert.match(patch, new RegExp(`name: ${pkg.name}`));
  assert.equal(pkg.dsh.bundle.patch, "./cordis.patch.yml");
});

test("every file the manifest ships is present", () => {
  const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
  for (const file of pkg.files) {
    assert.doesNotThrow(
      () => readFileSync(new URL(`../${file}`, import.meta.url)),
      `package.json lists ${file}, which is not in the repository`,
    );
  }
});
