package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A note's title is exempt from the collapse every other title gets on the way
// into the index (boundSourceTitle), and the notes file is the one store a
// person writes by hand — so a title can hold newlines, and every one-line
// surface has to flatten it itself. #2056 pinned that for the markdown export;
// the screens and the page were never asked.
//
// The lines matter beyond layout: the text below is shaped like deja's own
// export block, so a surface that printed it as written would show a state and
// a heading the note never had.
func TestANoteTitleWithNewlinesStaysOnOneLine(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	const forged = "- state: rejected"
	line, err := json.Marshal(map[string]any{
		"ts": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano), "kind": "promoted",
		"session": "claude:s1", "state": "accepted", "project": "app",
		"title": "pool timeout\n" + forged + "\n## a heading of my own",
		"text":  "the pool was exhausted while the migration held the lock",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// The surfaces that print a note's title. `search` and `show` are not
	// among them — both print the note's body, which is the answer there —
	// and the brief's own note line is left out because it needs a reuse
	// record to appear at all, so including it here would assert nothing.
	for _, args := range [][]string{
		{"last"},
		{"stats"},
	} {
		out, err := captureRun(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		// The premise: the title reached this surface at all.
		if !strings.Contains(out, "pool timeout") {
			t.Fatalf("%v does not print the note title, so it measures nothing:\n%s", args, out)
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), forged) || strings.HasPrefix(strings.TrimSpace(l), "## ") {
				t.Errorf("%v: a line of the title became a line of its own: %q", args, l)
			}
		}
	}
	// The page carries the title into HTML, where a raw newline is a line
	// break in the source rather than in the rendering — the same property,
	// checked in the JSON the page hands its script.
	page := filepath.Join(t.TempDir(), "v.html")
	if _, err := captureRun(t, "view", "--no-open", "--out", page); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "pool timeout") {
		t.Fatalf("the page does not carry the note title, so it measures nothing")
	}
	// Escaped, not raw: the page embeds the title through json.Marshal, so a
	// newline that survived is written as a backslash and an n. Asserting on a
	// raw one could never fire.
	if strings.Contains(string(b), `pool timeout\n`) {
		t.Errorf("the page carries the title's newline, escaped into its JSON")
	}
}
