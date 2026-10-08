package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Junie reads its user hooks from $JUNIE_HOME/config.json and its servers from
// $JUNIE_HOME/mcp/mcp.json. Two UserPromptSubmit lines differ only in their
// subcommand and flags, and a second install has to leave each one alone
// rather than read one as the other.
func TestInstallJunieAutoWritesHooksAndServer(t *testing.T) {
	hermeticEnv(t)
	jh := filepath.Join(t.TempDir(), "junie home")
	t.Setenv("JUNIE_HOME", jh)
	cfg := filepath.Join(jh, "config.json")
	writeFileMkdir(t, cfg, `{"theme":"dark"}`)

	if _, err := captureRun(t, "install", "junie-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	var mcp struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(jh, "mcp", "mcp.json"))), &mcp); err != nil {
		t.Fatal(err)
	}
	if !entryRunsDeja(mcp.Servers["deja"]) {
		t.Errorf("mcp.json has no deja server: %+v", mcp.Servers)
	}
	got := readFile(t, cfg)
	var root struct {
		Theme string                      `json:"theme"`
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(got), &root); err != nil {
		t.Fatal(err)
	}
	if root.Theme != "dark" {
		t.Errorf("install dropped the reader's own key:\n%s", got)
	}
	for _, h := range junieHookWiring {
		if !strings.Contains(got, strings.Join(h.Args, " ")) {
			t.Errorf("no %s hook running %v:\n%s", h.Event, h.Args, got)
		}
	}
	if len(root.Hooks["UserPromptSubmit"]) != 2 || len(root.Hooks["PreToolUse"]) != 1 {
		t.Errorf("hook entries = %v", root.Hooks)
	}
	if m, _ := root.Hooks["PreToolUse"][0]["matcher"].(string); m != "Bash|Read|Edit" {
		t.Errorf("PreToolUse matcher = %q", m)
	}
	for _, ev := range []string{"SessionStart", "Stop"} {
		if _, ok := root.Hooks[ev]; ok {
			t.Errorf("%s is wired; Junie drops its output or blocks on it", ev)
		}
	}
	if _, err := os.Stat(filepath.Join(jh, "commands", "deja.md")); err != nil {
		t.Errorf("no /deja command: %v", err)
	}

	if _, err := captureRun(t, "install", "junie-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if again := readFile(t, cfg); again != got {
		t.Errorf("second install changed config.json:\n%s", again)
	}
	if _, err := captureRun(t, "uninstall", "junie-auto"); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, cfg); strings.Contains(after, "hook-") {
		t.Errorf("uninstall left deja's hooks:\n%s", after)
	}
}

// The payload's cwd is Junie's home; the command has to see the project.
func TestJunieHookHandsTheProjectAsCwd(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_JUNIE_ROOT", t.TempDir())
	payload := `{"hook_event_name":"UserPromptSubmit","session_id":"session-261001-090000-ab12","cwd":"/home/u/.junie","project_path":"/work/notes","prompt":"hi"}`
	var seen string
	cmd := func(dir string, rest []string) error {
		raw := readHookStdin()
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		seen, _ = m["cwd"].(string)
		for _, a := range rest {
			if a == "--junie" {
				t.Errorf("the command was handed --junie: %v", rest)
			}
		}
		return nil
	}
	err := withStdin([]byte(payload), func() error {
		return runHookDeferred(t.TempDir(), "hook-context", []string{"--plain", "--junie"}, cmd)
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != "/work/notes" {
		t.Fatalf("cwd the command saw = %q, want the project", seen)
	}
}

func TestWithPlainExtra(t *testing.T) {
	if got := string(withPlainExtra([]byte("own\n"), "")); got != "own\n" {
		t.Fatalf("no extra: %q", got)
	}
	if got := string(withPlainExtra(nil, "pair")); got != "pair\n" {
		t.Fatalf("extra only: %q", got)
	}
	if got := string(withPlainExtra([]byte("own\n"), "pair")); got != "pair\n\nown\n" {
		t.Fatalf("both: %q", got)
	}
}
