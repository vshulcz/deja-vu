package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Grok's documented hook events are the same four deja wires for Claude Code,
// and it reads them from a file in ~/.grok/hooks. Getting the shape wrong is
// silent: the file loads, nothing fires, and recall simply never appears.
func TestGrokAutoWritesEveryHookEvent(t *testing.T) {
	hermeticEnv(t)
	r, err := installGrokAuto("/bin/deja", false)
	if err != nil || r.Action != "created" {
		t.Fatalf("install = %#v, %v", r, err)
	}
	b, err := os.ReadFile(grokHooksPath())
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("hooks file is not valid JSON: %v\n%s", err, b)
	}
	want := map[string]string{
		"SessionStart":     "hook-context",
		"UserPromptSubmit": "hook-prompt",
		"PreCompact":       "hook-precompact",
		"PreToolUse":       "hook-tool",
	}
	for event, sub := range want {
		groups, ok := root.Hooks[event]
		if !ok || len(groups) == 0 || len(groups[0].Hooks) == 0 {
			t.Fatalf("no %s hook:\n%s", event, b)
		}
		if got := groups[0].Hooks[0].Command; got == "" || groups[0].Hooks[0].Type != "command" {
			t.Fatalf("%s hook is not a command: %q\n%s", event, got, b)
		}
		if !containsSub(groups[0].Hooks[0].Command, sub) {
			t.Errorf("%s runs %q, want it to call %s", event, groups[0].Hooks[0].Command, sub)
		}
	}
	// The point-of-action hook must be scoped, or it fires on every read.
	if m := root.Hooks["PreToolUse"][0].Matcher; m == "" {
		t.Errorf("PreToolUse has no matcher, so it fires on reads too:\n%s", b)
	}

	if r, err = installGrokAuto("/bin/deja", false); err != nil || r.Action != "unchanged" {
		t.Fatalf("second install = %#v, %v", r, err)
	}
	if r, err = installGrokAuto("/bin/deja", true); err != nil || r.Action != "removed" {
		t.Fatalf("uninstall = %#v, %v", r, err)
	}
	if _, err := os.Stat(grokHooksPath()); !os.IsNotExist(err) {
		t.Fatalf("hooks file survived uninstall: %v", err)
	}
}

// grokHookEntries reads the groups grok would load for one event.
func grokHookEntries(t *testing.T, event string) []struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	} `json:"hooks"`
} {
	t.Helper()
	b, err := os.ReadFile(grokHooksPath())
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("hooks file is not valid JSON: %v\n%s", err, b)
	}
	return root.Hooks[event]
}

// Grok's docs name the session sources `startup` and `resume`; 1.0.5 sends
// `new` for a fresh session and `load` for a resumed one. deja copied the
// Claude Code matcher, which matched neither, so this hook had never once
// fired on grok — and it is the hook that starts the index warming, so grok
// was the harness where the first recall of every session waited for a
// rebuild. No matcher takes whatever source grok names next.
func TestGrokSessionHookTakesWhateverSourceGrokNames(t *testing.T) {
	hermeticEnv(t)
	if _, err := installGrokAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	groups := grokHookEntries(t, "SessionStart")
	if len(groups) == 0 {
		t.Fatal("no SessionStart hook")
	}
	if m := groups[0].Matcher; m != "" {
		t.Errorf("SessionStart still filters on %q, which grok's own sources do not match", m)
	}
}

// A matcher deja got wrong is written once and, without this, never corrected:
// every machine that had already installed deja would keep the broken one.
func TestAnOldGrokMatcherIsCorrectedOnReinstall(t *testing.T) {
	hermeticEnv(t)
	stale := `{"hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[` +
		`{"type":"command","command":"/bin/deja hook-context"}]}]}}`
	if err := os.MkdirAll(filepath.Dir(grokHooksPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(grokHooksPath(), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGrokAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	groups := grokHookEntries(t, "SessionStart")
	if len(groups) != 1 {
		t.Fatalf("reinstall did not take over the existing entry: %d groups", len(groups))
	}
	if m := groups[0].Matcher; m != "" {
		t.Errorf("the stale matcher survived a reinstall: %q", m)
	}
}

// The matcher covers every hook in its entry, so one deja shares with someone
// else's hook is not deja's to rewrite.
func TestASharedEntryKeepsItsMatcher(t *testing.T) {
	entry := map[string]any{"matcher": "startup|resume"}
	hooks := []any{
		map[string]any{"type": "command", "command": "/bin/deja hook-context"},
		map[string]any{"type": "command", "command": "/usr/local/bin/notify"},
	}
	adoptMatcher(entry, hooks, "")
	if entry["matcher"] != "startup|resume" {
		t.Errorf("a matcher shared with another hook was rewritten: %v", entry["matcher"])
	}
	adoptMatcher(entry, hooks[:1], "")
	if _, ok := entry["matcher"]; ok {
		t.Errorf("deja's own entry kept a matcher this build no longer wires: %v", entry["matcher"])
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
