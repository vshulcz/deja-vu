// The rule this guard exists for: the version in this repository is derived
// from what npm serves, and a derived copy nobody writes back is a file that
// says the wrong number. Every one of the four was behind — opencode-deja read
// 0.1.2 against 0.20.1 on npm — and the previous rule could not see it, because
// it compared npm against the newest release tag and a CI checkout has none
// (#3627).
import assert from "node:assert/strict";
import test from "node:test";

import { PACKAGES, behindNpm, compareVersions } from "./extension-drift.mjs";

const pkgs = {
  "extensions/opencode": { name: "opencode-deja", version: "0.20.1" },
  "extensions/dsh": { name: "dsh-deja", version: "0.20.8" },
  "extensions/openclaw": { name: "@vshulcz/openclaw-deja", version: "0.20.1" },
  "extensions/pi": { name: "@vshulcz/pi-deja", version: "0.20.1" },
};
const readPkg = (dir) => pkgs[dir];

// The state this was written for, on the day 0.20.1 shipped: dsh-deja's own
// line had run ahead and the release published the next patch of it, while
// every repository copy stayed where it was.
test("the September state is caught", () => {
  const behind = { ...pkgs, "extensions/dsh": { name: "dsh-deja", version: "0.20.5" } };
  const bad = behindNpm(PACKAGES, (d) => behind[d], (n) => (n === "dsh-deja" ? "0.20.8" : "0.20.1"));
  assert.deepEqual(bad.map((p) => p.name), ["dsh-deja"]);
  assert.equal(bad[0].npm, "0.20.8");
  assert.equal(bad[0].repo, "0.20.5");
});

test("level with npm is not drift", () => {
  assert.deepEqual(
    behindNpm(PACKAGES, readPkg, (n) => (n === "dsh-deja" ? "0.20.8" : "0.20.1")),
    [],
  );
});

// A release in flight bumps the repository first and publishes after, so ahead
// of npm is the normal state for the length of a release run.
test("ahead of npm is not drift", () => {
  assert.deepEqual(behindNpm(PACKAGES, readPkg, () => "0.19.0"), []);
});

test("what cannot be answered is not drift", () => {
  // npm unreachable, and a package it has never heard of: a pull request must
  // not fail for either.
  assert.deepEqual(behindNpm(PACKAGES, readPkg, () => ""), []);
  assert.deepEqual(behindNpm(PACKAGES, (d) => ({ name: pkgs[d].name }), () => "0.99.0"), []);
});

test("every package is reported when all are behind", () => {
  const bad = behindNpm(PACKAGES, readPkg, () => "0.99.0");
  assert.deepEqual(bad.map((p) => p.name).sort(), [
    "@vshulcz/openclaw-deja",
    "@vshulcz/pi-deja",
    "dsh-deja",
    "opencode-deja",
  ]);
});

test("versions order by number", () => {
  assert.equal(compareVersions("0.20.4", "0.19.2"), 1);
  assert.equal(compareVersions("0.9.0", "0.10.0"), -1);
  assert.equal(compareVersions("0.20.5", "0.20.5"), 0);
  assert.throws(() => compareVersions("0.21.0-rc.1", "0.20.3"), /not a plain version/);
});
