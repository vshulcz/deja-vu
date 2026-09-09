package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The repair exists because deja moves — a release replaces it, a package
// manager relocates it. It cannot tell that from one run of a build somewhere
// else, and the only guard was a temp directory (#2684), so a build under a
// home directory, a checkout or an agent's scratch adopted every config on its
// first run: one machine ended with eight hook entries per event, each naming a
// different build, all firing on every prompt (#3421).
func TestAStrangerDoesNotAdoptTheWiring(t *testing.T) {
	tmp := hermeticEnv(t)
	installed := filepath.Join(tmp, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWiringFixture(t, wiringState{
		Version: "older", Targets: []string{"claude-code"}, Exe: installed, Home: homeDir(),
	})

	// This test binary stands in for the stranger: a different path, and the
	// recorded one is still on disk.
	if got := refreshWiringAfterUpgrade(); len(got) != 0 {
		t.Errorf("a build that is not the installed deja rewrote %v", got)
	}

	// The honest case: the recorded binary is gone, so this one is the move.
	if err := os.Remove(installed); err != nil {
		t.Fatal(err)
	}
	if got := refreshWiringAfterUpgrade(); len(got) == 0 {
		t.Error("the recorded binary is gone and the repair still stood down")
	}
}

// An entry deja wrote from a build under another name is still deja's own: the
// record remembers the paths it installed from, so a later install replaces
// that entry instead of stacking a second one beside it (#3421).
func TestAnEntryFromAPreviousPathIsRecognisedAsOurs(t *testing.T) {
	hermeticEnv(t)
	const old = "/somewhere/scratch/deja-arm"
	writeWiringFixture(t, wiringState{
		Version: "dev", Targets: []string{"claude-code"}, Exe: "/usr/local/bin/deja",
		Exes: []string{old}, Home: homeDir(),
	})
	forgetWrittenExes()
	t.Cleanup(forgetWrittenExes)

	if !isDejaHookCommand(old+" hook-prompt", "/usr/local/bin/deja hook-prompt") {
		t.Errorf("an entry deja wrote from %s is not recognised as ours", old)
	}
	if isDejaHookCommand("/opt/somebody/else hook-prompt", "/usr/local/bin/deja hook-prompt") {
		t.Error("a command deja never wrote was claimed as ours")
	}
}

func writeWiringFixture(t *testing.T, st wiringState) {
	t.Helper()
	p := wiringStatePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
