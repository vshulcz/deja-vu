package sources

import (
	"path/filepath"
	"testing"
)

// Hermes finds its own home through HERMES_HOME; deja read only its own
// override, so a Hermes moved by its own switch was invisible to install,
// parse and doctor (#3203).
func TestHermesHomeFollowsHermesOwnSwitch(t *testing.T) {
	t.Setenv("DEJA_HERMES_HOME", "")
	t.Setenv("HERMES_HOME", filepath.Join(t.TempDir(), "hermes-alt"))
	if got, want := HermesHome(), filepath.Join(t.TempDir(), "hermes-alt"); filepath.Base(got) != filepath.Base(want) {
		t.Fatalf("HermesHome = %q, want the HERMES_HOME dir", got)
	}
	// deja's own override still wins.
	t.Setenv("DEJA_HERMES_HOME", filepath.Join(t.TempDir(), "deja-says"))
	if got := HermesHome(); filepath.Base(got) != "deja-says" {
		t.Fatalf("HermesHome = %q, want DEJA_HERMES_HOME to win", got)
	}
}
