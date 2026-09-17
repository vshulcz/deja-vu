package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The path scan skips a path with a space on purpose: the boundary that ends
// every other path ends that one in the middle. A quoted span has ends that
// can be read exactly, and since #3692 deja quotes its own hook lines where
// the shell needs it — so the file deja wrote is one doctor can answer for.
func TestDoctorNamesAMissingBinaryInsideAQuotedPath(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, "a path with spaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "deja-hook")
	path := filepath.Join(home, "hooks.json")
	b, err := json.Marshal(map[string]any{"hooks": map[string]any{
		"UserPromptSubmit": []any{map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": hookRun(gone, "hook-prompt")}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dejaHookCommandMissing(path); got != gone {
		t.Errorf("the missing binary inside a quoted path was not named: %q", got)
	}
	// And a binary that is there is not reported.
	if err := os.WriteFile(gone, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := dejaHookCommandMissing(path); got != "" {
		t.Errorf("a hook whose binary is there was called broken: %q", got)
	}
}

// A quoted path that is not deja's own is not this check's business: the
// subcommand beside it is what says the line is deja's.
func TestDoctorLeavesSomebodyElsesQuotedCommandAlone(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, "hooks.json")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"'/opt/their tool/bin/run' --watch"}]}]}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dejaHookCommandMissing(path); got != "" {
		t.Errorf("a command of somebody else's was reported as deja's: %q", got)
	}
	if strings.Contains(body, "deja") {
		t.Fatal("the fixture names deja, which is not the case under test")
	}
}
