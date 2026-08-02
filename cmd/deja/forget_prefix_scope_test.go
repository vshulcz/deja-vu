package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The refusal has to happen before anything is written. An earlier version of
// this check ran after index.Forget and printed "matches 2 sessions" over
// tombstones it had already added — a refusal that refused nothing (#870).
func TestForgetRefusesAWideSelectorWithoutDroppingAnything(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "s10", "s11"} {
		line := `{"type":"user","message":{"role":"user","content":"pool exhausted ` + id + `"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")

	// A prefix with no exact session behind it: refused, and nothing written.
	if err := runForget(dir, []string{"--session", "s"}); err == nil {
		t.Fatal("a prefix of three sessions was not refused")
	}
	if got := index.Tombstones(); len(got) != 0 {
		t.Fatalf("the refusal still forgot: %v", got)
	}

	// An id that names a session exactly means that session, not the two ids
	// it happens to be a prefix of.
	if err := runForget(dir, []string{"--session", "s1"}); err != nil {
		t.Fatalf("exact id refused: %v", err)
	}
	got := index.Tombstones()
	if len(got) != 1 || !strings.HasSuffix(got[0], ":s1") {
		t.Fatalf("tombstones = %v, want only claude:s1", got)
	}
}
