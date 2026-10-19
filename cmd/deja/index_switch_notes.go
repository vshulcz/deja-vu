package main

import (
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// fixDataNote says why `deja fix` may have nothing, or "": a search-only index
// mined no fixes, and the parse-level switches keep out the commands and the
// tool output a fix is paired from (#5395). An empty answer from such an index
// read as "this machine never saw that error". The parse switches are read as
// set now, which is what the next pass reads under, so the note says what is
// set rather than claiming the index was built that way.
func fixDataNote(dir string) string {
	switch {
	case index.BuiltSearchOnly(dir):
		return "this index was built search-only (" + index.SearchOnlyEnv + " or \"search_only\" in policy.json), so no fixes were mined — turn it off and run deja index"
	case !sources.IndexCommands():
		return "nothing found, and DEJA_INDEX_COMMANDS=0 is set: commands read under it are not indexed, and a fix is the command that followed an error — unset it and run deja index --rebuild"
	case !sources.IndexToolOutput():
		return "nothing found, and DEJA_INDEX_TOOL_OUTPUT=0 is set: tool output read under it is not indexed, so no error is there to pair a fix with — unset it and run deja index --rebuild"
	}
	return ""
}

// howDataNote is the same for `deja how`, which lists recorded commands.
func howDataNote() string {
	if !sources.IndexCommands() {
		return "nothing found, and DEJA_INDEX_COMMANDS=0 is set: commands read under it are not indexed — unset it and run deja index --rebuild"
	}
	return ""
}

// restoreDataNote is the same for `deja restore`, which reads recorded edits.
func restoreDataNote() string {
	if !sources.IndexEdits() {
		return "nothing found, and DEJA_INDEX_EDITS=0 is set: edits read under it are not indexed — unset it and run deja index --rebuild"
	}
	return ""
}
