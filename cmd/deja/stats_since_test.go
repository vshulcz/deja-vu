package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// "Recalls served" fell from 8100 to 101 when the log rotated, with nothing
// saying why (#763).
func TestStatsSaysSinceWhenTheLogHasAStart(t *testing.T) {
	var buf bytes.Buffer
	since := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	printStats(&buf, stats.Report{TotalSessions: 1, Recall: usage.Summary{Recalls: 101, Since: since}})
	if !strings.Contains(buf.String(), "Memory served    101 since "+search.DisplayDate(since)+" · 101 asked for by agents, 0 injected by hooks") {
		t.Errorf("output: %q", recallLine(buf.String()))
	}

	// No events, no period: an invented date would be worse than none. The
	// report needs a session for stats to print its body at all.
	buf.Reset()
	printStats(&buf, stats.Report{TotalSessions: 1, Recall: usage.Summary{}})
	if !strings.Contains(buf.String(), "Memory served    0\n") {
		t.Errorf("empty log: %q", recallLine(buf.String()))
	}
}

func recallLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Memory served") {
			return l
		}
	}
	return out
}
