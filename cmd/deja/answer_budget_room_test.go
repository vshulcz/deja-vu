package main

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// Every answer deja snapshots lands in one injection log, and that log holds a
// record of `usage.RecordRoom` before it starts rotating on every write — at
// which point half of two concurrent injections is rewritten away (#1971). The
// budgets that decide a record's size live here and in internal/search, which
// that package cannot import, so this is where the two meet.
//
// A budget raised past this line is not wrong on its own; what is wrong is
// raising it and leaving the log's threshold where it was.
func TestEveryAnswerFitsTheRoomTheLogHas(t *testing.T) {
	// The frame, the header and a trailing note ride along with the body: blame
	// is documented as ending up about its note's length over budget, so the
	// room has to cover more than the number itself.
	const overhead = 2048

	for _, c := range []struct {
		name   string
		budget int
	}{
		{"recall", recallMCPBudget},
		{"recall_context", contextMCPBudget},
		{"blame", blameMCPBudget},
		{"resource", search.ContextBudget},
		{"handoff", handoffBudget},
		{"deja vu", promptHookBudget},
	} {
		if c.budget+overhead > usage.RecordRoom {
			t.Errorf("%s answers in %d bytes and the log has room for %d: raise usage.snapshotRotateAt, not the budget",
				c.name, c.budget, usage.RecordRoom)
		}
	}
}
