// The rule this guard exists for: a package npm holds ahead of the release line
// is skipped by every release, silently, until the project catches up.
import assert from "node:assert/strict";
import test from "node:test";

import { PACKAGES, compareVersions, stranded } from "./extension-drift.mjs";

const names = {
  "extensions/opencode": { name: "opencode-deja" },
  "extensions/dsh": { name: "dsh-deja" },
  "extensions/openclaw": { name: "@vshulcz/openclaw-deja" },
};
const readPkg = (dir) => names[dir];

// The state this was written for: dsh-deja published by hand at 0.20.4 while
// the release line was 0.19.2. Comparing the package against its own manifest
// sees nothing wrong there — both say 0.20.4 — which is why the comparison is
// against the release line.
test("the August state is caught", () => {
  const bad = stranded(PACKAGES, readPkg, (n) => (n === "dsh-deja" ? "0.20.4" : "0.19.2"), "0.19.2");
  assert.deepEqual(bad.map((p) => p.name), ["dsh-deja"]);
  assert.equal(bad[0].npm, "0.20.4");
  assert.equal(bad[0].line, "0.19.2");
});

test("level with the line, or behind it, is not drift", () => {
  assert.deepEqual(stranded(PACKAGES, readPkg, () => "0.19.2", "0.19.2"), []);
  assert.deepEqual(stranded(PACKAGES, readPkg, () => "0.18.0", "0.19.2"), []);
});

test("what cannot be answered is not drift", () => {
  // npm unreachable, and a checkout without tags: a pull request must not fail
  // for either.
  assert.deepEqual(stranded(PACKAGES, readPkg, () => "", "0.19.2"), []);
  assert.deepEqual(stranded(PACKAGES, readPkg, () => "0.99.0", ""), []);
});

test("every package is reported when all are stranded", () => {
  const bad = stranded(PACKAGES, readPkg, () => "0.21.0", "0.19.2");
  assert.deepEqual(bad.map((p) => p.name).sort(), ["@vshulcz/openclaw-deja", "dsh-deja", "opencode-deja"]);
});

test("versions order by number", () => {
  assert.equal(compareVersions("0.20.4", "0.19.2"), 1);
  assert.equal(compareVersions("0.9.0", "0.10.0"), -1);
  assert.equal(compareVersions("0.20.5", "0.20.5"), 0);
  assert.throws(() => compareVersions("0.21.0-rc.1", "0.20.3"), /not a plain version/);
});
