import assert from "node:assert/strict";
import { test } from "node:test";
import {
  createSearchExecutor, createSearchHandler, installSearchRoute, normalizeSearchResult,
  searchArgv, SEARCH_MAX_BUFFER, SEARCH_TIMEOUT,
} from "../search.js";

// Synthetic fixtures following internal/search/{search.go,search_render.go}.
const hit = (id = "abc", extra = {}) => ({
  session: { id, harness: "deepseek", title: "Pool sizing", project: "lab", updated: "2026-10-01T12:00:00Z", messages: [{ text: "not sent to browser" }], ...extra },
  snippets: ["one", "two"], count: 4, score: 1.25,
});
const request = (query = "pool") => new Request(`http://localhost/api/deja-search?q=${encodeURIComponent(query)}`);

for (const query of ["version", "index", "--limit 9999", "-race", "quotes ' \" & $(anything) | ;", "连接 池"]) {
  test(`search uses fixed argv and one literal query: ${query}`, () => {
    assert.deepEqual(searchArgv(query), ["search", "--harness", "deepseek", "--json", "--limit", "20", "--", query]);
  });
}

test("async executor bounds execution and never enables a shell", async () => {
  let complete;
  const run = createSearchExecutor((binary, args, options, callback) => {
    assert.equal(binary, "/synthetic/deja");
    assert.deepEqual(args, searchArgv("--flag"));
    assert.deepEqual(options, { shell: false, windowsHide: true, encoding: "utf8", timeout: SEARCH_TIMEOUT, maxBuffer: SEARCH_MAX_BUFFER });
    complete = callback;
  });
  let settled = false;
  const pending = run("/synthetic/deja", searchArgv("--flag")).then((value) => { settled = true; return value; });
  await Promise.resolve();
  assert.equal(settled, false);
  complete(null, '{"hits":null}');
  assert.equal(await pending, '{"hits":null}');
});

test("executor passes an optional abort signal to execFile", async () => {
  const controller = new AbortController();
  const run = createSearchExecutor((binary, args, options, callback) => {
    assert.equal(options.signal, controller.signal);
    options.signal.addEventListener("abort", () => callback(Object.assign(new Error("cancelled"), { code: "ABORT_ERR" })), { once: true });
  });
  const pending = run("fake", searchArgv("pool"), controller.signal);
  controller.abort();
  await assert.rejects(pending, { code: "ABORT_ERR" });
});

test("handler forwards request signal and reports an in-flight cancellation", async () => {
  const controller = new AbortController();
  const req = new Request("http://localhost/api/deja-search?q=pool", { signal: controller.signal });
  const handler = createSearchHandler("fake", { run: async (binary, args, signal) => {
    assert.equal(signal, req.signal);
    controller.abort();
    throw Object.assign(new Error("cancelled"), { code: "ABORT_ERR" });
  } });
  const response = await handler(req);
  assert.equal(response.status, 499);
  assert.equal(response.headers.get("cache-control"), "no-store");
  assert.deepEqual(await response.json(), { hits: [], hasMore: false, error: { code: "cancelled", message: "Deja search was cancelled." } });
});

test("an already cancelled request does not launch a process", async () => {
  const controller = new AbortController();
  controller.abort();
  let calls = 0;
  const handler = createSearchHandler("fake", { run: async () => { calls++; return '{"hits":null}'; } });
  const response = await handler(new Request("http://localhost/api/deja-search?q=pool", { signal: controller.signal }));
  assert.equal(response.status, 499);
  assert.equal((await response.json()).error.code, "cancelled");
  assert.equal(calls, 0);
});

test("a cancellation racing a successful executor does not publish stale hits", async () => {
  const controller = new AbortController();
  const handler = createSearchHandler("fake", { run: async () => {
    controller.abort();
    return JSON.stringify({ hits: [hit()], total: 1 });
  } });
  const response = await handler(new Request("http://localhost/api/deja-search?q=pool", { signal: controller.signal }));
  assert.equal(response.status, 499);
  assert.equal((await response.json()).error.code, "cancelled");
});

test("executor propagates callback and synchronous launch errors", async () => {
  const error = Object.assign(new Error("synthetic"), { code: "ENOENT" });
  await assert.rejects(createSearchExecutor((a, b, c, callback) => callback(error))("fake", []), { code: "ENOENT" });
  await assert.rejects(createSearchExecutor(() => { throw error; })("fake", []), { code: "ENOENT" });
});

test("normalization returns compact DSH hits and preserves a single existing prefix", () => {
  const result = normalizeSearchResult({ schema_version: 1, tier: "exact", total: 3, capped: true, hits: [hit(), hit("session-def"), hit("other", { harness: "codex" }), hit("")] });
  assert.deepEqual(result, {
    hits: ["session-abc", "session-def"].map((sessionId) => ({ sessionId, title: "Pool sizing", project: "lab", updated: "2026-10-01T12:00:00Z", snippets: ["one", "two"], count: 4, score: 1.25 })),
    hasMore: true, total: 3, tier: "exact",
  });
  assert.equal(JSON.stringify(result).includes("messages"), false);
});

test("pagination uses total and capped, and bounds legacy arrays to twenty hits", () => {
  assert.equal(normalizeSearchResult({ hits: [hit()], total: 2 }).hasMore, true);
  assert.equal(normalizeSearchResult({ hits: [hit()], total: 1 }).hasMore, false);
  const legacy = normalizeSearchResult(Array.from({ length: 23 }, (_, i) => hit(String(i))));
  assert.equal(legacy.hits.length, 20);
  assert.equal(legacy.hasMore, true);
  assert.equal(legacy.total, 23);
});

test("nil and empty hit slices are successful no-match responses", () => {
  for (const hits of [null, []]) {
    assert.deepEqual(normalizeSearchResult({ hits, total: 0, tier: "exact" }), { hits: [], total: 0, hasMore: false, tier: "exact" });
  }
  for (const malformed of [null, {}, { hits: "wrong" }]) assert.throws(() => normalizeSearchResult(malformed));
});

test("GET handler passes only trimmed q to the executor and returns no-store JSON", async () => {
  const handler = createSearchHandler("fake", { run: async (binary, args) => {
    assert.equal(binary, "fake");
    assert.deepEqual(args, searchArgv("--flag"));
    return JSON.stringify({ hits: [hit()], total: 1 });
  } });
  const response = await handler(request("  --flag  "));
  assert.equal(response.status, 200);
  assert.equal(response.headers.get("cache-control"), "no-store");
  assert.equal((await response.json()).hits[0].sessionId, "session-abc");
});

// The settings page asks which plugin answers this route. The probe must
// answer even when deja itself is missing, and must never start a search.
test("the probe names the plugin and its version without launching deja", async () => {
  let calls = 0;
  const handler = createSearchHandler(null, { run: () => { calls++; throw new Error("must not run"); } });
  const response = await handler(new Request("http://localhost/api/deja-search?probe=1"));
  assert.equal(response.status, 200);
  assert.equal(response.headers.get("cache-control"), "no-store");
  const body = await response.json();
  assert.equal(body.ok, true);
  assert.equal(body.plugin, "dsh-deja");
  assert.match(body.version, /^\d+\.\d+\.\d+/);
  assert.equal(calls, 0, "the probe started a search");
  // A binary-less host still answers the probe: it is about the route.
  assert.equal((await handler(new Request("http://localhost/api/deja-search?probe=1&q=x"))).status, 200);
});

test("empty queries and rejected requests launch nothing", async () => {
  let calls = 0;
  const handler = createSearchHandler("fake", { run: () => { calls++; throw new Error("must not run"); } });
  for (const query of ["", " \n\t"]) {
    const response = await handler(request(query));
    assert.equal(response.status, 200);
    assert.deepEqual((await response.json()).hits, []);
  }
  assert.equal((await handler(request("x".repeat(501)))).status, 400);
  assert.equal((await handler({ method: "GET", url: "not a url" })).status, 400);
  assert.equal((await handler(new Request("http://localhost/api/deja-search", { method: "POST" }))).status, 405);
  assert.equal(calls, 0);
});

for (const [error, status, code] of [
  [{ code: "ENOENT" }, 503, "unavailable"],
  [{ killed: true }, 504, "timeout"],
  [{ code: "ETIMEDOUT" }, 504, "timeout"],
  [{ code: "ERR_CHILD_PROCESS_STDIO_MAXBUFFER", killed: true }, 502, "output_limit"],
  [{ code: 1, stderr: "private history" }, 502, "search_failed"],
]) {
  test(`process failures are explicit: ${code} (${error.code || "killed"})`, async () => {
    const response = await createSearchHandler("fake", { run: async () => { throw error; } })(request());
    assert.equal(response.status, status);
    const body = await response.json();
    assert.deepEqual(body.hits, []);
    assert.equal(body.error.code, code);
    assert.equal(JSON.stringify(body).includes("private history"), false);
  });
}

test("missing binary and empty or malformed stdout cannot masquerade as no matches", async () => {
  assert.equal((await createSearchHandler(null)(request())).status, 503);
  for (const stdout of ["", "not JSON", "{}", "null", '{"hits":"wrong"}']) {
    const response = await createSearchHandler("fake", { run: async () => stdout })(request());
    assert.equal(response.status, 502);
    assert.equal((await response.json()).error.code, "invalid_output");
  }
});

test("route installs only with both services, and tolerates older or duplicate hosts", async () => {
  for (const services of [[], ["connection"], ["webServer"], ["connection", "webServer"]]) {
    let route;
    const ctx = { inject(deps, callback) {
      assert.deepEqual(deps, ["connection", "webServer"]);
      if (deps.every((dep) => services.includes(dep))) callback({ connection: { fetch: { register(value) { route = value; } } }, webServer: {} });
    } };
    installSearchRoute(ctx, "fake", { run: async () => '{"hits":null,"total":0}' });
    assert.equal(!!route, services.length === 2);
    if (route) {
      assert.equal(route.path, "/api/deja-search");
      assert.deepEqual(route.methods, ["GET"]);
      assert.equal(route.requestBody, "buffered");
      assert.equal((await route.fetch(request())).status, 200);
    }
  }
  assert.doesNotThrow(() => installSearchRoute({}, "fake"));
  assert.doesNotThrow(() => installSearchRoute({ inject() { throw new Error("old host"); } }, "fake"));
  assert.doesNotThrow(() => installSearchRoute({ inject(deps, callback) { callback({ connection: { fetch: { register() { throw new Error("duplicate"); } } } }); } }, "fake"));
});
