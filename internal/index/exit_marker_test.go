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

// A token that is not a number is prose that ends like the marker, not a
// status: the sources write decimal and nothing else, and cutting the line
// there loses what the command was (#2048). So the command is kept whole, and
// both readers of the suffix agree on that.
func TestATokenThatIsNotANumberIsNotAStatus(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "connection refused on port 5432", Time: now},
		{Role: "command", Text: "pg_isready -h db  → exit 0x1", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "ok", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || pairs[0].Command != "pg_isready -h db  → exit 0x1" {
		t.Errorf("prose ending like the marker was read as a status: %+v", pairs)
	}
}
