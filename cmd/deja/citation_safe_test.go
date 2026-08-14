package main

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
)

// citationLine is appended to the same agent-facing block as the recall digest,
// but its title comes straight from a session — display-unsafe. A hostile title
// carrying an escape sequence, a bidi override or an invisible tag-block
// character must not ride into the agent's context intact.
func TestCitationLineStripsDisplayControls(t *testing.T) {
	s := model.Session{
		Harness: "claude",
		Title:   "deploy \u202eplan\u200b now\x1b[31m red\ttab\nline",
		Updated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	got := citationLine(s, nil)
	// The function opens with a newline of its own; every other rune must be
	// plain text.
	body := strings.TrimPrefix(got, "\n")
	for _, r := range body {
		if unicode.IsControl(r) {
			t.Fatalf("citation carried a control rune %U: %q", r, got)
		}
	}
	for _, bad := range []rune{'\u202e', '\u200b'} {
		if strings.ContainsRune(got, bad) {
			t.Fatalf("citation carried %U: %q", bad, got)
		}
	}
	if !strings.Contains(got, "deploy") || !strings.Contains(got, "plan") {
		t.Fatalf("citation lost its readable words: %q", got)
	}
}
