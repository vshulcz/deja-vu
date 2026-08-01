package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The #666 cleanup keyed off --session, so --project and --before left the
// borrowed line — a customer name, in the case that found this — sitting in
// notes.jsonl after the project was forgotten (#690).
func TestForgetByProjectClearsBorrowedTitlesAndNamesTheNotes(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	other := filepath.Join(tmp, "claude", "proj-q")
	for _, d := range []string{root, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	write := func(dir, sid, text string) {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/` + filepath.Base(dir) + `","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(root, "a1", "the payroll export for ACME-4471")
	write(root, "a2", "the payroll retry path")
	write(other, "b1", "unrelated caching work")
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "a1", "--state", "accepted", "--note", "batch the export nightly"); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The dry run has to warn about the notes too — it exists to answer "how
	// much am I about to lose", and the notes are the part worth keeping.
	// The project name is derived from the store's directory layout, so it
	// carries the platform's separator — hard-coding "proj/p" passes on unix
	// and matches nothing on Windows.
	project := projectOf(t, dir, "a1")
	dry, err := captureRun(t, "forget", "--project", project, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dry, "1 of them is a promoted note") {
		t.Errorf("dry run does not mention the note: %q", dry)
	}

	out, err := captureRun(t, "forget", "--project", project)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cleared the borrowed title") {
		t.Errorf("forget --project said %q", out)
	}
	// The count folds notes and transcripts together, so it has to say which
	// is which: the notes are what the reader deliberately kept.
	// Two raw sessions and one note: the exact split, not just the words
	// "promoted note" — the line above already contains those.
	if !strings.Contains(out, "1 of them is a promoted note") {
		t.Errorf("report does not distinguish notes: %q", out)
	}
	if !strings.Contains(out, "sessions dropped: 3") {
		t.Errorf("dropped count changed: %q", out)
	}
	b, err := os.ReadFile(filepath.Join(tmp, "notes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "ACME-4471") {
		t.Errorf("the forgotten project's first line survived on disk:\n%s", b)
	}
	if !strings.Contains(string(b), "batch the export nightly") {
		t.Errorf("the decision was destroyed:\n%s", b)
	}
}

// A project that was never promoted from must not have its notes touched.
func TestForgetLeavesOtherProjectsNotesAlone(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"a1","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"the payroll export"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(tmp, "notes.jsonl")
	keep := map[string]any{"ts": "2026-07-21T10:00:00Z", "project": "elsewhere", "text": "someone else's decision",
		"kind": "promoted", "session": "claude:zz9", "state": "accepted", "title": "a title from another project"}
	line, _ := json.Marshal(keep)
	if err := os.WriteFile(notes, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--project", projectOf(t, index.DefaultDir(), "a1")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(notes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "a title from another project") {
		t.Errorf("cleared a title belonging to a project that was not forgotten:\n%s", b)
	}
}

// projectOf reads a session's project back out of the manifest, so a test can
// name it the way this platform recorded it.
func projectOf(t *testing.T, dir, id string) string {
	t.Helper()
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.ID == id {
			return m.Project
		}
	}
	t.Fatalf("session %q not in the manifest", id)
	return ""
}
