#!/usr/bin/env node
// Report harness packages npm holds ahead of this project's release line.
//
// The release script refuses to publish a package when npm is ahead of the
// version being released, because moving `latest` backwards cannot be undone.
// That refusal is right, and it is also permanent: a package published by hand
// at a version the release line has not reached is skipped by every release
// after it, silently, until the project's own version catches up. dsh-deja sat
// at 0.20.4 that way from 25 August against a 0.19.2 release line, and two
// fixes merged for it would never have shipped.
//
// The comparison is against the newest release tag, not against the version in
// the package's own package.json: those two are equal in exactly the state this
// exists to catch.
//
// Nothing here publishes.
import { execSync } from "node:child_process";
import fs from "node:fs";

export const PACKAGES = ["extensions/opencode", "extensions/dsh"];

// npmLatest is the version npm serves as `latest`, or "" when the package has
// never been published or npm cannot be reached. Unreachable is not drift: a
// network failure must not fail a pull request.
export function npmLatest(name) {
  try {
    return execSync(`npm view ${name} version`, {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    }).trim();
  } catch {
    return "";
  }
}

// releaseLine is the newest release tag, without its leading v. "" when the
// tags are not in the checkout, which is the default for a shallow clone —
// again, not drift.
export function releaseLine() {
  try {
    const tag = execSync("git tag --list 'v*' --sort=-v:refname", {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    }).split("\n")[0].trim();
    return tag.replace(/^v/, "");
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

// stranded reports the packages the next release would skip. Lookups are
// injected so the rule can be tested without the network or a tag history.
export function stranded(packages, readPkg, latest, line) {
  if (!line) return [];
  const out = [];
  for (const dir of packages) {
    const { name } = readPkg(dir);
    const live = latest(name);
    if (!live) continue;
    if (compareVersions(live, line) > 0) {
      out.push({ dir, name, npm: live, line });
    }
  }
  return out;
}

function readPkg(dir) {
  const pkg = JSON.parse(fs.readFileSync(`${dir}/package.json`, "utf8"));
  return { name: pkg.name, version: pkg.version };
}

if (process.argv[1] && process.argv[1].endsWith("extension-drift.mjs")) {
  const bad = stranded(PACKAGES, readPkg, npmLatest, releaseLine());
  for (const p of bad) {
    console.error(
      `${p.name}: npm serves ${p.npm}, the release line is ${p.line} — ` +
        `every release skips it until this project's version passes ${p.npm}`,
    );
  }
  process.exit(bad.length ? 1 : 0);
}
