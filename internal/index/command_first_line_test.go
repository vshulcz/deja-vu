package index

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A command record holds the invocation on its first line, and the listing is
// what keeps it to one row. The claude parser refuses multi-line commands
// outright, but it is not the only parser writing these records — codex,
// cline, cursor and kimi write them without that rule — so this guard is the
// only thing between a heredoc and the `deja how` listing.
//
// Measured by removing it: with firstTextLine returning its argument whole, no
// test in this package failed. This is that test.
func TestCommandListingKeepsOneCommandToOneRow(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	// Two sessions run the same invocation with different heredoc bodies —
	// what a reader means by "the same command", and two sessions is what the
	// listing needs before it remembers one at all.
	session := func(id, body string) model.Session {
		return model.Session{
			Harness: "codex", ID: id, Project: "work",
			Messages: []model.Message{{
				Role: roleCommand,
				Text: "$ psql -c \"select count(*)\n  from orders\n  where state = '" + body + "'\"",
				Time: at,
			}},
		}
	}
	buildCommands(dir, []model.Session{session("c1", "stuck"), session("c2", "late")})

	uses := ReadCommands(dir)
	if len(uses) != 1 {
		t.Fatalf("the listing holds %d rows, want 1: %+v", len(uses), uses)
	}
	u := uses[0]
	if strings.Contains(u.Command, "\n") {
		t.Errorf("a row spans lines: %q", u.Command)
	}
	if u.Command != `$ psql -c "select count(*)` {
		t.Errorf("row command = %q", u.Command)
	}
	if u.Sessions != 2 || u.Runs != 2 {
		t.Errorf("two runs of one invocation counted as %d runs in %d sessions", u.Runs, u.Sessions)
	}
}

// A single-line command is untouched, and two genuinely different invocations
// stay two rows — the guard must not merge what only shares a first word.
func TestCommandListingKeepsDifferentInvocationsApart(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	msg := func(text string) model.Message {
		return model.Message{Role: roleCommand, Text: text, Time: at}
	}
	var ss []model.Session
	for _, id := range []string{"d1", "d2"} {
		ss = append(ss, model.Session{
			Harness: "codex", ID: id, Project: "work",
			Messages: []model.Message{msg("$ go test ./..."), msg("$ go build ./cmd/deja")},
		})
	}
	buildCommands(dir, ss)

	uses := ReadCommands(dir)
	if len(uses) != 2 {
		t.Fatalf("the listing holds %d rows, want 2: %+v", len(uses), uses)
	}
	for _, u := range uses {
		if strings.Contains(u.Command, "\n") {
			t.Errorf("a row spans lines: %q", u.Command)
		}
		if u.Sessions != 2 {
			t.Errorf("%q counted %d sessions, want 2", u.Command, u.Sessions)
		}
	}
}
