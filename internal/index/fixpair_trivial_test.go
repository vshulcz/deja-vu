package index

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Five changes widened what counts as a wall, so more errors now look for a
// command to pair with — and the next command after an error is usually the
// session moving on, not a remedy. Measured over the store on this machine
// after the widening: 24 walls, 12 with a pair, every one of them confirmed and
// none of them navigation. This holds the part that keeps it that way (#2448).
func TestACommandThatCannotBeARemedyIsNotStoredAsOne(t *testing.T) {
	now := time.Now()
	wall := "psql: error: connection to server at db-a, port 5432 failed: Connection refused"
	for _, cmd := range []string{
		"cd ../service",
		"ls -la internal/handler",
		"cat internal/handler/orders.go",
		"pwd",
		"git status",
	} {
		ms := []model.Message{
			{Role: roleCommand, Text: "psql -h db-a -c 'select 1'", Time: now},
			{Role: roleToolOutput, Text: wall, Time: now.Add(time.Minute)},
			{Role: roleCommand, Text: cmd, Time: now.Add(2 * time.Minute)},
			{Role: roleToolOutput, Text: "ok", Time: now.Add(3 * time.Minute)},
		}
		for _, p := range fixPairsIn(ms, "claude:s1", "work/app") {
			if strings.Contains(p.Command, cmd) {
				t.Errorf("`%s` was stored as what fixes a refused connection", cmd)
			}
		}
	}

	// And the control: a command that changes something is still stored.
	ms := []model.Message{
		{Role: roleCommand, Text: "psql -h db-a -c 'select 1'", Time: now},
		{Role: roleToolOutput, Text: wall, Time: now.Add(time.Minute)},
		{Role: roleCommand, Text: "brew services start postgresql@16", Time: now.Add(2 * time.Minute)},
		{Role: roleToolOutput, Text: "==> Successfully started postgresql@16", Time: now.Add(3 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "work/app")
	if len(pairs) == 0 {
		t.Fatalf("the command that fixed it was not stored at all")
	}
	if !strings.Contains(pairs[0].Command, "brew services start postgresql@16") {
		t.Errorf("stored %q", pairs[0].Command)
	}
}
