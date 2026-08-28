package policy

import "testing"

// The branches of matchesAnywhere that the rule-shaped cases never reach.
// internal/policy sits on a 100% floor, and the fallback below is where a
// pattern stops being a plain directory fragment.
func TestHowAPatternIsMatched(t *testing.T) {
	for _, c := range []struct {
		name, pattern, subject string
		want                   bool
	}{
		// path.Match answers on its own when the pattern covers the whole
		// string, which is the only case that never reaches the fallback.
		{"a glob over the whole path", "/build/*/repo", "/build/ci/repo", true},
		// An empty subject is not a match for anything: opencode carries its
		// directory in the project and leaves the path as the store, so one of
		// the two fields is routinely blank.
		{"nothing to match against", "*/ci/*", "", false},
		// A pattern with a wildcard in the middle has no literal to fall back
		// on, so it either matches as a glob or not at all.
		{"a wildcard inside the fragment", "*/ci-*/repo/*", "/build/ci-3/repo/s.jsonl", false},
		// A character class is the same case, and the trimmed literal must not
		// be searched for verbatim.
		{"a character class", "*/[abc]uild/*", "/x/build/s.jsonl", false},
		// A pattern that is nothing but wildcards has an empty literal, which
		// would otherwise match every string.
		{"only wildcards", "**", "/anything/at/all", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := matchesAnywhere(c.pattern, c.subject); got != c.want {
				t.Errorf("matchesAnywhere(%q, %q) = %v, want %v", c.pattern, c.subject, got, c.want)
			}
		})
	}
}
