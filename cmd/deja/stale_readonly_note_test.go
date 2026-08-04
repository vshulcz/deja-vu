package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An index that is behind and cannot be written stays behind. search names the
// state; the hook told the agent it "starts already knowing" a picture missing
// today's work, and MCP answered a confident negative about work that is on
// disk (#1005).
func TestTheAgentIsToldWhenTheIndexCannotCatchUp(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(tmp, "locked")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// Current and writable: nothing to say, on either surface.
	if note := staleReadOnlyNote(dir); note != "" {
		t.Errorf("a healthy index produced a warning: %q", note)
	}
	if got := emptyRecallAnswer(dir, "nothing"); !strings.Contains(got, "No prior deja sessions matched") {
		t.Errorf("the ordinary empty answer changed: %q", got)
	}

	// A session deja cannot index, and nowhere to write the result.
	newer := `{"type":"user","message":{"role":"user","content":"a session added after the parent was locked"},"timestamp":"2026-08-04T12:00:00Z","sessionId":"new","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "new.jsonl"), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	note := staleReadOnlyNote(dir)
	if !strings.Contains(note, "cannot be updated") {
		t.Errorf("the hook says nothing about an index that cannot catch up: %q", note)
	}
	if !strings.Contains(note, parent) {
		t.Errorf("the note does not name what to change: %q", note)
	}
	got := emptyRecallAnswer(dir, "third session")
	if strings.Contains(got, "No prior deja sessions matched") {
		t.Errorf("MCP answers a confident negative over an index that cannot catch up: %q", got)
	}
	if !strings.Contains(got, "may be missing") {
		t.Errorf("the MCP answer does not say what it might be missing: %q", got)
	}

	// Writable again — even while stale — is the ordinary case: the build will
	// happen on the next command, so neither surface warns.
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if note := staleReadOnlyNote(dir); note != "" {
		t.Errorf("a stale but writable index warned anyway: %q", note)
	}
}
