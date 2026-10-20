// dsh renders each prompt context through a strict {{name}} interpolation: a
// name it has not registered throws, and the turn ends before the model is
// called. Recalled history is prose from past sessions and quotes templates —
// and that very error — so one recall carrying {{cwd}} broke every turn after
// it (#5421).

import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { inert } from "../lib.js";

// The past session the issue reported, quoted back by deja.
const POISON = 'last time: unknown prompt variable "{{cwd}}" in context "deja:project"; also {{cwd b {{a b}} and {{model}}';

const STAND = mkdtempSync(join(tmpdir(), "deja-template-"));
const BIN = join(STAND, "deja");
writeFileSync(
  BIN,
  `#!/usr/bin/env node
const args = process.argv.slice(2);
if (args[0] === "version") { console.log("deja 0.0.0-test"); process.exit(0); }
if (args[0] === "hook-context" || args[0] === "hook-prompt") console.log(${JSON.stringify(POISON)});
`,
);
chmodSync(BIN, 0o755);
process.env.DEJA_BIN = BIN;
process.env.DSH_HOME = STAND;

// interpolate mirrors renderContextSections in @deepseek-ai/dsh-system-prompt
// 0.2.1-rc.2: a complete {{name}} group must name a registered variable, and a
// {{ that is not one with a }} anywhere after it is malformed. Both throw.
// Hosts before 0.2.1-alpha.2 have no interpolate option on a context.
function render(contexts, assembly, honoursFlag) {
  const variables = { provider: "deepseek", model: "deepseek-chat" };
  return contexts.map((c) => {
    const text = c.text(assembly);
    if (honoursFlag && c.interpolate === false) return text;
    let out = "";
    let last = 0;
    for (let open = text.indexOf("{{"); open >= 0; open = text.indexOf("{{", last)) {
      const group = /^\{\{([^{}]*)\}\}/.exec(text.slice(open));
      if (!group) {
        if (text.indexOf("}}", open + 2) >= 0) throw new Error(`malformed prompt variable reference in context "${c.name}"`);
        out += text.slice(last, open + 2);
        last = open + 2;
        continue;
      }
      if (!Object.hasOwn(variables, group[1])) {
        throw new Error(`unknown prompt variable "{{${group[1]}}}" in context "${c.name}"`);
      }
      out += text.slice(last, open) + variables[group[1]];
      last = open + group[0].length;
    }
    return out + text.slice(last);
  });
}

function assembly(sid) {
  return {
    agent: {
      sessionId: sid,
      session: {
        header: { cwd: STAND },
        events: [{ type: "user/message", data: { role: "user", content: [{ type: "text", text: "why did the turn fail" }] } }],
      },
    },
  };
}

const skip = process.platform === "win32";

for (const honoursFlag of [true, false]) {
  const host = honoursFlag ? "dsh 0.2.1-alpha.2 and later" : "an older dsh";
  test(`recalled {{...}} does not fail the turn on ${host}`, { skip }, async () => {
    const { default: apply } = await import(`../index.js?template-${honoursFlag}`);
    const contexts = [];
    apply({ tools: { register() {} }, commands: { register() {} }, systemPrompt: { context: (c) => contexts.push(c) } }, {});
    assert.deepEqual(contexts.map((c) => c.name), ["deja:project", "deja:recall"]);
    for (const c of contexts) assert.equal(c.interpolate, false, `${c.name} lets dsh read recalled text as a template`);

    const rendered = render(contexts, assembly(`session-${honoursFlag}`), honoursFlag);
    assert.equal(rendered.length, 2);
    for (const text of rendered) {
      assert.ok(text.includes("cwd"), "the recalled text did not reach the model");
      assert.ok(!text.includes("{{"), text);
      assert.ok(!text.includes("deepseek-chat"), "recalled {{model}} was substituted as if deja wrote a template");
    }
  });
}

test("inert splits template braces and leaves the rest alone", () => {
  assert.equal(inert("{{cwd}}"), "{ {cwd} }");
  assert.equal(inert("a {{{b}}} c"), "a { { {b} } } c");
  assert.equal(inert('{"a":{"b":1}}'), '{"a":{"b":1} }');
  assert.equal(inert("plain {one} $x ${y}"), "plain {one} $x ${y}");
  assert.equal(inert(""), "");
  assert.equal(inert(undefined), "");
});
