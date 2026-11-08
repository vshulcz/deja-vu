import { createHash } from "node:crypto";
import { accessSync, constants } from "node:fs";
import { createRequire } from "node:module";
import { homedir } from "node:os";
import { join } from "node:path";

const require = createRequire(import.meta.url);

export const WINDOWS = process.platform === "win32";
export const PLATFORM = WINDOWS ? "windows" : process.platform;
export const ARCH = process.arch === "x64" ? "amd64" : process.arch;
export const EXE = WINDOWS ? "deja.exe" : "deja";

// Defense-in-depth secret redaction on top of deja's index-time scrubber:
// any token or key pattern that survived in a transcript is masked before
// the recalled block is attached to an outbound model request.
export const secretPattern =
  /(?:(?:^|[^A-Za-z0-9])(?:[A-Za-z0-9]+[_-])*(?:api[_-]?key|access[_-]?key|access[_-]?token|token|authorization|[A-Za-z0-9]*(?:password|passwd|pwd)|secret|private[_-]?key|client[_-]?secret)["']?\s*[:=]\s*["']?[^\s"'<>]+|\b[a-z][a-z0-9+.-]*:\/\/[^\s/?#@]+:[^\s/?#@]+@|\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{8,}|\bAIza[0-9A-Za-z_-]{35}(?![0-9A-Za-z_-])|\bbearer\s+[A-Za-z0-9._~+/=-]{8,}|\bsk-[A-Za-z0-9_-]{8,}|\bgh[pousr]_[A-Za-z0-9]{20,}|\bgithub_pat_[A-Za-z0-9_]{20,}|\bxox[abprs]-[A-Za-z0-9-]{10,}|\bAKIA[0-9A-Z]{16}\b|-----BEGIN [^-]*PRIVATE KEY-----[\s\S]*?-----END [^-]*PRIVATE KEY-----)/giu;

export const sticky = new Map();
export const maxSessions = 256;
export const maxTurns = 128;

export function reset() {
  sticky.clear();
}

export function wellKnown() {
  const home = homedir();
  if (WINDOWS) {
    const local = process.env.LOCALAPPDATA || join(home, "AppData", "Local");
    return [join(local, "deja", "bin", EXE), join(home, ".local", "bin", EXE)];
  }
  return [
    join(home, ".local", "bin", EXE),
    "/opt/homebrew/bin/deja",
    "/usr/local/bin/deja",
    "/usr/bin/deja",
  ];
}

export function resolveDeja(setting) {
  const strSetting = typeof setting === "string" ? setting : "";
  const candidates = [strSetting, process.env.DEJA_BIN, ...wellKnown()];
  try {
    candidates.push(require.resolve(`@vshulcz/deja-vu-${PLATFORM}-${ARCH}/bin/${EXE}`));
  } catch {}
  for (const candidate of candidates) {
    if (!candidate || typeof candidate !== "string") continue;
    if (candidate.includes("/") || candidate.includes("\\")) {
      try {
        accessSync(candidate, constants.X_OK);
        return candidate;
      } catch {
        continue;
      }
    }
    return candidate;
  }
  return EXE;
}

export function maskSecrets(text) {
  return String(text || "").replace(secretPattern, (match) => {
    const eq = match.search(/[:=]/);
    if (eq > 0 && !match.startsWith("-----BEGIN") && !match.includes("://")) {
      return match.slice(0, eq + 1) + " [REDACTED]";
    }
    return "[REDACTED]";
  });
}

export function sha256(...parts) {
  const h = createHash("sha256");
  for (const p of parts) {
    h.update(String(p ?? ""));
    h.update("\0");
  }
  return h.digest("hex");
}

export function lru(map, key, create, limit) {
  if (map.has(key)) {
    const val = map.get(key);
    map.delete(key);
    map.set(key, val);
    return val;
  }
  const val = create();
  map.set(key, val);
  while (map.size > limit) {
    map.delete(map.keys().next().value);
  }
  return val;
}
