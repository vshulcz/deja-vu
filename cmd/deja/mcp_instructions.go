package main

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
)

// mcpInstructions is returned from the MCP initialize handshake. Hosts that
// support the field put it in the system prompt, which gives us an auto-recall
// channel on harnesses that have no hooks of their own.
func mcpInstructions(dir string) string {
	var b strings.Builder
	b.WriteString("deja indexes this user's past sessions across every AI coding tool they use. ")
	b.WriteString("Call the deja tool with mode recall before debugging an error or re-implementing anything that might already exist, ")
	// Every example used to be a question, so a user stating that something of
	// theirs exists — the same signal, with a stronger presumption behind it —
	// matched none of them: "так для этого у меня и есть deja watch" got a
	// repository search and nearly a denial, while one recall answered it
	// (#4004). The last sentence keys off what the agent is about to write
	// rather than what the user meant, which is the one condition a model can
	// check without inferring intent (#4005).
	b.WriteString("whenever the user implies the work happened before (\"didn't we fix this?\", \"what was that error\"), ")
	b.WriteString("and whenever they state that something of theirs already exists that you have no record of (\"I already have X\", \"we use Y for this\"). ")
	b.WriteString("Before telling the user that something on this machine does not exist — a command, a file, a setting, a past decision — recall first.")
	if s := readWarmupStatus(dir); s != nil {
		b.WriteString(" The index is still building (")
		b.WriteString(s.progress())
		b.WriteString("); recall works now but covers more history as it finishes.")
	} else if warmupJustRequested(dir) && index.HasManifest(dir) {
		// A harness with no auto-recall wiring reads this and nothing else. A
		// rebuild that has not reported yet left it with the ordinary
		// instructions and an index this build cannot read (#879).
		b.WriteString(" The index is being rebuilt right now; recall covers more history once it finishes.")
	}
	return b.String()
}
