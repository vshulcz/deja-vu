package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WorkBuddy runs CodeBuddy's agent with CODEBUDDY_CONFIG_DIR set to its own
// home, so wiring ~/.codebuddy never reaches it. `deja install workbuddy-auto`
// writes the hooks and the server into the WorkBuddy home on disk, and the
// server into the app's own mcp.json: the agent reads only the first of
// .mcp.json and mcp.json, so a new .mcp.json would hide every server added in
// the app.
func TestInstallWorkBuddyAuto(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	wb := filepath.Join(home, ".workbuddy-ai")
	if err := os.MkdirAll(filepath.Join(wb, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(wb, "settings.json")
	own := []byte(`{"proxy":{"mode":"system"}}`)
	if err := os.WriteFile(settings, own, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installTarget("workbuddy-auto", "/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	root := readCodeBuddyJSON(t, settings)
	hooks, _ := root["hooks"].(map[string]any)
	want := map[string]int{}
	for _, w := range codeBuddyHookWiring {
		want[w.Event]++
	}
	for event, n := range want {
		if entries, _ := hooks[event].([]any); len(entries) != n {
			t.Fatalf("%s: %d entries, want %d", event, len(entries), n)
		}
	}
	if root["proxy"] == nil {
		t.Fatalf("the app's settings were not kept: %v", root)
	}
	servers, _ := readCodeBuddyJSON(t, filepath.Join(wb, "mcp.json"))["mcpServers"].(map[string]any)
	if servers["deja"] == nil {
		t.Fatalf("mcp.json servers = %v", servers)
	}
	for _, p := range []string{filepath.Join(wb, ".mcp.json"), filepath.Join(home, ".codebuddy")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("workbuddy-auto wrote %s", p)
		}
	}

	if _, err := installTarget("workbuddy-auto", "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(settings); strings.Contains(string(b), "hook-context") {
		t.Fatalf("after uninstall settings = %s", b)
	}
}

// An existing .mcp.json is the file the agent reads, so the entry goes there.
func TestInstallWorkBuddyKeepsTheFileTheAgentReads(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	wb := filepath.Join(home, ".workbuddy")
	if err := os.MkdirAll(wb, 0o755); err != nil {
		t.Fatal(err)
	}
	dot := filepath.Join(wb, ".mcp.json")
	if err := os.WriteFile(dot, []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := installTarget("workbuddy", "/bin/deja", false)
	if err != nil || r.Path != dot {
		t.Fatalf("install = %q, %v; want %s", r.Path, err, dot)
	}
}

// --auto finds WorkBuddy by a session store of either edition.
func TestWorkBuddyDetectedByItsStore(t *testing.T) {
	home := filepath.Join(hermeticEnv(t), "home")
	has := func() bool {
		for _, n := range existingTargets() {
			if n == "workbuddy" {
				return true
			}
		}
		return false
	}
	if has() {
		t.Fatal("a machine without WorkBuddy counted as one")
	}
	if err := os.MkdirAll(filepath.Join(home, ".workbuddy-ai", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !has() || autoTargetFor("workbuddy") != "workbuddy-auto" {
		t.Fatal("~/.workbuddy-ai/projects did not count as a WorkBuddy machine")
	}
}
