package digest

import "testing"

func TestCompactionSummaryIsNotAPersonsLine(t *testing.T) {
	yes := []string{
		"Summary:\n1. Primary Request and Intent:\n   - MOST RECENT (active task): 'ты сам отревьювь строго'",
		"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion.",
	}
	no := []string{
		"Summary: the migration locked the table, we rolled it back",
		"Summary: what is the Primary Request and Intent here, in one line?",
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

// What Claude Code writes into the transcript when a hook returns a status
// line is the hook's, not the person's — and deja's own block coming back
// is plumbing for titles too (#3168).
func TestHookStatusLinesAreTheHosts(t *testing.T) {
	yes := []string{
		"UserPromptSubmit says: deja-vu — you have been here: \"what did we decide\" (today · via: x)",
		"SessionStart:compact says: deja recalled 3 prior sessions touching doctor.go",
		"⎿ SessionStart:startup says: deja: recalled 3 prior sessions from this project",
		"<deja-recall>\nRecalled history from prior sessions.\n- **app** `x`\n</deja-recall>",
	}
	no := []string{
		"the changelog says: nothing about hooks, so where is it documented?",
		"Vlad says: ship it without the retry",
		"Codex says: the lock is held by the daemon, is that right?",
		"Says who? the test passes locally",
	}
	for _, s := range yes {
		if !IsPlumbing(s) || !IsAgentArtifact(s) {
			t.Errorf("the host's line taken for a person's: %q", s[:40])
		}
	}
	for _, s := range no {
		if IsHookStatusLine(s) {
			t.Errorf("a person's line taken for a hook status: %q", s)
		}
	}
}
