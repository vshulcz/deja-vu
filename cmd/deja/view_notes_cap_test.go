package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The page bounds its transcripts and each cell's text, and carried every note
// there is — a thousand of them made it 7.6 MB, which is not the single fast
// file the cap on transcripts exists to keep (#2111).
func TestThePageCarriesTheNewestNotesAndSaysSo(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)

	body := strings.Repeat("the connection pool was exhausted while the migration held the lock. ", 90)
	var b strings.Builder
	const total = 1000
	for i := 0; i < total; i++ {
		// Oldest first in the file, so file order and date order disagree —
		// which is what makes the sort part of the fix rather than a detail.
		line, err := json.Marshal(map[string]any{
			"ts":   time.Now().Add(-time.Duration(total-i) * time.Hour).UTC().Format(time.RFC3339Nano),
			"kind": "promoted", "session": fmt.Sprintf("claude:s%04d", i),
			"state": "accepted", "project": "app",
			"title": fmt.Sprintf("decision %04d", i),
			// The marker sits below the first line, which is the note's own
			// display row and is on the page for every session (#2539); the
			// bound this measures is the one on the body.
			"text": fmt.Sprintf("decision %04d\nbody-marker-%04d ", i, i) + body,
		})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(notes, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "view.html")
	if _, err := captureRun(t, "view", "--no-open", "--out", out); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the notes reached the page.
	if !strings.Contains(string(page), "body-marker-0999") {
		t.Fatalf("the newest note's body is not on the page, so this measures nothing")
	}
	if n := len(page); n > 3<<20 {
		t.Errorf("the page is %d bytes for %d notes", n, total)
	}
	// The oldest note's row still appears among the sessions — a note is
	// indexed as one, and those stay browsable by metadata — but its body is
	// what the page carries for a note, and that is what the bound is about.
	if strings.Contains(string(page), "body-marker-0000") {
		t.Errorf("the page carries the body of the oldest of %d notes, so nothing was bounded", total)
	}
	if !strings.Contains(string(page), "most recent notes") {
		t.Errorf("the page does not say that it carries a subset of the notes")
	}
}
