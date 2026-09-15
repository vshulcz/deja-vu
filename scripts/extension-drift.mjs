#!/usr/bin/env node
// Report harness packages whose version in this repository is not what npm
// serves, and with --write, catch them up.
//
// npm is where these versions are decided: a package npm holds ahead of the
// release is published as the next patch of its own line rather than skipped
// (#2993), which is how dsh-deja reached 0.20.8 against a 0.20.1 release. The
// release writes that number into a temporary copy, so the repository's own
// package.json never learned it — every one of the four was behind, and
// `extensions/opencode/package.json` said 0.1.2 for a package npm serves as
// 0.20.1 (#3627).
//
// The previous rule compared npm against the newest release tag and called a
// package ahead of it stranded, which was right while a release skipped such a
// package — the state dsh-deja sat in from 25 August 2026 at 0.20.4 against a
// 0.19.2 line, when two fixes for it would never have shipped. It has not been
// right since #2993, and it could only ever fire where the tags are: a shallow
// CI checkout has none, so the job that was supposed to catch this said
// nothing.
//
// Nothing here publishes.
import { execFileSync } from "node:child_process";
import fs from "node:fs";

export const PACKAGES = ["extensions/opencode", "extensions/dsh", "extensions/openclaw", "extensions/pi"];

// npmLatest is the version npm serves as `latest`, or "" when the package has
// never been published or npm cannot be reached. Unreachable is not drift: a
// network failure must not fail a pull request.
export function npmLatest(name) {
  try {
    return execFileSync("npm", ["view", name, "version"], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    }).trim();
  } catch {
    return "";
  }
}

// parse rejects anything that is not a plain x.y.z, the rule the release script
// applies — a prerelease is a reason to stop and think.
export function parse(v) {
  const parts = String(v).split(".").map(Number);
  if (parts.length !== 3 || parts.some((n) => !Number.isInteger(n))) {
    throw new Error(`not a plain version: ${v}`);
  }
  return parts;
}

export function compareVersions(a, b) {
  const [x, y] = [parse(a), parse(b)];
  for (let i = 0; i < 3; i++) {
    if (x[i] !== y[i]) return x[i] < y[i] ? -1 : 1;
  }
  return 0;
}

// behindNpm reports the packages whose version in this repository is not the
// one npm serves. Lookups are injected so the rule can be tested without the
// network.
//
// A package npm has never heard of, or a lookup that failed, is not drift: a
// network failure must not fail a pull request. A repository version *ahead* of
// npm is not drift either — that is a release in flight, and the publish
// decides the number either way.
export function behindNpm(packages, readPkg, latest) {
  const out = [];
  for (const dir of packages) {
    const { name, version } = readPkg(dir);
    const live = latest(name);
    if (!live || !version) continue;
    if (compareVersions(version, live) < 0) {
      out.push({ dir, name, npm: live, repo: version });
    }
  }
  return out;
}

function readPkg(dir) {
  const pkg = JSON.parse(fs.readFileSync(`${dir}/package.json`, "utf8"));
  return { name: pkg.name, version: pkg.version };
}

if (process.argv[1] && process.argv[1].endsWith("extension-drift.mjs")) {
  const write = process.argv.includes("--write");
  const bad = behindNpm(PACKAGES, readPkg, npmLatest);
  for (const p of bad) {
    if (write) {
      const file = `${p.dir}/package.json`;
      const pkg = JSON.parse(fs.readFileSync(file, "utf8"));
      pkg.version = p.npm;
      fs.writeFileSync(file, JSON.stringify(pkg, null, 2) + "\n");
      console.log(`${p.name}: ${p.repo} -> ${p.npm}`);
      continue;
    }
    console.error(
      `${p.name}: this repository says ${p.repo}, npm serves ${p.npm} — ` +
        `run: node scripts/extension-drift.mjs --write`,
    );
  }
  process.exit(!write && bad.length ? 1 : 0);
}
