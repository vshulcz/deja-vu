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
	c := index.SnapshotCoverage(dir)
	if c.At.IsZero() {
		return ""
	}
	line := "deja: automatic indexing is off — answering from " + indexOfLine(c.At)
	if c.PartialOf > c.Sessions {
		line += fmt.Sprintf(", which holds only the %d most recent of %d sessions found — run deja index for the rest", c.Sessions, c.PartialOf)
	}
	return line
}

// indexOfLine is "the index of 2026-10-19 14:02 (3 minutes ago)".
func indexOfLine(at time.Time) string {
	return fmt.Sprintf("the index of %s (%s)", at.Local().Format("2006-01-02 15:04"), doctorAgo(time.Since(at)))
}

// coverageLine says, in one line, how much of the history an answer served
// from the index on disk covers while a build runs behind it (#5390): the
// early slice of an unfinished first build holds the newest sessions and is
// missing the oldest, and a refresh is missing what changed since the index
// was written.
func coverageLine(c index.Coverage) string {
	switch {
	case c.Sessions > 0 && c.PartialOf > c.Sessions:
		return fmt.Sprintf("the first index build has not finished — this answer covers only the %d most recent of %d sessions found; older ones are missing until it does", c.Sessions, c.PartialOf)
	case c.At.IsZero():
		return "answering from the index as it was — refreshing in the background"
	case c.Pending > 0 && c.Found > 0:
		return fmt.Sprintf("answering from %s, %d sessions — refreshing in the background, %d of %d transcripts are new or changed since", indexOfLine(c.At), c.Sessions, c.Pending, c.Found)
	}
	return fmt.Sprintf("answering from %s, %d sessions — refreshing in the background, newer sessions may be missing", indexOfLine(c.At), c.Sessions)
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
