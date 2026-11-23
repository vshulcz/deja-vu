import { createHash } from "node:crypto";
import { accessSync, chmodSync, constants, existsSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { homedir, tmpdir } from "node:os";
import { dirname, isAbsolute, join } from "node:path";

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
const touchedFiles = new Set();
export const maxSessions = 256;
export const maxTurns = 128;

function isTempDejaBin(dejaBin) {
  if (!dejaBin) return false;
  const fold = (s) => {
    const clean = s.replace(/\\/g, "/").replace(/\/+$/, "");
    return process.platform === "win32" ? clean.toLowerCase() : clean;
  };
  const bin = fold(dejaBin);
  return [tmpdir(), "/tmp", "/private/tmp", "/var/folders"]
    .filter(Boolean)
    .some((base) => {
      const root = fold(base);
      return root && bin.startsWith(root + "/");
    });
}

export function promptCachePath(name = "magpie-cache", dejaBin = "") {
  const override = process.env.DEJA_PROMPT_CACHE_FILE;
  if (override) return override;
  if (isTempDejaBin(dejaBin)) {
    return join(dirname(dejaBin), ".deja-cache", `${name}.json`);
  }
  const xdg = process.env.XDG_CACHE_HOME;
  if (xdg && isAbsolute(xdg)) return join(xdg, "deja-vu", `${name}.json`);
  const base = process.platform === "win32"
    ? (process.env.LOCALAPPDATA || join(homedir(), "AppData", "Local"))
    : join(homedir(), ".cache");
  return join(base, "deja-vu", `${name}.json`);
}

function nullMap(src) {
  const out = Object.create(null);
  if (!src || typeof src !== "object" || Array.isArray(src)) return out;
  for (const [k, v] of Object.entries(src)) out[k] = v;
  return out;
}

function readPromptCache(file = promptCachePath()) {
  try {
    if (!existsSync(file)) return { version: 1, sessions: Object.create(null) };
    const parsed = JSON.parse(readFileSync(file, "utf8"));
    if (parsed && parsed.version === 1 && parsed.sessions && typeof parsed.sessions === "object" && !Array.isArray(parsed.sessions)) {
      return { version: 1, sessions: nullMap(parsed.sessions) };
    }
  } catch {}
  return { version: 1, sessions: Object.create(null) };
}

function writePromptCache(data, keepConv, file = promptCachePath()) {
  try {
    const entries = Object.entries(data.sessions);
    if (entries.length > maxSessions) {
      entries.sort((a, b) => (b[1]?.updatedAt || 0) - (a[1]?.updatedAt || 0));
      const trimmed = Object.create(null);
      if (keepConv && Object.hasOwn(data.sessions, keepConv) && data.sessions[keepConv]) {
        trimmed[keepConv] = data.sessions[keepConv];
      }
      for (const [k, v] of entries) {
        if (Object.keys(trimmed).length >= maxSessions) break;
        trimmed[k] = v;
      }
      data.sessions = trimmed;
    }
    const dir = dirname(file);
    mkdirSync(dir, { recursive: true, mode: 0o700 });
    if (
      process.platform !== "win32" &&
      dir !== "/" &&
      dir !== "/tmp" &&
      dir !== "/private/tmp" &&
      dir !== tmpdir().replace(/\/+$/, "") &&
      dir !== homedir().replace(/\/+$/, "")
    ) {
      try { chmodSync(dir, 0o700); } catch {}
    }
    const tmp = `${file}.${process.pid}.${Math.random().toString(36).slice(2)}.tmp`;
    try {
      writeFileSync(tmp, JSON.stringify(data), { encoding: "utf8", mode: 0o600 });
      if (process.platform !== "win32") chmodSync(tmp, 0o600);
      renameSync(tmp, file);
      touchedFiles.add(file);
    } catch (err) {
      try { rmSync(tmp, { force: true }); } catch {}
      throw err;
    }
  } catch {}
}

export function loadConversation(convKey, dejaBin = "") {
  const map = new Map();
  const sessions = readPromptCache(promptCachePath("magpie-cache", dejaBin)).sessions;
  const entry = Object.hasOwn(sessions, convKey) ? sessions[convKey] : undefined;
  const turns = entry?.turns;
  if (!turns || typeof turns !== "object" || Array.isArray(turns)) return map;
  for (const [k, v] of Object.entries(turns)) {
    if (v && typeof v === "object" && typeof v.text === "string") {
      map.set(k, { text: v.text, bytes: typeof v.bytes === "number" ? v.bytes : 0 });
    }
  }
  return map;
}

export function saveTurn(convKey, tKey, formatted, dejaBin = "") {
  const file = promptCachePath("magpie-cache", dejaBin);
  const data = readPromptCache(file);
  const existing = Object.hasOwn(data.sessions, convKey) ? data.sessions[convKey] : undefined;
  const turns = nullMap(existing?.turns);
  if (Object.hasOwn(turns, tKey)) delete turns[tKey];
  turns[tKey] = { text: formatted.text, bytes: formatted.bytes };
  const keys = Object.keys(turns);
  while (keys.length > maxTurns) {
    delete turns[keys.shift()];
  }
  data.sessions[convKey] = { updatedAt: Date.now(), turns };
  writePromptCache(data, convKey, file);
}

export function reset(dejaBin = "") {
  sticky.clear();
  const targets = new Set([promptCachePath("magpie-cache", dejaBin), ...touchedFiles]);
  for (const f of targets) {
    try {
      rmSync(f, { force: true });
    } catch {}
  }
  touchedFiles.clear();
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
