package main

import (
	"fmt"
	"os"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
)

// builtNote is the one line the first session start after a build says
// about what the build found. Someone who installed deja from a marketplace
// never runs `deja install` and never sees the proof the CLI prints; for
// them the index was built in silence and recall stayed invisible until it
// happened to fire (#3073). Once per index, JSON path only.
func builtNote(dir string) string {
	if !index.HasManifest(dir) {
		return ""
	}
	marker := dir + ".builtnote"
	if _, err := os.Stat(marker); err == nil {
		return ""
	}
	pol := policy.Load()
	allow := func(project string) bool { return pol.Allows(policy.ActivationSearch, project) }
	sessions, harnesses, repeated := index.BuiltSummary(dir, allow)
	if sessions == 0 {
		// Nothing to report yet; the note waits for the build that finds
		// history rather than spending its one appearance on an empty store.
		return ""
	}
	_ = os.WriteFile(marker, []byte("1"), 0o600)
	// One line: the count and what it found. The sentence that used to
	// explain what an index is for, and the docs on a line of their own, made
	// the first session start four lines tall in every host.
	// A marketplace install never runs `deja install`, so this notice is the
	// first thing deja says to that person. It is also the only place left to
	// say where the docs are (#3030); the site links the tracker.
	return builtLine(sessions, harnesses, repeated, whereToLook(dir) != "")
}

// builtLine is the built note's text for what the build found.
func builtLine(sessions, harnesses, repeated int, docs bool) string {
	found := fmt.Sprintf("%d agent%s", harnesses, pluralS(harnesses))
	if repeated > 0 {
		found += fmt.Sprintf(", %s repeat question%s", formatStatNumber(repeated), pluralS(repeated))
	}
	line := fmt.Sprintf("deja indexed %s session%s (%s)", formatStatNumber(sessions), pluralS(sessions), found)
	if docs {
		line += " · " + docsLink
	}
	return line
}
