package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// A promoted note is filed under its own id, so the lifecycle state — a
// property of the decision, not of the transcript — never reached it. Recall
// shows the lines that matched, and a rejection whose words are not in the
// query never appeared: the agent was handed the accepted line of a decision
// that had been taken back (#974).
func TestAPromotedNoteCarriesItsOwnState(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"decision about the pool cap"},"timestamp":"2026-07-12T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "s1", "--state", "rejected", "--note", "not after all"); err != nil {
		t.Fatal(err)
	}

	hits := []search.Hit{
		{Session: model.Session{Harness: "deja", ID: "deja-note-claude-s1"}},
		{Session: model.Session{Harness: "claude", ID: "s1"}},
	}
	attachLifecycles(hits)
	if hits[0].Lifecycle != "rejected" {
		t.Errorf("the note carries lifecycle %q, want rejected", hits[0].Lifecycle)
	}
	if hits[1].Lifecycle != "rejected" {
		t.Errorf("the transcript stopped carrying its state: %q", hits[1].Lifecycle)
	}
	if !strings.Contains(lifecycleLine(hits[0]), "tried and rejected") {
		t.Errorf("the note's line does not say what happened: %q", lifecycleLine(hits[0]))
	}
	// An ordinary session with no decision on it stays unmarked.
	other := []search.Hit{{Session: model.Session{Harness: "claude", ID: "unrelated"}}}
	attachLifecycles(other)
	if other[0].Lifecycle != "" {
		t.Errorf("an unrelated session was marked %q", other[0].Lifecycle)
	}
}
