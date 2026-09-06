package main

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
)

// repeatLead sits above the untrusted line of the agent-facing block, and it
// quotes the earlier question straight from a session — display-unsafe. A
// hostile question carrying an escape sequence, a bidi override or an
// invisible tag-block character must not ride into the agent's context intact.
func TestRepeatLeadStripsDisplayControls(t *testing.T) {
	s := model.Session{
		Harness: "claude",
		ID:      "8f2c19ab77d40e6b5c31",
		Updated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	got := repeatLead(s, "deploy ‮plan​ now\x1b[31m red\ttab\nline")
	// The lead ends with a newline of its own; every other rune must be
	// plain text.
	body := strings.TrimSuffix(got, "\n")
	for _, r := range body {
		if unicode.IsControl(r) {
			t.Fatalf("lead carried a control rune %U: %q", r, got)
		}
	}
	for _, bad := range []rune{'‮', '​'} {
		if strings.ContainsRune(got, bad) {
			t.Fatalf("lead carried %U: %q", bad, got)
		}
	}
	if !strings.Contains(got, "deploy") || !strings.Contains(got, "plan") {
		t.Fatalf("lead lost its readable words: %q", got)
	}
	if !strings.Contains(got, "deja:8f2c19ab77d") || !strings.Contains(got, " in claude;") {
		t.Fatalf("lead lost its provenance: %q", got)
	}
}
