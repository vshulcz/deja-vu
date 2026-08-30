package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func cursorHooksFile(t *testing.T, home string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".cursor", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v", err)
	}
	return root
}

func cursorCommands(t *testing.T, root map[string]any, event string) []string {
	t.Helper()
	hooks, _ := root["hooks"].(map[string]any)
	entries, _ := hooks[event].([]any)
	var out []string
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if c, ok := m["command"].(string); ok {
			out = append(out, c)
		}
	}
	return out
}

func TestInstallCursorHooksWritesSessionStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, ".cursor"))
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installCursorHooks("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	root := cursorHooksFile(t, home)
	// Cursor's own event names, not Claude's — the file is read by cursor.
	if got := cursorCommands(t, root, "sessionStart"); len(got) != 1 || got[0] != "/bin/deja hook-context" {
		t.Fatalf("sessionStart = %v", got)
	}
	if got := cursorCommands(t, root, "beforeSubmitPrompt"); len(got) != 1 {
		t.Fatalf("beforeSubmitPrompt = %v", got)
	}
	if root["version"] != float64(1) {
		t.Fatalf("version = %v, want 1", root["version"])
	}
}

func TestInstallCursorHooksIsIdempotentAndReversible(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, ".cursor"))
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A hook the user already had; uninstall must not touch it.
	seed := `{"version":1,"hooks":{"sessionStart":[{"command":"/usr/bin/mine"}]}}`
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := installCursorHooks("/bin/deja", false); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	if got := cursorCommands(t, cursorHooksFile(t, home), "sessionStart"); len(got) != 2 {
		t.Fatalf("repeated installs duplicated entries: %v", got)
	}
	if _, err := installCursorHooks("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	root := cursorHooksFile(t, home)
	got := cursorCommands(t, root, "sessionStart")
	if len(got) != 1 || got[0] != "/usr/bin/mine" {
		t.Fatalf("uninstall did not leave the user's hook alone: %v", got)
	}
	if c := cursorCommands(t, root, "beforeSubmitPrompt"); len(c) != 0 {
		t.Fatalf("beforeSubmitPrompt survived uninstall: %v", c)
	}
}

// The binary moves and the repair reinstalls cursor. An entry deja wrote from
// the old path is deja's, not a stranger's: keeping it leaves cursor running a
// binary that is gone on every session start and every prompt, and one more
// entry accumulates with every move (#2691).
func TestInstallCursorHooksRewritesItsOwnEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, ".cursor"))
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	// One hook of the reader's own, and one they built around deja: neither is
	// deja's line to rewrite.
	seed := `{"version":1,"hooks":{"sessionStart":[` +
		`{"command":"/usr/bin/mine"},` +
		`{"command":"/old/bin/deja hook-context"},` +
		`{"command":"cd /tmp && /old/bin/deja hook-context | tee /tmp/log"}` +
		`]}}`
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installCursorHooks("/new/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	got := cursorCommands(t, cursorHooksFile(t, home), "sessionStart")
	want := []string{
		"/usr/bin/mine",
		"/new/bin/deja hook-context",
		"cd /tmp && /old/bin/deja hook-context | tee /tmp/log",
	}
	if len(got) != len(want) {
		t.Fatalf("sessionStart = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sessionStart[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// And uninstall takes back the line deja wrote from wherever it was
	// written, leaving the reader's two alone.
	if _, err := installCursorHooks("/newer/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got = cursorCommands(t, cursorHooksFile(t, home), "sessionStart")
	want = []string{"/usr/bin/mine", "cd /tmp && /old/bin/deja hook-context | tee /tmp/log"}
	if len(got) != len(want) {
		t.Fatalf("after uninstall sessionStart = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("after uninstall sessionStart[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Two shapes the rewrite has to answer for beyond the plain one: a file whose
// only entry is a wrapper the reader built, and a file that already collected
// several of deja's own lines from several paths (#2691).
func TestInstallCursorHooksLeavesOneEntryBehind(t *testing.T) {
	seedCursor := func(t *testing.T, entries string) string {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CURSOR_CONFIG_DIR", filepath.Join(home, ".cursor"))
		if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
			t.Fatal(err)
		}
		seed := `{"version":1,"hooks":{"sessionStart":[` + entries + `]}}`
		if err := os.WriteFile(filepath.Join(home, ".cursor", "hooks.json"), []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		return home
	}

	// The wrapper already runs the hook. Adding deja's line beside it runs it
	// twice per session.
	wrapper := `{"command":"cd /tmp && /old/bin/deja hook-context | tee /tmp/log"}`
	home := seedCursor(t, wrapper)
	if _, err := installCursorHooks("/new/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	got := cursorCommands(t, cursorHooksFile(t, home), "sessionStart")
	if len(got) != 1 || got[0] != "cd /tmp && /old/bin/deja hook-context | tee /tmp/log" {
		t.Errorf("a wrapper got deja's own line beside it: %v", got)
	}

	// And the file #2691 was written for: the stale entries go, one line for
	// this binary stays.
	home = seedCursor(t, `{"command":"/a/deja hook-context"},{"command":"/b/deja hook-context"}`)
	if _, err := installCursorHooks("/new/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	got = cursorCommands(t, cursorHooksFile(t, home), "sessionStart")
	if len(got) != 1 || got[0] != "/new/bin/deja hook-context" {
		t.Errorf("the entries deja left behind were not collected: %v", got)
	}
}
