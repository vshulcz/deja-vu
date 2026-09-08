package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The harness filter's description is the only place an agent learns which
// agents it can ask about. It carried nine hand-written names while sixteen
// more harnesses landed (#3382), so the names and the count come from the
// registry now — and this checks that the count is the same one the site and
// the README are held to.
func TestTheHarnessFilterCountsEveryHarness(t *testing.T) {
	got := harnessFilterDescription()
	n := registryHarnessCount(t, filepath.Join("..", ".."))
	if want := fmt.Sprintf("and %d more", n-4); !strings.Contains(got, want) {
		t.Errorf("the filter says %q; the registry has %d harnesses, so it should say %q", got, n, want)
	}
	// The names it does show have to be real ones, in the registry's order.
	for _, name := range []string{"claude", "codex"} {
		if !strings.Contains(got, name) {
			t.Errorf("the filter no longer names %q: %q", name, got)
		}
	}
	// And it points somewhere for the rest rather than pretending to be a
	// closed list.
	if !strings.Contains(got, "deja sources") {
		t.Errorf("the filter does not say where the rest are listed: %q", got)
	}
}
