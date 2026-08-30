package index

import (
	"testing"
)

// The commands table keys by project while the ignore rule matches on the
// session's path, so the callers that read that table cannot apply the rule
// exactly. This names the projects it touches at all, which is the safe
// direction for them (#2652).
func TestProjectsTouchedByIgnore(t *testing.T) {
	dir := halvedStore(t)
	touched := ProjectsTouchedByIgnore(dir)
	if !touched["w/scratch"] {
		t.Fatalf("the ignored project is not named: %v", touched)
	}
	if touched["w/keep"] {
		t.Fatalf("a project the rule does not touch was named: %v", touched)
	}
	if len(touched) != 1 {
		t.Fatalf("one project is covered by this rule, got %v", touched)
	}
}

// A store with no rule in force names nothing, and a missing store answers
// nothing rather than failing the caller.
func TestProjectsTouchedByIgnoreOnAStoreWithoutOne(t *testing.T) {
	if got := ProjectsTouchedByIgnore(t.TempDir()); len(got) != 0 {
		t.Fatalf("an unreadable store should name no projects, got %v", got)
	}
}
