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
	line := fmt.Sprintf("deja indexed %s session%s from %d agent%s on this machine",
		formatStatNumber(sessions), pluralS(sessions), harnesses, pluralS(harnesses))
	if repeated > 0 {
		line += fmt.Sprintf(" — %d question%s asked more than once; the earlier answers now arrive before you re-ask",
			repeated, pluralS(repeated))
	} else {
		line += " — what they decided now arrives before you re-ask"
	}
	return line
}
