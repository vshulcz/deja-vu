package policy

import "testing"

// Egress is not a read path, and no activation describes it. Borrowing the
// loosest of the three let a machine refuse to show a session to its own agent
// and ship the same text to an embedding endpoint (#1311).
func TestAllowsEgressTakesEveryActivation(t *testing.T) {
	for _, tc := range []struct {
		name string
		pol  Policy
		want bool
	}{
		{"no rules at all", Policy{}, true},
		{"every path allows", Policy{Activations: map[string]map[string]bool{
			"search": {"local": true}, "mcp": {"local": true}, "auto": {"local": true},
		}}, true},
		{"the reader's own terminal is the only one that allows", Policy{Activations: map[string]map[string]bool{
			"search": {"local": true}, "mcp": {"local": false}, "auto": {"local": false},
		}}, false},
		{"only auto denies", Policy{Activations: map[string]map[string]bool{
			"auto": {"local": false},
		}}, false},
		{"only mcp denies", Policy{Activations: map[string]map[string]bool{
			"mcp": {"local": false},
		}}, false},
		{"only search denies", Policy{Activations: map[string]map[string]bool{
			"search": {"local": false},
		}}, false},
	} {
		if got := tc.pol.AllowsEgress("myapp"); got != tc.want {
			t.Errorf("%s: AllowsEgress = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The rules are keyed by origin, so a deny aimed at imported work must not
// hold back the local project, and the other way round.
func TestAllowsEgressKeepsOriginsApart(t *testing.T) {
	p := Policy{Activations: map[string]map[string]bool{
		"auto": {"local": true, "imported": false},
	}}
	if !p.AllowsEgress("myapp") {
		t.Error("a rule about imported work held back a local project")
	}
	if p.AllowsEgress("imported:peer/app") {
		t.Error("imported work denied on the auto path was allowed off the machine")
	}
}
