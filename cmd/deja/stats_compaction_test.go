package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
)

func TestStatsRendersLocalCompactionRecoveryWithoutInventingABaseline(t *testing.T) {
	var out bytes.Buffer
	printStats(&out, stats.Report{
		TotalSessions: 1,
		Recall: usage.Summary{Compaction: &usage.CompactionSummary{
			Captures: 3, Measured: 2, Pending: 1, MedianActions: 3.5, P75Actions: 5,
		}},
	})
	got := out.String()
	for _, want := range []string{
		"After compaction 2 first edits · median 3.5 raw actions before edit · p75 5",
		"Compact samples   1 pending · 0 unmeasured",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stats missing %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"median 28", "p75 61"} {
		if strings.Contains(got, absent) {
			t.Errorf("stats invented maintainer baseline %q:\n%s", absent, got)
		}
	}
}

func TestStatsRendersUnmeasuredCompactionsWithoutSuccessMetric(t *testing.T) {
	var out bytes.Buffer
	printStats(&out, stats.Report{
		TotalSessions: 1,
		Recall: usage.Summary{Compaction: &usage.CompactionSummary{
			Captures: 2, Unmeasured: 2,
		}},
	})
	got := out.String()
	if !strings.Contains(got, "Compact samples   0 pending · 2 unmeasured") {
		t.Errorf("unmeasured state was not named:\n%s", got)
	}
	if strings.Contains(got, "After compaction") {
		t.Errorf("unmeasured state claimed a first-edit metric:\n%s", got)
	}
}
