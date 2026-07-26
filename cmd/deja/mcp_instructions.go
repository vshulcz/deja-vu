package main

import "strings"

// mcpInstructions is returned from the MCP initialize handshake. Hosts that
// support the field put it in the system prompt, which gives us an auto-recall
// channel on harnesses that have no hooks of their own.
func mcpInstructions(dir string) string {
	var b strings.Builder
	b.WriteString("deja indexes this user's past sessions across every AI coding tool they use. ")
	b.WriteString("Call the recall tool before debugging an error or re-implementing anything that might already exist, ")
	b.WriteString("and whenever the user implies the work happened before (\"didn't we fix this?\", \"what was that error\").")
	if s := readWarmupStatus(dir); s != nil {
		b.WriteString(" The index is still building (")
		b.WriteString(s.progress())
		b.WriteString("); recall works now but covers more history as it finishes.")
	}
	return b.String()
}
