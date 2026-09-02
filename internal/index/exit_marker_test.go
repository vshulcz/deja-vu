package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The exit status is written as a suffix — two spaces, the marker, the digits,
// end of record — and it was read anywhere in the record, so a command whose
// own text carries a line ending that way was taken for a command that failed
// and dropped from the pairs (#2820, the trap #2048 recorded for the hook's
// reading of the same suffix).
func TestTheMarkerCountsOnlyWhereItIsWritten(t *testing.T) {
	now := time.Now()
	script := "docker compose run --rm db sh -c 'psql\n  → exit 1\nselect 1'"
	ms := []model.Message{
		{Role: "tool-output", Text: "connection refused on port 5432", Time: now},
		{Role: "command", Text: script, Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "db is up", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 {
		t.Fatalf("a command carrying the marker in its own text was read as a failed one: %+v", pairs)
	}
}

// The parser is what the three readers share, so the decisions live here: a
// token that is not a number is prose that ends like the marker rather than a
// status (#2048), a status is read through trailing whitespace, and it counts
// only where a source writes it — at the end of the record, after the last
// line of a multi-line script (internal/sources/codex.go).
func TestTheExitOutcomeIsReadWhereItIsWritten(t *testing.T) {
	for _, c := range []struct {
		text     string
		cmd      string
		code     int
		recorded bool
	}{
		{"$ make test  → exit 2", "$ make test", 2, true},
		{"$ make test  → exit 0", "$ make test", 0, true},
		{"$ make test  → exit 2\n", "$ make test", 2, true},
		// Prose that ends like the marker: kept whole.
		{"pg_isready -h db  → exit 0x1", "pg_isready -h db  → exit 0x1", 0, false},
		{`echo "  → exit later"`, `echo "  → exit later"`, 0, false},
		// Not where it is written: a line of the command's own text.
		{"run.sh\n  → exit 1\nmore", "run.sh\n  → exit 1\nmore", 0, false},
		// Where it is written, for a script: after the last line.
		{"python - <<'EOF'\nprint(1)\nEOF  → exit 1", "python - <<'EOF'\nprint(1)\nEOF", 1, true},
		{"go build ./...", "go build ./...", 0, false},
	} {
		cmd, code, recorded := CommandExitOutcome(c.text)
		if cmd != c.cmd || code != c.code || recorded != c.recorded {
			t.Errorf("CommandExitOutcome(%q) = %q, %d, %v; want %q, %d, %v",
				c.text, cmd, code, recorded, c.cmd, c.code, c.recorded)
		}
		// And the two older readers say the same thing, because they are this
		// one.
		if got := withoutExitStatus(c.text); got != cmd {
			t.Errorf("withoutExitStatus(%q) = %q, want %q", c.text, got, cmd)
		}
		if got, ok := CommandExitStatus(c.text); got != code || ok != recorded {
			t.Errorf("CommandExitStatus(%q) = %d, %v; want %d, %v", c.text, got, ok, code, recorded)
		}
	}
}
