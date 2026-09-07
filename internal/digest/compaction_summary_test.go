package digest

import "testing"

// A compaction summary is the agent's own account of the session, written into
// the transcript as the first user turn after a compaction. Taken as something
// the user said, it becomes the session's title, and the "you have been here"
// line then reads `"Summary: 1. Primary Request and Intent: - MOST R…"` — which
// says nothing about the session and pushes the real question out (#3157).
func TestCompactionSummariesAreArtifacts(t *testing.T) {
	summary := "Summary: 1. Primary Request and Intent:\n" +
		"   The user asked for the retry queue to stop double-acking.\n" +
		"2. Key Technical Concepts:\n   - advisory locks\n"
	if !IsAgentArtifact(summary) {
		t.Error("a compaction summary was taken as something the user said")
	}
	resumed := "This session is being continued from a previous conversation that ran out of context. " +
		"The summary below covers the earlier portion of the conversation."
	if !IsAgentArtifact(resumed) {
		t.Error("the resumption preamble was taken as something the user said")
	}

	// The control the issue names: an ordinary message that opens with the same
	// word and carries none of the shape is still a title.
	for _, real := range []string{
		"Summary: the retry queue double-acks under two shards",
		"Summary: I moved the lock into the outbox writer, tests are green",
		"summary of what we decided about the shipping queue",
	} {
		if IsAgentArtifact(real) {
			t.Errorf("a real message was discarded as an artifact: %q", real)
		}
	}
}
