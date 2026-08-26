package index

import "testing"

// A session is re-projected onto the repository root once it has touched enough
// files under it (projectFromPaths), so the name recorded for a session started
// in a subdirectory is two segments the caller only reaches by walking up. A
// match against the cwd's own last segments alone missed exactly the sessions
// people have (#1999).
func TestSameProjectWalksUpToTheRepositoryRoot(t *testing.T) {
	for _, c := range []struct {
		recorded, cwd string
		want          bool
	}{
		{"w/app", "/w/app", true},
		{"app", "/w/app", true},
		{"w/app", "/w/app/", true}, // a trailing slash is still the same place
		{"home2/repo", "/home2/repo/cmd/deja", true},
		{"goprojects/deja-vu", "/Users/x/goprojects/deja-vu/internal/index", true},
		{"x/my app", "/Users/x/my app", true},
		{"x/проект", "/Users/x/проект", true},
		{"app/app", "/w/app/", false},          // and not a project of that name
		{"cmd", "/home2/repo/cmd/deja", false}, // a bare name matches where the caller stands
		{"other/app", "/w/app", false},
		{"", "/w/app", false},
		{"w/app", "", false},
		{"w/app", "/", false},
	} {
		if got := sameProject(c.recorded, c.cwd); got != c.want {
			t.Errorf("sameProject(%q, %q) = %v, want %v", c.recorded, c.cwd, got, c.want)
		}
	}
}
