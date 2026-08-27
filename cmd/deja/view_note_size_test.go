package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The page caps a session's preview so it stays a single fast file. A note's
// body is the same kind of content in the same page and had no cap, so one
// promoted note outweighed a hundred sessions (#2100).
func TestANoteDoesNotOutweighThePage(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)

	render := func(what string) int64 {
		t.Helper()
		if _, err := captureRun(t, "index"); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "view.html")
		if _, err := captureRun(t, "view", "--no-open", "--out", out); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d bytes", what, fi.Size())
		return fi.Size()
	}
	small := render("an empty store")

	huge := strings.Repeat("the pool was exhausted. ", 40000) // about a megabyte
	line, err := json.Marshal(map[string]any{
		"ts": time.Now().UTC().Format(time.RFC3339Nano), "kind": "promoted",
		"session": "claude:s1", "state": "accepted", "project": "app",
		"title": "pool sizing", "text": huge,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	withNote := render("plus one note of a megabyte")

	// The premise: the note reached the page at all.
	if withNote <= small {
		t.Fatalf("the note did not reach the page, so this measures nothing")
	}
	// A promoted note reaches the page twice — as a note, and as the deja
	// session it is indexed as, whose preview is capped too — so the page grows
	// by two capped copies and the rows around them.
	if grew := withNote - small; grew > 3*viewPreviewBytes {
		t.Errorf("one note grew the page by %d bytes; the preview cap is %d", grew, viewPreviewBytes)
	}
}
