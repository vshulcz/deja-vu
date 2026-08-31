package index

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The exit status a source records is appended to the whole command record,
// and a pair stores its first line — so for a heredoc or a pasted script the
// marker is left behind and what the reader is handed is a command that failed,
// offered as the one that settled the error (#2051).
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

// And what is stored carries the marker when the source recorded one, so a
// reader downstream can still tell: the pair is the whole command's story, not
// its opening line.
func TestAStoredCommandKeepsTheExitItWasJudgedOn(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "connection refused on port 5432", Time: now},
		{Role: "command", Text: "pg_isready -h db -p 5432\n  → exit 0", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "db:5432 - accepting connections", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %d", len(pairs))
	}
	if strings.Contains(pairs[0].Command, "exit") {
		t.Errorf("a successful run carried its exit status into the stored command: %q", pairs[0].Command)
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
