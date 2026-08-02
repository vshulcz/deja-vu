package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The repair derives every path from the environment it runs in. A process
// whose HOME points elsewhere while the record is still visible — sudo with a
// preserved XDG_CONFIG_HOME, a container, an su session — wrote a fresh set of
// configs into a home nobody installed into, left the real ones pointing at
// the old binary, and marked the record repaired (#885).
func TestWiringRepairStaysInTheHomeItWasWrittenFor(t *testing.T) {
	tmp := hermeticEnv(t)
	cfg := filepath.Join(tmp, "config")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if err := os.MkdirAll(filepath.Join(cfg, "deja"), 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(tmp, "other-home")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(home string) {
		b, err := json.Marshal(wiringState{Version: "older", Targets: []string{"claude-auto"}, Exe: filepath.Join(tmp, "old-deja"), Home: home})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(wiringStatePath(), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The record belongs to another home: nothing is repaired, nothing spreads.
	write(elsewhere)
	if changed := refreshWiringAfterUpgrade(); len(changed) != 0 {
		t.Errorf("repair ran in a home it was not written for: %v", changed)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("configs were written into a home nobody installed into: %v", err)
	}

	// The record belongs to this home: the repair runs as before.
	write(os.Getenv("HOME"))
	if changed := refreshWiringAfterUpgrade(); len(changed) == 0 {
		t.Error("the repair stopped working in its own home")
	}

	// A record from before this field exists: repaired, as it always was. The
	// config is removed first so the repair has something to write — after the
	// case above it already points at this binary.
	if err := os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".claude")); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(wiringState{Version: "older", Targets: []string{"claude-auto"}, Exe: filepath.Join(tmp, "old-deja")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if changed := refreshWiringAfterUpgrade(); len(changed) == 0 {
		t.Error("a record written before the home field stopped being repaired")
	}
}
