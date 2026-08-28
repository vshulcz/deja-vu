package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A promoted note's title is the session's own opening line — the question —
// and the decision is what the person wrote. The Notes tab led each row with
// the badge and the title, so the page read "accepted: should we move the
// reports to a redis cache", which is the misread #2455 fixed for agents,
// still on the page a person passes around (#2460).
func TestTheNotesTabLeadsWithTheDecision(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := `{"type":"user","sessionId":"dec","timestamp":"` + at + `","cwd":"/work/app",` +
		`"message":{"role":"user","content":"should we move the reports to a redis cache"}}`
	if err := os.WriteFile(filepath.Join(store, "dec.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	rec := `{"ts":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","project":"work/app","kind":"promoted",` +
		`"session":"claude:dec","state":"accepted","title":"should we move the reports to a redis cache",` +
		`"text":"no redis in front of the reports: they are materialised"}`
	if err := os.WriteFile(notes, []byte(rec+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "page.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	m := regexp.MustCompile(`(?s)const S=(.*?),R=(.*?),N=(.*?);\n`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page's script no longer starts with the three arrays")
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(m[3]), &rows); err != nil {
		t.Fatalf("the notes array does not decode: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want one note on the page, got %d", len(rows))
	}
	// Both halves reach the page: the decision and the question it answers.
	title, _ := rows[0]["title"].(string)
	text, _ := rows[0]["text"].(string)
	if !strings.Contains(text, "no redis in front of the reports") {
		t.Errorf("the decision is not on the page: %q / %q", title, text)
	}
	if !strings.Contains(title, "should we move the reports") {
		t.Errorf("the question is not on the page: %q / %q", title, text)
	}
	// And the row is built decision-first. The page renders itself, so this is
	// where a Go test can hold it: the headline takes the decision and falls
	// back to the title only when a note has no text of its own.
	row := regexp.MustCompile(`function rowN\(n\)\{([^\n]*)`).FindStringSubmatch(page)
	if row == nil {
		t.Fatalf("the page no longer builds its note rows in rowN")
	}
	if !strings.Contains(row[1], "n.text||n.title") {
		t.Errorf("the note row does not lead with the decision:\n%s", row[1])
	}
	head := strings.Index(row[1], "class=\"t\"")
	body := strings.Index(row[1], "<pre")
	if head < 0 || body < 0 || head > body {
		t.Errorf("the row no longer puts a headline above a body:\n%s", row[1])
	}
}
