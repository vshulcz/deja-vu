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
	got := repeatLead(s, "deploy \u202eplan\u200b now\x1b[31m red\ttab\nline")
	// The lead ends with a newline of its own; every other rune must be
	// plain text.
	body := strings.TrimSuffix(got, "\n")
	for _, r := range body {
		if unicode.IsControl(r) {
			t.Fatalf("lead carried a control rune %U: %q", r, got)
		}
	}
	for _, bad := range []rune{'\u202e', '\u200b'} {
		if strings.ContainsRune(got, bad) {
			t.Fatalf("lead carried %U: %q", bad, got)
		}
	}
	if !strings.Contains(got, "deploy") || !strings.Contains(got, "plan") {
		t.Fatalf("lead lost its readable words: %q", got)
	}
	opener := repeatOpener(s, "deploy \u202eplan\u200b now\x1b[31m red\ttab\nline \"quoted\"")
	for _, r := range strings.TrimPrefix(opener, "\n") {
		if unicode.IsControl(r) {
			t.Fatalf("opener carried a control rune %U: %q", r, opener)
		}
	}
	if !strings.Contains(opener, "deja:8f2c19ab77d") || !strings.Contains(opener, "(claude, Jan 2") || !strings.Contains(opener, "'quoted'") {
		t.Fatalf("opener lost its provenance or kept a quote that closes the line: %q", opener)
	}
}

// The opener quotes the matched line straight from a session, above the
// untrusted line, so it gets the same treatment as the repeat lead — and a
// quote inside the title must not close the quoted line early.
func TestOpenerLineStripsDisplayControls(t *testing.T) {
	s := model.Session{
		Harness: "claude",
		ID:      "8f2c19ab77d40e6b5c31",
		Updated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Title:   "deploy \u202eplan\u200b now\x1b[31m red\ttab\nline \"quoted\"",
	}
	got := openerLine(s, nil)
	// The line opens with a newline of its own; every other rune must be
	// plain text.
	for _, r := range strings.TrimPrefix(got, "\n") {
		if unicode.IsControl(r) {
			t.Fatalf("opener carried a control rune %U: %q", r, got)
		}
	}
	for _, bad := range []rune{'\u202e', '\u200b'} {
		if strings.ContainsRune(got, bad) {
			t.Fatalf("opener carried %U: %q", bad, got)
		}
	}
	if !strings.Contains(got, "deploy") || !strings.Contains(got, "'quoted'") {
		t.Fatalf("opener lost its readable words or kept a quote that closes the line: %q", got)
	}
}
