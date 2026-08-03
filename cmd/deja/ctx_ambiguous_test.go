package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An id copied off a result line is elided in the middle, so it can reach more
// than one session. show, share, resume, promote, forget and handoff all say
// so; ctx — the one an agent is told to call — answered from one of them in
// silence (#923).
func TestCtxSaysWhenAnIdReachedMoreThanOneSession(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, ts string) string {
		return `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"` + ts + `","sessionId":"` + sid + `","cwd":"/proj"}` + "\n"
	}
	for _, s := range []struct{ id, ts string }{
		{"a1b2c3d4-1111-4000-8000-d6e7f8a9b0c1", "2026-07-01T10:00:00Z"},
		{"a1b2c3d4-2222-4000-8000-d6e7f8a9b0c1", "2026-07-02T10:00:00Z"},
	} {
		if err := os.WriteFile(filepath.Join(store, s.id+".jsonl"), []byte(line(s.id, s.ts)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	note, err := captureRunStderr(t, "ctx", "a1b2c3d4…d6e7f8a9b0c1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "2 sessions match") || !strings.Contains(note, "deja last") {
		t.Errorf("ctx answered from one of two without saying so: %q", note)
	}

	// An id that reaches exactly one session is not a choice, and says nothing.
	note, err = captureRunStderr(t, "ctx", "a1b2c3d4-1111-4000-8000-d6e7f8a9b0c1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "sessions match") {
		t.Errorf("an unambiguous id was called a choice: %q", note)
	}
}
