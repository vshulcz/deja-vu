package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// share refuses a session whose project the exclude list covers, because the
// pattern only runs at ingest and the index may still hold it. promote wrote
// the note anyway and told the reader it now outranks the transcript in recall
// — while every reading surface filters that note out again (#2278).
func TestPromoteRefusesAnExcludedProject(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-secretproj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"sec1","timestamp":%q,"cwd":"/work/secretproj",`+
		`"message":{"role":"user","content":"the payroll export for acme-prod drops rows"}}`, at)
	if err := os.WriteFile(filepath.Join(claude, "s.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: without a pattern the promotion works.
	if _, err := captureRun(t, "promote", "sec1", "--state", "accepted"); err != nil {
		t.Fatalf("promote without an exclude pattern: %v", err)
	}

	t.Setenv("DEJA_EXCLUDE_PROJECTS", "secretproj")
	before, _ := os.ReadFile(notes)
	out, err := captureRun(t, "promote", "sec1", "--state", "rejected")
	if err == nil {
		t.Errorf("promote of an excluded project succeeded, printing %q", out)
	} else if !strings.Contains(err.Error(), "exclude") {
		t.Errorf("promote said %v — it should name the exclude list, as share does", err)
	}
	after, _ := os.ReadFile(notes)
	if len(after) != len(before) {
		t.Error("the refusal still wrote a note")
	}

	// Correcting the note written before the pattern existed is still allowed:
	// it copies nothing new out of the excluded project, and taking a mark back
	// on work you have since excluded is the cleanup someone would want.
	if _, err := captureRun(t, "promote", "deja-note-claude-sec1", "--state", "rejected"); err != nil {
		t.Errorf("correcting an existing note: %v", err)
	}
}
