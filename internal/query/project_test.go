package query

import "testing"

// The label a listing prints for an imported session was accepted by no filter,
// so the one string a reader can copy off that screen worked nowhere (#2644).
func TestProjectMatches(t *testing.T) {
	for _, tc := range []struct {
		name, project, from, want string
		match                     bool
	}{
		{"no filter takes everything", "alpha", "", "", true},
		{"the stored form", "imported:alpha", "mini", "imported:alpha", true},
		{"the bare project", "imported:alpha", "mini", "alpha", true},
		{"the label a listing prints", "imported:alpha", "mini", "mini:alpha", true},
		{"case does not matter", "imported:Alpha", "Mini", "mini:alpha", true},
		{"another machine's label", "imported:alpha", "mini", "laptop:alpha", false},
		{"another project on that machine", "imported:beta", "mini", "mini:alpha", false},
		// The machine on its own stays --from's question: a project filter that
		// took it would select everything that machine sent.
		{"the machine alone", "imported:alpha", "mini", "mini", false},
		{"a local session has no second form", "alpha", "", "mini:alpha", false},
		{"a local session by its own name", "alpha", "", "alpha", true},
		// A session with a sending machine but no prefix — nothing to rewrite,
		// so only the stored name matches.
		{"from without the prefix", "alpha", "mini", "mini:alpha", false},
		{"a want with a colon and no match", "alpha", "", "a:b", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProjectMatches(tc.project, tc.from, tc.want); got != tc.match {
				t.Fatalf("ProjectMatches(%q, %q, %q) = %v, want %v", tc.project, tc.from, tc.want, got, tc.match)
			}
		})
	}
}

func TestDisplayProject(t *testing.T) {
	for _, tc := range []struct{ project, from, want string }{
		{"alpha", "", "alpha"},
		{"imported:alpha", "mini", "mini:alpha"},
		{"alpha", "mini", "alpha"},
		{"imported:alpha", "", "imported:alpha"},
	} {
		if got := DisplayProject(tc.project, tc.from); got != tc.want {
			t.Errorf("DisplayProject(%q, %q) = %q, want %q", tc.project, tc.from, got, tc.want)
		}
	}
}

// A project has more than one name form — a worktree of a repository records
// its own — so a surface scoped to "the project I was asked in" asks about a
// set of names (#3713).
func TestProjectAnyMatches(t *testing.T) {
	for _, tc := range []struct {
		name    string
		project string
		from    string
		wants   []string
		match   bool
	}{
		{"no names is no filter", "alpha", "", nil, true},
		{"the first name", "goprojects/alpha", "", []string{"alpha", "alpha-wt"}, true},
		{"the second name", "alpha-wt", "", []string{"alpha", "alpha-wt"}, true},
		{"none of them", "beta", "", []string{"alpha", "alpha-wt"}, false},
		{"a blank name is skipped, not a wildcard", "beta", "", []string{"  ", "alpha"}, false},
		{"only blanks means nothing matches", "beta", "", []string{"", " "}, false},
		{"the imported label still works", "imported:alpha", "mini", []string{"mini:alpha"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProjectAnyMatches(tc.project, tc.from, tc.wants); got != tc.match {
				t.Fatalf("ProjectAnyMatches(%q, %q, %v) = %v, want %v", tc.project, tc.from, tc.wants, got, tc.match)
			}
		})
	}
}

// ProjectWants is what the filter reads: the set where a caller gave one, the
// single --project name otherwise, and nothing for the machine.
func TestOptionsProjectWants(t *testing.T) {
	if got := (Options{}).ProjectWants(); got != nil {
		t.Errorf("an empty request filters nothing, got %v", got)
	}
	if got := (Options{Project: "  "}).ProjectWants(); got != nil {
		t.Errorf("a blank --project filters nothing, got %v", got)
	}
	if got := (Options{Project: "alpha"}).ProjectWants(); len(got) != 1 || got[0] != "alpha" {
		t.Errorf("got %v, want the one name", got)
	}
	// The set wins: a caller that means one project under several names has
	// already put --project in it if there was one.
	o := Options{Project: "alpha", Projects: []string{"beta", "beta-wt"}}
	if got := o.ProjectWants(); len(got) != 2 || got[0] != "beta" {
		t.Errorf("got %v, want the set", got)
	}
}
