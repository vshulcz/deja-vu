package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A note remembered in the project is newer than the agent's session, and
// handoff with no id picked it: the note went out as the session to continue.
func TestHandoffWithNoIdSkipsRememberedNotes(t *testing.T) {
	tmp := hermeticEnv(t)
	work := filepath.Join(tmp, "proj")
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"wire the exporter into the ticker loop"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"agent1","cwd":"` + filepath.ToSlash(work) + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "agent1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	back, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(back) })
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "remember", "exporter frames must be a slice"); err != nil {
		t.Fatal(err)
	}
	s, err := handoffSource(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Harness != "claude" || s.ID != "agent1" {
		t.Fatalf("handoff picked %s:%s, want the agent session claude:agent1", s.Harness, s.ID)
	}
}
