package main

// promptCacheImportsJS imports Node built-ins needed by promptCacheStoreJS in
// generated ESM/TypeScript harness extensions.
const promptCacheImportsJS = `import { chmodSync, existsSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { dirname, isAbsolute, join } from "node:path";
`

// promptCacheStoreJS persists per-session digest, recall, and tool-repair maps
// on disk so harness restarts emit byte-identical prompt prefixes instead of
// busting provider prompt caches (#6155). Written in plain ES without Go
// template backticks or `const args` so every harness — including TypeScript
// stripped via node --experimental-strip-types — runs it unchanged.
const promptCacheStoreJS = `
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

function promptCachePath(name, dejaBin = typeof DEJA === "string" ? DEJA : "") {
  const override = process.env.DEJA_PROMPT_CACHE_FILE;
  if (override) return override;
  if (isTempDejaBin(dejaBin)) {
    return join(dirname(dejaBin), ".deja-cache", name + ".json");
  }
  const xdg = process.env.XDG_CACHE_HOME;
  if (xdg && isAbsolute(xdg)) return join(xdg, "deja-vu", name + ".json");
  const base = process.platform === "win32"
    ? (process.env.LOCALAPPDATA || join(homedir(), "AppData", "Local"))
    : join(homedir(), ".cache");
  return join(base, "deja-vu", name + ".json");
}

function nullMap(src) {
  const out = Object.create(null);
  if (!src || typeof src !== "object" || Array.isArray(src)) return out;
  for (const pair of Object.entries(src)) {
    out[pair[0]] = pair[1];
  }
  return out;
}

function readPromptCacheFile(file) {
  try {
    if (!existsSync(file)) return { version: 1, sessions: Object.create(null) };
    const parsed = JSON.parse(readFileSync(file, "utf8"));
    if (parsed && parsed.version === 1 && parsed.sessions && typeof parsed.sessions === "object" && !Array.isArray(parsed.sessions)) {
      return { version: 1, sessions: nullMap(parsed.sessions) };
    }
  } catch {}
  return { version: 1, sessions: Object.create(null) };
}

function writePromptCacheFile(file, data, keepScope, maxSessions) {
  try {
    const entries = Object.entries(data.sessions);
    if (entries.length > maxSessions) {
      entries.sort((a, b) => ((b[1] && b[1].updatedAt) || 0) - ((a[1] && a[1].updatedAt) || 0));
      const trimmed = Object.create(null);
      if (keepScope && Object.hasOwn(data.sessions, keepScope) && data.sessions[keepScope]) {
        trimmed[keepScope] = data.sessions[keepScope];
      }
      for (const pair of entries) {
        if (Object.keys(trimmed).length >= maxSessions) break;
        trimmed[pair[0]] = pair[1];
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
    const tmp = file + "." + process.pid + "." + Math.random().toString(36).slice(2) + ".tmp";
    try {
      writeFileSync(tmp, JSON.stringify(data), { encoding: "utf8", mode: 0o600 });
      if (process.platform !== "win32") chmodSync(tmp, 0o600);
      renameSync(tmp, file);
    } catch (err) {
      try { rmSync(tmp, { force: true }); } catch {}
      throw err;
    }
  } catch {}
}

function clonePromptSession(raw) {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return { epoch: 0, updatedAt: 0, recalled: Object.create(null), repairs: Object.create(null) };
  }
  const out = {
    epoch: Number.isInteger(raw.epoch) ? raw.epoch : 0,
    updatedAt: typeof raw.updatedAt === "number" ? raw.updatedAt : 0,
    recalled: nullMap(raw.recalled),
    repairs: nullMap(raw.repairs),
  };
  if (Object.hasOwn(raw, "digest")) out.digest = String(raw.digest == null ? "" : raw.digest);
  if (raw.told) out.told = true;
  if (typeof raw.empties === "number") out.empties = raw.empties;
  return out;
}

function rememberEntry(mapObj, key, val, maxEntries = 512) {
  if (Object.hasOwn(mapObj, key)) delete mapObj[key];
  mapObj[key] = val;
  const keys = Object.keys(mapObj);
  while (keys.length > maxEntries) {
    delete mapObj[keys.shift()];
  }
}

function createSessionStore(name, maxSessions = 256) {
  const pathOf = () => promptCachePath(name);
  const read = (scope) => {
    const data = readPromptCacheFile(pathOf());
    const raw = Object.hasOwn(data.sessions, scope) ? data.sessions[scope] : undefined;
    const session = clonePromptSession(raw);
    return { epoch: session.epoch, session };
  };
  const write = (scope, expectedEpoch, mutator) => {
    const file = pathOf();
    const data = readPromptCacheFile(file);
    const raw = Object.hasOwn(data.sessions, scope) ? data.sessions[scope] : undefined;
    const cur = clonePromptSession(raw);
    if (cur.epoch > expectedEpoch) return { stale: true, epoch: cur.epoch, session: cur };
    mutator(cur);
    cur.epoch = cur.epoch > expectedEpoch ? cur.epoch : expectedEpoch;
    cur.updatedAt = Date.now();
    data.sessions[scope] = cur;
    writePromptCacheFile(file, data, scope, maxSessions);
    return { stale: false, epoch: cur.epoch, session: cur };
  };
  const compact = (scope, clearDigest = false) => {
    const file = pathOf();
    const data = readPromptCacheFile(file);
    const raw = Object.hasOwn(data.sessions, scope) ? data.sessions[scope] : undefined;
    const cur = clonePromptSession(raw);
    const next = { epoch: cur.epoch + 1, updatedAt: Date.now(), recalled: Object.create(null), repairs: Object.create(null) };
    if (!clearDigest && Object.hasOwn(cur, "digest")) next.digest = cur.digest;
    if (cur.told) next.told = true;
    data.sessions[scope] = next;
    writePromptCacheFile(file, data, scope, maxSessions);
    return next;
  };
  return { read, write, compact };
}
`
