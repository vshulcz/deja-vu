package policy

import "testing"

// Filter built its result with items[:0], so it rewrote the caller's own slice.
// Callers that read the input afterwards — handoff learns what was withheld
// that way — saw the kept elements copied over the dropped ones (#1013).
func TestFilterLeavesTheCallersSliceAlone(t *testing.T) {
	p := Policy{Activations: map[string]map[string]bool{
		ActivationSearch: {"local": true, "imported": false},
	}}
	items := []string{"imported:tmp/w27", "tmp/w27"}
	kept := Filter(p, ActivationSearch, items, func(s string) string { return s })
	if len(kept) != 1 || kept[0] != "tmp/w27" {
		t.Fatalf("filter kept %v", kept)
	}
	if items[0] != "imported:tmp/w27" || items[1] != "tmp/w27" {
		t.Errorf("the caller's slice was rewritten: %v", items)
	}
}
