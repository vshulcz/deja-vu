package digest

import "testing"

func TestCompactionSummaryIsNotAPersonsLine(t *testing.T) {
	yes := []string{
		"Summary:\n1. Primary Request and Intent:\n   - MOST RECENT (active task): 'ты сам отревьювь строго'",
		"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion.",
	}
	no := []string{
		"Summary: the migration locked the table, we rolled it back",
		"what did we decide about the primary request timeout?",
	}
	for _, s := range yes {
		if !IsCompactionSummary(s) || !IsAgentArtifact(s) || !IsPlumbing(s) {
			t.Errorf("not recognised as a compaction summary: %q", s[:40])
		}
	}
	for _, s := range no {
		if IsCompactionSummary(s) {
			t.Errorf("a person's line taken for a compaction summary: %q", s)
		}
	}
}
