package index

import "testing"

// The automatic scope takes a candidate as a path suffix, which is right for
// the full forms — one project reached under different parents, worktrees of
// one repo — and wrong for the bare basename the candidates also carry: with it
// a session start in /work/api recalled a client's acme/api "from this
// project" (#2333).
func TestProjectScopeDoesNotMatchOnABareBasename(t *testing.T) {
	cases := []struct {
		project, want string
		in            bool
	}{
		{"work/api", "work/api", true},
		{"work/api", "api", false}, // the bare name reaches through the full form
		{"acme/api", "api", false}, // another project that ends the same way
		{"acme/api", "acme/api", true},
		{"src/clients/acme/api", "acme/api", true}, // the full form still reaches
		{"imported:acme/api", "acme/api", true},    // a peer's copy of that project
		{"imported:acme/api", "api", true},         // a peer's path is not this machine's
		{`acme\api`, "acme/api", false},            // windows separator, path form
		{`src\acme\api`, `acme\api`, true},
		{"acme/api", "", false},
	}
	for _, c := range cases {
		if got := projectInScope(c.project, c.want); got != c.in {
			t.Errorf("projectInScope(%q, %q) = %v, want %v", c.project, c.want, got, c.in)
		}
	}
}

// The strict rule drops the allowance synced work gets: right for recall, wrong
// for a command that packages one session for another agent (#2347).
func TestStrictProjectScopeDropsTheSyncedAllowance(t *testing.T) {
	cases := []struct {
		project, want string
		in            bool
	}{
		{"work/api", "work/api", true},
		{"imported:work/api", "work/api", true},
		{"imported:clients/acme/api", "api", false},
		{"imported:goprojects/svc", "svc", false},
		{"imported:svc", "svc", true},
		{"src/clients/acme/api", "acme/api", true},
		{"acme/api", "", false},
	}
	for _, c := range cases {
		if got := ProjectInScopeStrict(c.project, c.want); got != c.in {
			t.Errorf("ProjectInScopeStrict(%q, %q) = %v, want %v", c.project, c.want, got, c.in)
		}
	}
	// And the loose rule still answers the synced case recall depends on.
	if !ProjectInScope("imported:goprojects/svc", "svc") {
		t.Error("the loose rule no longer reaches a peer's project by its local name")
	}
}
