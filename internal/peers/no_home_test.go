package peers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// With no home directory the config base is relative, and this file is one deja
// writes: `deja peer add` on such a machine left `./.config/deja/peers.json` in
// whatever checkout it happened to run in. Nowhere is the right answer, the way
// policy.Path answers it (#2790).
func TestPeersHaveNoPathWithoutAHome(t *testing.T) {
	t.Setenv("DEJA_PEERS_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	if p := Path(); p != "" {
		t.Errorf("Path() = %q with no home, want nowhere", p)
	}
	// A relative XDG_CONFIG_HOME is the same case by another route: the spec
	// says to ignore it, and following it writes into the working directory.
	t.Setenv("XDG_CONFIG_HOME", ".config")
	if p := Path(); p != "" {
		t.Errorf("Path() = %q for a relative XDG_CONFIG_HOME, want nowhere", p)
	}
}

// And a write says so rather than creating the directory it would have written
// into: silently putting a list somewhere it will never be read again is worse
// than refusing.
func TestPeerWritesRefuseWithoutAHome(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("DEJA_PEERS_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	if err := Record("box", true, time.Now(), nil); err == nil {
		t.Error("peer add wrote the list somewhere with no home to write it to")
	} else if !strings.Contains(err.Error(), "home") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".config")); err == nil {
		t.Error("the list went into the working directory")
	}
	if list := Load(); len(list) != 0 {
		t.Errorf("read back %d peer(s) from nowhere", len(list))
	}
}
