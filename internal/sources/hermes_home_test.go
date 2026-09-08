package sources

import (
	"path/filepath"
	"testing"
)

// Hermes finds its own home through HERMES_HOME, and deja read only its own
// variable — so on a relocated Hermes, install wrote into ~/.hermes, a
// directory Hermes does not read, and the real store indexed as nothing while
// doctor reported it missing (#3203).
func TestHermesFollowsItsOwnHomeVariable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_HERMES_HOME", "")
	t.Setenv("HERMES_HOME", "")
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")

	if got, want := HermesHome(), filepath.Join(home, ".hermes"); got != want {
		t.Fatalf("default home = %q, want %q", got, want)
	}

	alt := filepath.Join(home, "hermes-alt")
	t.Setenv("HERMES_HOME", alt)
	if got := HermesHome(); got != alt {
		t.Errorf("HERMES_HOME ignored: home = %q, want %q", got, alt)
	}
	// Everything hanging off the home follows it, which is the half that made
	// the store invisible.
	if got, want := HermesProfilesRoot(), filepath.Join(alt, "profiles"); got != want {
		t.Errorf("profiles root = %q, want %q", got, want)
	}

	// deja's own variable still wins, for tests and for a store that is neither.
	mine := filepath.Join(home, "deja-says")
	t.Setenv("DEJA_HERMES_HOME", mine)
	if got := HermesHome(); got != mine {
		t.Errorf("DEJA_HERMES_HOME lost to HERMES_HOME: %q", got)
	}
}
