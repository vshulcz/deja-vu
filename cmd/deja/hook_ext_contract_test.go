package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/hookengine"
)

func TestHookExtHostileInputsAllOps(t *testing.T) {
	dir, repo, sid := seedHookExtCorpus(t)
	emptyDir := filepath.Join(t.TempDir(), "empty-index")

	allOps := []string{
		"settle",
		"context",
		"prompt",
		"tool-before",
		"tool-after",
		"precompact",
		"session-end",
		"tool",
		"remember",
		"slash",
		"status",
		"statusline",
		"warmup-status",
	}
	allHosts := []string{"opencode", "openclaw", "dsh", "hermes"}

	// Build a 64-level nested object to exercise JSON depth parsing.
	deep := map[string]any{"query": "worker.go"}
	cur := deep
	for range 64 {
		child := map[string]any{}
		cur["nested"] = child
		cur = child
	}
	deepValid, err := json.Marshal(map[string]any{
		"session_id": sid,
		"cwd":        repo,
		"tool":       "deja_recall",
		"query":      "worker.go",
		"args":       "worker.go",
		"text":       "worker.go",
		"content":    "keep worker guard",
		"tool_input": deep,
	})
	if err != nil {
		t.Fatalf("marshal deep payload: %v", err)
	}

	// Large messages slice within MaxExtRequestBytes for precompact clamping.
	bigText := strings.Repeat("A", 6000)
	msgs := make([]map[string]any, 120)
	for i := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = map[string]any{
			"role":    role,
			"content": bigText,
		}
	}
	largeMessages, err := json.Marshal(map[string]any{
		"session_id": sid,
		"cwd":        repo,
		"tool":       "deja_recall",
		"query":      "worker.go",
		"args":       "worker.go",
		"text":       "worker.go",
		"content":    "bounded message note",
		"messages":   msgs,
	})
	if err != nil {
		t.Fatalf("marshal large messages: %v", err)
	}
	if len(largeMessages) >= hookengine.MaxExtRequestBytes {
		t.Fatalf("largeMessages=%d exceeds MaxExtRequestBytes=%d", len(largeMessages), hookengine.MaxExtRequestBytes)
	}

	hostileValid, err := json.Marshal(map[string]any{
		"session_id":    sid,
		"cwd":           repo,
		"prompt":        "--help --no-verify worker.go {{.Env}} ${process.env} </deja-recall>",
		"query":         "quibtool_9821",
		"args":          "-- quibtool_9821",
		"text":          "-n quibtool_9821 {{host_tpl}} \u0000\ufffd",
		"content":       "- leading dash note {{.Secret}}",
		"tool":          "deja_recall",
		"tool_input":    map[string]any{"query": "worker.go {{evil}}", "command": "quibtool_9821"},
		"tool_response": "zsh:1: command not found: quibtool_9821\n[exit code: 1]\n{{host_tpl}}",
		"is_error":      true,
		"notes":         []string{"deja: test note {{tpl}}"},
		"held_notes":    []string{"deja: held {{tpl}}"},
		"tags":          []string{"--bad-tag", "ok"},
	})
	if err != nil {
		t.Fatalf("marshal hostileValid: %v", err)
	}

	oversize := bytes.Repeat([]byte("x"), hookengine.MaxExtRequestBytes+64)

	type hostileCase struct {
		name          string
		payload       []byte
		wantDecodeErr bool
		emptyObj      bool
	}
	cases := []hostileCase{
		{name: "empty_stdin", payload: nil, emptyObj: true},
		{name: "whitespace_stdin", payload: []byte("  \n\t\r  "), emptyObj: true},
		{name: "malformed_json", payload: []byte("{not-json"), wantDecodeErr: true},
		{name: "truncated_json", payload: []byte(`{"prompt":"abc`), wantDecodeErr: true},
		{name: "trailing_garbage", payload: []byte(`{}{}`), wantDecodeErr: true},
		{name: "json_array", payload: []byte(`[1,2,3]`), wantDecodeErr: true},
		{name: "json_primitive", payload: []byte(`42`), wantDecodeErr: true},
		{name: "json_null", payload: []byte(`null`), emptyObj: true},
		{name: "raw_nul_invalid_utf8", payload: []byte("{\"prompt\":\"\xff\xfe\x00\",\"session_id\":\"s1\"}"), wantDecodeErr: true},
		{name: "type_confused", payload: []byte(`{"session_id":123,"prompt":["x"],"limit":"bad","offset":-9999,"messages":"nope","tags":[1,true],"notes":{},"tool_input":"str","is_error":"yes"}`), wantDecodeErr: true},
		{name: "empty_object", payload: []byte(`{}`), emptyObj: true},
		{name: "hostile_strings_and_templates", payload: hostileValid},
		{name: "deeply_nested", payload: deepValid},
		{name: "large_bounded_messages", payload: largeMessages},
		{name: "oversized_payload", payload: oversize, wantDecodeErr: true},
	}

	hookOps := map[string]bool{
		"settle":        true,
		"context":       true,
		"prompt":        true,
		"tool-before":   true,
		"tool-after":    true,
		"precompact":    true,
		"session-end":   true,
		"status":        true,
		"statusline":    true,
		"warmup-status": true,
	}

	for _, target := range []struct {
		label string
		dir   string
	}{
		{label: "indexed", dir: dir},
		{label: "unindexed", dir: emptyDir},
	} {
		for _, host := range allHosts {
			for _, op := range allOps {
				for _, hc := range cases {
					var out bytes.Buffer
					runErr := runHookExt(target.dir, []string{"--host", host, "--op", op}, bytes.NewReader(hc.payload), &out)
					if runErr != nil {
						t.Fatalf("%s host=%s op=%s case=%s: unexpected error: %v", target.label, host, op, hc.name, runErr)
					}
					trimmed := bytes.TrimSpace(out.Bytes())
					if bytes.Contains(trimmed, []byte("\n")) {
						t.Fatalf("%s host=%s op=%s case=%s: expected single JSON line, got %q", target.label, host, op, hc.name, out.String())
					}
					var resp hookengine.ExtResponse
					if uerr := json.Unmarshal(trimmed, &resp); uerr != nil {
						t.Fatalf("%s host=%s op=%s case=%s: invalid JSON output %q: %v", target.label, host, op, hc.name, out.String(), uerr)
					}
					if hc.wantDecodeErr {
						if resp.Ok || resp.Kind != "error" || resp.Error == "" {
							t.Fatalf("%s host=%s op=%s case=%s: expected error envelope, got %+v", target.label, host, op, hc.name, resp)
						}
						continue
					}
					if hookOps[op] {
						if !resp.Ok {
							t.Fatalf("%s host=%s op=%s case=%s: hook op expected ok=true, got %+v", target.label, host, op, hc.name, resp)
						}
						if host == "dsh" {
							for _, field := range []string{resp.Context, resp.Text, resp.Output} {
								if strings.Contains(field, "{{") || strings.Contains(field, "}}") {
									t.Fatalf("%s host=dsh op=%s case=%s: unneutralized template braces in %q", target.label, op, hc.name, field)
								}
							}
						}
					} else if hc.emptyObj && (op == "tool" || op == "remember" || op == "slash") {
						if resp.Ok || resp.Kind != "error" || resp.Error == "" {
							t.Fatalf("%s host=%s op=%s case=%s: empty CLI op expected error envelope, got %+v", target.label, host, op, hc.name, resp)
						}
					}
				}
			}
		}
	}
}

func TestHookExtLiveMultiRuntimeRoundTrip(t *testing.T) {
	dir, repo, _ := seedHookExtCorpus(t)
	home := os.Getenv("HOME")
	xdgCfg := filepath.Join(home, ".config")
	xdgCache := filepath.Join(home, ".cache")
	xdgData := filepath.Join(home, ".local", "share")
	dshHome := filepath.Join(home, ".dsh")
	ocState := filepath.Join(home, ".openclaw")
	for _, d := range []string{xdgCfg, xdgCache, xdgData, dshHome, ocState} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", xdgCfg)
	t.Setenv("XDG_CACHE_HOME", xdgCache)
	t.Setenv("XDG_DATA_HOME", xdgData)
	t.Setenv("DSH_HOME", dshHome)
	t.Setenv("OPENCLAW_STATE_DIR", ocState)
	t.Setenv("DEJA_WARMUP_SENTINEL", filepath.Join(home, "warmup-guard"))
	t.Setenv("DEJA_HOOK_REFRESH", "1")
	t.Setenv("TERM_PROGRAM", "")

	exeName := "deja"
	if runtime.GOOS == "windows" {
		exeName = "deja.exe"
	}
	exe := filepath.Join(t.TempDir(), exeName)
	buildCmd := exec.CommandContext(t.Context(), "go", "build", "-o", exe, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build deja: %v\n%s", err, out)
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// Bare name (no slash/backslash) ensures resolveDeja falls directly through to
	// ENOENT rather than probing EXE/wellKnown paths where a user install may live.
	missingCmd := "deja-nonexistent-test-bin-9999"

	runNodeModule := func(t *testing.T, script string, extraEnv map[string]string) string {
		t.Helper()
		nodeBin, lerr := exec.LookPath("node")
		if lerr != nil {
			t.Skip("node not installed; skipping JS live runtime check")
		}
		cmd := exec.CommandContext(t.Context(), nodeBin, "--input-type=module", "-e", script)
		cmd.Dir = repo
		env := make([]string, 0, len(os.Environ())+len(extraEnv))
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			overridden := false
			for ek := range extraEnv {
				if (runtime.GOOS == "windows" && strings.EqualFold(k, ek)) || k == ek {
					overridden = true
					break
				}
			}
			if !overridden {
				env = append(env, kv)
			}
		}
		for k, v := range extraEnv {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
		out, cerr := cmd.CombinedOutput()
		if cerr != nil {
			t.Fatalf("node round-trip failed: %v\n%s", cerr, out)
		}
		return string(out)
	}

	t.Run("node_opencode", func(t *testing.T) {
		_ = os.Remove(hookseenPath(dir))
		entry := filepath.Join(repoRoot, "extensions", "opencode", "index.js")
		libEntry := filepath.Join(repoRoot, "extensions", "opencode", "lib.js")
		script := `
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { DejaPlugin } = await import(pathToFileURL(process.env.EXT_ENTRY).href);
const { TOOL_SPECS } = await import(pathToFileURL(process.env.EXT_LIB).href);

const toasts = [];
const client = { tui: { showToast: async ({ body }) => toasts.push(body.message) } };
const hooks = await DejaPlugin({ client, directory: process.env.TEST_REPO }, { bin: process.env.TEST_BIN });

const sys = { system: ["Base prompt"] };
await hooks["experimental.chat.system.transform"]({ sessionID: "live-oc-1" }, sys);
assert.ok(sys.system[0].startsWith("Base prompt"), sys.system[0]);

const msgs = {
  messages: [{
    info: { id: "m1", role: "user", sessionID: "live-oc-1" },
    parts: [{ type: "text", text: "Check worker.go --no-verify" }],
  }],
};
await hooks["experimental.chat.messages.transform"]({ sessionID: "live-oc-1" }, msgs);
assert.match(msgs.messages[0].parts[0].text, /worker\.go|no-verify/i);

const bashOut = { output: "zsh:1: command not found: quibtool_9821" };
await hooks["tool.execute.after"]({ tool: "bash", sessionID: "live-oc-1", args: { command: "quibtool_9821 check ./..." } }, bashOut);
assert.match(bashOut.output, /brew install quibtool_9821-integration/);

const readOut = { output: "package app" };
await hooks["tool.execute.after"]({ tool: "read", sessionID: "live-oc-1", args: { filePath: process.env.TEST_WORKER } }, readOut);

await hooks["experimental.session.compacting"]({ sessionID: "live-oc-1" });
await hooks.event({ event: { type: "session.idle", properties: { sessionID: "live-oc-1" } } });

const recallText = await hooks[TOOL_SPECS].deja_recall.execute({ query: "worker.go", limit: 3 });
assert.match(recallText, /worker\.go/);
const blameText = await hooks[TOOL_SPECS].deja_blame.execute({ path: process.env.TEST_WORKER });
assert.match(blameText, /worker\.go|pytest/);
const fixText = await hooks[TOOL_SPECS].deja_fix.execute({ error: "zsh:1: command not found: quibtool_9821" });
assert.match(fixText, /quibtool_9821-integration/);

// Missing binary degrades cleanly without throwing.
const missingHooks = await DejaPlugin({ client: {}, directory: process.env.TEST_REPO }, { bin: process.env.TEST_MISSING_BIN });
const missingSys = { system: ["Base"] };
await missingHooks["experimental.chat.system.transform"]({ sessionID: "live-oc-missing" }, missingSys);
assert.equal(missingSys.system[0], "Base");
const missingRecall = await missingHooks[TOOL_SPECS].deja_recall.execute({ query: "worker.go" });
assert.match(missingRecall, /not installed/i);
`
		runNodeModule(t, script, map[string]string{
			"EXT_ENTRY":        entry,
			"EXT_LIB":          libEntry,
			"TEST_BIN":         exe,
			"TEST_MISSING_BIN": missingCmd,
			"DEJA_BIN":         "",
			"TEST_REPO":        repo,
			"TEST_WORKER":      filepath.Join(repo, "worker.go"),
		})
	})

	t.Run("node_openclaw", func(t *testing.T) {
		_ = os.Remove(hookseenPath(dir))
		entry := filepath.Join(repoRoot, "extensions", "openclaw", "index.mjs")
		script := `
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { default: plugin } = await import(pathToFileURL(process.env.EXT_ENTRY).href);

function mount(bin) {
  const tools = new Map();
  const events = new Map();
  const hooks = new Map();
  let cmd = null;
  const warnings = [];
  plugin.register({
    id: "deja-vu",
    pluginConfig: { bin },
    config: { hooks: { internal: { enabled: false } } },
    logger: { warn: (m) => warnings.push(m) },
    registerTool: (factory, meta) => tools.set(meta.name, factory),
    on: (name, fn) => {
      if (!events.has(name)) events.set(name, []);
      events.get(name).push(fn);
    },
    registerHook: (name, fn) => hooks.set(name, fn),
    registerCommand: (c) => { cmd = c; },
  });
  return { tools, events, hooks, cmd, warnings };
}

const live = mount(process.env.TEST_BIN);
const recallTool = live.tools.get("deja_recall")({ workspaceDir: process.env.TEST_REPO });
const recallRes = await recallTool.execute("id-1", { query: "worker.go" });
assert.match(recallRes.content[0].text, /worker\.go/);

const fixTool = live.tools.get("deja_fix")({ workspaceDir: process.env.TEST_REPO });
const fixRes = await fixTool.execute("id-2", { error: "zsh:1: command not found: quibtool_9821" });
assert.match(fixRes.content[0].text, /quibtool_9821-integration/);

const blameTool = live.tools.get("deja_blame")({ workspaceDir: process.env.TEST_REPO });
const blameRes = await blameTool.execute("id-3", { path: process.env.TEST_WORKER });
assert.match(blameRes.content[0].text, /worker\.go|pytest/);

const cmdOut = await live.cmd.handler({ args: "worker.go" });
assert.match(cmdOut.text, /worker\.go/);

for (const fn of live.events.get("before_prompt_build") || []) {
  const out = await fn({ prompt: "why is --no-verify kept in worker.go?" }, { sessionKey: "agent:main:main", workspaceDir: process.env.TEST_REPO });
  assert.ok(out && typeof out.prependContext === "string" && out.prependContext.length > 0);
}
for (const fn of live.events.get("session_end") || []) {
  await fn({ sessionId: "live-claw-1" }, { sessionKey: "agent:main:main", workspaceDir: process.env.TEST_REPO });
}

// Missing binary degrades cleanly without throwing.
const missing = mount(process.env.TEST_MISSING_BIN);
const missTool = missing.tools.get("deja_recall")({ workspaceDir: process.env.TEST_REPO });
const missRes = await missTool.execute("id-m", { query: "worker.go" });
assert.match(missRes.content[0].text, /not installed/i);
const missCmd = await missing.cmd.handler({ args: "worker.go" });
assert.match(missCmd.text, /not installed/i);
`
		runNodeModule(t, script, map[string]string{
			"EXT_ENTRY":        entry,
			"TEST_BIN":         exe,
			"TEST_MISSING_BIN": missingCmd,
			"DEJA_BIN":         "",
			"TEST_REPO":        repo,
			"TEST_WORKER":      filepath.Join(repo, "worker.go"),
		})
	})

	t.Run("node_dsh", func(t *testing.T) {
		_ = os.Remove(hookseenPath(dir))
		entry := filepath.Join(repoRoot, "extensions", "dsh", "index.js")
		script := `
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { default: apply } = await import(pathToFileURL(process.env.EXT_ENTRY).href);

const contexts = new Map();
const handlers = new Map();
let slashCmd = null;
const ctx = {
  tools: { register: () => {} },
  commands: { register: (c) => { slashCmd = c; } },
  systemPrompt: { context: (c) => contexts.set(c.name, c.text) },
  inject: () => {},
  on: (ev, fn) => {
    if (!handlers.has(ev)) handlers.set(ev, []);
    handlers.get(ev).push(fn);
  },
};
apply(ctx, {});

const dshLog = [{ type: "user/message", data: { role: "user", source: { kind: "user" }, content: [{ type: "text", text: "why is --no-verify kept in worker.go?" }] } }];
const assembly = {
  agent: {
    sessionId: "session-live-dsh-1",
    session: { header: { cwd: process.env.TEST_REPO }, get seq() { return dshLog.length; }, eventAt: (i) => dshLog[i] },
  },
};
const promptCtx = String(contexts.get("deja:recall")(assembly) || "");
assert.ok(promptCtx.length > 0, "expected DSH prompt recall");
assert.ok(!promptCtx.includes("{{"), "DSH prompt context leaked template braces: " + promptCtx);

const projCtx = String(contexts.get("deja:project")(assembly) || "");
assert.ok(projCtx.length > 0, "expected DSH project digest");
assert.ok(!projCtx.includes("{{"), "DSH project context leaked template braces: " + projCtx);

const slashRes = await slashCmd.handler({ rawInput: "worker.go", agent: { sessionId: "session-live-dsh-1" } });
assert.equal(slashRes.kind, "success");
assert.match(slashRes.text, /worker\.go/);

async function runPostExecute(exec, result) {
  let out = { kind: "accept" };
  for (const fn of handlers.get("tools/post-execute") || []) {
    out = await fn(exec, result, async () => out);
  }
  return out;
}

const agentRef = { sessionId: "session-live-dsh-1", session: { header: { cwd: process.env.TEST_REPO } } };
const readRes = await runPostExecute({ name: "read", arguments: { file_path: process.env.TEST_WORKER }, agent: agentRef }, {});
if (readRes.additionalContexts) {
  const txt = readRes.additionalContexts.map((m) => m.content?.[0]?.text || "").join("\n");
  assert.ok(!txt.includes("{{"), "DSH tool-before leaked template braces: " + txt);
}

const bashFail = await runPostExecute(
  { name: "bash", arguments: { command: "quibtool_9821 check ./..." }, agent: agentRef },
  { isError: false, content: [{ type: "text", text: "zsh:1: command not found: quibtool_9821\n[exit code: 1]" }] },
);
assert.equal((bashFail.additionalContexts || []).length, 1);
const fixText = bashFail.additionalContexts[0].content[0].text;
assert.match(fixText, /quibtool_9821-integration/);
assert.ok(!fixText.includes("{{"), "DSH tool-after leaked template braces: " + fixText);

const bashOk = await runPostExecute(
  { name: "bash", arguments: { command: "true" }, agent: agentRef },
  { isError: false, content: [{ type: "text", text: "ok\n[exit code: 0]" }] },
);
assert.equal(bashOk.additionalContexts, undefined);

for (const fn of handlers.get("session/created") || []) {
  await fn({ id: "session-live-dsh-1", header: { cwd: process.env.TEST_REPO } });
}
for (const fn of handlers.get("session/disposed") || []) {
  await fn({ id: "session-live-dsh-1" });
}
`
		runNodeModule(t, script, map[string]string{
			"EXT_ENTRY":   entry,
			"DEJA_BIN":    exe,
			"TEST_REPO":   repo,
			"TEST_WORKER": filepath.Join(repo, "worker.go"),
		})

		emptyHome := t.TempDir()
		missingScript := `
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { default: apply } = await import(pathToFileURL(process.env.EXT_ENTRY).href);
const contexts = new Map();
let slashCmd = null;
apply({
  tools: { register: () => {} },
  commands: { register: (c) => { slashCmd = c; } },
  systemPrompt: { context: (c) => contexts.set(c.name, c.text) },
  inject: () => {},
  on: () => {},
}, {});
const slashOut = await slashCmd.handler({ rawInput: "worker.go" });
assert.equal(slashOut.kind, "error");
assert.match(slashOut.text, /not installed/i);
const dshLog = [{ type: "user/message", data: { role: "user", source: { kind: "user" }, content: [{ type: "text", text: "Check worker.go" }] } }];
const promptOut = contexts.get("deja:recall")({
  agent: {
    sessionId: "session-live-dsh-miss",
    session: { header: { cwd: process.env.TEST_REPO }, get seq() { return dshLog.length; }, eventAt: (i) => dshLog[i] },
  },
});
assert.equal(promptOut, "");
`
		runNodeModule(t, missingScript, map[string]string{
			"EXT_ENTRY":   entry,
			"DEJA_BIN":    filepath.Join(t.TempDir(), "missing", exeName),
			"PATH":        t.TempDir(),
			"HOME":        emptyHome,
			"USERPROFILE": emptyHome,
			"TEST_REPO":   repo,
		})
	})

	t.Run("python_hermes", func(t *testing.T) {
		var pyBin string
		for _, cand := range []string{"python3", "python"} {
			p, err := exec.LookPath(cand)
			if err != nil {
				continue
			}
			if exec.CommandContext(t.Context(), p, "-c", "import sys; raise SystemExit(0 if sys.version_info[0] >= 3 else 1)").Run() == nil {
				pyBin = p
				break
			}
		}
		if pyBin == "" {
			t.Skip("working python 3 not installed; skipping Hermes live runtime check")
		}
		scriptPath := filepath.Join(repoRoot, "scripts", "test-hermes-extension.py")
		cmd := exec.CommandContext(t.Context(), pyBin, scriptPath)
		cmd.Dir = repoRoot
		cmd.Env = append([]string{}, os.Environ()...)
		cmd.Env = append(cmd.Env, "DEJA_BIN="+exe)
		if out, cerr := cmd.CombinedOutput(); cerr != nil {
			t.Fatalf("python Hermes contract suite failed: %v\n%s", cerr, out)
		}
	})
}
