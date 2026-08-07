package search

import (
	"strings"
	"testing"
)

// The commands that echo a path back — `saved <path>`, `exported to <path>`,
// doctor's store table, `no such directory: <path>` — printed it verbatim.
// A newline in the path ends deja's line and starts one of the caller's, which
// reads as deja's own output; the escape byte and U+202E came with it.
func TestSafeLineKeepsAnEchoedPathOnOneLine(t *testing.T) {
	path := "/tmp/no\u001b[31msuch\ndeja: store is clean\u202edir/c.svg"
	got := SafeLine(path)
	if strings.Contains(got, "\n") {
		t.Errorf("SafeLine left a newline, so the path can forge a line: %q", got)
	}
	for _, bad := range []string{"\u001b", "\u202e"} {
		if strings.Contains(got, bad) {
			t.Errorf("SafeLine passed %q through: %q", bad, got)
		}
	}
	if !strings.Contains(got, "/tmp/no") || !strings.Contains(got, "c.svg") {
		t.Errorf("SafeLine lost the readable part of the path: %q", got)
	}
	// An ordinary path is returned unchanged.
	if got := SafeLine("/home/me/notes.jsonl"); got != "/home/me/notes.jsonl" {
		t.Errorf("SafeLine altered an ordinary path: %q", got)
	}
}
