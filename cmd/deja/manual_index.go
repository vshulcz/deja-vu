package main

import (
	"fmt"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// manualIndexLine is the line an answer carries when automatic indexing is
// off (#5385): the index it came from is only as new as the last `deja index`,
// and the reader has to know how old that is. "" in the default mode, where
// the index is refreshed before it answers.
func manualIndexLine(dir string) string {
	if !index.Manual() {
		return ""
	}
	at := index.LastUpdated(dir)
	if at.IsZero() {
		return ""
	}
	return fmt.Sprintf("deja: automatic indexing is off — answering from the index of %s (%s)", at.Local().Format("2006-01-02 15:04"), doctorAgo(time.Since(at)))
}

// manualIndexNote is the session-start sentence for an index that cannot
// answer while automatic indexing is off, or "". The digest also refuses an
// index of an older content version, which the other readers answer from.
func manualIndexNote(dir string) string {
	if err := index.ManualStatus(dir); err != nil {
		return "deja: " + err.Error()
	}
	switch {
	case !index.IsCurrentVersion(dir):
		return "deja: the index is from an older deja, and automatic indexing is off — run: deja index"
	case index.Damaged(dir):
		return "deja: the index could not be read, and automatic indexing is off — rebuild it with: deja index --rebuild"
	}
	return ""
}

// manualIndexNoteForAgent is the same for an MCP tool: only an index the
// tools cannot answer from, and worded so the agent hands the command to the
// user rather than waiting for a build that will not come.
func manualIndexNoteForAgent(dir string) string {
	if err := index.ManualStatus(dir); err != nil {
		return "deja: " + err.Error() + " — tell the user to run it."
	}
	return ""
}
