// The release script publishes to npm, so the parts worth testing are the two
// decisions it makes on its own: which version wins, and what it does when npm
// is already ahead. Both are pinned here because getting them wrong moves the
// `latest` tag backwards, which cannot be undone by publishing again.
import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

const source = fs.readFileSync(new URL("./release-npm.mjs", import.meta.url), "utf8");

// The functions live in a script that publishes on import, so they are read out
// of it rather than imported. A rename breaks this loudly instead of silently
// testing nothing.
function extract(name) {
  const at = source.indexOf(`function ${name}(`);
  assert.notEqual(at, -1, `${name} is gone from release-npm.mjs`);
  const end = source.indexOf("\n}", at);
  return eval(`(${source.slice(at, end + 2)})`); // eslint-disable-line no-eval
}

const compareVersions = extract("compareVersions");

test("plain versions order by number, not by string", () => {
  assert.equal(compareVersions("0.18.0", "0.20.3"), -1);
  assert.equal(compareVersions("0.21.0", "0.20.3"), 1);
  assert.equal(compareVersions("0.18.0", "0.18.0"), 0);
  // "0.9.0" > "0.10.0" as strings, which is the whole reason this exists.
  assert.equal(compareVersions("0.10.0", "0.9.0"), 1);
});

test("anything that is not a plain x.y.z stops the release", () => {
  assert.throws(() => compareVersions("0.21.0-rc.1", "0.20.3"), /not a plain version/);
  assert.throws(() => compareVersions("0.21", "0.20.3"), /not a plain version/);
});

test("the extension packages are published from the release version", () => {
  // The dependency has to move with it: a plugin pinned to an older deja is a
  // plugin that ships the wrong binary as its fallback.
  assert.match(source, /outPkg\.version = version/);
  assert.match(source, /outPkg\.dependencies\["@vshulcz\/deja-vu"\] = `\^\$\{version\}`/);
  assert.match(source, /const extensions = \["opencode", "dsh"\]/);
});

test("a release behind npm skips instead of moving latest backwards", () => {
  assert.match(source, /compareVersions\(version, live\) <= 0/);
  assert.match(source, /skipping \$\{pkg\.name\}/);
});
