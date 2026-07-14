#!/usr/bin/env node
// Build and publish npm packages from goreleaser release archives.
// Usage: node scripts/release-npm.mjs <version> <dist-dir>
//   <dist-dir> must contain deja-vu_<version>_<os>_<arch>.{tar.gz,zip}
// Publishes @vshulcz/deja-vu-<os>-<arch> for each platform, then @vshulcz/deja-vu.
// Requires NODE_AUTH_TOKEN (set by actions/setup-node from the NPM_TOKEN secret).
import { execSync } from "node:child_process";
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
const run = (cmd, cwd) => execSync(cmd, { cwd, stdio: "inherit" });

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
    run(`unzip -o -q ${JSON.stringify(archive)} deja.exe -d ${JSON.stringify(path.join(dir, "bin"))}`);
  } else {
    run(`tar -xzf ${JSON.stringify(archive)} -C ${JSON.stringify(path.join(dir, "bin"))} deja`);
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
  run("npm publish --access public", dir);
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
run("npm publish --access public", mainDir);
console.log(`published ${platforms.length} platform packages + @vshulcz/deja-vu@${version}`);
