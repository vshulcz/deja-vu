package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The default result cap (15) was silent: 15 hits looked like the whole
// answer and nothing pointed at --all for the rest. deja narrates every other
// place the ladder hides a session, so a capped search says so too.
func TestSearchCapNamesTheHiddenRest(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two sessions match; a --limit of 1 caps it, which is the same shape the
	// default cap makes when more than 15 match.
	for _, id := range []string{"capone", "captwo"} {
		line := `{"type":"user","message":{"role":"user","content":"capneedle here in ` + id + `"},"timestamp":"2026-08-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRunStderr(t, "--no-embed", "--limit", "1", "capneedle")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "showing 1 of 2") || !strings.Contains(out, "--all") {
		t.Errorf("capped search hid the rest silently:\n%s", out)
	}

	// --all shows everything, so there is nothing to point at.
	out, err = captureRunStderr(t, "--no-embed", "--all", "capneedle")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "showing") && strings.Contains(out, "of") {
		t.Errorf("--all should not announce a cap:\n%s", out)
	}
}
