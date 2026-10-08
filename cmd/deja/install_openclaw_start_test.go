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

// The digest goes in through agent:bootstrap, which OpenClaw fires on every
// agent run. The plugin answers it as a plugin hook, which runs under --local
// and needs no allowConversationAccess; in the gateway the hook pack answers
// the same event first. A gateway session got the digest twice, because the
// pack named no session and the once-per-session check had nothing to match.
// Both now send the transcript id with deja_once, so deja hands out one.
// An OpenClaw without plugin hooks falls back to agent_turn_prepare.
func TestOpenClawPluginOpensWithTheProjectDigest(t *testing.T) {
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
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\n' \"$in\" >> " + calls + "\n" +
		"case \"$1\" in hook-context) printf 'DIGEST for this project' ;; esac\n"
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
	// The context OpenClaw 2026.9.8 builds for agent:bootstrap.
	driver := `
import plugin from "` + plugin + `";
import pack from "` + pack + `";
const ctx = () => ({ workspaceDir: "/w", bootstrapFiles: [], sessionKey: "agent:main:s1", sessionId: "6bb01805-a4da-4f4c-a26b-6159d976f6d5", agentId: "main" });
const on = {}, hooks = {};
plugin.register({ on: (name, fn) => { on[name] = fn }, registerHook: (name, fn) => { hooks[name] = fn } });
const a = { type: "agent", action: "bootstrap", sessionKey: "agent:main:s1", context: ctx() };
await hooks["agent:bootstrap"](a);
const b = { type: "agent", action: "bootstrap", sessionKey: "agent:main:s1", context: ctx() };
await pack(b);
const old = {};
plugin.register({ on: (name, fn) => { old[name] = fn } });
console.log(JSON.stringify({
  turnPrepare: "agent_turn_prepare" in on,
  plugin: a.context.bootstrapFiles,
  pack: b.context.bootstrapFiles,
  fallback: await old["agent_turn_prepare"]?.({ prompt: "hello" }, { sessionKey: "agent:main:s1", sessionId: "6bb01805-a4da-4f4c-a26b-6159d976f6d5" }),
}));
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	var got struct {
		TurnPrepare bool
		Plugin      []map[string]any
		Pack        []map[string]any
		Fallback    map[string]string
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &got); err != nil {
		t.Fatalf("driver printed %s", out)
	}
	if got.TurnPrepare {
		t.Error("the plugin also answers agent_turn_prepare where plugin hooks run, a second digest path")
	}
	for name, files := range map[string][]map[string]any{"plugin": got.Plugin, "pack": got.Pack} {
		if len(files) != 1 || !strings.Contains(files[0]["content"].(string), "DIGEST") || files[0]["path"] != "deja://recall" {
			t.Errorf("the %s's bootstrap hook did not add the digest to the Project Context: %v", name, files)
		}
	}
	if !strings.Contains(got.Fallback["prependContext"], "DIGEST") {
		t.Errorf("without plugin hooks the digest should come back as prependContext: %v", got.Fallback)
	}
	asked, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("nothing called deja: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(asked)), "\n")
	if len(lines) != 3 {
		t.Fatalf("want three hook-context calls, got %d:\n%s", len(lines), asked)
	}
	for i, l := range lines {
		var payload struct {
			SessionID string `json:"session_id"`
			Once      bool   `json:"deja_once"`
			CWD       string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(l), &payload); err != nil {
			t.Fatalf("payload %d is not JSON: %v (%s)", i, err, l)
		}
		// One id across all of them, the transcript's: the guard that keeps
		// the digest to one turn is keyed on it.
		if payload.SessionID != "6bb01805-a4da-4f4c-a26b-6159d976f6d5" {
			t.Errorf("payload %d names session %q", i, payload.SessionID)
		}
		if !payload.Once {
			t.Errorf("payload %d does not ask for one digest per session", i)
		}
		if i < 2 && payload.CWD != "/w" {
			t.Errorf("payload %d names project %q, not the agent's workspace", i, payload.CWD)
		}
	}
}
