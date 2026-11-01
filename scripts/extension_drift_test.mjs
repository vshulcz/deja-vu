// The rule this guard exists for: the version in this repository is derived
// from what npm serves, and a derived copy nobody writes back is a file that
// says the wrong number. Every one of the four was behind — opencode-deja read
// 0.1.2 against 0.20.1 on npm — and the previous rule could not see it, because
// it compared npm against the newest release tag and a CI checkout has none
// (#3627).
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

import { MIRRORS, PACKAGES, awaitPublished, behindNpm, compareVersions, notYetPublished } from "./extension-drift.mjs";

const pkgs = {
  "extensions/opencode": { name: "opencode-deja", version: "0.20.1" },
  "extensions/dsh": { name: "dsh-deja", version: "0.20.8" },
  "extensions/openclaw": { name: "@vshulcz/openclaw-deja", version: "0.20.1" },
  "extensions/pi": { name: "@vshulcz/pi-deja", version: "0.20.1" },
  "extensions/magpie": { name: "@vshulcz/magpie-deja", version: "0.20.1" },
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
    "@vshulcz/magpie-deja",
    "@vshulcz/openclaw-deja",
    "@vshulcz/pi-deja",
    "dsh-deja",
    "opencode-deja",
  ]);
});

// The root manifest mirrors extensions/dsh for the DeepSeek Harness catalogs,
// and cmd/deja's TestRootManifestMirrorsTheDshPlugin pins them to one version.
// Catching the extension up and leaving the mirror behind fails that test on
// every pull request, which is what happened here.
test("the mirror names a real file and holds the same version", () => {
  const root = path.join(import.meta.dirname, "..");
  const version = (p) => JSON.parse(fs.readFileSync(path.join(root, p), "utf8")).version;
  assert.deepEqual(Object.keys(MIRRORS), ["extensions/dsh"]);
  for (const [dir, mirror] of Object.entries(MIRRORS)) {
    assert.equal(version(mirror), version(`${dir}/package.json`), `${mirror} vs ${dir}`);
  }
});

test("versions order by number", () => {
  assert.equal(compareVersions("0.20.4", "0.19.2"), 1);
  assert.equal(compareVersions("0.9.0", "0.10.0"), -1);
  assert.equal(compareVersions("0.20.5", "0.20.5"), 0);
  assert.throws(() => compareVersions("0.21.0-rc.1", "0.20.3"), /not a plain version/);
});

// The follow-up asked npm the moment the release finished, while the publishes
// were still in flight, and called a repository that was behind "caught up":
// on 0.20.2 the check ran at 13:59:05 and the two packages landed on npm at
// 14:01:54 and 14:02:50. Waiting for the released version is what makes the
// check after it mean anything (#3776).
test("notYetPublished names the packages npm has not served yet", () => {
  const readPkg = (dir) => ({ name: dir, version: "0.20.1" });
  const latest = (name) => (name === "b" ? "0.20.2" : "0.20.1");
  const pending = notYetPublished(["a", "b"], readPkg, latest, "0.20.2");
  assert.deepEqual(
    pending.map((p) => p.name),
    ["a"],
  );
});

test("a package npm has never served is not something to wait for", () => {
  const readPkg = (dir) => ({ name: dir, version: "0.20.2" });
  const pending = notYetPublished(["a"], readPkg, () => "", "0.20.2");
  assert.deepEqual(pending, []);
});

test("awaitPublished stops as soon as npm catches up, and gives up bounded", async () => {
  const readPkg = (dir) => ({ name: dir, version: "0.20.1" });
  let asked = 0;
  const latest = () => {
    asked++;
    return asked < 3 ? "0.20.1" : "0.20.2";
  };
  let waits = 0;
  const wait = async () => {
    waits++;
  };
  const got = await awaitPublished(["a"], readPkg, latest, "0.20.2", { wait, everyMs: 1, tries: 10 });
  assert.equal(got.settled, true);
  assert.equal(waits, 2);

  const stuck = await awaitPublished(["a"], readPkg, () => "0.20.1", "0.20.2", { wait, everyMs: 1, tries: 3 });
  assert.equal(stuck.settled, false);
  assert.deepEqual(
    stuck.pending.map((p) => p.name),
    ["a"],
  );
});
