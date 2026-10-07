package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// seedFileHistory gives /work/alpha/config.go six sessions and a decision, so
// the file line has something to say.
func seedFileHistory(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := 0; i < 6; i++ {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"Decision: config.go must load the ARENAGUARD sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
}

// The hosts whose only channel at a tool is its result send the file line's
// event after the tool, in their own payloads, recorded on their stands. The
// answer names the event a host checks it against: PostToolUse.
func TestFileLineAfterTheToolInEachHostsPayload(t *testing.T) {
	seedFileHistory(t)
	for _, tc := range []struct{ host, payload string }{
		// gemini-cli 0.60.0, AfterTool on read_file
		{"gemini read_file", `{"session_id":"g1","cwd":"/work/alpha","hook_event_name":"AfterTool","tool_name":"read_file","tool_input":{"file_path":"/work/alpha/config.go"},"tool_response":{"llmContent":"package app\n","returnDisplay":""}}`},
		{"gemini replace", `{"session_id":"g2","cwd":"/work/alpha","hook_event_name":"AfterTool","tool_name":"replace","tool_input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}`},
		// qwen-code 0.20.0, PostToolUse on read_file
		{"qwen read_file", `{"session_id":"q1","cwd":"/work/alpha","hook_event_name":"PostToolUse","permission_mode":"yolo","tool_name":"read_file","tool_input":{"file_path":"/work/alpha/config.go"},"tool_response":{"llmContent":"package app\n"}}`},
		// CodeBuddy's Claude-shaped PostToolUse after Read
		{"codebuddy Read", `{"session_id":"c1","cwd":"/work/alpha","hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"/work/alpha/config.go"}}`},
	} {
		t.Run(tc.host, func(t *testing.T) {
			out := toolHookRun(t, tc.payload)
			var resp sessionStartHookResponse
			if err := json.Unmarshal([]byte(out), &resp); err != nil {
				t.Fatalf("not the hook envelope: %v (%q)", err, out)
			}
			if resp.HookSpecificOutput.HookEventName != "PostToolUse" {
				t.Errorf("event %q, want PostToolUse", resp.HookSpecificOutput.HookEventName)
			}
			if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "ARENAGUARD") {
				t.Errorf("no file line: %q", resp.HookSpecificOutput.AdditionalContext)
			}
		})
	}
	// Claude Code's Read before the tool stays silent: it has the edit to
	// speak at.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"cc","cwd":"/work/alpha"}`); got != "" {
		t.Errorf("Claude Code's Read spoke: %q", got)
	}
	// Grok 1.0.41 hands read_file's PreToolUse context to the model, and names
	// the file under target_file.
	grok := `{"hookEventName":"pre_tool_use","sessionId":"gk","cwd":"/work/alpha","toolName":"read_file","toolInput":{"target_file":"/work/alpha/config.go"},"hook_event_name":"PreToolUse","session_id":"gk","tool_name":"read_file","tool_input":{"target_file":"/work/alpha/config.go"}}`
	if got := toolHookRun(t, grok); !strings.Contains(got, "ARENAGUARD") || !strings.Contains(got, `"PreToolUse"`) {
		t.Errorf("grok's read_file got %q", got)
	}
	// Once per session: the same file read again says nothing.
	if got := toolHookRun(t, `{"session_id":"g1","cwd":"/work/alpha","hook_event_name":"AfterTool","tool_name":"read_file","tool_input":{"file_path":"/work/alpha/config.go"}}`); got != "" {
		t.Errorf("the line repeated in the same session: %q", got)
	}
}

// TRAE CLI fires only PostToolUseFailure for a Bash that exits non-zero, with
// the output under error (traecli 0.207.1).
func TestTraeFailureEventCarriesTheFixPair(t *testing.T) {
	var pre, fail string
	for _, h := range traeHookWiring() {
		switch h.Event {
		case "PreToolUse":
			pre = h.Matcher
		case "PostToolUseFailure":
			fail = h.Sub + " " + h.Matcher
		}
	}
	if fail != "hook-tool-after Bash" {
		t.Fatalf("trae PostToolUseFailure = %q, want hook-tool-after on Bash", fail)
	}
	if pre != "Bash|apply_patch|Edit|Write" {
		t.Fatalf("trae PreToolUse matcher = %q", pre)
	}
	for _, h := range codexHookWiring {
		if h.Event == "PostToolUseFailure" {
			t.Fatal("codex's wiring changed with trae's: every codex user would have to trust the hook again")
		}
	}

	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	payload := `{"session_id":"01a115db-9529-72a0-9de5-a73952a2b106","cwd":"/work/app","hook_event_name":"PostToolUseFailure","model":"stub-model","permission_mode":"bypassPermissions","tool_name":"Bash","tool_input":{"command":"make"},"error":"Exit code 1\ngoroutine 1 [running]:\npanic: sql: database is closed","tool_use_id":"call_27ada812"}`
	var out strings.Builder
	if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out.String()), &resp); err != nil {
		t.Fatalf("no answer for trae's failure payload: %v (%q)", err, out.String())
	}
	if resp.HookSpecificOutput.HookEventName != "PostToolUseFailure" || !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "CGO_ENABLED=0") {
		t.Errorf("answer %+v", resp.HookSpecificOutput)
	}
}

// ZCode takes additionalContext from its tool events, so the file line is
// wired before Edit and Write and the fix pair after Bash, which fires
// PostToolUse with its exitCode when it fails (3.14.4).
func TestZCodeWiresTheToolEvents(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	if _, err := installZCodeHooks("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(zcodeConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks struct {
			Enabled bool `json:"enabled"`
			Events  map[string][]struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"events"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	for event, want := range map[string][2]string{
		"PreToolUse":  {"Bash|Edit|Write", "hook-tool"},
		"PostToolUse": {"Bash", "hook-tool-after"},
	} {
		es := root.Hooks.Events[event]
		if len(es) != 1 || es[0].Matcher != want[0] || !strings.HasSuffix(es[0].Hooks[0].Command, " "+want[1]) {
			t.Errorf("%s = %+v, want %s on %s", event, es, want[1], want[0])
		}
	}
	// A reinstall replaces deja's entries instead of stacking them, and an
	// uninstall takes them out.
	if _, err := installZCodeHooks("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(zcodeConfigPath())
	if string(b2) != string(b) {
		t.Errorf("a second install changed the file:\n%s", b2)
	}
	if _, err := installZCodeHooks("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(zcodeConfigPath()); strings.Contains(string(after), "hook-tool") {
		t.Errorf("uninstall left the tool hooks:\n%s", after)
	}
}

// An install over one from before the file line keeps qwen's old fix pair off
// PostToolUse and puts the file line there.
func TestQwenRetiresOnlyTheOldFixPair(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	settings := filepath.Join(home, ".qwen", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"hooks":{"PostToolUse":[{"matcher":"run_shell_command","hooks":[{"type":"command","command":"/bin/deja hook-tool-after","timeout":60000}]}]}}`
	if err := os.WriteFile(settings, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installQwenAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(settings)
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	post := root.Hooks["PostToolUse"]
	if len(post) != 1 || post[0].Matcher != "read_file|edit|write_file" || !strings.HasSuffix(post[0].Hooks[0].Command, " hook-tool") {
		t.Errorf("PostToolUse = %+v, want only the file line", post)
	}
}

// Grok's read_file goes through PreToolUse with the editors.
func TestGrokWiresTheReadBeforeTheEdit(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	t.Setenv("GROK_HOME", filepath.Join(home, ".grok"))
	if _, err := installGrokAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(grokHooksPath())
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	pre := root.Hooks["PreToolUse"]
	if len(pre) != 1 {
		t.Fatalf("PreToolUse = %+v", pre)
	}
	re := regexp.MustCompile("^(?:" + pre[0].Matcher + ")$")
	for _, tool := range []string{"Bash", "Read", "Edit", "Write", "Agent"} {
		if !re.MatchString(tool) {
			t.Errorf("grok PreToolUse matcher %q misses %s", pre[0].Matcher, tool)
		}
	}
}

// answeringDeja is a stand-in deja that records each run and answers
// hook-tool and hook-tool-after with a line naming the subcommand.
func answeringDeja(t *testing.T) (string, func() []pluginCall) {
	t.Helper()
	stub, calls := recordingDeja(t)
	b, err := os.ReadFile(stub)
	if err != nil {
		t.Fatal(err)
	}
	script := string(b) + "case \"$1\" in hook-tool|hook-tool-after) printf 'DEJA-LINE-%s' \"$1\";; esac\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub, calls
}

// toolCalls is the runs of one subcommand, with the tool it was asked about.
func toolCalls(calls []pluginCall, sub string) []map[string]any {
	var out []map[string]any
	for _, c := range calls {
		if strings.Fields(c.args)[0] == sub {
			out = append(out, c.input)
		}
	}
	return out
}

func inputPath(in map[string]any) string {
	ti, _ := in["tool_input"].(map[string]any)
	p, _ := ti["file_path"].(string)
	return p
}

// pi, Senpi and gjc share an extension; it now answers an edit and a write
// as it did a read. omp's edit names its file in the anchor its input opens
// with.
func TestPiFamilyFileLineAfterEditAndWrite(t *testing.T) {
	stub, calls := answeringDeja(t)
	out := runNodeDriver(t, "deja.ts", piExtensionTS(stub), `
const mod = await import("PLUGIN");
const handlers = {};
mod.default({ on(n, f) { (handlers[n] ||= []).push(f); }, registerCommand() {} });
const r = await handlers.tool_result[0]({ toolName: "edit", toolCallId: "c1", input: { path: "/w/config.go" }, content: [{ type: "text", text: "ok" }], isError: false });
console.log(JSON.stringify(r));
`)
	if !strings.Contains(out, "DEJA-LINE-hook-tool") {
		t.Errorf("pi's edit result was not given the file line: %s", out)
	}
	if got := toolCalls(calls(), "hook-tool"); len(got) != 1 || got[0]["tool_name"] != "edit" || inputPath(got[0]) != "/w/config.go" {
		t.Errorf("hook-tool asked %v", got)
	}

	stub, calls = answeringDeja(t)
	out = runNodeDriver(t, "index.js", ompExtensionJS(stub), `
const mod = await import("PLUGIN");
const handlers = {};
mod.default({ on(n, f) { (handlers[n] ||= []).push(f); }, registerCommand() {} });
const r = await handlers.tool_result[0]({ toolName: "edit", toolCallId: "c1", input: { input: "[/w/config.go#1A2B]\nreplace 3" }, content: [{ type: "text", text: "ok" }] });
console.log(JSON.stringify(r));
`)
	if !strings.Contains(out, "DEJA-LINE-hook-tool") {
		t.Errorf("omp's anchored edit was not given the file line: %s", out)
	}
	if got := toolCalls(calls(), "hook-tool"); len(got) != 1 || inputPath(got[0]) != "/w/config.go" {
		t.Errorf("hook-tool asked %v", got)
	}
}

// prime's model has one tool, ipython; files and commands are in its code
// and the exit code only in the BashResult text (prime-agent 0.9.8).
func TestPrimeFileAndFailureLinesFromTheCell(t *testing.T) {
	stub, calls := answeringDeja(t)
	out := runNodeDriver(t, "deja.ts", primeExtensionTS(stub), `
const mod = await import("PLUGIN");
const handlers = {};
mod.default({ on(n, f) { (handlers[n] ||= []).push(f); }, registerCommand() {} });
const read = await handlers.tool_result[0]({ toolName: "ipython", toolCallId: "c1", input: { code: "print(open('/w/config.go').read())" }, content: [{ type: "text", text: "package app" }], isError: false });
const fail = await handlers.tool_result[0]({ toolName: "ipython", toolCallId: "c2", input: { code: "r = await bash('make')" }, content: [{ type: "text", text: "BashResult(exit_code=3, output='fatal: ZORBLAX widget cache is stale\\n', duration=0.01)\n" }], isError: false });
const ok = await handlers.tool_result[0]({ toolName: "ipython", toolCallId: "c3", input: { code: "r = await bash('true')" }, content: [{ type: "text", text: "BashResult(exit_code=0, output='', duration=0.01)" }], isError: false });
console.log(JSON.stringify({ read, fail, ok }));
`)
	var got struct{ Read, Fail, Ok any }
	if err := json.Unmarshal([]byte(strings.TrimSpace(out[strings.LastIndex(strings.TrimSpace(out), "\n")+1:])), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !strings.Contains(out, "DEJA-LINE-hook-tool\"") || got.Ok != nil {
		t.Errorf("results %s", out)
	}
	after := toolCalls(calls(), "hook-tool-after")
	if len(after) != 1 || after[0]["tool_response"] != "fatal: ZORBLAX widget cache is stale\n\nExit Code: 3" {
		t.Errorf("hook-tool-after asked %v", after)
	}
	if files := toolCalls(calls(), "hook-tool"); len(files) != 1 || inputPath(files[0]) != "/w/config.go" {
		t.Errorf("hook-tool asked %v", files)
	}
}

// Cline CLI 3.0.69 hands beforeTool {toolCall:{toolName}, input} and puts the
// appendContext it returns beside the tool's result.
func TestClineBeforeToolGivesTheFileLine(t *testing.T) {
	stub, calls := answeringDeja(t)
	out := runNodeDriver(t, "index.js", clinePluginJS(stub), `
const mod = await import("PLUGIN");
const p = mod.default;
p.setup({ registerMessageBuilder() {}, registerRule() {}, registerCommand() {} }, { session: { sessionId: "1791367717716_ldkpz" }, workspaceInfo: { rootPath: "/w" } });
const read = await p.hooks.beforeTool({ snapshot: {}, tool: {}, toolCall: { toolName: "read_files" }, input: { files: [{ path: "/w/config.go" }] } });
const edit = await p.hooks.beforeTool({ snapshot: {}, tool: {}, toolCall: { toolName: "editor" }, input: { path: "/w/config.go", old_text: "a", new_text: "b" } });
const cmd = await p.hooks.beforeTool({ snapshot: {}, tool: {}, toolCall: { toolName: "run_commands" }, input: { commands: ["ls"] } });
console.log(JSON.stringify({ read, edit, cmd }));
`)
	if strings.Count(out, `"appendContext":"DEJA-LINE-hook-tool"`) != 2 || strings.Contains(out, `"cmd":{`) {
		t.Errorf("beforeTool answered %s", out)
	}
	got := toolCalls(calls(), "hook-tool")
	if len(got) != 2 || got[0]["tool_name"] != "read" || got[1]["tool_name"] != "edit" || got[0]["session_id"] != "1791367717716_ldkpz" || got[0]["cwd"] != "/w" {
		t.Errorf("hook-tool asked %v", got)
	}
}

// OpenClaw 2026.7's tool result middleware rewrites what the model reads
// back: the file line after read, edit and write, the fix pair after exec.
func TestOpenClawMiddlewareAddsTheLines(t *testing.T) {
	if !strings.Contains(openclawPluginManifest(), `"agentToolResultMiddleware": ["openclaw"]`) {
		t.Fatal("the manifest does not declare the middleware contract OpenClaw requires")
	}
	stub, calls := answeringDeja(t)
	out := runNodeDriver(t, "index.mjs", openclawPluginJS(stub), `
import plugin from "PLUGIN";
let mw, opts;
plugin.register({ on() {}, registerAgentToolResultMiddleware(fn, o) { mw = fn; opts = o; } });
const ctx = { runtime: "openclaw", sessionId: "7efce465", sessionKey: "agent:main:main" };
const read = await mw({ toolCallId: "c1", toolName: "read", args: { path: "/w/config.go" }, result: { content: [{ type: "text", text: "package app" }], details: {} } }, ctx);
const exec = await mw({ toolCallId: "c2", toolName: "exec", args: { command: "make" }, isError: true, result: { content: [{ type: "text", text: "fatal: ZORBLAX widget cache is stale\n\n(Command exited with code 3)" }] } }, ctx);
const other = await mw({ toolCallId: "c3", toolName: "web_fetch", args: { url: "x" }, result: { content: [] } }, ctx);
console.log(JSON.stringify({ read, exec, other, opts }));
`)
	if !strings.Contains(out, `"text":"DEJA-LINE-hook-tool"`) || !strings.Contains(out, `"text":"DEJA-LINE-hook-tool-after"`) || strings.Contains(out, `"other":{`) {
		t.Errorf("middleware answered %s", out)
	}
	if !strings.Contains(out, `"runtimes":["openclaw"]`) {
		t.Errorf("registered for %s", out)
	}
	if got := toolCalls(calls(), "hook-tool"); len(got) != 1 || inputPath(got[0]) != "/w/config.go" || got[0]["session_id"] != "7efce465" {
		t.Errorf("hook-tool asked %v", got)
	}
}

// Amp's tool.result {toolUseID, tool, input, status, output} takes
// {status, output} back.
func TestAmpToolResultGivesTheFileLine(t *testing.T) {
	stub, calls := answeringDeja(t)
	out := runNodeDriver(t, "deja.ts", ampPluginTS(stub), `
const mod = await import("PLUGIN");
const handlers = {};
mod.default({ on(n, f) { handlers[n] = f; }, registerCommand() {}, onDispose() {} });
const read = await handlers["tool.result"]({ toolUseID: "t1", tool: "Read", input: { path: "/w/config.go" }, status: "done", output: "package app", thread: { id: "T-1" } });
const again = await handlers["tool.result"]({ toolUseID: "t1", tool: "Read", input: { path: "/w/config.go" }, status: "done", output: "package app", thread: { id: "T-1" } });
console.log(JSON.stringify({ read, again }));
`)
	if strings.Count(out, `"output":"package app\n\nDEJA-LINE-hook-tool"`) != 2 {
		t.Errorf("tool.result answered %s", out)
	}
	if got := toolCalls(calls(), "hook-tool"); len(got) != 1 || got[0]["session_id"] != "T-1" {
		t.Errorf("hook-tool asked %v, want once for the call", got)
	}
}

// Hermes passes args to transform_tool_result; read_file, write_file and
// patch name the file there, and a patch in patch mode inside its text.
func TestHermesFileLineOnTransformToolResult(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	stub, calls := answeringDeja(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "plugin.py"), []byte(hermesPluginPy(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := "import importlib.util, json\n" +
		"spec = importlib.util.spec_from_file_location('dejahook', 'plugin.py')\n" +
		"m = importlib.util.module_from_spec(spec)\n" +
		"spec.loader.exec_module(m)\n" +
		"out = [m.repair(tool_name='read_file', args={'path': '/w/config.go'}, result='{\"content\": \"x\"}', session_id='h1'),\n" +
		"       m.repair(tool_name='patch', args={'mode': 'patch', 'patch': '*** Begin Patch\\n*** Update File: /w/config.go\\n@@\\n+x\\n*** End Patch'}, result='ok', session_id='h1'),\n" +
		"       m.repair(tool_name='web_search', args={'query': 'x'}, result='ok', session_id='h1')]\n" +
		"print(json.dumps(out))\n"
	cmd := exec.Command(py, "-c", probe)
	cmd.Dir = root
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("plugin: %v\n%s", err, b)
	}
	var got []any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if len(got) != 3 || !strings.HasSuffix(got[0].(string), "\n\nDEJA-LINE-hook-tool") || got[1] == nil || got[2] != nil {
		t.Errorf("repair returned %v", got)
	}
	asked := toolCalls(calls(), "hook-tool")
	if len(asked) != 2 || asked[0]["tool_name"] != "read" || asked[1]["tool_name"] != "apply_patch" {
		t.Errorf("hook-tool asked %v", asked)
	}
}
