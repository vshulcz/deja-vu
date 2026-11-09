import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { runInNewContext } from "node:vm";

const source = readFileSync(new URL("../client.js", import.meta.url), "utf8");
function load(extras = {}, modules = {}) {
  let registration;
  const context = {
    window: { __ModuleLoader__: { load: (r) => { registration = r; } } },
    URL,
    // The sidebar adapter owns its DOM seams: a detached result container and
    // an attribute observer that never fires in this fixture.
    document: { createElement: () => ({ className: "", dataset: {}, hidden: false, remove() {} }) },
    MutationObserver: class { observe() {} disconnect() {} },
    setTimeout,
    clearTimeout,
    ...extras,
  };
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
function sidebarAnchor(expanded = false) {
  const listeners = { input: new Map(), button: new Map() };
  const emit = (target, event) => {
    const handler = listeners[target].get(event.type);
    if (handler) handler(event);
    return true;
  };
  const input = { type: "text", value: "", parentElement: null, focus() {}, dispatchEvent: (event) => emit("input", event) };
  const button = { aria: expanded ? "true" : "false", pressed: null, title: null, parentElement: null, dispatchEvent: (event) => emit("button", event) };
  for (const [name, target] of [["input", input], ["button", button]]) {
    target.addEventListener = (type, handler) => listeners[name].set(type, handler);
    target.removeEventListener = (type) => listeners[name].delete(type);
  }
  const list = { hidden: false, className: "fixture_listArea", parentElement: { appendChild() {}, removeChild() {} } };
  button.getAttribute = (name) => name === "aria-expanded" ? button.aria : button[name === "aria-pressed" ? "pressed" : name === "title" ? "title" : "pressed"];
  button.setAttribute = (name, value) => { if (name === "aria-expanded") button.aria = value; else if (name === "aria-pressed") button.pressed = value; else button.title = value; };
  button.removeAttribute = (name) => { if (name === "aria-expanded") button.aria = null; else if (name === "aria-pressed") button.pressed = null; else button.title = null; };
  // The adapter walks input → its parent container → the expanded button.
  input.parentElement = { querySelector: () => button };
  const anchor = {
    input, button, list,
    querySelector(selector) {
      if (selector === 'input[type="text"]') return input;
      if (selector === 'button[aria-expanded]') return button;
      if (selector === '[class*="_listArea"]') return list;
      return null;
    },
  };
  return anchor;
}
function shortcuts(mod, prefs) {
  const state = { id: "session-first", drafts: new Map(), projections: new Map(), focused: [], persisted: [] };
  let command;
  const ctx = {
    shortcuts: { register(value) { command = value; return () => {}; } },
    uiSession: { current: { getSnapshot: () => ({ key: state.id }) } },
    sessions: { binding: (id) => ({ ctx: { id } }) },
    conversation: { input: { for: ({ id }) => ({
      projection: state.projections.get(id) || { clipboardText: state.drafts.get(id)?.text || "", occurrences: [] },
      actions: { setDraft: (draft) => state.drafts.set(id, draft), persistDraft: () => state.persisted.push(id) },
      focus: () => state.focused.push(id),
    }) } },
  };
  mod.installFind(ctx, prefs);
  return { command, ctx, state };
}

test("Mod+F writes a real /deja command and keeps the draft and its references", () => {
  const mod = load();
  const { command, state } = shortcuts(mod, mod.preferences());
  state.projections.set("session-first", { clipboardText: "中文 query", occurrences: [] });
  const action = command.resolve({ modal: null, region: "editable" });
  assert.equal(action.status, "handled");
  action.run();
  assert.deepEqual(JSON.parse(JSON.stringify(state.drafts.get("session-first"))), {
    text: "/deja --session first -- 中文 query",
    command: { token: "/deja" },
    references: [],
  });
  assert.deepEqual(state.persisted, ["session-first"]);
  assert.deepEqual(state.focused, ["session-first"]);
  // A second press focuses the same command instead of nesting a prefix.
  command.resolve({ modal: null }).run();
  assert.equal(state.drafts.get("session-first").text, "/deja --session first -- 中文 query");
  // The README promises these exact defaults for every platform.
  // web:linux has no default: the browser service admits only a short
  // allowlist there, and a rejected default throws out of register(), which
  // would leave the command unregistered on every platform.
  const promised = {
    "desktop:windows": "KeyF:primary",
    "desktop:linux": "KeyF:primary",
    "desktop:macos": "KeyF:primary",
    "web:windows": "KeyF:primary+alt",
    "web:macos": "KeyF:primary+alt",
  };
  for (const [profile, expected] of Object.entries(promised)) {
    const row = command.defaults[profile];
    assert.equal(row && row.code + ":" + row.modifiers.join("+"), expected,
      profile + " must match the documented default");
  }
  assert.equal(command.defaults["web:linux"], undefined,
    "web:linux must stay unbound so register() does not throw");
});

test("a reference chip keeps its identity and moves past the command prefix", () => {
  const mod = load();
  const { command, state } = shortcuts(mod, mod.preferences());
  const chip = { source: "file", ref: "src/app.js", label: "app.js", offset: 0, length: 10, clipboardText: "@src/app.j", appearance: "file" };
  state.projections.set("session-first", { clipboardText: chip.clipboardText + " tail", occurrences: [chip] });
  command.resolve({ modal: null }).run();
  const draft = state.drafts.get("session-first");
  assert.equal(draft.text, "/deja --session first -- @src/app.j tail");
  assert.equal(draft.references.length, 1);
  assert.deepEqual(JSON.parse(JSON.stringify(draft.references[0])), {
    source: "file", ref: "src/app.js", label: "app.js",
    offset: "/deja --session first -- ".length, length: 10, clipboardText: "@src/app.j", appearance: "file",
  });
});

test("a draft led by someone else's command is left alone", () => {
  const mod = load();
  const { command, state } = shortcuts(mod, mod.preferences());
  state.projections.set("session-first", { clipboardText: "/review this diff", occurrences: [], command: { token: "/review" } });
  command.resolve({ modal: null }).run();
  assert.equal(state.drafts.has("session-first"), false, "another command's chip was overwritten");
  assert.deepEqual(state.focused, [], "a draft left alone is not re-focused");
});

test("session ids dsh would not write leave the key to DSH", () => {
  const mod = load();
  const { command, state } = shortcuts(mod, mod.preferences());
  state.id = "session-bad id";
  assert.equal(command.resolve({ modal: null }).status, "pass");
});

test("navigation between resolution and execution never writes the previous session", () => {
  const mod = load();
  const { command, state } = shortcuts(mod, mod.preferences());
  const action = command.resolve({ modal: null });
  state.id = "session-second";
  action.run();
  assert.equal(state.drafts.has("session-first"), false);
  assert.equal(state.drafts.get("session-second").text, "/deja --session second -- ");
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
  assert.deepEqual(disk.value(), { conversation: false, sidebar: false, opensDeja: true });
  assert.equal(updates, 1);
  dispose();
  prefs.set("conversation", true);
  assert.equal(updates, 1);
  assert.equal(mod.preferences(disk).getSnapshot().conversation, true);
  prefs.set("unknown", true);
  assert.equal("unknown" in prefs.getSnapshot(), false);
  // A saved v1 document without the new switch falls back to its default.
  const upgradedDisk = storage('{"conversation":true,"sidebar":true}');
  const upgraded = mod.preferences(upgradedDisk);
  assert.equal(upgraded.getSnapshot().opensDeja, true);
  upgraded.set("opensDeja", false);
  assert.deepEqual(JSON.parse(upgradedDisk.getItem("dsh.deja.search.v1")), { conversation: true, sidebar: true, opensDeja: false });
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

test("Mod+Shift+K opens the sidebar in Deja full-text mode and the magnifier restores the native view", () => {
  const mod = load();
  const prefs = mod.preferences();
  const anchor = sidebarAnchor(true);
  anchor.list.hidden = false;
  const views = [];
  const attachment = mod.attachSidebar(anchor, prefs, (view) => views.push(view));
  assert.ok(attachment, "the adapter attaches to the semantic search controls");
  assert.deepEqual(views.map((view) => view.active), [false]);
  // Exercise the real event path: the deja.search-sessions command calls
  // prefs.requestOpen(), which notifies onOpen listeners and claims the
  // expansion. Calling attachment.claimOpen() directly would bypass it.
  prefs.requestOpen();
  assert.deepEqual(views.map((view) => view.active), [false, true], "the Mod+Shift+K gesture lands in Deja mode");
  assert.equal(anchor.list.hidden, true);
  assert.equal(anchor.button.getAttribute("aria-pressed"), "true");
  assert.equal(prefs.getSnapshot().opensDeja, true);
  // The magnifier is the escape hatch back to the native search.
  anchor.button.dispatchEvent({ type: "click", bubbles: true, cancelable: true, preventDefault() {}, stopPropagation() {} });
  assert.equal(views.at(-1).active, false, "the click restores the native search");
  assert.equal(anchor.list.hidden, false);
  assert.equal(anchor.button.getAttribute("aria-pressed"), "false");
  // After the handoff the claim is spent: the next opening stays native
  // unless Deja claims it again.
  anchor.button.setAttribute("aria-expanded", "true");
  assert.equal(views.at(-1).active, false);
  attachment.dispose();
});

test("the claim helper only settles while the sidebar is expanding, and is released otherwise", async () => {
  const mod = load();
  const prefs = mod.preferences();
  const anchor = sidebarAnchor(false);
  const views = [];
  const attachment = mod.attachSidebar(anchor, prefs, (view) => views.push(view));
  // A collapsed search has nothing to claim, so the pending flag is released
  // after the bounded wait rather than by the first synchronous check.
  assert.equal(attachment.claimOpen(), null, "the claim follows the expansion asynchronously");
  await new Promise((resolve) => setTimeout(resolve, 550));
  assert.deepEqual(views.map((view) => view.active), [false], "a search that never opens leaves the native view alone");
  anchor.button.setAttribute("aria-expanded", "true");
  assert.equal(views.at(-1).active, false, "an unclaimed opening stays native");
  anchor.button.setAttribute("aria-expanded", "false");
  // The switches gate the takeover independently.
  prefs.set("sidebar", false);
  assert.equal(attachment.claimOpen(), false, "a disabled sidebar cannot claim");
  prefs.set("opensDeja", false);
  prefs.set("sidebar", true);
  assert.equal(attachment.claimOpen(), false, "the takeover switch can be turned off");
  // A claim settles as soon as the expansion the shortcut triggered lands.
  prefs.set("opensDeja", true);
  assert.equal(attachment.claimOpen(), null, "the claim waits for the expansion");
  anchor.button.setAttribute("aria-expanded", "true");
  await new Promise((resolve) => setTimeout(resolve, 30));
  assert.equal(views.at(-1).active, true, "the expansion the shortcut opened becomes Deja's");
  attachment.dispose();
});

test("the direct Deja search shortcut keeps its own binding distinct from native Mod+K", () => {
  const mod = load({}, { "react-dom": { createPortal() {} } });
  const commands = new Map();
  const services = { shortcuts: { register: (command) => { commands.set(command.id, command); return () => {}; } }, uiSession: {}, sessions: {}, conversation: {}, uiWorkspace: {} };
  const ctx = {
    ...services,
    effect: (fn) => fn(),
    slots: { inject: (_slot, fn) => fn(), register: () => () => {} },
    inject(names, fn) {
      if (!names.every((name) => name in services)) return;
      fn(ctx);
    },
  };
  mod.searchRoute().set("present");
  mod.installSearch(ctx);
  const direct = commands.get("deja.search-sessions");
  assert.ok(direct, "a discoverable direct binding is registered");
  // These are the exact bindings the README promises. Keep them in sync:
  // `primary` is the registry's logical modifier — Cmd on macOS, Ctrl
  // elsewhere — so one declaration covers every desktop platform.
  // web:linux stays unbound: the browser service admits only a short
  // allowlist there, and a rejected default throws out of register(), which
  // would leave the command unregistered on every platform.
  const promised = {
    "desktop:windows": "KeyK:primary+shift",
    "desktop:linux": "KeyK:primary+shift",
    "desktop:macos": "KeyK:primary+shift",
    "web:windows": "KeyK:primary+shift",
    "web:macos": "KeyK:primary+shift",
  };
  for (const [profile, expected] of Object.entries(promised)) {
    const row = direct.defaults[profile];
    assert.equal(row && row.code + ":" + row.modifiers.join("+"), expected,
      profile + " must match the documented default");
  }
  assert.equal(direct.defaults["web:linux"], undefined,
    "web:linux must stay unbound so register() does not throw");
  // The chord must fire from the composer too: the dispatcher passes when the
  // focused region is not listed, and the message input is an editable region.
  // Compare serialized values: the command comes from the plugin's vm realm.
  assert.equal(JSON.stringify(direct.regions), JSON.stringify(["page", "editable"]),
    "deja.search-sessions must fire from the editable composer, like dsh-shortcuts");
  // The official session.search keeps primary+K; Deja must not collide with it.
  const keys = new Set([...commands.values()].map((command) => command.defaults["desktop:windows"]?.code + ":" + (command.defaults["desktop:windows"]?.modifiers || []).join("+")));
  assert.equal(keys.has("KeyK:primary+shift"), true);
  assert.equal(keys.has("KeyK:primary"), false, "the native session.search binding stays untouched");
  // The command closes over installSearch's own preferences object; reaching
  // it through a second preferences() instance would read a different copy.
  // The switch is exercised end to end through the sidebar takeover tests
  // above; here the binding's own resolution contract is what matters.
  assert.equal(direct.resolve({ modal: "settings" }).status, "pass", "modals keep the key for DSH");
  assert.equal(direct.resolve({ modal: null }).status, "handled", "the direct binding runs when enabled");
  assert.doesNotThrow(() => direct.resolve({ modal: null }).run());
});

test("all Deja commands are editable in the built-in shortcuts editor", () => {
  const mod = load({}, { "react-dom": { createPortal() {} } });
  const commands = new Map();
  const services = { shortcuts: { register: (command) => { commands.set(command.id, command); return () => {}; } }, uiSession: {}, sessions: {}, conversation: {}, uiWorkspace: {} };
  const ctx = {
    ...services,
    effect: (fn) => fn(),
    slots: { inject: (_slot, fn) => fn(), register: () => () => {} },
    inject(names, fn) { if (names.every((name) => name in services)) fn(ctx); },
  };
  mod.installFind(ctx, mod.preferences());
  mod.installSearch(ctx);
  // Normal register() commands are editable; registerFixed() is read-only.
  const registered = [...commands.values()];
  for (const command of registered) {
    assert.equal(typeof command.label, "function", `${command.id} needs a label for the editor row`);
    assert.ok(Array.isArray(command.aliases) && command.aliases.length > 0, `${command.id} needs aliases so search finds it`);
    assert.ok(command.defaults["desktop:windows"], `${command.id} needs a default users can reset to`);
  }
  assert.deepEqual(registered.map((command) => command.id).sort(), ["deja.search-session", "deja.search-sessions"]);
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
  assert.deepEqual(registered, ["settings.section:deja", "deja.search-session", "shell.overlay:deja-search", "deja.search-sessions"]);
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

test("a CLI-only install without the search route keeps its keys and hides the sidebar mode", () => {
  // `deja install dsh-auto` loads this same browser file without the route
  // the package's index.js registers. A 404 must stand the whole sidebar
  // feature down: the key stays DSH's and the switches are not offered.
  const mod = load({ fetch: async () => ({ status: 404 }) }, { "react-dom": { createPortal() {} } });
  const commands = new Map();
  const services = { shortcuts: { register: (command) => { commands.set(command.id, command); return () => {}; } }, uiSession: {}, sessions: {}, conversation: {}, uiWorkspace: {} };
  const ctx = {
    ...services,
    effect: (fn) => fn(),
    slots: { inject: (_slot, fn) => fn(), register: () => () => {} },
    inject(names, fn) { if (names.every((name) => name in services)) fn(ctx); },
  };
  const route = mod.searchRoute();
  mod.installSearch(ctx);
  assert.equal(route.getSnapshot(), "unknown", "the probe runs asynchronously, and until then the key stays DSH's");
  const direct = commands.get("deja.search-sessions");
  assert.equal(direct.resolve({ modal: null }).status, "pass", "no sidebar search to open yet");
  const settled = new Promise((resolve) => setTimeout(resolve, 0));
  return settled.then(() => {
    assert.equal(route.getSnapshot(), "absent", "a 404 means no search endpoint");
    assert.equal(route.getVersion(), "", "an absent route has no version to report");
    assert.equal(direct.resolve({ modal: null }).status, "pass", "the absent route keeps releasing the key");
    // A live route names the plugin behind it, which is what Settings shows.
    const probes = [];
    const live = load({ fetch: async (url) => {
      probes.push(url);
      return { status: 200, json: async () => ({ ok: true, plugin: "dsh-deja", version: "0.24.0" }) };
    } });
    const store = live.searchRoute();
    live.installSearch({ inject() {}, slots: { inject: () => {}, register: () => () => {} } });
    return new Promise((resolve) => setTimeout(resolve, 0)).then(async () => {
      assert.equal(store.getSnapshot(), "present");
      assert.equal(store.getVersion(), "0.24.0", "the installed version is not reported");
      assert.match(probes[0], /\?probe=1/, "the probe is not the route's own ping");
      // Only the route's own probe payload is evidence of a working endpoint.
      // Every other answer (500, HTML fallback, wrong body) must stand the
      // sidebar down: a toggle with no route behind it is worse than no toggle.
      const badResponses = {
        server_error: { status: 500, ok: false, json: async () => ({ ok: true, plugin: "dsh-deja" }) },
        bad_gateway: { status: 502, ok: false, json: async () => { throw Error("HTML"); } },
        html_fallback: { status: 200, ok: true, json: async () => { throw Error("Unexpected token '<'"); } },
        foreign_route: { status: 200, ok: true, json: async () => ({ ok: true, plugin: "something-else" }) },
        missing_ok: { status: 200, ok: true, json: async () => ({ version: "0.24.0" }) },
        empty_body: { status: 204, ok: true, json: async () => null },
      };
      for (const [name, response] of Object.entries(badResponses)) {
        const host = load({ fetch: async () => response });
        const store = host.searchRoute();
        host.installSearch({ inject() {}, slots: { inject: () => {}, register: () => () => {} } });
        await new Promise((resolve) => setTimeout(resolve, 0));
        assert.equal(store.getSnapshot(), "absent", name + " must not enable the sidebar");
        assert.equal(store.getVersion(), "", name + " must not report a version");
      }
    });
  });
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
