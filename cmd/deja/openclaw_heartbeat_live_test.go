package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// OpenClaw's heartbeat polls run on agent:main:main and fire agent:bootstrap
// and before_prompt_build like any turn. The digest call and the prompt hook
// both stamped the session live again, so a session the user had left with
// /new dropped out of their other sessions' recall for twenty minutes after
// each poll (#5270).
func TestOpenClawHeartbeatDoesNotStampTheSessionLive(t *testing.T) {
	withStatsStores(t)
	dir := index.DefaultDir()
	cwd := t.TempDir()
	ask := func(fields map[string]any) {
		t.Helper()
		b, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		withHookStdin(t, string(b))
		captureStdout(t, func() { _ = runHookContext(dir, true) })
	}
	const sid = "fd320b23-1476-4773-805e-680e122f9ef1"
	poll := map[string]any{"session_id": sid, "cwd": cwd, "source": "startup", "deja_once": true, "deja_prompt_stamps": true}

	ask(poll)
	if !liveSessionIDs(dir)[sid] {
		t.Fatal("the session's first run did not stamp it")
	}
	// /new ends it.
	endSessionLive(dir, sid)
	// A heartbeat poll on the same session: its bootstrap asks for the digest
	// the session already had.
	ask(poll)
	if liveSessionIDs(dir)[sid] {
		t.Error("a heartbeat's digest call stamped a session that had ended live again")
	}

	// Control: a host whose digest call is all it sends per turn still
	// stamps on every one of them.
	const other = "c0ffee00-1476-4773-805e-680e122f9ef1"
	once := map[string]any{"session_id": other, "cwd": cwd, "source": "startup", "deja_once": true}
	ask(once)
	endSessionLive(dir, other)
	ask(once)
	if !liveSessionIDs(dir)[other] {
		t.Error("a deja_once host without a prompt hook stopped stamping its turns")
	}
}

// The plugin's prompt hook and tool middleware stay out of a heartbeat run:
// no recall on the poll's prompt, and no stamp from it (#5270).
func TestOpenClawPluginSkipsHeartbeatTurns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin openclaw would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s %s\\n' \"$1\" \"$in\" >> " + calls + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "index.mjs")
	if err := os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(home, "handler.mjs")
	if err := os.WriteFile(pack, []byte(openclawHandlerJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import plugin from "` + plugin + `";
import pack from "` + pack + `";
const on = {}, hooks = {};
let middleware;
plugin.register({ on: (n, fn) => { on[n] = fn }, registerHook: (n, fn) => { hooks[n] = fn }, registerAgentToolResultMiddleware: (fn) => { middleware = fn } });
const ctx = (trigger) => ({ sessionKey: "agent:main:main", sessionId: "fd320b23-1476-4773-805e-680e122f9ef1", workspaceDir: "/w", trigger, runId: "r-" + trigger });
const tool = { toolName: "read", args: { path: "/w/a.go" }, result: { content: [] } };
for (const trigger of ["heartbeat", "user"]) {
  await on.before_prompt_build({ prompt: "Follow the heartbeat monitor scratch context when provided.", messages: [] }, ctx(trigger));
  await middleware(tool, ctx(trigger));
}
const b = { type: "agent", action: "bootstrap", sessionKey: "agent:main:main", context: { workspaceDir: "/w", bootstrapFiles: [], sessionId: "fd320b23-1476-4773-805e-680e122f9ef1" } };
await hooks["agent:bootstrap"](b);
await pack(b);
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), node, run).CombinedOutput(); err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	got := readFile(t, calls)
	var prompts, tools, digests int
	for l := range strings.SplitSeq(strings.TrimSpace(got), "\n") {
		sub, payload, _ := strings.Cut(l, " ")
		switch sub {
		case "hook-prompt":
			prompts++
		case "hook-tool":
			tools++
		case "hook-context":
			digests++
			if !strings.Contains(payload, `"deja_prompt_stamps":true`) {
				t.Errorf("a digest call does not say the prompt hook stamps turns: %s", payload)
			}
		}
	}
	// The user turn reaches deja once each; the heartbeat turn not at all.
	if prompts != 1 || tools != 1 || digests != 2 {
		t.Errorf("hook-prompt %d, hook-tool %d, hook-context %d calls; want 1, 1, 2:\n%s", prompts, tools, digests, got)
	}
}
