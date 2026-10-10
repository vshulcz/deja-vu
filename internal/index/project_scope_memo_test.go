package index

import "testing"

// projectScope is projectInScope over a list of candidates, answered once per
// project name. It must agree with asking each candidate in turn.
func TestProjectScopeAgreesWithProjectInScope(t *testing.T) {
	projects := []string{
		"", "-", "api", "acme/api", "work/api", "Work/API", "imported:api", "imported:peer/acme/api",
		`c:\work\api`, `C:\Work\Api`, "deja-vu", "goprojects/deja-vu", "deja-push", "x/deja-vu/sub",
		"imported:", "/", "api/", "Ünïcode/Проект", "ünïcode/проект",
	}
	wants := [][]string{
		nil,
		{""},
		{"api"},
		{"work/api", "api"},
		{"WORK/api"},
		{"deja-vu", "goprojects/deja-vu"},
		{"проект", "ünïcode/ПРОЕКТ"},
		{"", "acme/api", ""},
		{`work\api`},
	}
	for _, ws := range wants {
		scope := newProjectScope(ws...)
		for _, p := range projects {
			want := false
			for _, w := range ws {
				if projectInScope(p, w) {
					want = true
					break
				}
			}
			// Twice: the second answer comes from the memo.
			for range 2 {
				if got := scope.has(p); got != want {
					t.Fatalf("wants %q, project %q: scope says %v, projectInScope says %v", ws, p, got, want)
				}
			}
		}
	}
}

func TestEndsWithSegment(t *testing.T) {
	for _, c := range []struct {
		p, w string
		want bool
	}{
		{"acme/api", "api", true},
		{`acme\api`, "api", true},
		{"api", "api", false},
		{"/api", "api", true},
		{"xapi", "api", false},
		{"acme/api", "acme/api", false},
		{"a/acme/api", "acme/api", true},
		{"", "", false},
		{"/", "", true},
	} {
		if got := endsWithSegment(c.p, c.w); got != c.want {
			t.Errorf("endsWithSegment(%q, %q) = %v, want %v", c.p, c.w, got, c.want)
		}
	}
}
