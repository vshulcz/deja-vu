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

// How far up the match was found decides between two sessions that both match:
// the project's own session beats one recorded against a directory above it,
// however fresh that one is. People do run an agent from a home directory, which
// is the case projectFromPaths was written for.
func TestProjectDistanceRanksTheNearerMatch(t *testing.T) {
	cwd := "/Users/me/work/app"
	for _, c := range []struct {
		recorded string
		want     int
	}{
		{"app", 0},
		{"work/app", 0},
		{"me/work", 1},
		{"Users/me", 2},
		{"work", -1}, // a bare name matches only where the caller stands
		{"other/app", -1},
	} {
		if got := projectDistance(c.recorded, cwd); got != c.want {
			t.Errorf("projectDistance(%q, %q) = %d, want %d", c.recorded, cwd, got, c.want)
		}
	}
	if !nearerProject(0, 2) || !nearerProject(3, -1) {
		t.Error("a nearer match, and any match at all, has to win")
	}
	if nearerProject(2, 0) || nearerProject(-1, 3) || nearerProject(-1, -1) || nearerProject(1, 1) {
		t.Error("a farther match, or none, must not displace what is held")
	}
}
