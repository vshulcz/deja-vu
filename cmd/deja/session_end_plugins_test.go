package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// pluginCall is one run of the stand-in deja: its arguments and its stdin.
type pluginCall struct {
	args  string
	input map[string]any
}

// recordingDeja writes a stand-in deja that appends "args|stdin" to a log and
// answers nothing, and returns its path and a reader for the log.
func recordingDeja(t *testing.T) (string, func() []pluginCall) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in deja is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	stub := filepath.Join(dir, "deja")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s|%s\\n' \"$*\" \"$in\" >> " + log + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub, func() []pluginCall {
		b, _ := os.ReadFile(log)
		var out []pluginCall
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			args, in, ok := strings.Cut(l, "|")
			if !ok {
				continue
			}
			c := pluginCall{args: args}
			_ = json.Unmarshal([]byte(in), &c.input)
			out = append(out, c)
		}
		return out
	}
}

// endedSessions is the session ids hook-session-end was run with, in order.
func endedSessions(calls []pluginCall) []string {
	var out []string
	for _, c := range calls {
		if strings.HasPrefix(c.args, "hook-session-end") {
			id, _ := c.input["session_id"].(string)
			out = append(out, id)
		}
	}
	return out
}

// runNodeDriver writes the plugin source and a driver beside it and runs the
// driver with node, stripping types when the plugin is TypeScript.
func runNodeDriver(t *testing.T, name, src, driver string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin the host would run")
	}
	args := []string{}
	if strings.HasSuffix(name, ".ts") {
		if err := exec.Command(node, "--experimental-strip-types", "-e", "").Run(); err != nil {
			t.Skip("this node cannot strip types")
		}
		args = append(args, "--experimental-strip-types", "--no-warnings")
	}
	dir := t.TempDir()
	plugin := filepath.Join(dir, name)
	if err := os.WriteFile(plugin, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	drive := filepath.Join(dir, "drive.mjs")
	if err := os.WriteFile(drive, []byte(strings.ReplaceAll(driver, "PLUGIN", plugin)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, append(args, drive)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	return string(out)
}

// The pi-shaped extension — pi, Senpi and gjc load the same file — ends the
// session it served when the host shuts it down. pi 0.73 sends
// {type, reason} and a ctx that, on /new or /resume, may already name the
// next session, so the id is the one the extension held.
func TestPiFamilyExtensionsEndTheirSession(t *testing.T) {
	for _, tc := range []struct{ name, file string }{
		{"pi", "deja.ts"},
		{"prime", "deja.ts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub, calls := recordingDeja(t)
			src := piExtensionTS(stub)
			if tc.name == "prime" {
				src = primeExtensionTS(stub)
			}
			runNodeDriver(t, tc.file, src, `
const mod = await import("PLUGIN");
const handlers = {};
const pi = { on(n, f) { (handlers[n] ||= []).push(f); }, registerCommand() {} };
mod.default(pi);
const ui = { setStatus() {}, notify() {} };
const ctxFor = (id) => ({ ui, sessionManager: { getSessionId: () => id } });
for (const f of handlers.before_agent_start) await f({ prompt: "hello" }, ctxFor("PI-1"));
for (const f of handlers.session_shutdown) await f({ type: "session_shutdown", reason: "new", targetSessionFile: "/x/next.jsonl" }, ctxFor("PI-2"));
`)
			if got := endedSessions(calls()); len(got) != 1 || got[0] != "PI-1" {
				t.Errorf("hook-session-end ran for %v, want the session that ended, PI-1", got)
			}
		})
	}
}

// omp ends a session on exit and on a switch to another one: omp 17.4
// emits session_switch {reason, previousSessionFile} after /new, /resume and a
// fork, and session_shutdown {} on exit.
func TestOmpExtensionEndsSessionsOnSwitchAndExit(t *testing.T) {
	stub, calls := recordingDeja(t)
	runNodeDriver(t, "index.js", ompExtensionJS(stub), `
const mod = await import("PLUGIN");
const handlers = {};
const pi = { on(n, f) { (handlers[n] ||= []).push(f); }, registerCommand() {} };
mod.default(pi);
const ui = { setStatus() {}, notify() {} };
const ctxFor = (id) => ({ ui, sessionManager: { getSessionId: () => id } });
for (const f of handlers.before_agent_start) await f({}, ctxFor("OMP-1"));
for (const f of handlers.session_switch) await f({ type: "session_switch", reason: "new", previousSessionFile: "/x/one.jsonl" }, ctxFor("OMP-2"));
for (const f of handlers.session_shutdown) await f({ type: "session_shutdown" }, ctxFor("OMP-2"));
`)
	if got := endedSessions(calls()); strings.Join(got, ",") != "OMP-1,OMP-2" {
		t.Errorf("hook-session-end ran for %v, want OMP-1 at the switch and OMP-2 at exit", got)
	}
}

// OpenClaw 2026.7 runs session_end with {sessionId, sessionKey, reason,
// sessionFile, nextSessionId} on /new, idle expiry, a compaction that rotates
// the id and gateway shutdown.
func TestOpenClawPluginEndsTheSession(t *testing.T) {
	stub, calls := recordingDeja(t)
	runNodeDriver(t, "index.mjs", openclawPluginJS(stub), `
import plugin from "PLUGIN";
const handlers = {};
plugin.register({ on: (name, fn) => { handlers[name] = fn } });
const ctx = { sessionKey: "agent:main:main", sessionId: "OC-1", agentId: "main" };
await handlers.session_end({ sessionId: "OC-1", sessionKey: "agent:main:main", messageCount: 2, reason: "shutdown", sessionFile: "/x/OC-1.jsonl" }, ctx);
`)
	if got := endedSessions(calls()); len(got) != 1 || got[0] != "OC-1" {
		t.Errorf("hook-session-end ran for %v, want OC-1", got)
	}
}

// Cline's runtime hooks are declared on the plugin and handed no session, so
// the plugin keeps the one setup was given. afterRun ends it; the next prompt
// stamps it again.
func TestClinePluginEndsTheSessionAfterARun(t *testing.T) {
	stub, calls := recordingDeja(t)
	runNodeDriver(t, "index.js", clinePluginJS(stub), `
const mod = await import("PLUGIN");
const plugin = mod.default;
if (!plugin.manifest.capabilities.includes("hooks")) throw new Error("hooks capability not declared");
plugin.setup({ registerMessageBuilder() {}, registerRule() {}, registerCommand() {} }, { session: { sessionId: "1791369007452_agxq3" } });
await plugin.hooks.afterRun({ snapshot: { agentId: "a", status: "completed", iteration: 1, messages: [] }, result: {} });
`)
	if got := endedSessions(calls()); len(got) != 1 || got[0] != "1791369007452_agxq3" {
		t.Errorf("hook-session-end ran for %v, want the session setup was given", got)
	}
}

// dsh disposes of a session in the web and TUI profiles, and the headless
// profile exits without disposing anything (0.1.1-rc.2), so the plugin ends
// what it saw created on session/disposed and on process exit, once each.
func TestDSHPluginEndsSessionsOnDisposeAndExit(t *testing.T) {
	stub, calls := recordingDeja(t)
	runNodeDriver(t, "auto.js", dshAutoJS(stub), `
const mod = await import("PLUGIN");
const handlers = {};
const ctx = {
  systemPrompt: { context() {} },
  on(name, fn) { (handlers[name] ||= []).push(fn); },
};
mod.default(ctx);
for (const f of handlers["session/created"]) f({ id: "session-A" });
for (const f of handlers["session/created"]) f({ id: "session-B" });
for (const f of handlers["session/disposed"]) f({ id: "session-A" });
for (const f of handlers["session/disposed"]) f({ id: "session-A" });
`)
	// The live stamp is under the bare uuid hook-context was given; an end
	// naming "session-A" matched no stamp and left it (stand on 0.1.1-rc.2).
	if got := endedSessions(calls()); strings.Join(got, ",") != "A,B" {
		t.Errorf("hook-session-end ran for %v, want A at its disposal and B at exit, prefix stripped", got)
	}
}

// Amp has no event for a thread ending. agent.end closes a turn, so the
// thread's stamp goes there and comes back with the next agent.start, and
// agent.end still answers with Amp's own default.
func TestAmpPluginEndsTheThreadAfterATurn(t *testing.T) {
	stub, calls := recordingDeja(t)
	out := runNodeDriver(t, "deja.ts", ampPluginTS(stub), `
const mod = await import("PLUGIN");
const handlers = {};
const disposers = [];
const amp = { on(n, f) { handlers[n] = f; }, registerCommand() {}, onDispose(f) { disposers.push(f); } };
mod.default(amp);
const ctx = { ui: { notify: async () => {} } };
await handlers["agent.start"]({ thread: { id: "T-1" }, message: "hello", id: "m1" }, ctx);
console.log(JSON.stringify(await handlers["agent.end"]({ thread: { id: "T-1" }, status: "done" }, ctx)));
for (const f of disposers) await f();
`)
	if strings.TrimSpace(out) != `{"action":"done"}` {
		t.Errorf("agent.end answered %q, want Amp's default {\"action\":\"done\"}", out)
	}
	if got := endedSessions(calls()); len(got) < 1 || got[0] != "T-1" {
		t.Errorf("hook-session-end ran for %v, want T-1", got)
	}
}

// Hermes runs on_session_finalize(session_id, platform, reason) on exit, on
// /new and when a single query finishes. on_session_end is not it: that one
// fires after every turn.
func TestHermesPluginEndsTheSessionOnFinalize(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	stub, calls := recordingDeja(t)
	root := t.TempDir()
	dir := filepath.Join(root, "plugins", "deja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "__init__.py"), []byte(hermesPluginPy(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := "import importlib.util\n" +
		"spec = importlib.util.spec_from_file_location('dejahook', 'plugins/deja/__init__.py')\n" +
		"m = importlib.util.module_from_spec(spec)\n" +
		"spec.loader.exec_module(m)\n" +
		"hooks = {}\n" +
		"class Ctx:\n" +
		"    def register_hook(self, name, fn): hooks[name] = fn\n" +
		"    def register_command(self, *a, **k): pass\n" +
		"m.register(Ctx())\n" +
		"hooks['on_session_finalize'](session_id='20261007_132227_1adb27', platform='cli', reason='shutdown')\n" +
		"hooks['on_session_finalize'](session_id=None, platform='cli', reason='shutdown')\n"
	cmd := exec.Command(py, "-c", probe)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin: %v\n%s", err, out)
	}
	if got := endedSessions(calls()); len(got) != 1 || got[0] != "20261007_132227_1adb27" {
		t.Errorf("hook-session-end ran for %v, want the finalized session once", got)
	}
	if !strings.Contains(hermesPluginManifest, "on_session_finalize") {
		t.Error("the manifest does not list on_session_finalize")
	}
}

// The payloads the hook-file hosts send at the end of a session, recorded on
// their live stands. Each one names the session its prompt hook stamped, and
// hook-session-end has to find it under the host's own spelling.
func TestSessionEndReadsEveryHostsPayload(t *testing.T) {
	for _, tc := range []struct{ host, id, payload string }{
		// kimi-code 0.28.1
		{"kimi", "session_a2bf21be-23aa-44b5-a6fa-c5eadfeab90f",
			`{"hook_event_name":"SessionEnd","session_id":"session_a2bf21be-23aa-44b5-a6fa-c5eadfeab90f","cwd":"/tmp/w","reason":"exit"}`},
		// grok 1.0.41 sends both spellings
		{"grok", "01a115ee-2302-7ed0-b66f-c58acfb5023a",
			`{"hookEventName":"session_end","sessionId":"01a115ee-2302-7ed0-b66f-c58acfb5023a","cwd":"/tmp/w","reason":"shutdown","hook_event_name":"SessionEnd","session_id":"01a115ee-2302-7ed0-b66f-c58acfb5023a"}`},
		// goose 1.46.0
		{"goose", "20261007_1", `{"event":"SessionEnd","session_id":"20261007_1","matcher_context":null}`},
		// Antigravity's Stop names the conversation in camelCase
		{"antigravity", "9b1c2d3e-0000-4000-8000-000000000001",
			`{"conversationId":"9b1c2d3e-0000-4000-8000-000000000001","workspacePaths":["/tmp/w"]}`},
	} {
		t.Run(tc.host, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "index.db")
			markSessionLive(dir, tc.id)
			markSessionLive(dir, "someone-else")
			runHookSessionEnd(dir, strings.NewReader(tc.payload))
			live := liveSessionIDs(dir)
			if live[tc.id] {
				t.Errorf("%s's session is still stamped live after its end", tc.host)
			}
			if !live["someone-else"] {
				t.Error("another session's stamp went with it")
			}
		})
	}
}

// goose's prompt hook re-encodes the payload it is given, and the session id
// was dropped there: nothing stamped a goose session, so its end had nothing
// to clear and recall could not dedupe within it.
func TestGoosePromptHookStampsTheSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GOOSE_PATH_ROOT", "")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", filepath.Join(home, "moim.md"))
	dir := filepath.Join(home, "index.db")
	payload := `{"event":"UserPromptSubmit","session_id":"20261007_1","matcher_context":"hello there","message":"hello there"}`
	_ = refreshGooseForPrompt(dir, []byte(payload))
	if !liveSessionIDs(dir)["20261007_1"] {
		t.Fatal("the prompt hook did not stamp goose's session live")
	}
	if _, err := writeGooseHook("/bin/deja"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(gooseHookPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"SessionEnd"`) || !strings.Contains(string(b), "hook-session-end") {
		t.Errorf("goose's hooks do not end the session:\n%s", b)
	}
}

// Kimi and Grok take a SessionEnd entry beside the ones they had, and an
// uninstall takes it out with them.
func TestKimiAndGrokWireSessionEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIMI_CODE_HOME", filepath.Join(home, ".kimi-code"))
	t.Setenv("GROK_HOME", filepath.Join(home, ".grok"))
	if _, err := installKimiAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	kimi, _ := os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml"))
	if !strings.Contains(string(kimi), "event = \"SessionEnd\"") || !strings.Contains(string(kimi), "hook-session-end") {
		t.Errorf("kimi config has no SessionEnd hook:\n%s", kimi)
	}
	if _, err := installGrokAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	grok, _ := os.ReadFile(grokHooksPath())
	var root struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(grok, &root); err != nil {
		t.Fatal(err)
	}
	if ev := root.Hooks["SessionEnd"]; len(ev) != 1 || !strings.Contains(ev[0].Hooks[0].Command, "hook-session-end") {
		t.Errorf("grok hooks have no SessionEnd entry:\n%s", grok)
	}
	if _, err := installKimiAuto("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml")); strings.Contains(string(after), "hook-session-end") {
		t.Errorf("uninstall left kimi's SessionEnd hook:\n%s", after)
	}
	if _, err := installGrokAuto("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if after, err := os.ReadFile(grokHooksPath()); err == nil && strings.Contains(string(after), "hook-session-end") {
		t.Errorf("uninstall left grok's SessionEnd hook:\n%s", after)
	}
}

// Antigravity has no session-end event. PreInvocation stamps the conversation
// and Stop, when the execution loop ends, drops the stamp and answers with the
// empty object.
func TestAntigravityStampsAndStopEndsTheConversation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEJA_RECALL", "")
	dir := filepath.Join(home, "index.db")
	payload := `{"invocationNum":1,"workspacePaths":["` + filepath.ToSlash(home) + `"],"conversationId":"C-1","transcriptPath":""}`
	var out strings.Builder
	if err := runHookAntigravity(dir, strings.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	if !liveSessionIDs(dir)["C-1"] {
		t.Fatal("PreInvocation did not stamp the conversation live")
	}
	runHookSessionEnd(dir, strings.NewReader(`{"conversationId":"C-1"}`))
	if liveSessionIDs(dir)["C-1"] {
		t.Error("Stop's payload did not end the conversation")
	}
	t.Setenv("GEMINI_CLI_HOME", "")
	if _, err := installAntigravityPlugin("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(antigravityConfigHome(), "plugins", "deja", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"Stop"`) || !strings.Contains(string(b), "hook-session-end --json") {
		t.Errorf("the plugin does not end the conversation at Stop:\n%s", b)
	}
}

// Reasonix 2.30 sends extension/event session.end {sessionPath, phase:"end"}
// on every exit; the sidecar now subscribes to it and drops the stamp.
func TestReasonixExtSessionEndDropsTheStamp(t *testing.T) {
	fakeRxHooks(t, func(string, map[string]any) (string, error) { return "", nil })
	h := startFakeRxHost(t)
	h.handshake()
	h.notify("extension/event", map[string]any{"event": "session.start", "payload": map[string]any{"sessionPath": "RX-1"}})
	markSessionLive(h.dir, "RX-1")
	h.notify("extension/event", map[string]any{"event": "session.end", "payload": map[string]any{"sessionPath": "/x/projects/p/sessions/RX-1.jsonl", "phase": "end"}})
	deadline := time.Now().Add(3 * time.Second)
	for liveSessionIDs(h.dir)["RX-1"] && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if liveSessionIDs(h.dir)["RX-1"] {
		t.Error("session.end left the session stamped live")
	}
	found := false
	for _, s := range rxSubscriptions {
		found = found || s == "session.end"
	}
	if !found {
		t.Error("the sidecar does not subscribe to session.end")
	}
}
