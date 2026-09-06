package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// dsh's per-prompt recall answers the question just asked; the project digest —
// what this project settled, regardless of the question — had no channel, so a
// dsh session opened knowing nothing about the project until it happened to ask
// something the store answered. agent/session-start is emit-only, so the digest
// rides the same systemPrompt.context seam the recall does, gated to one turn
// per session.
func TestDeepSeekAutoOpensWithTheProjectDigest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", filepath.Join(home, ".dsh"))
	if _, err := installDeepSeekAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	js, err := os.ReadFile(filepath.Join(home, ".dsh", "plugins", "deja", "auto.js"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(js)
	if !strings.Contains(s, `name: "deja:project"`) {
		t.Errorf("no project-digest context is registered:\n%s", s)
	}
	if !strings.Contains(s, `"hook-context", "--plain"`) {
		t.Errorf("the digest never asks deja for the project's history:\n%s", s)
	}
	if !strings.Contains(s, "deja_once: true") {
		t.Errorf("the digest is not gated to one turn, so it repeats every assembly:\n%s", s)
	}
}

// The plugin's contract exercised the way the host uses it: two assemblies of
// one session get the digest exactly once, a second session gets its own, and
// the digest asks deja with deja_once and the session id.
func TestDeepSeekAutoDigestContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin dsh would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s %s\\n' \"$1\" \"$in\" >> " + calls + "\n" +
		"case \"$1\" in hook-context) printf 'DIGEST for this project' ;; esac\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "auto.js")
	if err := os.WriteFile(plugin, []byte(dshAutoJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import plugin from "` + plugin + `";
const contexts = [];
plugin({ systemPrompt: { context: (c) => contexts.push(c) } });
const digest = contexts.find((c) => c.name === "deja:project");
if (!digest) { console.log("NODIGEST"); process.exit(0) }
const a = { sessionId: "sess-A", session: { events: [] } };
const b = { sessionId: "sess-B", session: { events: [] } };
console.log(JSON.stringify(digest.text({ agent: a })));
console.log(JSON.stringify(digest.text({ agent: a })));
console.log(JSON.stringify(digest.text({ agent: b })));
`
	run := filepath.Join(home, "drive.mjs")
	if err := os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, run).CombinedOutput()
	if err != nil {
		t.Fatalf("driving the plugin: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if lines[0] == "NODIGEST" {
		t.Fatal("the plugin registers no deja:project context, so a session opens with no memory of the project")
	}
	if len(lines) < 3 {
		t.Fatalf("driver said %q", out)
	}
	if !strings.Contains(lines[0], "DIGEST") {
		t.Errorf("the session's first turn carried no digest: %q", lines[0])
	}
	if strings.Contains(lines[1], "DIGEST") {
		t.Errorf("the digest came back on the same session's second turn: %q", lines[1])
	}
	if !strings.Contains(lines[2], "DIGEST") {
		t.Errorf("a second session was refused the digest the first one had: %q", lines[2])
	}
	// One session, one call: the in-process guard must keep the long-lived web
	// and tui profiles from spawning deja on every assembly.
	asked, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the plugin never called deja: %v", err)
	}
	if n := strings.Count(string(asked), "hook-context"); n != 2 {
		t.Errorf("expected one hook-context per session (2 total), got %d:\n%s", n, asked)
	}
	if !strings.Contains(string(asked), `"deja_once":true`) {
		t.Errorf("the digest payload does not ask for one per session:\n%s", asked)
	}
	if !strings.Contains(string(asked), `"session_id":"sess-A"`) {
		t.Errorf("the digest payload names no session, and the guard is keyed on it:\n%s", asked)
	}
}
