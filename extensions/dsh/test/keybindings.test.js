import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import { test } from "node:test";

let registration;
runInNewContext(readFileSync(new URL("../client.js", import.meta.url), "utf8"), {
  window: { __ModuleLoader__: { load: (value) => { registration = value; } } },
});
const { bindingEditor } = registration.factory(() => ({}));
const key = (code = "KeyJ", extras = {}) => ({ code, ctrlKey: true, preventDefault() {}, stopPropagation() {}, ...extras });
function fixture() {
  const operations = [], protections = [], states = [];
  let config = { status: "ready", revision: "reviewed" };
  const shortcuts = {
    config: { getSnapshot: () => config },
    catalog: { getSnapshot: () => [{ id: "occupied", label: "Other command" }] },
    recording: async (active) => { protections.push(active); },
    describeBinding: (binding) => ({ binding, keys: ["Ctrl", "J"], issue: null, conflicts: ["deja.search-session"] }),
    edit: async (operation, revision) => { operations.push({ operation, revision }); return { status: "saved" }; },
  };
  const editor = bindingEditor(shortcuts, "deja.search-session", (state) => states.push(state));
  return { shortcuts, editor, operations, protections, states, config: (value) => { config = value; } };
}

test("recording awaits native protection and saves once on release using the reviewed revision", async () => {
  const f = fixture();
  await f.editor.start();
  f.editor.keydown(key());
  assert.equal(f.operations.length, 0);
  f.editor.keydown(key("KeyJ", { repeat: true }));
  await f.editor.keyup(key());
  await f.editor.keyup(key());
  assert.equal(f.operations.length, 1);
  const saved = JSON.parse(JSON.stringify(f.operations[0]));
  assert.deepEqual(saved, { operation: { type: "set", id: "deja.search-session", binding: { code: "KeyJ", modifiers: ["control"] } }, revision: "reviewed" });
  assert.deepEqual(f.protections, [true, false]);
  assert.ok(f.states.some((state) => state.message === "已保存"));
});

test("conflicts and reserved combinations never write preferences", async () => {
  for (const description of [{ conflicts: ["occupied"], issue: null }, { conflicts: [], issue: "reserved" }]) {
    const f = fixture();
    f.shortcuts.describeBinding = (binding) => ({ binding, keys: [], ...description });
    await f.editor.start();
    f.editor.keydown(key());
    await f.editor.keyup(key());
    assert.equal(f.operations.length, 0);
    assert.ok(f.states.some((state) => /冲突|不可用/.test(state.message)));
    f.editor.dispose();
  }
});

test("stale edits retain the reviewed revision and report failure without claiming success", async () => {
  const f = fixture();
  await f.editor.start();
  f.config({ status: "ready", revision: "changed" });
  f.shortcuts.edit = async (_operation, revision) => { assert.equal(revision, "reviewed"); return { status: "stale" }; };
  f.editor.keydown(key());
  await f.editor.keyup(key());
  assert.ok(f.states.some((state) => /stale/.test(state.message)));
  assert.equal(f.states.some((state) => state.message === "已保存"), false);
});

test("reset and clear use native per-command operations without touching other bindings", async () => {
  const f = fixture();
  await f.editor.reset();
  await f.editor.clear();
  assert.deepEqual(JSON.parse(JSON.stringify(f.operations)), [
    { operation: { type: "reset", id: "deja.search-session" }, revision: "reviewed" },
    { operation: { type: "set", id: "deja.search-session", binding: null }, revision: "reviewed" },
  ]);
});

test("blur, Escape, IME and AltGraph cancel candidates without saving", async () => {
  for (const cancel of [(f) => f.editor.cancel(), (f) => f.editor.keydown(key("Escape")),
    (f) => f.editor.keydown(key("KeyJ", { isComposing: true })),
    (f) => f.editor.keydown(key("KeyJ", { getModifierState: () => true }))]) {
    const f = fixture();
    await f.editor.start();
    f.editor.keydown(key());
    cancel(f);
    await f.editor.keyup(key());
    assert.equal(f.operations.length, 0);
    f.editor.dispose();
  }
});

test("modifier release commits Command recordings even when the main keyup is missing", async () => {
  const f = fixture();
  await f.editor.start();
  f.editor.keydown(key("KeyJ", { ctrlKey: false, metaKey: true }));
  await f.editor.keyup(key("MetaLeft", { ctrlKey: false }));
  assert.equal(f.operations.length, 1);
  assert.equal(f.operations[0].operation.binding.modifiers.join(), "meta");
  assert.deepEqual(f.protections, [true, false]);
});

test("cancel during asynchronous startup cannot reactivate recording or steal focus", async () => {
  const f = fixture();
  let release;
  f.shortcuts.recording = (active) => { f.protections.push(active); return active ? new Promise((resolve) => { release = resolve; }) : Promise.resolve(); };
  const starting = f.editor.start();
  await Promise.resolve(); await Promise.resolve();
  f.editor.cancel();
  release();
  await starting;
  await Promise.resolve(); await Promise.resolve();
  f.editor.keydown(key());
  await f.editor.keyup(key());
  assert.equal(f.operations.length, 0);
  assert.deepEqual(f.protections, [true, false]);
  assert.equal(f.states.at(-1).recording, false);
});

test("failed startup and disposal during startup release native protection", async () => {
  const f = fixture();
  let release;
  f.shortcuts.recording = (active) => { f.protections.push(active); return active ? new Promise((resolve) => { release = resolve; }) : Promise.resolve(); };
  const starting = f.editor.start();
  await Promise.resolve(); await Promise.resolve();
  f.editor.dispose();
  release();
  await starting;
  await Promise.resolve(); await Promise.resolve();
  assert.deepEqual(f.protections, [true, false]);
  const failed = fixture();
  failed.shortcuts.recording = async () => { throw Error("bridge unavailable"); };
  await failed.editor.start();
  failed.editor.keydown(key());
  await failed.editor.keyup(key());
  assert.equal(failed.operations.length, 0);
  assert.ok(failed.states.some((state) => /无法启用/.test(state.message)));
});
