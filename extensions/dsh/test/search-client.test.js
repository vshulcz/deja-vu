import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { runInNewContext } from "node:vm";

const source = readFileSync(new URL("../client.js", import.meta.url), "utf8");
function load(extras = {}, modules = {}) {
  let registration;
  const context = { window: { __ModuleLoader__: { load: (r) => { registration = r; } } }, URL, ...extras };
  runInNewContext(source, context);
  return registration.factory((name) => {
    if (name in modules) return modules[name];
    assert.equal(name, "react");
    return {};
  });
}
function storage(initial) {
  let saved = initial;
  return { getItem: () => saved, setItem: (_key, next) => { saved = next; }, value: () => JSON.parse(saved) };
}
function shortcuts(mod, prefs) {
  const state = { id: "session-first", drafts: new Map(), focused: [], persisted: [] };
  let command;
  const ctx = {
    shortcuts: { register(value) { command = value; return () => {}; } },
    uiSession: { current: { getSnapshot: () => ({ key: state.id }) } },
    sessions: { binding: (id) => ({ ctx: { id } }) },
    conversation: { input: { for: ({ id }) => ({
      projection: { clipboardText: state.drafts.get(id) || "" },
      actions: { setDraft: (text) => state.drafts.set(id, text), persistDraft: () => state.persisted.push(id) },
      focus: () => state.focused.push(id),
    }) } },
  };
  mod.installFind(ctx, prefs);
  return { command, ctx, state };
}

test("Ctrl+F fills and persists the active session's draft without submitting", () => {
  const mod = load();
  const { command, state } = shortcuts(mod, mod.preferences());
  state.drafts.set("session-first", "中文 query");
  const action = command.resolve({ modal: null, region: "editable" });
  assert.equal(action.status, "handled");
  action.run();
  assert.equal(state.drafts.get("session-first"), "deja search --session first 中文 query");
  assert.deepEqual(state.persisted, ["session-first"]);
  assert.deepEqual(state.focused, ["session-first"]);
  command.resolve({ modal: null }).run();
  assert.equal(state.drafts.get("session-first"), "deja search --session first 中文 query");
  assert.equal(command.defaults["desktop:windows"].code, "KeyF");
  assert.equal(command.defaults["web:windows"].modifiers.join("+"), "primary+alt");
});

test("navigation between resolution and execution never writes the previous session", () => {
  const mod = load();
  const { command, state } = shortcuts(mod, mod.preferences());
  const action = command.resolve({ modal: null });
  state.id = "session-second";
  action.run();
  assert.equal(state.drafts.has("session-first"), false);
  assert.equal(state.drafts.get("session-second"), "deja search --session second ");
  state.id = undefined;
  action.run();
  assert.equal(state.drafts.size, 1);
});

test("disabled find, modals, terminals, missing and invalid sessions pass the key through", () => {
  const mod = load();
  const prefs = mod.preferences();
  const { command, ctx, state } = shortcuts(mod, prefs);
  for (const context of [{ modal: "settings" }, { modal: "dialog" }, { modal: null, region: "terminal" }]) {
    assert.equal(command.resolve(context).status, "pass");
  }
  prefs.set("conversation", false);
  assert.equal(command.resolve({ modal: null }).status, "pass");
  prefs.set("conversation", true);
  state.id = undefined;
  assert.equal(command.resolve({ modal: null }).status, "pass");
  state.id = "bad; command";
  assert.equal(command.resolve({ modal: null }).status, "pass");
  state.id = "session-first";
  ctx.sessions.binding = () => undefined;
  assert.equal(command.resolve({ modal: null }).status, "pass");
});

test("preferences persist independent switches and notify subscribers", () => {
  const mod = load();
  const disk = storage('{"conversation":false,"sidebar":true,"unknown":true}');
  const prefs = mod.preferences(disk);
  let updates = 0;
  const dispose = prefs.subscribe(() => updates++);
  prefs.set("sidebar", false);
  assert.deepEqual(disk.value(), { conversation: false, sidebar: false });
  assert.equal(updates, 1);
  dispose();
  prefs.set("conversation", true);
  assert.equal(updates, 1);
  assert.equal(mod.preferences(disk).getSnapshot().conversation, true);
  prefs.set("unknown", true);
  assert.equal("unknown" in prefs.getSnapshot(), false);
});

test("malformed or unavailable storage leaves both controls usable", () => {
  const mod = load();
  for (const disk of [undefined, storage("not JSON"), storage('{"conversation":"false"}'), { getItem() { throw Error("denied"); }, setItem() { throw Error("denied"); } }]) {
    const prefs = mod.preferences(disk);
    assert.equal(prefs.getSnapshot().conversation, true);
    assert.equal(prefs.getSnapshot().sidebar, true);
    prefs.set("conversation", false);
    assert.equal(prefs.getSnapshot().conversation, false);
    assert.equal(prefs.getSnapshot().sidebar, true);
  }
});

test("search composes through the settings list and root overlay, preserving the native sidebar slot", () => {
  const mod = load({}, { "react-dom": { createPortal() {} } });
  const registered = [];
  const services = { shortcuts: { register: (command) => { registered.push(command.id); return () => {}; } }, uiSession: {}, sessions: {}, conversation: {}, uiWorkspace: {} };
  const ctx = {
    ...services,
    effect: (fn) => fn(),
    slots: {
      inject: (_slot, fn) => fn(),
      register: (options) => {
        // The installed layout owns shell.leading as a single occupied slot;
        // controller-only plugin components must use its root overlay list.
        assert.ok(["settings.section", "shell.overlay"].includes(options.name));
        registered.push(options.name + ":" + options.id);
        return () => {};
      },
    },
    inject: (names, fn) => { if (names.every((name) => name in services)) fn(ctx); },
  };
  mod.installSearch(ctx);
  assert.deepEqual(registered, ["settings.section:deja", "deja.search-session", "shell.overlay:deja-search"]);
});

test("the package and CLI browser panel share a single search owner", () => {
  const mod = load();
  const cleanups = [];
  let settings = 0;
  const ctx = {
    effect: (fn) => { const dispose = fn(); if (dispose) cleanups.push(dispose); },
    inject() {},
    slots: { inject: (_slot, fn) => fn(), register: () => { settings++; return () => {}; } },
  };
  mod.installSearch(ctx);
  mod.installSearch(ctx);
  assert.equal(settings, 1);
  for (const dispose of cleanups) dispose();
  mod.installSearch(ctx);
  assert.equal(settings, 2, "a fresh mount can claim ownership after disposal");
});

test("search query remains one encoded parameter and host errors stay visible", async () => {
  const requests = [];
  const abort = {};
  const mod = load({ fetch: async (url, options) => {
    requests.push({ url, options });
    return { ok: true, json: async () => ({ hits: [], total: 0 }) };
  } });
  await mod.searchHistory("--all & 中文 #?", abort);
  assert.equal(new URL(requests[0].url, "http://localhost/").searchParams.get("q"), "--all & 中文 #?");
  assert.equal(requests[0].options.signal, abort);
  assert.equal(requests[0].options.credentials, "same-origin");
  const failed = load({ fetch: async () => ({ ok: false, json: async () => ({ error: { message: "Deja search timed out." } }) }) });
  await assert.rejects(failed.searchHistory("q"), /timed out/);
  const missing = load({ fetch: async () => ({ ok: false, json: async () => { throw Error("404 HTML"); } }) });
  await assert.rejects(missing.searchHistory("q"), /Update the dsh-deja host plugin/);
  const malformed = load({ fetch: async () => ({ ok: true, json: async () => ({ hits: null }) }) });
  await assert.rejects(malformed.searchHistory("q"), /invalid search data/);
});
