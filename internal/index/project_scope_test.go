package index

import "testing"

// Automatic project scoping decides whose history recall ranks against. It was
// any substring, and the names on a real machine show what that joins: the same
// repository is recorded as "deja-vu" by some harnesses and "goprojects/deja-vu"
// by others, so several forms have to match — but "deja" is not "deja-push",
// and a project that parsed as "-" is not every hyphenated directory on the
// disk.
func TestProjectScopeMatchesSegmentsNotSubstrings(t *testing.T) {
	joined := []struct{ project, want string }{
		{"deja-vu", "deja-vu"},
		{"goprojects/deja-vu", "deja-vu"},
		{"Users/shulcz", "shulcz"},
		{"scratchpad/demo", "demo"},
		{"imported:deja-vu", "deja-vu"},
		{"imported:goprojects/deja-vu", "deja-vu"},
	}
	for _, c := range joined {
		if !projectInScope(c.project, c.want) {
			t.Errorf("%q should be in scope for %q and is not", c.project, c.want)
		}
	}

	// All six of these are joined by a substring rule, and all six are wrong.
	separate := []struct{ project, want string }{
		{"deja-push", "deja"},
		{"deja-vu", "deja"},
		{"goprojects/d3fvxl-redirect", "d3fvxl"},
		{"shulcz/coding", "shulcz"},
		{"pin-manifests", "-"},
		{"telegram-mtproxy-forwarder", "-"},
	}
	for _, c := range separate {
		if projectInScope(c.project, c.want) {
			t.Errorf("%q is a different project from %q and was pulled into its scope", c.project, c.want)
		}
	}

	if projectInScope("anything", "") {
		t.Error("an empty project name matched something")
	}
}
