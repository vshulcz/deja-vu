package search

import "strings"

// dejaCallNames are the tools an agent calls to reach deja. A transcript line
// carrying one of them beside a JSON argument object is the record of a call,
// not something anyone said.
var dejaCallNames = []string{
	"deja_recall_context", "deja_recall", "deja_remember",
	"deja_blame", "deja_fix", "deja_how",
	"recall_context",
}

// withoutOwnCallLog removes the lines where a transcript recorded a call to
// deja, so a question does not match the log of that same question being asked.
//
// An agent run from inside a session writes its stdout into that session's
// transcript, tool-call lines and all, and those lines carry the queries it
// sent. Asked which wording option had been chosen, recall led with
// `⚙ deja_recall {"query":"deja-vu repository description…"}` from a real
// working session, and the agent answered with an invented phrase (#2067).
//
// The lines are removed from matching only. The fact that a call happened stays
// in the transcript, which is what `deja how` and `deja fix` are built on —
// dropping it at ingest would buy this at their cost.
//
// Deliberately narrow: deja's own tool names beside a JSON object. A general
// rule about tool logs would have to guess at every harness's formatting, and
// the echo that matters is the one deja creates for itself.
func withoutOwnCallLog(text string) string {
	if !strings.Contains(text, `{"`) {
		return text
	}
	lines := strings.Split(text, "\n")
	kept := lines[:0:0]
	for _, line := range lines {
		if isOwnCallLine(line) {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == len(lines) {
		return text
	}
	return strings.Join(kept, "\n")
}

// isOwnCallLine reports whether a line records a call to deja: one of its tool
// names, and a JSON argument object after it.
func isOwnCallLine(line string) bool {
	low := strings.ToLower(line)
	for _, name := range dejaCallNames {
		at := strings.Index(low, name)
		if at < 0 {
			continue
		}
		// The arguments follow the name — a line merely discussing the tool
		// ("recall_context returns a digest") has no object after it and is
		// ordinary prose worth keeping.
		if strings.Contains(low[at+len(name):], `{"`) {
			return true
		}
	}
	return false
}
