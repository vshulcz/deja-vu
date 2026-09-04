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

// OpenClaw's bootstrap hook recalls once against the session and only in
// gateway mode, so a local run had no memory of the project at all until it
// happened to ask a question the store answered. before_agent_start is the
// plugin's session-start channel and its prependContext reaches the model
// (measured on OpenClaw 2026.7.1-2, read off the provider request).
//
// It fires once per agent run rather than once per session, which is why the
// payload carries deja_once: without it the project digest would go in front of
// the model on every message.
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
	driver := `
import plugin from "` + plugin + `";
let handler;
plugin.register({ on: (name, fn) => { if (name === "before_agent_start") handler = fn } });
if (!handler) { console.log("NOHOOK"); process.exit(0) }
const out = await handler({ prompt: "hello" }, { sessionKey: "agent:main:explicit:s1" });
console.log(JSON.stringify(out ?? null));
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	first := strings.TrimSpace(strings.Split(strings.TrimSpace(string(out)), "\n")[0])
	if first == "NOHOOK" {
		t.Fatal("the plugin registers no before_agent_start handler, so a local " +
			"session still opens with no memory of the project")
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(first), &got); err != nil {
		t.Fatalf("handler returned %s", first)
	}
	if !strings.Contains(got["prependContext"], "DIGEST") {
		t.Errorf("the digest did not come back as prependContext, which is the "+
			"only field openclaw reads here: %s", first)
	}
	asked, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the plugin never called deja: %v", err)
	}
	var payload struct {
		SessionID string `json:"session_id"`
		Once      bool   `json:"deja_once"`
		CWD       string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(strings.Split(strings.TrimSpace(string(asked)), "\n")[0]), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v (%s)", err, asked)
	}
	if !payload.Once {
		t.Error("the payload does not ask for one digest per session, so it would " +
			"go in on every message")
	}
	if payload.SessionID == "" {
		t.Error("the payload names no session, and the guard that keeps the digest " +
			"to one turn is keyed on it")
	}
	if payload.CWD == "" {
		t.Error("the payload names no project, so the digest would be for whatever " +
			"deja's own working directory happens to be")
	}
}
