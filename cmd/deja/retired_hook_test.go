package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// hookNames is written out because the dispatch table cannot be read from
// install.go without an initialisation cycle. This is what stops the two from
// drifting: a hook added or retired without touching the list fails here.
func TestHookNamesMatchTheDispatchTable(t *testing.T) {
	for name := range commands {
		if !strings.HasPrefix(name, "hook-") {
			continue
		}
		if !hookNames[name] {
			t.Errorf("%s is a hook this build has and hookNames does not list it", name)
		}
	}
	for name := range hookNames {
		if _, ok := commands[name]; !ok {
			t.Errorf("hookNames lists %s, which this build does not have", name)
		}
	}
}

// A line deja wrote under a hook name that has since gone sat beside the new
// one and ran on every session start, doing nothing but costing a process.
func TestInstallRemovesItsOwnRetiredHookLine(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"hooks":{"SessionStart":[{"hooks":[` +
		`{"type":"command","command":"/old/bin/deja hook-session"},` +
		`{"type":"command","command":"/usr/bin/env myown-wrapper deja hook-session"}` +
		`]}]}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := captureRun(t, "install", "claude-auto", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "/old/bin/deja hook-session") {
		t.Errorf("deja's own retired hook line survived the repair:\n%s", got)
	}
	// Somebody else's wrapper is theirs, whatever it calls.
	if !strings.Contains(got, "myown-wrapper") {
		t.Errorf("a line deja did not write was removed:\n%s", got)
	}
	// And the repair still wired what this build does have.
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "hook-context") {
		t.Errorf("the current session-start hook was not written:\n%s", got)
	}
}
