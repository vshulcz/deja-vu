package policy

import (
	"fmt"
	"strings"
	"testing"
)

// "local-only" is the name of the rule that keeps local sessions and drops
// imported ones. A policy that denies both was described with it, next to a
// count on the same doctor line saying every session was withheld (#995).
func TestDescribeNamesTheRuleThatIsActuallyInForce(t *testing.T) {
	both := Policy{Activations: map[string]map[string]bool{
		ActivationSearch: {"local": false, "imported": false},
	}}
	if got := both.Describe(ActivationSearch); got != "nothing activates" {
		t.Errorf("a policy that denies every origin describes itself as %q", got)
	}
	if both.Allows(ActivationSearch, "proj") || both.Allows(ActivationSearch, "imported:proj") {
		t.Error("a policy that denies every origin let something through")
	}

	localOnly := Policy{Activations: map[string]map[string]bool{
		ActivationSearch: {"local": true, "imported": false},
	}}
	if got := localOnly.Describe(ActivationSearch); got != "local-only" {
		t.Errorf("the ordinary rule changed its name: %q", got)
	}
	// Denying only `local` keeps the generic form it has always had, and it is
	// still the rule in force.
	importedOnly := Policy{Activations: map[string]map[string]bool{
		ActivationSearch: {"local": false, "imported": true},
	}}
	if got := importedOnly.Describe(ActivationSearch); got != "deny local" {
		t.Errorf("a rule that denies only local describes itself as %q", got)
	}
	if !importedOnly.Allows(ActivationSearch, "imported:proj") || importedOnly.Allows(ActivationSearch, "proj") {
		t.Error("the description and the filter disagree")
	}
}

// Twenty group rules made one 1000-character line on the screen people read to
// find out what is allowed. The file itself is named beside it and holds the
// full set (#1023).
func TestDescribeCapsALongDenyList(t *testing.T) {
	rules := map[string]bool{"local": true}
	for i := 0; i < 20; i++ {
		rules[fmt.Sprintf("imported:grp%02d", i)] = false
	}
	got := Policy{Activations: map[string]map[string]bool{ActivationSearch: rules}}.Describe(ActivationSearch)
	if len(got) > 120 {
		t.Errorf("the description is %d characters long: %q", len(got), got)
	}
	if !strings.Contains(got, "+16 more") {
		t.Errorf("the description does not say how many it left out: %q", got)
	}
	if !strings.Contains(got, "imported:grp00") {
		t.Errorf("the description names none of the rules: %q", got)
	}

	// A short list is printed whole.
	short := Policy{Activations: map[string]map[string]bool{
		ActivationSearch: {"local": true, "imported:a": false, "imported:b": false},
	}}
	if got := short.Describe(ActivationSearch); got != "deny imported:a,imported:b" {
		t.Errorf("a short list was truncated: %q", got)
	}
}
