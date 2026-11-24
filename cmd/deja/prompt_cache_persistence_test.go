package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func statefulStubDeja(t *testing.T, dir string) (bin string, counts func(name string) int) {
	t.Helper()
	bin = filepath.Join(dir, "deja")
	script := `#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const dir = ` + strconv.Quote(dir) + `;
const args = process.argv.slice(2);
const cmd = args[0] || "";
const bump = (name) => {
  const f = path.join(dir, "cnt_" + name);
  let n = 0;
  try { n = Number(fs.readFileSync(f, "utf8")) || 0; } catch {}
  n++;
  fs.writeFileSync(f, String(n));
  return n;
};
if (cmd === "version") { process.stdout.write("0.0.0\n"); process.exit(0); }
if (cmd === "warmup-status" || cmd === "statusline") { process.exit(0); }
const input = fs.readFileSync(0, "utf8");
const plain = args.includes("--plain");
if (cmd === "hook-context") {
  let payload = {};
  try { payload = JSON.parse(input); } catch {}
  const n = bump("ctx");
  if (String(payload.cwd || "").includes("__empty_ctx__")) process.exit(0);
  if (payload.deja_once === true && payload.session_id) {
    const mark = path.join(dir, "once_" + String(payload.session_id).replace(/[^a-zA-Z0-9_.-]/g, "_"));
    if (fs.existsSync(mark)) process.exit(0);
    fs.writeFileSync(mark, "1");
  }
  process.stdout.write(plain ? "DIGEST-" + n : JSON.stringify({ hookSpecificOutput: { additionalContext: "DIGEST-" + n } }) + "\n");
} else if (cmd === "hook-prompt") {
  let payload = {};
  try { payload = JSON.parse(input); } catch {}
  if (!payload.prompt) process.exit(0);
  const n = bump("prompt");
  if (payload.prompt === "__miss__") process.exit(0);
  const blk = "<deja-recall>\nPROMPT-" + n + "\n</deja-recall>";
  process.stdout.write(plain ? blk : JSON.stringify({ hookSpecificOutput: { additionalContext: blk } }) + "\n");
} else if (cmd === "hook-tool-after") {
  const n = bump("after");
  const line = "REPAIR-" + n;
  process.stdout.write(plain ? line : JSON.stringify({ hookSpecificOutput: { additionalContext: line } }) + "\n");
} else if (cmd === "hook-precompact") {
  bump("precompact");
  fs.writeFileSync(path.join(dir, "last_hook-precompact.json"), input);
} else if (cmd === "hook-session-end") {
  bump("end");
  fs.writeFileSync(path.join(dir, "last_hook-session-end.json"), input);
}
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	counts = func(name string) int {
		b, err := os.ReadFile(filepath.Join(dir, "cnt_"+name))
		if err != nil {
			return 0
		}
		n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		return n
	}
	return bin, counts
}

func runNodeScriptInDir(t *testing.T, dir, scriptName, code string, extraArgs ...string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin")
	}
	scriptPath := filepath.Join(dir, scriptName)
	if err = os.WriteFile(scriptPath, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append(append([]string(nil), extraArgs...), scriptPath)
	cmd := exec.CommandContext(t.Context(), node, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+filepath.Join(dir, ".xdg-cache"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node %s: %v\n%s", scriptName, err, out)
	}
}

func TestOpencodeGeneratedPluginsPersistStateAcrossRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses #!/usr/bin/env node executable stub")
	}
	for _, tc := range []struct {
		name  string
		v2    bool
		build func(bin string) string
	}{
		{"opencode-2x", true, opencodePluginJS},
		{"opencode-1x", false, func(bin string) string { return legacyPluginJSFor("opencode-auto", bin) }},
		{"kilocode-1x", false, func(bin string) string { return legacyPluginJSFor("kilocode-auto", bin) }},
		{"mimo-1x", false, func(bin string) string { return legacyPluginJSFor("mimo-auto", bin) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin, counts := statefulStubDeja(t, dir)
			pluginPath := filepath.Join(dir, "plugin.mjs")
			if err := os.WriteFile(pluginPath, []byte(tc.build(bin)), 0o644); err != nil {
				t.Fatal(err)
			}

			var proc1, proc2 string
			if tc.v2 {
				proc1 = `
import assert from "node:assert/strict";
import plugin from ` + strconv.Quote(pluginPath) + `;
const contextHooks = [];
const compactHooks = [];
const ctx = {
  location: { directory: ` + strconv.Quote(dir) + ` },
  session: {
    get: async () => ({}),
    hook: async (ev, fn) => {
      if (ev === "context") contextHooks.push(fn);
      if (ev === "compaction") compactHooks.push(fn);
    },
  },
  tool: { hook: async () => {} },
};
const cleanup = await plugin.setup(ctx);
const evA = { sessionID: "ses_A", system: [{ type: "text", text: "base" }], messages: [{ id: "mA1", role: "user", content: [{ type: "text", text: "first q\n" }] }] };
for (const fn of contextHooks) await fn(evA);
assert.equal(evA.system[0].text, "base\n\nDIGEST-1");
assert.equal(evA.messages[0].content[0].text, "first q\n\n<deja-recall>\nPROMPT-1\n</deja-recall>");

// Re-running on kept system & kept message must emit byte-identical prefixes.
for (const fn of contextHooks) await fn(evA);
assert.equal(evA.system[0].text, "base\n\nDIGEST-1");
assert.equal(evA.messages[0].content[0].text, "first q\n\n<deja-recall>\nPROMPT-1\n</deja-recall>");

// Empty system array reused across repeated calls must not duplicate DIGEST.
const evEmptySys = { sessionID: "ses_EmptySys", system: [], messages: [] };
for (const fn of contextHooks) await fn(evEmptySys);
for (const fn of contextHooks) await fn(evEmptySys);
assert.equal(evEmptySys.system.length, 1);
assert.equal(evEmptySys.system[0].text, "DIGEST-2");

const evB = { sessionID: "ses_B", system: [{ type: "text", text: "base" }], messages: [{ id: "mB1", role: "user", content: [{ type: "text", text: "other q" }] }] };
for (const fn of contextHooks) await fn(evB);
assert.equal(evB.messages[0].content[0].text, "other q\n\n<deja-recall>\nPROMPT-2\n</deja-recall>");
if (typeof cleanup === "function") await cleanup();
`
				proc2 = `
import assert from "node:assert/strict";
import plugin from ` + strconv.Quote(pluginPath) + `;
const contextHooks = [];
const compactHooks = [];
const ctx = {
  location: { directory: ` + strconv.Quote(dir) + ` },
  session: {
    get: async () => ({}),
    hook: async (ev, fn) => {
      if (ev === "context") contextHooks.push(fn);
      if (ev === "compaction") compactHooks.push(fn);
    },
  },
  tool: { hook: async () => {} },
};
const cleanup = await plugin.setup(ctx);
const evA2 = {
  sessionID: "ses_A",
  system: [{ type: "text", text: "base" }],
  messages: [
    { id: "mA1", role: "user", content: [{ type: "text", text: "first q\n" }] },
    { id: "mA2", role: "user", content: [{ type: "text", text: "follow up" }] },
  ],
};
for (const fn of contextHooks) await fn(evA2);
assert.equal(evA2.system[0].text, "base\n\nDIGEST-1");
assert.equal(evA2.messages[0].content[0].text, "first q\n\n<deja-recall>\nPROMPT-1\n</deja-recall>");
assert.equal(evA2.messages[1].content[0].text, "follow up\n\n<deja-recall>\nPROMPT-3\n</deja-recall>");

// Compact ses_A; ses_B must retain PROMPT-2 across restart+compaction.
for (const fn of compactHooks) await fn({ sessionID: "ses_A" });
const evB2 = {
  sessionID: "ses_B",
  system: [{ type: "text", text: "base" }],
  messages: [
    { id: "mB1", role: "user", content: [{ type: "text", text: "other q" }] },
    { id: "mB2", role: "user", content: [{ type: "text", text: "beta second" }] },
  ],
};
for (const fn of contextHooks) await fn(evB2);
assert.equal(evB2.system[0].text, "base\n\nDIGEST-3");
assert.equal(evB2.messages[0].content[0].text, "other q\n\n<deja-recall>\nPROMPT-2\n</deja-recall>");
assert.equal(evB2.messages[1].content[0].text, "beta second\n\n<deja-recall>\nPROMPT-4\n</deja-recall>");
if (typeof cleanup === "function") await cleanup();
`
			} else {
				preamble := `
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { DejaRecall } from ` + strconv.Quote(pluginPath) + `;
const execFileAsync = promisify(execFile);
const shQuote = (v) => "'" + String(v).replace(/'/g, "'\\''") + "'";
const shell = (strings, ...vals) => {
  let cmd = "";
  strings.forEach((s, i) => { cmd += s + (i < vals.length ? shQuote(vals[i]) : ""); });
  const run = async () => {
    const { stdout } = await execFileAsync("/bin/sh", ["-c", cmd]);
    return stdout;
  };
  return { text: run, quiet: run };
};
const client = { tui: { showToast: async () => {} }, session: { get: async () => ({ data: {} }) } };
const hooks = await DejaRecall({ $: shell, client, directory: ` + strconv.Quote(dir) + ` });
`
				proc1 = preamble + `
const sysA = { system: ["base"] };
await hooks["experimental.chat.system.transform"]({ sessionID: "ses_A" }, sysA);
assert.equal(sysA.system[0], "base\n\nDIGEST-1");
await hooks["experimental.chat.system.transform"]({ sessionID: "ses_A" }, sysA);
assert.equal(sysA.system[0], "base\n\nDIGEST-1");
const sysEmpty = { system: [] };
await hooks["experimental.chat.system.transform"]({ sessionID: "ses_EmptySys" }, sysEmpty);
await hooks["experimental.chat.system.transform"]({ sessionID: "ses_EmptySys" }, sysEmpty);
assert.equal(sysEmpty.system.length, 1);
assert.equal(sysEmpty.system[0], "DIGEST-2");
const mA1 = { info: { id: "mA1", role: "user", sessionID: "ses_A" }, parts: [{ type: "text", text: "first q\n" }] };
await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_A" }, { messages: [mA1] });
assert.equal(mA1.parts[0].text, "first q\n\n<deja-recall>\nPROMPT-1\n</deja-recall>");
await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_A" }, { messages: [mA1] });
assert.equal(mA1.parts[0].text, "first q\n\n<deja-recall>\nPROMPT-1\n</deja-recall>");

const sysB = { system: ["base"] };
await hooks["experimental.chat.system.transform"]({ sessionID: "ses_B" }, sysB);
assert.equal(sysB.system[0], "base\n\nDIGEST-3");
const mB1 = { info: { id: "mB1", role: "user", sessionID: "ses_B" }, parts: [{ type: "text", text: "other q" }] };
await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_B" }, { messages: [mB1] });
assert.equal(mB1.parts[0].text, "other q\n\n<deja-recall>\nPROMPT-2\n</deja-recall>");
if (typeof hooks.dispose === "function") await hooks.dispose();
`
				proc2 = preamble + `
const sysA2 = { system: ["base"] };
await hooks["experimental.chat.system.transform"]({ sessionID: "ses_A" }, sysA2);
assert.equal(sysA2.system[0], "base\n\nDIGEST-1");
const mA1 = { info: { id: "mA1", role: "user", sessionID: "ses_A" }, parts: [{ type: "text", text: "first q\n" }] };
const mA2 = { info: { id: "mA2", role: "user", sessionID: "ses_A" }, parts: [{ type: "text", text: "follow up" }] };
await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_A" }, { messages: [mA1, mA2] });
assert.equal(mA1.parts[0].text, "first q\n\n<deja-recall>\nPROMPT-1\n</deja-recall>");
assert.equal(mA2.parts[0].text, "follow up\n\n<deja-recall>\nPROMPT-3\n</deja-recall>");

await hooks["experimental.session.compacting"]({ sessionID: "ses_A" });
const sysB2 = { system: ["base"] };
await hooks["experimental.chat.system.transform"]({ sessionID: "ses_B" }, sysB2);
assert.equal(sysB2.system[0], "base\n\nDIGEST-3");
const mB1 = { info: { id: "mB1", role: "user", sessionID: "ses_B" }, parts: [{ type: "text", text: "other q" }] };
const mB2 = { info: { id: "mB2", role: "user", sessionID: "ses_B" }, parts: [{ type: "text", text: "beta second" }] };
await hooks["experimental.chat.messages.transform"]({ sessionID: "ses_B" }, { messages: [mB1, mB2] });
assert.equal(mB1.parts[0].text, "other q\n\n<deja-recall>\nPROMPT-2\n</deja-recall>");
assert.equal(mB2.parts[0].text, "beta second\n\n<deja-recall>\nPROMPT-4\n</deja-recall>");
if (typeof hooks.dispose === "function") await hooks.dispose();
`
			}

			runNodeScriptInDir(t, dir, "proc1.mjs", proc1)
			runNodeScriptInDir(t, dir, "proc2.mjs", proc2)
			if got := counts("ctx"); got != 3 {
				t.Fatalf("hook-context calls = %d, want 3 (once per distinct session)", got)
			}
			if got := counts("prompt"); got != 4 {
				t.Fatalf("hook-prompt calls = %d, want 4", got)
			}
		})
	}
}

func TestOmpExtensionPersistsHistoryAndStateAcrossRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses #!/usr/bin/env node executable stub")
	}
	dir := t.TempDir()
	bin, counts := statefulStubDeja(t, dir)
	extPath := filepath.Join(dir, "omp.mjs")
	if err := os.WriteFile(extPath, []byte(ompExtensionJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}

	preamble := `
import assert from "node:assert/strict";
import ext from ` + strconv.Quote(extPath) + `;
const handlers = {};
ext({ on: (e, fn) => (handlers[e] ||= []).push(fn), registerCommand() {}, registerTool() {} });
const ctx = { cwd: ` + strconv.Quote(dir) + `, sessionManager: { getSessionId: () => "omp_s1", getSessionFile: () => "" }, ui: { notify() {}, setStatus() {} } };
const fire = async (name, event) => {
  let last;
  for (const fn of handlers[name] || []) last = await fn(event, ctx);
  return last;
};
`
	proc1 := preamble + `
await fire("session_start", {});
const d1 = await fire("before_agent_start", { prompt: "q1" });
assert.equal(d1?.message?.content, "DIGEST-1");

const u1 = { role: "user", content: [{ type: "text", text: "first q" }] };
const c1 = await fire("context", { messages: [u1] });
assert.equal(c1.messages[0].content[0].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");

// Turn 2 in the same process must still preserve Turn 1's recall block on messages[0].
const u2 = { role: "user", content: [{ type: "text", text: "second q" }] };
const a1 = { role: "assistant", content: [{ type: "text", text: "ok" }] };
const c2 = await fire("context", { messages: [u1, a1, u2] });
assert.equal(c2.messages[0].content[0].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");
assert.equal(c2.messages[2].content[0].text, "<deja-recall>\nPROMPT-2\n</deja-recall>");

const tr1 = await fire("tool_result", { toolName: "bash", toolCallId: "call_1", isError: true, content: [{ type: "text", text: "boom" }] });
assert.equal(tr1.content[1].text, "REPAIR-1");
`
	proc2 := preamble + `
await fire("session_start", {});
// After restart on same session, before_agent_start must not re-ask hook-context or re-emit another digest.
const d2 = await fire("before_agent_start", { prompt: "q3" });
assert.equal(d2, undefined);

const u1 = {
  role: "user",
  content: [
    { type: "text", text: "<deja-recall>\nOLD-1\n</deja-recall>" },
    { type: "text", text: "<deja-recall>\nOLD-2\n</deja-recall>" },
    { type: "text", text: "first q" },
  ],
};
const a1 = { role: "assistant", content: [{ type: "text", text: "ok" }] };
const u2 = { role: "user", content: [{ type: "text", text: "second q" }] };
const a2 = { role: "assistant", content: [{ type: "text", text: "ok2" }] };
const u3 = { role: "user", content: [{ type: "text", text: "third q" }] };
const c3 = await fire("context", { messages: [u1, a1, u2, a2, u3] });
assert.equal(c3.messages[0].content.length, 2);
assert.equal(c3.messages[0].content[0].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");
assert.equal(c3.messages[0].content[1].text, "first q");
assert.equal(c3.messages[2].content[0].text, "<deja-recall>\nPROMPT-2\n</deja-recall>");
assert.equal(c3.messages[4].content[0].text, "<deja-recall>\nPROMPT-3\n</deja-recall>");

const trRepeat = await fire("tool_result", { toolName: "bash", toolCallId: "call_1", isError: true, content: [{ type: "text", text: "boom" }] });
assert.equal(trRepeat.content[1].text, "REPAIR-1");
`

	runNodeScriptInDir(t, dir, "omp1.mjs", proc1)
	runNodeScriptInDir(t, dir, "omp2.mjs", proc2)
	if got := counts("ctx"); got != 1 {
		t.Fatalf("omp hook-context calls = %d, want 1", got)
	}
	if got := counts("prompt"); got != 3 {
		t.Fatalf("omp hook-prompt calls = %d, want 3", got)
	}
	if got := counts("after"); got != 1 {
		t.Fatalf("omp hook-tool-after calls = %d, want 1", got)
	}
}

func TestClinePluginPersistsStateAcrossRestartAndCompaction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses #!/usr/bin/env node executable stub")
	}
	dir := t.TempDir()
	ws := filepath.Join(dir, "work")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	bin, counts := statefulStubDeja(t, dir)
	pluginPath := filepath.Join(dir, "cline.mjs")
	if err := os.WriteFile(pluginPath, []byte(clinePluginJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}

	preamble := `
import assert from "node:assert/strict";
import plugin from ` + strconv.Quote(pluginPath) + `;
let builder, rule;
plugin.setup(
  {
    registerMessageBuilder: (b) => { builder = b; },
    registerRule: (r) => { rule = r; },
    registerCommand() {},
  },
  { session: { sessionId: "cline_s1" }, workspaceInfo: { rootPath: ` + strconv.Quote(ws) + ` } },
);
`
	proc1 := preamble + `
assert.equal(rule.content(), "DIGEST-1");
const u1 = { role: "user", content: [{ type: "text", text: "first q" }] };
const b1 = builder.build([u1]);
assert.equal(b1[0].content[0].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");

const a1 = { role: "assistant", content: [{ type: "text", text: "running" }] };
const u2 = { role: "user", content: [{ type: "text", text: "second q" }] };
const failMsg = { role: "user", content: [{ type: "tool_result", name: "run_commands", tool_use_id: "tu_1", content: [{ query: "go test", result: "failed", success: false }] }] };
const b2 = builder.build([u1, a1, u2, failMsg]);
assert.equal(b2[0].content[0].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");
assert.equal(b2[2].content[0].text, "<deja-recall>\nPROMPT-2\n</deja-recall>");
assert.equal(b2[3].content[0].content[0].result, "failed\n\nREPAIR-1");

// Re-running on kept messages is a no-op (returns undefined) and leaves b2 unchanged.
const b2Kept = builder.build(b2);
assert.equal(b2Kept, undefined);
assert.equal(b2[0].content.length, 2);
assert.equal(b2[0].content[0].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");
assert.equal(b2[2].content.length, 2);
assert.equal(b2[2].content[0].text, "<deja-recall>\nPROMPT-2\n</deja-recall>");
assert.equal(b2[3].content[0].content[0].result, "failed\n\nREPAIR-1");

// Once a newer tool_result arrives, the historical failing tool_result must keep REPAIR-1 intact.
const a2 = { role: "assistant", content: [{ type: "text", text: "re-running" }] };
const okMsg = { role: "user", content: [{ type: "tool_result", name: "run_commands", tool_use_id: "tu_2", content: [{ query: "go test", result: "ok", success: true }] }] };
const b2Next = builder.build([u1, a1, u2, failMsg, a2, okMsg]);
assert.equal(b2Next[3].content[0].content[0].result, "failed\n\nREPAIR-1");
`
	proc2 := preamble + `
assert.equal(rule.content(), "DIGEST-1");
const u1Stacked = {
  role: "user",
  content: [
    { type: "text", text: "<deja-recall>\nOLD-1\n</deja-recall>" },
    { type: "text", text: "<deja-recall>\nOLD-2\n</deja-recall>" },
    { type: "text", text: "first q" },
  ],
};
const a1 = { role: "assistant", content: [{ type: "text", text: "running" }] };
const u2 = { role: "user", content: [{ type: "text", text: "second q" }] };
const failMsg = { role: "user", content: [{ type: "tool_result", name: "run_commands", tool_use_id: "tu_1", content: [{ query: "go test", result: "failed", success: false }] }] };
const a2 = { role: "assistant", content: [{ type: "text", text: "re-running" }] };
const okMsg = { role: "user", content: [{ type: "tool_result", name: "run_commands", tool_use_id: "tu_2", content: [{ query: "go test", result: "ok", success: true }] }] };
const b3 = builder.build([u1Stacked, a1, u2, failMsg, a2, okMsg]);
assert.equal(b3[0].content.length, 2);
assert.equal(b3[0].content[0].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");
assert.equal(b3[2].content[0].text, "<deja-recall>\nPROMPT-2\n</deja-recall>");
assert.equal(b3[3].content[0].content[0].result, "failed\n\nREPAIR-1");

plugin.hooks.onEvent({ type: "status-notice", metadata: { kind: "auto_compaction", phase: "started" } });
const b4 = builder.build([u2]);
assert.equal(b4[0].content[0].text, "<deja-recall>\nPROMPT-3\n</deja-recall>");
`

	runNodeScriptInDir(t, dir, "cline1.mjs", proc1)
	runNodeScriptInDir(t, dir, "cline2.mjs", proc2)
	if got := counts("ctx"); got != 1 {
		t.Fatalf("cline hook-context calls = %d, want 1", got)
	}
	if got := counts("prompt"); got != 3 {
		t.Fatalf("cline hook-prompt calls = %d, want 3", got)
	}
	if got := counts("after"); got != 1 {
		t.Fatalf("cline hook-tool-after calls = %d, want 1", got)
	}
}

func TestPrimeExtensionPersistsStateAcrossRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses #!/usr/bin/env node executable stub")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin")
	}
	if exec.CommandContext(t.Context(), node, "--experimental-strip-types", "-e", "").Run() != nil {
		t.Skip("this node cannot strip types")
	}
	dir := t.TempDir()
	bin, counts := statefulStubDeja(t, dir)
	extPath := filepath.Join(dir, "prime.ts")
	if err := os.WriteFile(extPath, []byte(primeExtensionTS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}

	preamble := `
import assert from "node:assert/strict";
import ext from ` + strconv.Quote(extPath) + `;
const handlers = {};
ext({ on: (e, fn) => (handlers[e] ||= []).push(fn), registerCommand() {}, registerTool() {} });
const ctx = { cwd: ` + strconv.Quote(dir) + `, sessionManager: { getSessionId: () => "prime_s1", getSessionFile: () => "" }, ui: { notify() {}, setStatus() {} } };
const fire = async (name, event) => {
  let last;
  for (const fn of handlers[name] || []) last = await fn(event, ctx);
  return last;
};
`
	proc1 := preamble + `
await fire("session_start", {});
const d1 = await fire("before_agent_start", { prompt: "turn 1" });
assert.equal(d1?.message?.content, "DIGEST-1");
const tr1 = await fire("tool_result", { toolName: "ipython", toolCallId: "cell_1", isError: true, input: { code: "" }, content: [{ type: "text", text: "boom" }] });
assert.equal(tr1.content[1].text, "REPAIR-1");
`
	proc2 := preamble + `
await fire("session_start", {});
// After restart on an already-started session, before_agent_start must call hook-prompt instead of re-asking hook-context.
const p2 = await fire("before_agent_start", { prompt: "follow up" });
assert.equal(p2?.message?.content, "<deja-recall>\nPROMPT-1\n</deja-recall>");
const tr2 = await fire("tool_result", { toolName: "ipython", toolCallId: "cell_1", isError: true, input: { code: "" }, content: [{ type: "text", text: "boom" }] });
assert.equal(tr2.content[1].text, "REPAIR-1");
`

	runNodeScriptInDir(t, dir, "prime1.mjs", proc1, "--experimental-strip-types", "--no-warnings")
	runNodeScriptInDir(t, dir, "prime2.mjs", proc2, "--experimental-strip-types", "--no-warnings")
	if got := counts("ctx"); got != 1 {
		t.Fatalf("prime hook-context calls = %d, want 1", got)
	}
	if got := counts("prompt"); got != 1 {
		t.Fatalf("prime hook-prompt calls = %d, want 1", got)
	}
	if got := counts("after"); got != 1 {
		t.Fatalf("prime hook-tool-after calls = %d, want 1", got)
	}
}

func TestCommandCodeModPersistsRecallAcrossTurnsAndRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses #!/usr/bin/env node executable stub")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin")
	}
	if exec.CommandContext(t.Context(), node, "--experimental-strip-types", "-e", "").Run() != nil {
		t.Skip("this node cannot strip types")
	}
	dir := t.TempDir()
	bin, counts := statefulStubDeja(t, dir)
	modPath := filepath.Join(dir, "commandcode.ts")
	if err := os.WriteFile(modPath, []byte(commandCodeModTS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}

	preamble := `
import assert from "node:assert/strict";
import mod from ` + strconv.Quote(modPath) + `;
let hooks = {};
const listeners = {};
mod({
  cwd: ` + strconv.Quote(dir) + `,
  ui: { setStatus() {} },
  on: (name, fn) => { (listeners[name] ||= []).push(fn); },
  hooks: (h) => { hooks = h; },
});
`
	proc1 := preamble + `
hooks.onSessionStart({ sessionId: "cc_s1" });
const r1 = hooks.transformContext({
  messages: [{ role: "user", content: "first q", meta: { messageId: "u1" } }],
});
assert.equal(r1[0].content[1].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");

// Re-running on kept messages with attached recall parts must not stack blocks or re-query deja.
const r1Again = hooks.transformContext({ messages: r1 });
assert.equal(r1Again[0].content.length, 2);
assert.equal(r1Again[0].content[1].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");
const r1Third = hooks.transformContext({ messages: r1Again });
assert.equal(r1Third[0].content.length, 2);
assert.equal(r1Third[0].content[1].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");

const r2 = hooks.transformContext({
  messages: [
    ...r1Again,
    { role: "assistant", content: "working" },
    { role: "user", content: "second q", meta: { messageId: "u2" } },
  ],
});
assert.equal(r2[0].content.length, 2);
assert.equal(r2[0].content[1].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");
assert.equal(r2[2].content.length, 2);
assert.equal(r2[2].content[1].text, "<deja-recall>\nPROMPT-2\n</deja-recall>");

// Missed recalls must settle so repeated calls/restarts do not re-query deja.
const rMiss1 = hooks.transformContext({
  messages: [
    ...r2,
    { role: "assistant", content: "done" },
    { role: "user", content: "__miss__", meta: { messageId: "u3" } },
  ],
});
assert.equal(rMiss1[4].content, "__miss__");
`
	proc2 := preamble + `
hooks.onSessionStart({ sessionId: "cc_s1" });
const r3 = hooks.transformContext({
  messages: [
    { role: "user", content: "first q", meta: { messageId: "u1" } },
    { role: "assistant", content: "working" },
    { role: "user", content: "second q", meta: { messageId: "u2" } },
    { role: "assistant", content: "done" },
    { role: "user", content: "__miss__", meta: { messageId: "u3" } },
  ],
});
assert.equal(r3[0].content.length, 2);
assert.equal(r3[0].content[1].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");
assert.equal(r3[2].content.length, 2);
assert.equal(r3[2].content[1].text, "<deja-recall>\nPROMPT-2\n</deja-recall>");
assert.equal(r3[4].content, "__miss__");

// A duplicate stack already present on a message collapses back to one block across restart.
const rDedup = hooks.transformContext({
  messages: [
    {
      role: "user",
      meta: { messageId: "u1" },
      content: [
        { type: "text", text: "first q" },
        { type: "text", text: "<deja-recall>\nPROMPT-1\n</deja-recall>" },
        { type: "text", text: "<deja-recall>\nEXTRA\n</deja-recall>" },
      ],
    },
    { role: "assistant", content: "working" },
    r3[2],
    { role: "assistant", content: "done" },
    { role: "user", content: "__miss__", meta: { messageId: "u3" } },
  ],
});
assert.equal(rDedup[0].content.length, 2);
assert.equal(rDedup[0].content[1].text, "<deja-recall>\nPROMPT-1\n</deja-recall>");
assert.equal(rDedup[2].content.length, 2);
assert.equal(rDedup[2].content[1].text, "<deja-recall>\nPROMPT-2\n</deja-recall>");
assert.equal(rDedup[4].content, "__miss__");
`

	runNodeScriptInDir(t, dir, "cc1.mjs", proc1, "--experimental-strip-types", "--no-warnings")
	runNodeScriptInDir(t, dir, "cc2.mjs", proc2, "--experimental-strip-types", "--no-warnings")
	if got := counts("prompt"); got != 3 {
		t.Fatalf("commandcode hook-prompt calls = %d, want 3", got)
	}
}

func TestOpenClawPersistDigestAndSeenAcrossTurnsAndRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses #!/usr/bin/env node executable stub")
	}
	dir := t.TempDir()
	bin, counts := statefulStubDeja(t, dir)
	pluginPath := filepath.Join(dir, "openclaw-plugin.mjs")
	if err := os.WriteFile(pluginPath, []byte(openclawPluginJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}
	packPath := filepath.Join(dir, "openclaw-handler.mjs")
	if err := os.WriteFile(packPath, []byte(openclawHandlerJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}

	preamble := `
import assert from "node:assert/strict";
import fs from "node:fs";
import plugin from ` + strconv.Quote(pluginPath) + `;
import pack from ` + strconv.Quote(packPath) + `;
const on = {};
const hooks = {};
plugin.register({
  on: (name, fn) => { on[name] = fn; },
  registerHook: (name, fn) => { hooks[name] = fn; },
});
`
	proc1 := preamble + `
// Shared bootstrapFiles within one run deduplicates plugin + handler pack.
const shared = { workspaceDir: "/workspace/proj-a", bootstrapFiles: [], sessionKey: "agent:main:s1", sessionId: "uuid-oc1" };
await hooks["agent:bootstrap"]({ type: "agent", action: "bootstrap", sessionKey: "agent:main:s1", context: shared });
await pack({ type: "agent", action: "bootstrap", sessionKey: "agent:main:s1", context: shared });
assert.equal(shared.bootstrapFiles.length, 1);
assert.equal(shared.bootstrapFiles[0].content, "DIGEST-1");

// Separate turn for pack caches independently in its own store; turn 2 for plugin reuses cached DIGEST-1.
const packCtx = { workspaceDir: "/workspace/proj-a", bootstrapFiles: [], sessionKey: "agent:main:s2", sessionId: "uuid-oc-pack" };
await pack({ type: "agent", action: "bootstrap", sessionKey: "agent:main:s2", context: packCtx });
assert.equal(packCtx.bootstrapFiles.length, 1);
assert.equal(packCtx.bootstrapFiles[0].content, "DIGEST-2");

const turn2 = { workspaceDir: "/workspace/proj-a", bootstrapFiles: [], sessionKey: "agent:main:s1", sessionId: "uuid-oc1" };
await hooks["agent:bootstrap"]({ type: "agent", action: "bootstrap", sessionKey: "agent:main:s1", context: turn2 });
assert.equal(turn2.bootstrapFiles.length, 1);
assert.equal(turn2.bootstrapFiles[0].content, "DIGEST-1");
`
	proc2 := preamble + `
// After process restart, both plugin and handler pack re-attach their cached Project Context digests without calling hook-context.
const turn3 = { workspaceDir: "/workspace/proj-a", bootstrapFiles: [], sessionKey: "agent:main:s1", sessionId: "uuid-oc1" };
await hooks["agent:bootstrap"]({ type: "agent", action: "bootstrap", sessionKey: "agent:main:s1", context: turn3 });
assert.equal(turn3.bootstrapFiles.length, 1);
assert.equal(turn3.bootstrapFiles[0].content, "DIGEST-1");

const packTurn2 = { workspaceDir: "/workspace/proj-a", bootstrapFiles: [], sessionKey: "agent:main:s2", sessionId: "uuid-oc-pack" };
await pack({ type: "agent", action: "bootstrap", sessionKey: "agent:main:s2", context: packTurn2 });
assert.equal(packTurn2.bootstrapFiles.length, 1);
assert.equal(packTurn2.bootstrapFiles[0].content, "DIGEST-2");

// sessionKey-only compaction and session_end after restart must resolve uuid-oc1 and /workspace/proj-a from persisted seen store.
await on["before_compaction"]({ sessionFile: "/workspace/proj-a/t.jsonl" }, { sessionKey: "agent:main:s1" });
await on["session_end"]({}, { sessionKey: "agent:main:s1" });
`

	runNodeScriptInDir(t, dir, "oc1.mjs", proc1)
	runNodeScriptInDir(t, dir, "oc2.mjs", proc2)
	if got := counts("ctx"); got != 2 {
		t.Fatalf("openclaw hook-context calls = %d, want 2", got)
	}
	if got := counts("precompact"); got != 1 {
		t.Fatalf("openclaw hook-precompact calls = %d, want 1", got)
	}
	if got := counts("end"); got != 1 {
		t.Fatalf("openclaw hook-session-end calls = %d, want 1", got)
	}
	for _, name := range []string{"last_hook-precompact.json", "last_hook-session-end.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
		if !strings.Contains(string(raw), `"session_id":"uuid-oc1"`) || !strings.Contains(string(raw), `"cwd":"/workspace/proj-a"`) {
			t.Fatalf("%s = %s, want session_id uuid-oc1 and cwd /workspace/proj-a", name, raw)
		}
	}
}

func TestDSHPluginPersistsRecallAcrossSessionsAndRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub uses sh")
	}
	dir := t.TempDir()
	stub, counts := statefulStubDeja(t, dir)
	pluginPath := filepath.Join(dir, "auto.js")
	if err := os.WriteFile(pluginPath, []byte(dshAutoJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	hostPreamble := `
import assert from "node:assert/strict";
import apply from ` + strconv.Quote(pluginPath) + `;

function makeHost() {
  const contexts = new Map();
  const listeners = new Map();
  const ctx = {
    systemPrompt: { context: (c) => contexts.set(c.name, c.text) },
    on: (name, fn) => {
      if (!listeners.has(name)) listeners.set(name, []);
      listeners.get(name).push(fn);
    },
  };
  apply(ctx);
  return { contexts, listeners };
}

function makeAssembly(sid, prompt) {
  const log = [
    { type: "user/message", data: { role: "user", source: { kind: "user" }, content: [{ type: "text", text: prompt }] } },
    { type: "turn/start", data: { turn: 1 } },
  ];
  return { agent: { sessionId: sid, session: { id: sid, header: { cwd: "/workspace/dsh-proj" }, get seq() { return log.length; }, eventAt: (i) => log[i] } } };
}
`
	proc1 := hostPreamble + `
const h = makeHost();
const project = h.contexts.get("deja:project");
const recall = h.contexts.get("deja:recall");

assert.equal(project(makeAssembly("session-dsh-1", "__miss__")), "DIGEST-1");
assert.equal(project(makeAssembly("session-dsh-1", "__miss__")), "DIGEST-1");

assert.equal(recall(makeAssembly("session-dsh-1", "__miss__")), "");
assert.equal(recall(makeAssembly("session-dsh-1", "__miss__")), "");

assert.equal(recall(makeAssembly("session-dsh-1", "alpha question")), "<deja-recall>\nPROMPT-2\n</deja-recall>");
assert.equal(recall(makeAssembly("session-dsh-2", "beta question")), "<deja-recall>\nPROMPT-3\n</deja-recall>");
assert.equal(recall(makeAssembly("session-dsh-1", "alpha question")), "<deja-recall>\nPROMPT-2\n</deja-recall>");
`
	proc2 := hostPreamble + `
const h = makeHost();
const project = h.contexts.get("deja:project");
const recall = h.contexts.get("deja:recall");

assert.equal(project(makeAssembly("session-dsh-1", "alpha question")), "DIGEST-1");
assert.equal(recall(makeAssembly("session-dsh-1", "alpha question")), "<deja-recall>\nPROMPT-2\n</deja-recall>");

const sessionEvents = h.listeners.get("session/event") || [];
const preSteps = h.listeners.get("agent/pre-step") || [];
const sessObj = { id: "session-dsh-1", header: { cwd: "/workspace/dsh-proj" } };
for (const fn of sessionEvents) fn(sessObj, { type: "compaction/start" });
for (const fn of preSteps) await fn({ agent: { sessionId: "session-dsh-1", session: sessObj } }, async () => ({ kind: "accept" }));

assert.equal(recall(makeAssembly("session-dsh-1", "alpha question")), "<deja-recall>\nPROMPT-4\n</deja-recall>");
assert.equal(recall(makeAssembly("session-dsh-1", "alpha question")), "<deja-recall>\nPROMPT-4\n</deja-recall>");
`
	runNodeScriptInDir(t, dir, "dsh1.mjs", proc1)
	runNodeScriptInDir(t, dir, "dsh2.mjs", proc2)
	if got := counts("ctx"); got != 1 {
		t.Fatalf("dsh hook-context calls = %d, want 1", got)
	}
	if got := counts("prompt"); got != 4 {
		t.Fatalf("dsh hook-prompt calls = %d, want 4", got)
	}
	if got := counts("precompact"); got != 1 {
		t.Fatalf("dsh hook-precompact calls = %d, want 1", got)
	}
}

func TestPrimeEmptyDigestSettlesAndFallsThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses #!/usr/bin/env node executable stub")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin")
	}
	if exec.CommandContext(t.Context(), node, "--experimental-strip-types", "-e", "").Run() != nil {
		t.Skip("this node cannot strip types")
	}
	dir := t.TempDir()
	workDir := filepath.Join(dir, "__empty_ctx__")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin, counts := statefulStubDeja(t, dir)
	extPath := filepath.Join(dir, "prime.ts")
	if err := os.WriteFile(extPath, []byte(primeExtensionTS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}

	preamble := `
import assert from "node:assert/strict";
import ext from ` + strconv.Quote(extPath) + `;
const handlers = {};
ext({ on: (e, fn) => (handlers[e] ||= []).push(fn), registerCommand() {}, registerTool() {} });
const ctx = { cwd: ` + strconv.Quote(workDir) + `, sessionManager: { getSessionId: () => "prime_empty_1", getSessionFile: () => "" }, ui: { notify() {}, setStatus() {} } };
const fire = async (name, event) => {
  let last;
  for (const fn of handlers[name] || []) last = await fn(event, ctx);
  return last;
};
`
	proc1 := preamble + `
await fire("session_start", {});
const p1 = await fire("before_agent_start", { prompt: "why does build fail" });
assert.equal(p1?.message?.content, "<deja-recall>\nPROMPT-1\n</deja-recall>");
`
	proc2 := preamble + `
await fire("session_start", {});
const p2 = await fire("before_agent_start", { prompt: "follow up" });
assert.equal(p2?.message?.content, "<deja-recall>\nPROMPT-2\n</deja-recall>");
`
	runNodeScriptInDir(t, workDir, "prime_empty_1.mjs", proc1, "--experimental-strip-types", "--no-warnings")
	runNodeScriptInDir(t, workDir, "prime_empty_2.mjs", proc2, "--experimental-strip-types", "--no-warnings")
	if got := counts("ctx"); got != 1 {
		t.Fatalf("prime empty-digest hook-context calls = %d, want 1", got)
	}
	if got := counts("prompt"); got != 2 {
		t.Fatalf("prime empty-digest hook-prompt calls = %d, want 2", got)
	}
}

func TestPiExtensionsPersistEmptyDigestAndRepairsAcrossRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses #!/usr/bin/env node executable stub")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin")
	}
	if exec.CommandContext(t.Context(), node, "--experimental-strip-types", "-e", "").Run() != nil {
		t.Skip("this node cannot strip types")
	}
	pkgExt, err := filepath.Abs(filepath.Join("..", "..", "extensions", "pi", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		isPkg bool
	}{
		{"auto", false},
		{"pkg", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			workDir := filepath.Join(dir, "__empty_ctx__")
			if err := os.MkdirAll(workDir, 0o755); err != nil {
				t.Fatal(err)
			}
			bin, counts := statefulStubDeja(t, dir)
			t.Setenv("HOME", dir)
			t.Setenv("USERPROFILE", dir)
			t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(dir, "isolated-pi-agent"))
			t.Setenv("DEJA_BIN", bin)

			extPath := pkgExt
			if !tc.isPkg {
				extPath = filepath.Join(dir, "deja.ts")
				if err := os.WriteFile(extPath, []byte(piExtensionTS(bin)), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			preamble := `
import assert from "node:assert/strict";
import ext from ` + strconv.Quote(extPath) + `;
const handlers = {};
ext({ on: (e, fn) => (handlers[e] ||= []).push(fn), registerCommand() {}, registerTool() {} });
let branch = [{ type: "message", id: "m1" }];
const ctx = {
  sessionManager: {
    getSessionId: () => "pi_s1",
    getSessionFile: () => "",
    getHeader: () => ({}),
    getBranch: () => branch,
  },
  ui: { notify() {}, setStatus() {} },
};
const fire = async (name, event) => {
  let last;
  for (const fn of handlers[name] || []) last = await fn(event, ctx);
  return last;
};
`
			proc1 := preamble + `
const p1 = await fire("before_agent_start", { prompt: "first question" });
assert.equal(p1?.message?.content, "<deja-recall>\nPROMPT-1\n</deja-recall>");
const tr1 = await fire("tool_result", { toolName: "bash", toolCallId: "bash_1", isError: true, content: [{ type: "text", text: "boom" }] });
assert.equal(tr1.content[1].text, "REPAIR-1");
`
			proc2 := preamble + `
const p2 = await fire("before_agent_start", { prompt: "second question" });
assert.equal(p2?.message?.content, "<deja-recall>\nPROMPT-2\n</deja-recall>");
const tr2 = await fire("tool_result", { toolName: "bash", toolCallId: "bash_1", isError: true, content: [{ type: "text", text: "boom" }] });
assert.equal(tr2.content[1].text, "REPAIR-1");
branch.push({ type: "compaction", id: "c1", firstKeptEntryId: "m2" });
const p3 = await fire("before_agent_start", { prompt: "after compact" });
assert.equal(p3?.message?.content, "<deja-recall>\nPROMPT-3\n</deja-recall>");
`
			runNodeScriptInDir(t, workDir, "pi1.mjs", proc1, "--experimental-strip-types", "--no-warnings")
			runNodeScriptInDir(t, workDir, "pi2.mjs", proc2, "--experimental-strip-types", "--no-warnings")
			if got := counts("ctx"); got != 2 {
				t.Fatalf("pi %s hook-context calls = %d, want 2", tc.name, got)
			}
			if got := counts("prompt"); got != 3 {
				t.Fatalf("pi %s hook-prompt calls = %d, want 3", tc.name, got)
			}
			if got := counts("after"); got != 1 {
				t.Fatalf("pi %s hook-tool-after calls = %d, want 1", tc.name, got)
			}
		})
	}
}

func TestAmpPluginPersistsThreadsAndRepairsAcrossSwitchAndRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses #!/usr/bin/env node executable stub")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin")
	}
	if exec.CommandContext(t.Context(), node, "--experimental-strip-types", "-e", "").Run() != nil {
		t.Skip("this node cannot strip types")
	}
	dir := t.TempDir()
	workEmpty := filepath.Join(dir, "__empty_ctx__")
	if err := os.MkdirAll(workEmpty, 0o755); err != nil {
		t.Fatal(err)
	}
	bin, counts := statefulStubDeja(t, dir)
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	pluginPath := filepath.Join(dir, "amp-deja.ts")
	if err := os.WriteFile(pluginPath, []byte(ampPluginTS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}

	harnessHeader := `
import assert from "node:assert/strict";
import setup from ` + strconv.Quote(pluginPath) + `;
const handlers = {};
setup({ on: (ev, fn) => (handlers[ev] ||= []).push(fn), registerCommand() {}, registerTool() {} });
const ctx = { ui: { async notify() {} } };
const fire = async (ev, payload) => {
  let out;
  for (const fn of handlers[ev] || []) out = await fn(payload, ctx);
  return out;
};
`
	proc1 := harnessHeader + `
const rA1 = await fire("agent.start", { thread: { id: "T-A" }, message: "first A" });
assert.ok(rA1?.message?.content?.includes("DIGEST-1"));
const rB1 = await fire("agent.start", { thread: { id: "T-B" }, message: "first B" });
assert.ok(rB1?.message?.content?.includes("DIGEST-2"));
const rA2 = await fire("agent.start", { thread: { id: "T-A" }, message: "follow up A" });
assert.ok(rA2?.message?.content?.includes("PROMPT-1"));
const tr1 = await fire("tool.result", { status: "error", tool: "Bash", toolUseID: "tu_1", output: "exit 1", thread: { id: "T-A" } });
assert.equal(tr1?.status, "error");
assert.ok(tr1?.output?.includes("REPAIR-1"));
`
	proc2 := harnessHeader + `
const rA3 = await fire("agent.start", { thread: { id: "T-A" }, message: "after restart" });
assert.ok(rA3?.message?.content?.includes("PROMPT-2"));
const tr2 = await fire("tool.result", { status: "error", tool: "Bash", toolUseID: "tu_1", output: "exit 1", thread: { id: "T-A" } });
assert.equal(tr2?.status, "error");
assert.ok(tr2?.output?.includes("REPAIR-1"));
`
	runNodeScriptInDir(t, dir, "amp1.mjs", proc1, "--experimental-strip-types", "--no-warnings")
	runNodeScriptInDir(t, dir, "amp2.mjs", proc2, "--experimental-strip-types", "--no-warnings")
	if got := counts("ctx"); got != 2 {
		t.Fatalf("amp hook-context calls across T-A/T-B switch and restart = %d, want 2", got)
	}
	if got := counts("prompt"); got != 2 {
		t.Fatalf("amp hook-prompt calls = %d, want 2", got)
	}
	if got := counts("after"); got != 1 {
		t.Fatalf("amp hook-tool-after calls = %d, want 1", got)
	}

	empty1 := harnessHeader + `
const rE1 = await fire("agent.start", { thread: { id: "T-empty" }, message: "empty first" });
assert.ok(rE1?.message?.content?.includes("PROMPT-3"));
`
	empty2 := harnessHeader + `
const rE2 = await fire("agent.start", { thread: { id: "T-empty" }, message: "empty second" });
assert.ok(rE2?.message?.content?.includes("PROMPT-4"));
`
	runNodeScriptInDir(t, workEmpty, "amp_empty1.mjs", empty1, "--experimental-strip-types", "--no-warnings")
	runNodeScriptInDir(t, workEmpty, "amp_empty2.mjs", empty2, "--experimental-strip-types", "--no-warnings")
	if got := counts("ctx"); got != 3 {
		t.Fatalf("amp total hook-context calls after empty-digest restart = %d, want 3", got)
	}
	if got := counts("prompt"); got != 4 {
		t.Fatalf("amp total hook-prompt calls = %d, want 4", got)
	}
}

func TestHermesResumeDoesNotReinjectStartupDigest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub deja is a shebang script")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is needed for stub deja")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	pkgDir, err := filepath.Abs(filepath.Join("..", "..", "extensions", "hermes"))
	if err != nil {
		t.Fatal(err)
	}
	pkgInit := filepath.Join(pkgDir, "__init__.py")

	runPy := func(t *testing.T, workDir string, pathPrefix string, code string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), py, "-c", code)
		cmd.Dir = workDir
		env := append(os.Environ(), "PATH="+pathPrefix+string(os.PathListSeparator)+os.Getenv("PATH"))
		cmd.Env = env
		out, cerr := cmd.CombinedOutput()
		if cerr != nil {
			t.Fatalf("python3: %v\n%s", cerr, out)
		}
		return string(out)
	}

	for _, tc := range []struct {
		name string
		kind string
	}{
		{"memory-auto", "memory-auto"},
		{"memory-pkg", "memory-pkg"},
		{"hook-auto", "hook-auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin, _ := statefulStubDeja(t, dir)
			t.Setenv("HERMES_HOME", filepath.Join(dir, "hermes-home"))

			if merr := os.MkdirAll(filepath.Join(dir, "agent"), 0o755); merr != nil {
				t.Fatal(merr)
			}
			if werr := os.WriteFile(filepath.Join(dir, "agent", "__init__.py"), nil, 0o644); werr != nil {
				t.Fatal(werr)
			}
			if werr := os.WriteFile(filepath.Join(dir, "agent", "memory_provider.py"), []byte("class MemoryProvider:\n    pass\n"), 0o644); werr != nil {
				t.Fatal(werr)
			}

			var firstCmd, resumeCmd string
			switch tc.kind {
			case "memory-auto":
				if werr := os.WriteFile(filepath.Join(dir, "provider.py"), []byte(hermesMemoryPy(bin)), 0o644); werr != nil {
					t.Fatal(werr)
				}
				mkScript := func(q string) string {
					return "import provider\n" +
						"p = provider.DejaMemoryProvider()\n" +
						"p.initialize('sess-hermes-1', workspace_dir=" + strconv.Quote(dir) + ")\n" +
						"print(p.prefetch(" + strconv.Quote(q) + "))\n"
				}
				firstCmd = mkScript("first question")
				resumeCmd = mkScript("resumed question")
			case "memory-pkg":
				mkScript := func(q string) string {
					return "import importlib.util, sys\n" +
						"sys.path.insert(0, " + strconv.Quote(dir) + ")\n" +
						"spec = importlib.util.spec_from_file_location('deja_hermes_pkg', " + strconv.Quote(pkgInit) + ")\n" +
						"mod = importlib.util.module_from_spec(spec)\n" +
						"spec.loader.exec_module(mod)\n" +
						"p = mod.DejaMemoryProvider()\n" +
						"p.initialize('sess-hermes-1', workspace_dir=" + strconv.Quote(dir) + ")\n" +
						"print(p.prefetch(" + strconv.Quote(q) + "))\n"
				}
				firstCmd = mkScript("first question")
				resumeCmd = mkScript("resumed question")
			case "hook-auto":
				if werr := os.WriteFile(filepath.Join(dir, "hook.py"), []byte(hermesPluginPy(bin)), 0o644); werr != nil {
					t.Fatal(werr)
				}
				mkScript := func(q string) string {
					return "import hook\n" +
						"r = hook.recall(session_id='sess-hermes-1', user_message=" + strconv.Quote(q) + ", is_first_turn=True)\n" +
						"print(r['context'] if r else '')\n"
				}
				firstCmd = mkScript("first question")
				resumeCmd = mkScript("resumed question")
			}

			out1 := runPy(t, dir, dir, firstCmd)
			if !strings.Contains(out1, "DIGEST-") {
				t.Fatalf("%s first process output missing DIGEST-: %q", tc.name, out1)
			}
			if tc.kind != "hook-auto" && !strings.Contains(out1, "PROMPT-") {
				t.Fatalf("%s first process output missing PROMPT-: %q", tc.name, out1)
			}

			out2 := runPy(t, dir, dir, resumeCmd)
			if strings.Contains(out2, "DIGEST-") {
				t.Fatalf("%s resumed process re-injected DIGEST- across restart: %q", tc.name, out2)
			}
			if !strings.Contains(out2, "PROMPT-") {
				t.Fatalf("%s resumed process missing PROMPT-: %q", tc.name, out2)
			}
		})
	}
}
