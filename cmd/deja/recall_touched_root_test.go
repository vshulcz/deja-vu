package main

import (
	"strings"
	"testing"
)

// The line says the shared directory once and lists names under it, which only
// worked when every path shared one. A session that edited files in a repo and
// ran one script in /private/tmp printed four absolute paths instead — 255 bytes
// where 90 say the same thing. Across sixteen recall calls on a real store, 5 of
// the 10 lines served were absolute for that reason, and the ten lines together
// cost 2258 bytes against 1808 with the root named for the majority.
func TestOneStrayPathDoesNotMakeTheWholeLineAbsolute(t *testing.T) {
	dir, s := touchedLineStore(t,
		"/w/app/internal/queue/backoff.go",
		"/w/app/internal/queue/retry.go",
		"/w/app/internal/queue/jitter.go",
		"/private/tmp/probe/run3.sh",
	)
	got := recallTouchedLine(dir, s, nil)
	if strings.Count(got, "/w/app/internal/queue") != 1 {
		t.Errorf("the repo the three files share is not named once: %q", got)
	}
	// The stray path is still there in full: it is not under the root, and
	// trimming a prefix it does not have would name a file that does not exist.
	if !strings.Contains(got, "/private/tmp/probe/run3.sh") {
		t.Errorf("the path outside the root was lost or mangled: %q", got)
	}
}

// Four paths from four trees have no root to name: saying one would claim it
// over paths that are not under it.
func TestPathsFromDifferentTreesClaimNoRoot(t *testing.T) {
	dir, s := touchedLineStore(t,
		"/w/one/alpha.go",
		"/x/two/beta.go",
		"/y/three/gamma.go",
		"/z/four/delta.go",
	)
	got := recallTouchedLine(dir, s, nil)
	if strings.Contains(got, " in /") {
		t.Errorf("a root was named over paths that do not share it: %q", got)
	}
	for _, want := range []string{"/w/one/alpha.go", "/x/two/beta.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s is missing: %q", want, got)
		}
	}
}
