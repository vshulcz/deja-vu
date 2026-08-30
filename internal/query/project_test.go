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
