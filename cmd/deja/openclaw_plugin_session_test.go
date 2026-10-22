package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// driveOpenClawPlugin registers the generated plugin against a stub api, calls
// one handler the way OpenClaw 2026.7.1-2 does — (event, ctx) — and returns
// every payload the plugin piped to deja.
func driveOpenClawPlugin(t *testing.T, hook, event string) []map[string]any {
	t.Helper()
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
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\n' \"$in\" >> " + calls + "\n"
	if err = os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "index.mjs")
	if err = os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	// The ctx OpenClaw builds for these hooks carries both: the session key,
	// which names a conversation slot, and the session id its transcript file
	// is named after.
	driver := `
import plugin from "` + plugin + `";
const handlers = {};
plugin.register({ on: (name, fn) => { handlers[name] = fn } });
const ctx = { sessionKey: "agent:main:main", sessionId: "7efce465-14b2-4671-a4d0-6dc309dd4992", agentId: "main" };
await handlers[` + "`" + hook + "`" + `](` + event + `, ctx);
`
	run := filepath.Join(home, "drive.mjs")
	if err = os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, runErr := exec.CommandContext(t.Context(), node, run).CombinedOutput(); runErr != nil {
		t.Fatalf("driving the plugin: %v\n%s", runErr, out)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the plugin never ran deja: %v", err)
	}
	var got []map[string]any
	for l := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("payload %q: %v", l, err)
		}
		got = append(got, m)
	}
	return got
}

// before_prompt_build is handed {prompt, messages}, and the session is on the
// second argument. Read off the event it was always "", so per-prompt recall
// had nothing to dedupe against and sent the same block on every turn (#4581).
func TestOpenClawPluginPromptRecallKnowsItsSession(t *testing.T) {
	got := driveOpenClawPlugin(t, "before_prompt_build", `{ prompt: "what did we do about the retry loop", messages: [] }`)
	if len(got) != 1 {
		t.Fatalf("want one hook-prompt call, got %v", got)
	}
	if id, _ := got[0]["session_id"].(string); id != "7efce465-14b2-4671-a4d0-6dc309dd4992" {
		t.Errorf("hook-prompt was sent session_id %q, so recall repeats itself on every turn", id)
	}
}

// The live stamp drops the asking session from MCP recall by its transcript
// id. The plugin stamped OpenClaw's session key — agent:main:main names no
// transcript at all — so the session asking came back as its own top hit, and
// the forget on compaction cleared rows filed under another name (#4582).
func TestOpenClawPluginStampsTheTranscriptID(t *testing.T) {
	for _, tc := range []struct{ hook, event string }{
		{"agent_turn_prepare", `{ prompt: "hello", messages: [], queuedInjections: [] }`},
		{"before_compaction", `{ messageCount: 40, tokenCount: 90000 }`},
	} {
		t.Run(tc.hook, func(t *testing.T) {
			got := driveOpenClawPlugin(t, tc.hook, tc.event)
			if len(got) != 1 {
				t.Fatalf("want one deja call, got %v", got)
			}
			if id, _ := got[0]["session_id"].(string); id != "7efce465-14b2-4671-a4d0-6dc309dd4992" {
				t.Errorf("%s sent session_id %q, not the transcript id the index knows the session by", tc.hook, id)
			}
		})
	}
}
