package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// notesPayload returns the page's notes array as it was written into the
// script, before any JSON decoding — the notes are their own array beside the
// sessions, and reading the whole page instead would be satisfied by the same
// text appearing in a session preview.
func notesPayload(t *testing.T, page string) string {
	t.Helper()
	const marker = ",N="
	i := strings.Index(page, marker)
	if i < 0 {
		t.Fatalf("the view page no longer embeds a notes array; this test reads the wrong thing now")
	}
	rest := page[i+len(marker):]
	if j := strings.Index(rest, "];"); j >= 0 {
		return rest[:j+1]
	}
	return rest
}

// The page carries promoted notes in their own array, and a note's project,
// title, text and tags are all text the user typed. Session text has been
// pinned against closing the page's script block since the view landed; the
// notes array had no such test, and notes written before #1811 can still hold
// a tag with anything in it (#1817).
func TestNoteTagsCannotCloseThePagesScript(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"v1","cwd":"/proj","timestamp":"2026-08-22T01:00:00Z","message":{"role":"user","content":"the retry budget question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}

	// A promoted note as an older deja would have stored it: tags uncleaned.
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	body, err := json.Marshal(map[string]any{
		"ts": "2026-08-20T10:00:00Z", "project": "proj", "kind": "promoted",
		"session": "sess1", "state": "accepted", "title": "retries",
		"text": "we cap retries at three",
		"tags": []string{"ok", "red\x1b[31malert\x1b[0m", "</script><script>alert(1)</script>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	path, _, err := writeViewHTML(dir, filepath.Join(tmp, "view.html"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	notesJSON := notesPayload(t, string(page))

	if strings.Contains(notesJSON, "</script>") {
		t.Errorf("a note's tag closed the page's script block:\n%s", notesJSON)
	}
	// The control names the escaped form the page actually writes: looking for
	// "</script>" anywhere on the page would be satisfied by the template's own
	// closing tag, and looking at the whole page would be satisfied by the same
	// tag appearing in a session preview.
	if !strings.Contains(notesJSON, `alert(1)`) {
		t.Errorf("the hostile tag never reached the notes array, so the assertion above proves nothing:\n%s", notesJSON)
	}
	if !strings.Contains(notesJSON, `"ok"`) {
		t.Errorf("the ordinary tag beside it is missing too:\n%s", notesJSON)
	}
}
