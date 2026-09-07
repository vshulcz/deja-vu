#!/usr/bin/env node
// Build and publish npm packages from goreleaser release archives.
// Usage: node scripts/release-npm.mjs <version> <dist-dir>
//   <dist-dir> must contain deja-vu_<version>_<os>_<arch>.{tar.gz,zip}
// Publishes @vshulcz/deja-vu-<os>-<arch> for each platform, then @vshulcz/deja-vu,
// then the harness packages under extensions/ at the same version.
//
// One version for everything is the point: a user reading `dsh-deja@0.21.0`
// knows which deja it was built against, and nobody has to remember to publish
// a plugin by hand — which is how opencode-deja sat unpublished while its
// install instructions were already in the README.
// Requires NODE_AUTH_TOKEN (set by actions/setup-node from the NPM_TOKEN secret).
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const [version, dist] = process.argv.slice(2);
if (!version || !dist) {
  console.error("usage: release-npm.mjs <version> <dist-dir>");
  process.exit(1);
}

const platforms = [
  ["darwin", "arm64"], ["darwin", "amd64"],
  ["linux", "arm64"], ["linux", "amd64"],
  ["windows", "arm64"], ["windows", "amd64"],
];

const work = fs.mkdtempSync("/tmp/deja-npm-");
const run = (cmd, args, cwd) => execFileSync(cmd, args, { cwd, stdio: "inherit" });

for (const [goos, goarch] of platforms) {
  const ext = goos === "windows" ? "zip" : "tar.gz";
  const archive = path.join(dist, `deja-vu_${version}_${goos}_${goarch}.${ext}`);
  if (!fs.existsSync(archive)) {
    console.error(`missing archive: ${archive}`);
    process.exit(1);
  }
  const pkg = `deja-vu-${goos}-${goarch}`;
  const dir = path.join(work, pkg);
  fs.mkdirSync(path.join(dir, "bin"), { recursive: true });
  if (ext === "zip") {
    run("unzip", ["-o", "-q", archive, "deja.exe", "-d", path.join(dir, "bin")]);
  } else {
    run("tar", ["-xzf", archive, "-C", path.join(dir, "bin"), "deja"]);
  }
  fs.writeFileSync(path.join(dir, "package.json"), JSON.stringify({
    name: `@vshulcz/${pkg}`,
    version,
    description: `deja binary for ${goos}/${goarch}`,
    license: "MIT",
    repository: "github:vshulcz/deja-vu",
    os: [goos === "windows" ? "win32" : goos],
    cpu: [goarch === "amd64" ? "x64" : "arm64"],
    files: ["bin"],
  }, null, 2));
  run("npm", ["publish", "--access", "public"], dir);
}

// main wrapper package from npm/ in the repo
const mainDir = path.join(work, "deja-vu");
fs.cpSync("npm", mainDir, { recursive: true });
const mainPkgPath = path.join(mainDir, "package.json");
const main = JSON.parse(fs.readFileSync(mainPkgPath, "utf8"));
main.version = version;
main.optionalDependencies = Object.fromEntries(
  platforms.map(([o, a]) => [`@vshulcz/deja-vu-${o}-${a}`, version]),
);
fs.writeFileSync(mainPkgPath, JSON.stringify(main, null, 2));
run("npm", ["publish", "--access", "public"], mainDir);

// The harness packages ride the same version where they can. Two of them were
// versioned independently before this, and publishing them at a release behind
// what npm already has would move the `latest` tag backwards, which cannot be
// undone. Those get the next patch of their own line rather than being skipped:
// skipping left dsh-deja on npm asking for a deja two releases old, and its
// version line is ahead of deja's, so "they converge on their own" never came
// (#2993).
const extensions = ["opencode", "dsh", "openclaw", "pi"];
const published = [];
for (const name of extensions) {
  const dir = path.join("extensions", name);
  const pkg = JSON.parse(fs.readFileSync(path.join(dir, "package.json"), "utf8"));
  const live = npmLatest(pkg.name);
  const publishAs = live && compareVersions(version, live) <= 0 ? nextPatch(live) : version;
  if (publishAs !== version) {
    console.log(`${pkg.name}: npm has ${live}, this release is ${version} — publishing ${publishAs}`);
  }
  const out = path.join(work, name);
  fs.cpSync(dir, out, { recursive: true });
  const outPkgPath = path.join(out, "package.json");
  const outPkg = JSON.parse(fs.readFileSync(outPkgPath, "utf8"));
  outPkg.version = publishAs;
  // The dependency names this release either way: what the plugin installs
  // when there is no deja on PATH has to be the binary this release built.
  if (outPkg.dependencies?.["@vshulcz/deja-vu"]) {
    outPkg.dependencies["@vshulcz/deja-vu"] = `^${version}`;
  }
  fs.writeFileSync(outPkgPath, JSON.stringify(outPkg, null, 2) + "\n");
  run("npm", ["publish", "--access", "public"], out);
  published.push(`${outPkg.name}@${publishAs}`);
}

console.log(
  `published ${platforms.length} platform packages + @vshulcz/deja-vu@${version}` +
    (published.length ? ` + ${published.join(", ")}` : ""),
);

// npmLatest is the version npm serves as `latest`, or "" when the package has
// never been published.
function npmLatest(name) {
  try {
    return execFileSync("npm", ["view", name, "version"], { encoding: "utf8" }).trim();
  } catch {
    return "";
  }
}

// nextPatch is the next version on a package's own line. A harness package
// whose version has run ahead of deja's is published here rather than skipped,
// and this is the smallest version npm accepts from it.
function nextPatch(v) {
  const parts = v.split(".").map(Number);
  if (parts.length !== 3 || parts.some((n) => !Number.isInteger(n))) {
    throw new Error(`not a plain version: ${v}`);
  }
  return `${parts[0]}.${parts[1]}.${parts[2] + 1}`;
}

// compareVersions orders two plain x.y.z versions. Releases here are always
// plain, so this deliberately does not try to be a semver implementation — a
// prerelease would be a reason to stop and think rather than to publish.
function compareVersions(a, b) {
  const parse = (v) => {
    const parts = v.split(".").map(Number);
    if (parts.length !== 3 || parts.some((n) => !Number.isInteger(n))) {
      throw new Error(`not a plain version: ${v}`);
    }
    return parts;
  };
  const [x, y] = [parse(a), parse(b)];
  for (let i = 0; i < 3; i++) {
    if (x[i] !== y[i]) return x[i] < y[i] ? -1 : 1;
  }
  return 0;
}
