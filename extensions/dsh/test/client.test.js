// The browser half, run in node against a minimal hooks runtime: enough
// useState and useEffect to render the panel and step it through a session
// switch, without React or a browser.

import assert from "node:assert/strict";
import { test } from "node:test";

function runtime() {
  let slots = [];
  let i = 0;
  let pending = [];
  const React = {
    createElement: (type, props, ...children) => ({ type, props, children }),
    useState(init) {
      const k = i++;
      if (!(k in slots)) slots[k] = { value: typeof init === "function" ? init() : init };
      const slot = slots[k];
      return [slot.value, (next) => { slot.value = typeof next === "function" ? next(slot.value) : next; }];
    },
    useEffect(fn, deps) {
      const k = i++;
      const prev = slots[k];
      if (prev && deps && prev.deps && deps.every((d, j) => Object.is(d, prev.deps[j]))) return;
      pending.push(() => {
        if (prev && typeof prev.cleanup === "function") prev.cleanup();
        slots[k] = { deps, cleanup: fn() };
      });
    },
  };
  return {
    React,
    async render(Component, props) {
      i = 0;
      Component(props);
      const run = pending;
      pending = [];
      run.forEach((f) => f());
      // Let the fetches the effects started settle, then render what they left.
      await new Promise((r) => setTimeout(r, 0));
      i = 0;
      return Component(props);
    },
  };
}

function text(tree) {
  if (!tree) return "";
  if (typeof tree === "string") return tree;
  return (tree.children || []).flat().map(text).join("|");
}

async function load(notesFor) {
  const rt = runtime();
  let registration = null;
  globalThis.window = { __ModuleLoader__: { load: (r) => (registration = r) } };
  globalThis.fetch = async (url) => {
    const sid = new URL(url, "http://x/").searchParams.get("session");
    const notes = notesFor[sid] || [];
    notesFor[sid] = [];
    return { ok: true, json: async () => ({ notes }) };
  };
  await import(`../client.js?${Math.random()}`);
  const mod = registration.factory((name) => {
    if (name === "react") return rt.React;
    throw new Error(`unexpected require ${name}`);
  });
  let Component = null;
  mod.apply({ slots: { inject: (_name, cb) => cb(), register: (_opts, c) => { Component = c; return () => {}; } } });
  return { rt, Component };
}

test("the panel shows a session's lines and draws nothing without them", async () => {
  const { rt, Component } = await load({ A: ["deja-vu — you have been here: \"x\""] });
  const idle = (selector) => selector({ running: false });
  assert.match(text(await rt.render(Component, { sessionId: "A", useSession: idle })), /you have been here/);
});

// dsh may keep the component and hand it another session. Both idle, so no
// turn starts to clear the panel: A's receipt stood under B's conversation.
test("switching to another idle session clears the last one's lines", async () => {
  const { rt, Component } = await load({ A: ["receipt for A"] });
  const idle = (selector) => selector({ running: false });
  assert.match(text(await rt.render(Component, { sessionId: "A", useSession: idle })), /receipt for A/);
  assert.equal(await rt.render(Component, { sessionId: "B", useSession: idle }), null, "A's line is still drawn under B");
});
