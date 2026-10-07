package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The Hermes memory provider ran hook-context with nothing on stdin, so its
// first turn stamped no session live and the deja tool could answer that turn
// with the session asking it. The hook plugin was fixed for this in #4246; the
// provider, and the catalog package generated from it, were not.
func TestHermesProviderNamesTheSessionToHookContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in deja is a shell script")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	stub := filepath.Join(dir, "deja")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s|' \"$*\" >> "+log+"\ncat >> "+log+"\necho >> "+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "__init__.py"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "memory_provider.py"), []byte("class MemoryProvider:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "provider.py"), []byte(hermesMemoryPy(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	// Hermes hands prefetch the session on some paths and only initialize on
	// others; a /new switches it. Each first turn has to name the session it
	// belongs to.
	run := "import provider\n" +
		"p = provider.DejaMemoryProvider()\n" +
		"p.initialize('20261007_101500_aaaaaa')\n" +
		"p.prefetch('')\n" +
		"p.on_session_switch('20261007_101900_bbbbbb', reset=True)\n" +
		"p.prefetch('')\n" +
		"p.on_session_end([])\n"
	cmd := exec.Command(py, "-c", run)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("provider: %v\n%s", err, out)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	ended := false
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "hook-session-end") && strings.Contains(line, `"session_id": "20261007_101900_bbbbbb"`) {
			ended = true
		}
		if !strings.HasPrefix(line, "hook-context") {
			continue
		}
		for _, id := range []string{"20261007_101500_aaaaaa", "20261007_101900_bbbbbb"} {
			if strings.Contains(line, `"session_id": "`+id+`"`) {
				ids = append(ids, id)
			}
		}
	}
	if strings.Join(ids, ",") != "20261007_101500_aaaaaa,20261007_101900_bbbbbb" {
		t.Fatalf("hook-context was not told which session is starting, got %v:\n%s", ids, b)
	}
	// Hermes ends a provider's session at exit, /reset, /new and expiry only.
	if !ended {
		t.Fatalf("on_session_end did not drop the session's live stamp:\n%s", b)
	}
}

// The 2.x plugin sent no parent_session_id, so a sub-agent's digest and recall
// led with the parent that spawned it — live, and asking through it. 1.x got
// this in #4548 through client.session.get; 2.x has ctx.session.get, whose
// SessionInfo carries parentID.
func TestOpencode2PluginNamesASubagentsParent(t *testing.T) {
	dir := t.TempDir()
	bin, calls := opencodeStubDeja(t, dir)
	plugin := filepath.Join(dir, "deja.mjs")
	if err := os.WriteFile(plugin, []byte(opencodePluginJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}
	runNode(t, dir, `
import plugin from "`+plugin+`";
const hooks = { session: {}, tool: {} };
const domain = (name) => ({ hook: async (event, fn) => { (hooks[name][event] ||= []).push(fn) } });
const session = domain("session");
session.get = async ({ sessionID }) => (sessionID === "ses_child" ? { id: sessionID, parentID: "ses_parent" } : { id: sessionID });
await plugin.setup({ location: { directory: "`+dir+`" }, session, tool: domain("tool") });
for (const fn of hooks.session.context) {
  await fn({ sessionID: "ses_child", system: [], messages: [{ role: "user", content: [{ type: "text", text: "the retry loop" }] }] });
}
`)
	for _, sub := range []string{"hook-context", "hook-prompt"} {
		got := strings.Join(stubCalls(calls, sub), "\n")
		if !strings.Contains(got, `"session_id":"ses_child"`) || !strings.Contains(got, `"parent_session_id":"ses_parent"`) {
			t.Errorf("%s was not told the sub-agent's parent: %q", sub, got)
		}
	}
}

// dsh's per-prompt recall ran with no session id until #4799, so the session
// was stamped live only by the digest and a prompt after it changed nothing.
// Both seams name the session by its bare uuid, the id deja indexes it under.
func TestDshAutoPluginNamesTheSessionToEveryHook(t *testing.T) {
	dir := t.TempDir()
	bin, calls := opencodeStubDeja(t, dir)
	plugin := filepath.Join(dir, "auto.mjs")
	if err := os.WriteFile(plugin, []byte(dshAutoJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}
	runNode(t, dir, `
import apply from `+jsString(plugin)+`;
const contexts = [];
apply({ systemPrompt: { context: (c) => contexts.push(c) } });
const agent = { sessionId: "session-0f1e", session: { header: { cwd: "/w" }, events: [{ type: "user/message", data: { content: "the retry loop" } }] } };
for (const c of contexts) c.text({ agent });
`)
	for _, sub := range []string{"hook-context", "hook-prompt"} {
		got := strings.Join(stubCalls(calls, sub), "\n")
		if !strings.Contains(got, `"session_id":"0f1e"`) {
			t.Errorf("%s was not told the session: %q", sub, got)
		}
	}
}

// cline's rule ran hook-context with no stdin, so the session was never
// stamped live and the digest ranked by wherever cline was launched (#4199).
// setup is handed the session id and the workspace root; both go with it.
func TestClinePluginNamesTheSessionToHookContext(t *testing.T) {
	dir := t.TempDir()
	bin, calls := opencodeStubDeja(t, dir)
	plugin := filepath.Join(dir, "index.mjs")
	if err := os.WriteFile(plugin, []byte(clinePluginJS(bin)), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(dir, "work")
	runNode(t, dir, `
import plugin from `+jsString(plugin)+`;
let rule;
const api = { registerMessageBuilder() {}, registerRule: (r) => { rule = r }, registerCommand() {} };
plugin.setup(api, { session: { sessionId: "1791367688197_z0dgx" }, workspaceInfo: { rootPath: `+jsString(ws)+` } });
rule.content();
`)
	got := strings.Join(stubCalls(calls, "hook-context"), "\n")
	if !strings.Contains(got, `"session_id":"1791367688197_z0dgx"`) || !strings.Contains(got, `"cwd":`+jsString(ws)) {
		t.Fatalf("hook-context was not told the session and its workspace: %q", got)
	}
}

// omp keeps one extension process across /new, a resume and a fork, which
// arrive as session_switch. The digest flag was set once per process, so every
// session after the first started with no digest and was never stamped live.
// pi and prime had the same flag. The digest now goes once per session id,
// read from the session manager on each turn.
func TestPiFamilyDigestFollowsTheSessionAfterNew(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the extension")
	}
	typescript := exec.Command(node, "-e", "process.exit(process.features.typescript ? 0 : 1)").Run() == nil
	for _, c := range []struct {
		name, file string
		ts         bool
	}{
		{"omp", "omp.mjs", false},
		{"pi", "pi.ts", true},
		{"prime", "prime.ts", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.ts && !typescript {
				t.Skip("this node does not strip TypeScript types")
			}
			dir := t.TempDir()
			calls := filepath.Join(dir, "calls")
			bin := filepath.Join(dir, "deja")
			script := "#!/bin/sh\nprintf '%s %s\\n' \"$1\" \"$(cat)\" >> " + calls + "\n" +
				"[ \"$1\" = hook-context ] && echo '{\"hookSpecificOutput\":{\"additionalContext\":\"past work\"}}'\nexit 0\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			src := map[string]string{"omp": ompExtensionJS(bin), "pi": piExtensionTS(bin), "prime": primeExtensionTS(bin)}[c.name]
			ext := filepath.Join(dir, c.file)
			if err := os.WriteFile(ext, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			runNode(t, dir, `
import ext from "`+ext+`";
const handlers = {};
const pi = { on: (e, fn) => (handlers[e] ||= []).push(fn), registerCommand() {}, registerTool() {} };
ext(pi);
let id = "ses_first";
const ctx = { cwd: "`+dir+`", sessionManager: { getSessionId: () => id, getSessionFile: () => "" }, ui: { notify() {}, setStatus() {} } };
const fire = async (name, event) => { for (const fn of handlers[name] || []) await fn(event, ctx) };
await fire("session_start", {});
await fire("before_agent_start", { prompt: "one" });
await fire("before_agent_start", { prompt: "two" });
id = "ses_after_new";
await fire("session_switch", { reason: "new" });
await fire("session_start", {});
await fire("before_agent_start", { prompt: "three" });
`)
			var ids []string
			for _, l := range stubCalls(calls, "hook-context") {
				for _, id := range []string{"ses_first", "ses_after_new"} {
					if strings.Contains(l, `"session_id":"`+id+`"`) {
						ids = append(ids, id)
					}
				}
			}
			if got := strings.Join(ids, ","); got != "ses_first,ses_after_new" {
				t.Fatalf("digest calls by session = %q, want one for each session", got)
			}
		})
	}
}
