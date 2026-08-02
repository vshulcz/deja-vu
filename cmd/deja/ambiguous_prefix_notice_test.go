package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// show learned to say when a prefix reached more than one session (#719, #859).
// promote, handoff, resume and share resolve the same way and picked in
// silence — promote records a state against whichever one it chose (#872).
func TestEveryPrefixCommandSaysWhenItPicked(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"deja-2026-08-01-hyper-service", "deja-2026-08-01-super-service"} {
		line := `{"type":"user","message":{"role":"user","content":"pool exhausted in ` + id + `"},"timestamp":"2026-08-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	elided := "deja-2026…er-service"

	for _, cmd := range []struct{ name, action string }{
		{"show", "showing"},
		{"promote", "promoting"},
		{"resume", "resuming"},
		{"share", "sharing"},
	} {
		out, err := captureRunStderr(t, cmd.name, elided)
		if err != nil {
			t.Fatalf("%s: %v", cmd.name, err)
		}
		if !strings.Contains(out, "2 sessions match") || !strings.Contains(out, cmd.action+" the most recent") {
			t.Errorf("%s picked in silence:\n%s", cmd.name, out)
		}
		if !strings.Contains(out, "the line elides") {
			t.Errorf("%s offers a prefix the reader cannot see:\n%s", cmd.name, out)
		}
	}

	// One match, no notice: the line is about a choice having been made.
	out, err := captureRunStderr(t, "show", "deja-2026-08-01-hyper-service")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sessions match") {
		t.Errorf("an unambiguous id was told it was ambiguous:\n%s", out)
	}
}
