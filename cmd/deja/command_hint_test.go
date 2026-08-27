package main

import (
	"strings"
	"testing"
)

// The hint exists for the guess that fell through to search (#674). A guess
// with an exact answer behind it was sent somewhere else: `hook context` is the
// hyphen-free spelling of a command that exists, and the hint proposed `deja
// how`, because the candidate list drops the plumbing and `how` is what
// survives nearest to `hook` (#2115).
func TestTheHintNamesTheCommandThatWasGuessedAt(t *testing.T) {
	for _, c := range []struct {
		query string
		want  string
	}{
		{"hook context", "deja hook-context"},
		{"hook prompt --plain", "deja hook-prompt"},
		// No second word to join, so the stem is all deja has: say the
		// commands exist rather than pointing at an unrelated one.
		{"hook", "hook-"},
		// The word deja's own MCP tool is called, and the docs use it
		// throughout, so it is a fair guess at the shell form.
		{"recall pgbouncer", "deja \"pgbouncer\""},
	} {
		got := commandHint(c.query)
		if !strings.Contains(got, c.want) {
			t.Errorf("%q -> %q, want it to name %q", c.query, got, c.want)
		}
		if strings.Contains(got, "deja how") {
			t.Errorf("%q was sent to an unrelated command: %q", c.query, got)
		}
	}
}

// And the hints that already worked keep working.
func TestTheHintStillAnswersTheGuessesItAlreadyKnew(t *testing.T) {
	for _, c := range []struct {
		query, want string
	}{
		{"serch pool", "deja search"},
		{"unpromote", "promote <id> --state rejected"},
		{"unforget", "forget --unforget"},
	} {
		if got := commandHint(c.query); !strings.Contains(got, c.want) {
			t.Errorf("%q -> %q, want it to name %q", c.query, got, c.want)
		}
	}
	// A word that is a word, not a guess: no hint at all.
	for _, q := range []string{
		"pgbouncer pool timeout", "brief", "search pool",
		// Prose that starts with a stem or with a tool's name is a search, not
		// a guess at a command.
		"hook up the pool", "recall the decision we made about the pool",
	} {
		if got := commandHint(q); got != "" {
			t.Errorf("%q got a command hint it did not need: %q", q, got)
		}
	}
}
