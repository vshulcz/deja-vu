import { execFile } from "node:child_process";

export const SEARCH_PATH = "/api/deja-search";
export const SEARCH_LIMIT = 20;
export const SEARCH_TIMEOUT = 20000;
export const SEARCH_MAX_BUFFER = 2 * 1024 * 1024;

// Keep the entire query one argv entry, including command names and flags.
export function searchArgv(query) {
  return ["search", "--harness", "deepseek", "--json", "--limit", String(SEARCH_LIMIT), "--", query];
}

// A callback seam lets tests exercise the actual process options without
// starting a shell, touching an index, or reading anybody's session history.
export function createSearchExecutor(execute = execFile) {
  return (binary, args, signal) => new Promise((resolve, reject) => {
    try {
      execute(binary, args, {
        shell: false,
        windowsHide: true,
        encoding: "utf8",
        timeout: SEARCH_TIMEOUT,
        maxBuffer: SEARCH_MAX_BUFFER,
        ...(signal ? { signal } : {}),
      }, (error, stdout) => {
        if (error) reject(error);
        else resolve(String(stdout || ""));
      });
    } catch (error) {
      reject(error);
    }
  });
}

const text = (value) => typeof value === "string" ? value : "";
const number = (value) => typeof value === "number" && Number.isFinite(value) ? value : 0;

export function normalizeSearchResult(payload) {
  // Older deja releases emitted a bare array for exact matches.
  const rows = Array.isArray(payload) ? payload : payload?.hits;
  // Go encodes a nil hit slice as null when there are no matches.
  if (!Array.isArray(rows) && !(rows === null && payload && !Array.isArray(payload))) {
    throw new Error("invalid search envelope");
  }
  const hits = [];
  for (const row of rows || []) {
    const session = row?.session;
    if (session?.harness !== "deepseek" || !text(session.id)) continue;
    const bare = session.id.replace(/^session-/, "");
    if (!bare) continue;
    hits.push({
      sessionId: `session-${bare}`,
      title: text(session.title),
      project: text(session.project),
      updated: text(session.updated),
      snippets: Array.isArray(row.snippets) ? row.snippets.filter((s) => typeof s === "string").slice(0, 3) : [],
      count: number(row.count),
      score: number(row.score),
    });
  }
  const total = Array.isArray(payload) ? rows.length : Math.max(0, number(payload.total));
  return {
    hits: hits.slice(0, SEARCH_LIMIT),
    hasMore: payload.capped === true || total > (rows || []).length || hits.length > SEARCH_LIMIT,
    total,
    tier: text(payload.tier),
  };
}

function json(body, status = 200) {
  return Response.json(body, { status, headers: { "cache-control": "no-store" } });
}

function failure(status, code, message) {
  return json({ hits: [], hasMore: false, error: { code, message } }, status);
}

function executionFailure(error) {
  if (error?.code === "ENOENT") return failure(503, "unavailable", "Deja is not installed or could not be found.");
  if (error?.code === "ERR_CHILD_PROCESS_STDIO_MAXBUFFER") return failure(502, "output_limit", "Deja search exceeded the output limit.");
  if (error?.killed || error?.code === "ETIMEDOUT") return failure(504, "timeout", "Deja search timed out.");
  return failure(502, "search_failed", "Deja search failed. Check the local Deja installation and index.");
}

export function createSearchHandler(binary, { run = createSearchExecutor() } = {}) {
  return async (request) => {
    if (request.method !== "GET") return failure(405, "method_not_allowed", "Use GET for search.");
    let query;
    try {
      query = (new URL(request.url).searchParams.get("q") || "").trim();
    } catch {
      return failure(400, "invalid_request", "Invalid search URL.");
    }
    if (!query) return json({ hits: [], hasMore: false, total: 0, tier: "" });
    if (query.length > 500) return failure(400, "query_too_long", "Search queries must be at most 500 characters.");
    const cancelled = () => failure(499, "cancelled", "Deja search was cancelled.");
    if (request.signal?.aborted) return cancelled();
    if (!binary) return failure(503, "unavailable", "Deja is not installed or could not be found.");
    let stdout;
    try {
      stdout = await run(binary, searchArgv(query), request.signal);
    } catch (error) {
      return request.signal?.aborted ? cancelled() : executionFailure(error);
    }
    if (request.signal?.aborted) return cancelled();
    try {
      return json(normalizeSearchResult(JSON.parse(stdout)));
    } catch {
      return failure(502, "invalid_output", "Deja returned invalid search data. Update Deja and try again.");
    }
  };
}

// Match notesRoute: DSH's API mount supplies its Host/Origin and login fence.
// Cordis owns route disposal; duplicate/older/headless hosts may stand down.
export function installSearchRoute(ctx, binary, options) {
  if (typeof ctx?.inject !== "function") return;
  try {
    ctx.inject(["connection", "webServer"], (c) => {
      try {
        c.connection.fetch.register({
          path: SEARCH_PATH,
          methods: ["GET"],
          requestBody: "buffered",
          fetch: createSearchHandler(binary, options),
        });
      } catch {}
    });
  } catch {}
}
