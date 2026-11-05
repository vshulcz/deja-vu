package index

import "testing"

// Repo memory is named from the reader's own checkout, so it scopes by the
// local rule once the mark is off: a bare basename does not reach it.
func TestRepoMemoryScopesLikeLocalWork(t *testing.T) {
	for _, c := range []struct {
		project, want string
		in            bool
	}{
		{"repo:goprojects/api", "goprojects/api", true},
		{"repo:goprojects/api", "api", false},
		{"imported:goprojects/api", "api", true},
		{"repo:api", "api", true},
	} {
		if got := ProjectInScope(c.project, c.want); got != c.in {
			t.Errorf("ProjectInScope(%q, %q) = %v, want %v", c.project, c.want, got, c.in)
		}
	}
	if !ProjectInScopeStrict("repo:goprojects/api", "goprojects/api") {
		t.Error("strict scope does not take repo memory of its own project")
	}
}
