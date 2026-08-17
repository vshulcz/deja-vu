package sources

import "testing"

// A project name is an identifier, not a path on this disk. Assembled with the
// OS separator it reads goprojects\deja-vu on windows and goprojects/deja-vu
// everywhere else — two names for one project.
//
// Scoping then misses on windows, where eleven internal/index tests were
// failing on exactly this, and sync is worse: a project exported from a windows
// machine arrives as "tmp\touch" and never matches the "tmp/touch" the same
// repository is called on any other machine, so the peer's history is indexed
// and unreachable.
func TestAProjectNameIsSpelledTheSameOnEveryPlatform(t *testing.T) {
	if got := projectSegments("goprojects", "deja-vu"); got != "goprojects/deja-vu" {
		t.Errorf("project name = %q, want it separated by / on every platform", got)
	}
}
