package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A note title carries no bound into the index on purpose — the state suffix is
// what every one-line surface reads it for — so each reader clips it for its own
// layout instead. Nothing held that in place, and the comment on the exemption
// gave the wrong reason for why it was safe (#2092).
func TestEverySurfaceThatPrintsANoteTitleBoundsIt(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	// The marker sits deep inside the title, so a surface that prints it has
	// not clipped — which is the property, rather than a line-length guess.
	long := "pgbouncer pool " + strings.Repeat("verylongword ", 400) + "deepmarker [accepted]"
	// In the text as well as the title: the title is what the store holds and
	// `promote` echoes, the text is what every listing shows (#2539).
	line, err := json.Marshal(map[string]any{
		"ts": time.Now().UTC().Format(time.RFC3339Nano), "kind": "promoted",
		"session": "claude:s1", "state": "accepted", "project": "app",
		"title": long,
		"text":  "the pool was exhausted while the migration held the lock " + long,
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
	// The premise: the title really is unbounded in the index, which is what
	// makes the readers' clips the thing under test.
	stored, ok, err := index.FindByIdentity(os.Getenv("DEJA_INDEX_DIR"), "deja", "deja-note-claude-s1")
	if err != nil || !ok {
		t.Fatalf("the note is not indexed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(stored.Title, "deepmarker") {
		t.Fatalf("the stored title is already bounded, so this measures nothing: %d runes", len([]rune(stored.Title)))
	}

	// Each surface clips for its own layout, so the bound here is generous: it
	// is the difference between a row and a page of one.
	const roomForARow = 400
	// `show` and the page are not among them: both carry a note's body whole
	// on purpose — that is where a whole note belongs (#1645) — and since
	// #2539 the display line is the body's first line, so the marker is in
	// what they are supposed to print.
	for _, args := range [][]string{{"last"}, {"stats"}, {"search", "migration"}} {
		out, err := captureRun(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(out, "deepmarker") {
			t.Errorf("%v printed the whole note title, marker and all", args)
		}
		for _, l := range strings.Split(out, "\n") {
			if n := len([]rune(l)); n > roomForARow {
				t.Errorf("%v printed a %d-rune line from a note title", args, n)
			}
		}
	}
}
