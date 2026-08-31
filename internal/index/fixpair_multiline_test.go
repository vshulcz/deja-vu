package index

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A guard rather than a fix: #2051 expected the marker to be lost with the
// rest of the record, because a pair stores only the first line. It is not —
// commandFailed reads the whole record — and this is what says so, so the
// next change to either end has to keep it true.
func TestAFailedMultiLineCommandIsNotAFix(t *testing.T) {
	now := time.Now()
	script := "python - <<'EOF'\nimport socket\nsocket.create_connection(('db', 5432))\nEOF  → exit 1"
	ms := []model.Message{
		{Role: "tool-output", Text: "connection refused on port 5432", Time: now},
		{Role: "command", Text: script, Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "done", Time: now.Add(2 * time.Minute)},
	}
	if pairs := fixPairsIn(ms, "claude:s1", "p"); len(pairs) != 0 {
		t.Errorf("a command that exited 1 was stored as the fix: %+v", pairs)
	}
}

// Skipping a heredoc is not abandoning the error: the window keeps being
// scanned, and the one-liner two records on is the pair worth having.
func TestTheCommandAfterASkippedHeredocIsStillThePair(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "connection refused on port 5432", Time: now},
		{Role: "command", Text: "python - <<'EOF'\nimport socket\nEOF", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "ok", Time: now.Add(2 * time.Minute)},
		{Role: "command", Text: "docker compose up -d db", Time: now.Add(3 * time.Minute)},
		{Role: "tool-output", Text: "db started", Time: now.Add(4 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %d: %+v", len(pairs), pairs)
	}
	if pairs[0].Command != "docker compose up -d db" {
		t.Errorf("wrong command stored: %q", pairs[0].Command)
	}
}

// The stored command is what a reader is told to run. A heredoc's first line
// is not a command — it opens one — so storing it hands the agent something
// that hangs waiting for input.
func TestAHeredocFirstLineIsNotOfferedAsTheFix(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "connection refused on port 5432", Time: now},
		{Role: "command", Text: "python - <<'EOF'\nimport socket\nsocket.create_connection(('db', 5432))\nEOF", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "ok", Time: now.Add(2 * time.Minute)},
	}
	for _, p := range fixPairsIn(ms, "claude:s1", "p") {
		if strings.Contains(p.Command, "<<") {
			t.Errorf("a heredoc opener was stored as the fix: %q", p.Command)
		}
	}
}

// A herestring is a whole command, not the opening of one.
func TestAHerestringIsStillAFix(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "connection refused on port 5432", Time: now},
		{Role: "command", Text: `psql <<< "select 1"`, Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "1", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || pairs[0].Command != `psql <<< "select 1"` {
		t.Errorf("a herestring was read as an opener: %+v", pairs)
	}
}
