package main

import "testing"

// "1 sessions" is the first line anyone sees from deja, and "1 session share"
// was mine from this audit (#737).
func TestPluralHelpers(t *testing.T) {
	for _, c := range []struct {
		n           int
		s, y, share string
	}{
		{0, "s", "ies", "share"},
		{1, "", "y", "shares"},
		{2, "s", "ies", "share"},
		{11, "s", "ies", "share"},
	} {
		if got := pluralS(c.n); got != c.s {
			t.Errorf("pluralS(%d) = %q, want %q", c.n, got, c.s)
		}
		if got := pluralY(c.n); got != c.y {
			t.Errorf("pluralY(%d) = %q, want %q", c.n, got, c.y)
		}
		if got := verbShare(c.n); got != c.share {
			t.Errorf("verbShare(%d) = %q, want %q", c.n, got, c.share)
		}
	}
}
