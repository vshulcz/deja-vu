package sources

import "testing"

// splitEncodedWindowsDrive's whole job is to fire on the Windows "C--…" form
// and NEVER on unix or pi encodings — a false positive would rewrite good
// project paths. This runs on every platform (unlike the windows-gated
// resolver tests) because that invariant is what regresses silently.
func TestSplitEncodedWindowsDrive(t *testing.T) {
	cases := []struct {
		in   string
		root string
		rest string
		ok   bool
	}{
		{"C--Users-x-app", "C:", "-Users-x-app", true},
		{"d--Users-y", "d:", "-Users-y", true},
		{"-Users-x-app", "", "", false},    // unix: leading slash
		{"--Users-x-app--", "", "", false}, // pi prefix/suffix
		{"1--Users", "", "", false},        // not a drive letter
		{"C-Users", "", "", false},         // single dash, no separator
		{"C--", "", "", false},             // too short to carry a path
	}
	for _, c := range cases {
		root, rest, ok := splitEncodedWindowsDrive(c.in)
		if ok != c.ok || root != c.root || rest != c.rest {
			t.Errorf("split(%q) = (%q,%q,%v), want (%q,%q,%v)", c.in, root, rest, ok, c.root, c.rest, c.ok)
		}
	}
}
