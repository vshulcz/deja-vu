package mark

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// scripts/demo/story.py carries the sprite as a Python literal, because that
// script draws the README's film and has no import path to Go. Its own comment
// admitted the arrangement: "checked by eye against the banner". Everything else
// that draws this animal now reads it from here, and this is what stops the one
// remaining copy from drifting off quietly.
func TestTheDemoFilmDrawsTheSameCat(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "demo", "story.py")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)CAT_BODY = \[(.*?)\n\]`).FindSubmatch(src)
	if m == nil {
		t.Fatal("story.py no longer has a CAT_BODY literal in the shape this expects")
	}
	var rows []string
	for _, line := range strings.Split(string(m[1]), "\n") {
		line = strings.TrimSpace(line)
		if q := strings.Index(line, `"`); q >= 0 {
			if end := strings.LastIndex(line, `"`); end > q {
				rows = append(rows, line[q+1:end])
			}
		}
	}

	// The film draws the ready pose: eyes open, tail up.
	want := Grid(Ready)
	if len(rows) != len(want) {
		t.Fatalf("the film's sprite has %d rows, the mark has %d", len(rows), len(want))
	}
	for r := range want {
		if rows[r] != string(want[r]) {
			t.Errorf("row %d has drifted:\n film %q\n mark %q", r, rows[r], string(want[r]))
		}
	}
}
