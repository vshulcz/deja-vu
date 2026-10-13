// The browser half, run in node against a minimal hooks runtime: enough
// useState and useEffect to render the card and step it through a session
// switch, without React or a browser.

import assert from "node:assert/strict";
import { test } from "node:test";

function runtime() {
  const slots = [];
  let i = 0;
  let pending = [];
  const React = {
    createElement: (type, props, ...children) => ({ type, props: props || {}, children }),
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
  // Function components are expanded in place, sharing one hook list, which
  // holds while the tree keeps its shape between renders.
  const expand = (node) => {
    if (Array.isArray(node)) return node.map(expand);
    if (!node || typeof node !== "object") return node;
    if (typeof node.type === "function") return expand(node.type({ ...node.props, children: node.children }));
    return { ...node, children: node.children.map(expand) };
  };
  return {
    React,
    async render(Component, props) {
      i = 0;
      expand(Component(props));
      const run = pending;
      pending = [];
      run.forEach((f) => f());
      // Let the fetches the effects started settle, then render what they left.
      await new Promise((r) => setTimeout(r, 0));
      i = 0;
      return expand(Component(props));
    },
  };
}

function text(tree) {
  if (tree === null || tree === undefined || tree === false) return "";
  if (typeof tree === "string" || typeof tree === "number") return String(tree);
  if (Array.isArray(tree)) return tree.map(text).join("|");
  return text(tree.children);
}

function find(tree, pred, out = []) {
  if (Array.isArray(tree)) tree.forEach((t) => find(t, pred, out));
  else if (tree && typeof tree === "object") {
    if (pred(tree)) out.push(tree);
    find(tree.children, pred, out);
  }
  return out;
}

async function load(notesFor = {}) {
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
  return { rt, Component, mod };
}

const idle = (selector) => selector({ running: false });

// What deja printed on the stand, verbatim.
const STAND = [
  "deja recalled 3 prior sessions from this project · today: memory arrived 3 times",
  "deja indexed 13 sessions (1 agent) · docs: vshulcz.github.io/deja-vu",
  "if deja is useful, a GitHub star helps others find it — github.com/vshulcz/deja-vu",
  'deja-vu — you have been here: "how should the zorbit queue drain on deploy" (3d ago · via grace)',
];

test("the card leads with the receipt and drops deja's name from each line", async () => {
  const { mod } = await load();
  const p = mod.parseNotes(STAND);
  // Only the brand is stripped: the lead says what the quote is.
  assert.deepEqual(p.receipt, { lead: "You have been here:", topic: "how should the zorbit queue drain on deploy", meta: "3d ago · via grace" });
  // An older deja wrote "via:"; it reads the same.
  assert.equal(mod.parseNotes(['deja-vu — you have been here: "x" (3d ago · via: drain, zorbit)']).receipt.meta, "3d ago · via drain, zorbit");
  assert.equal(
    mod.parseNotes(['deja-vu — this was done on another machine: "rotate the keys" (2w ago · via: rotate)']).receipt.lead,
    "This was done on another machine:",
  );
  assert.deepEqual(p.notes, [
    "Recalled 3 prior sessions from this project · today: memory arrived 3 times",
    "Indexed 13 sessions (1 agent)",
  ]);
  // The docs address at the end of the built note goes to the link row.
  assert.deepEqual(p.links, [{ label: "Docs", href: "https://vshulcz.github.io/deja-vu" }]);
  assert.deepEqual(p.linked, [{ text: "if deja is useful, a GitHub star helps others find it", href: "https://github.com/vshulcz/deja-vu" }]);
  // A line of nothing but links, as `deja install` prints it, is all link row.
  const older = mod.parseNotes(["docs: vshulcz.github.io/deja-vu · issues: github.com/vshulcz/deja-vu/issues"]);
  assert.deepEqual(older.links.map((l) => l.label), ["Docs", "Issues"]);
  assert.deepEqual(older.notes, []);
  // A link part in the middle of a line is text: only the tail is peeled.
  assert.deepEqual(mod.parseNotes(["docs: vshulcz.github.io/deja-vu · then more"]).notes, ["docs: vshulcz.github.io/deja-vu · then more"]);
  // The name only goes where it leads the line.
  assert.equal(mod.stripName("deja this week: 4 recalls"), "This week: 4 recalls");
  assert.equal(mod.stripName("dejavu is not deja"), "dejavu is not deja");
  assert.equal(mod.stripName("the index is behind"), "the index is behind");
});

test("only an http(s) address becomes a link", async () => {
  const { mod } = await load();
  assert.equal(mod.safeHref("github.com/vshulcz/deja-vu"), "https://github.com/vshulcz/deja-vu");
  assert.equal(mod.safeHref("http://example.com/x"), "http://example.com/x");
  assert.equal(mod.safeHref("javascript:alert(1)"), null);
  assert.equal(mod.safeHref("data:text/html,hi"), null);
  assert.equal(mod.safeHref("/api/deja-notes"), null);
  assert.equal(mod.safeHref("localhost"), null);
  // A line whose address is refused stays text.
  const p = mod.parseNotes(["docs: javascript:alert(1)", "see this — javascript:alert(1)"]);
  assert.deepEqual(p.links, []);
  assert.deepEqual(p.linked, []);
  assert.equal(p.notes.length, 2);
});

test("the card keeps to the receipt and one note, the rest behind more, links in view", async () => {
  const { rt, Component } = await load({ A: [...STAND] });
  const tree = await rt.render(Component, { sessionId: "A", useSession: idle });
  const shown = text(tree);
  assert.match(shown, /deja-vu/);
  assert.match(shown, /how should the zorbit queue drain on deploy/);
  assert.match(shown, /Recalled 3 prior sessions/);
  assert.doesNotMatch(shown, /Indexed 13 sessions/, "a second note is not behind the toggle");
  assert.match(shown, /1 more/);
  // The one-time sentence and the docs are links in view, not raw addresses.
  assert.match(shown, /a GitHub star/);
  assert.doesNotMatch(shown, /github\.com/);
  const links = find(tree, (n) => n.type === "a");
  assert.deepEqual(links.map((a) => text(a)), ["Docs", "if deja is useful, a GitHub star helps others find it"]);
  for (const a of links) {
    assert.equal(a.props.target, "_blank");
    assert.match(a.props.rel, /noopener/);
    assert.match(a.props.href, /^https?:\/\//);
  }
});

// dsh may keep the component and hand it another session. Both idle, so no
// turn starts to clear the card: A's receipt stood under B's conversation.
test("switching to another idle session clears the last one's lines", async () => {
  const { rt, Component } = await load({ A: ["receipt for A"] });
  assert.match(text(await rt.render(Component, { sessionId: "A", useSession: idle })), /receipt for A/);
  assert.equal(await rt.render(Component, { sessionId: "B", useSession: idle }), null, "A's line is still drawn under B");
});
