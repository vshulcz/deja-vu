// Guard the real acceptance rule of DSH's shortcut service: a default that
// the service rejects makes register() throw, which leaves the command
// unregistered on EVERY platform. This test loads the shipped client.js and
// reads the defaults it actually registers.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { runInNewContext } from "node:vm";

const source = readFileSync(new URL("../client.js", import.meta.url), "utf8");

// The service normalizes `primary` to the device modifier before checking
// anything, and compares its own allowlist candidates the same way, so both
// sides of the comparison below are physical modifiers.
const WEB_LINUX_ALLOWLIST = [
  { code: "Slash", modifiers: ["primary"] },
  { code: "Comma", modifiers: ["primary", "shift"] },
  { code: "Period", modifiers: ["primary", "shift"] },
];

function normalize(row, platform) {
  const primary = platform === "macos" ? "meta" : "control";
  return { code: row.code, modifiers: row.modifiers.map((m) => (m === "primary" ? primary : m)) };
}

function bindingKey(row) {
  return row.modifiers.join("+") + "+" + row.code;
}

function webBindingAllowed(row, platform) {
  const binding = normalize(row, platform);
  if (platform === "windows" || platform === "macos") {
    if (binding.modifiers.length >= 3) return true;
    const primary = platform === "macos" ? "meta" : "control";
    if (binding.modifiers.length === 2 && binding.modifiers.includes(primary)
      && (binding.modifiers.includes("alt") || binding.modifiers.includes("shift"))) return true;
  }
  return WEB_LINUX_ALLOWLIST.some((c) => bindingKey(normalize(c, platform)) === bindingKey(binding));
}

// The defaults come from the plugin's own vm realm, so compare serialized
// values rather than deep-comparing foreign-realm objects.
function sameBinding(left, right) {
  return JSON.stringify(left) === JSON.stringify(right);
}

// Load the plugin the way the host does and collect what it registers.
function registeredDefaults() {
  let registration;
  const context = {
    window: { __ModuleLoader__: { load: (r) => { registration = r; } } },
    URL,
    document: { createElement: () => ({ className: "", dataset: {}, hidden: false, remove() {} }) },
    MutationObserver: class { observe() {} disconnect() {} },
    setTimeout,
    clearTimeout,
  };
  runInNewContext(source, context);
  const mod = registration.factory(() => ({}));
  const commands = new Map();
  const ctx = {
    shortcuts: { register: (command) => { commands.set(command.id, command); return () => {}; } },
    uiSession: {}, sessions: {}, conversation: {}, uiWorkspace: {},
    effect: (fn) => fn(),
    slots: { inject: (_slot, fn) => fn(), register: () => () => {} },
    inject(names, fn) { if (names.every((name) => name in ctx)) fn(ctx); },
  };
  mod.installFind(ctx, mod.preferences());
  mod.installSearch(ctx);
  return commands;
}

const commands = registeredDefaults();

for (const commandId of ["deja.search-session", "deja.search-sessions"]) {
  test(commandId + " declares defaults the shortcut service accepts on every profile", () => {
    const command = commands.get(commandId);
    assert.ok(command, commandId + " must be registered");
    const defaults = command.defaults;
    assert.ok(Object.keys(defaults).length > 0, commandId + " must declare defaults");
    for (const [profile, row] of Object.entries(defaults)) {
      const [runtime, platform] = profile.split(":");
      if (runtime === "web") {
        assert.equal(webBindingAllowed(row, platform), true,
          profile + " must be accepted by the browser service: " + JSON.stringify(row));
      }
      assert.equal(row.modifiers.length === 0, false, profile + " needs a modifier");
      assert.equal(row.modifiers.every((m) => m === "shift"), false, profile + " needs a non-shift modifier");
    }
  });

  test(commandId + " leaves web:linux unbound so one rejected profile cannot unregister it", () => {
    const command = commands.get(commandId);
    assert.ok(command, commandId + " must be registered");
    assert.equal(command.defaults["web:linux"], undefined,
      "web:linux must be omitted: the browser service rejects it and register() throws");
  });
}

test("the documented shortcuts match the declared defaults", () => {
  const readme = readFileSync(new URL("../README.md", import.meta.url), "utf8");
  assert.match(readme, /Mod\+Shift\+K opens Deja full-text search/);  const find = commands.get("deja.search-session").defaults;
  assert.ok(sameBinding(find["desktop:windows"], { code: "KeyF", modifiers: ["primary"] }),
    "desktop:windows for deja.search-session: " + JSON.stringify(find["desktop:windows"]));
  const sessions = commands.get("deja.search-sessions").defaults;
  assert.ok(sameBinding(sessions["desktop:windows"], { code: "KeyK", modifiers: ["primary", "shift"] }),
    "desktop:windows for deja.search-sessions: " + JSON.stringify(sessions["desktop:windows"]));
});
