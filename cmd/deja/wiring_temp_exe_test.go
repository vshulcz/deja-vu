package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// underTempDir is what decides it, so it is pinned on its own: the paths a real
// deja lives at, and the ones a scratch build does.
func TestUnderTempDirKnowsAScratchBuild(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, tc := range []struct {
		path string
		temp bool
	}{
		{filepath.Join(tmp, "deja"), true},
		{filepath.Join(tmp, "go-build123", "b001", "exe", "deja"), true},
		{"/tmp/deja", true},
		{"/private/tmp/scratch/deja", true},
		{"/usr/local/bin/deja", false},
		{"/opt/homebrew/bin/deja", false},
		// A literal home, not this process's: the suite runs with HOME under a
		// temp directory, where ~/.local/bin really is temporary.
		{"/home/someone/.local/bin/deja", false},
		{"", false},
	} {
		if got := underTempDir(tc.path); got != tc.temp {
			t.Errorf("underTempDir(%q) = %v, want %v", tc.path, got, tc.temp)
		}
	}
}

// The repair exists because deja moves — a release replaces the binary, a
// package manager relocates it — and every config still names the old path. It
// cannot tell that from a one-off run of a build somewhere else, so a `go run`,
// a colleague's checkout or a CI job rewrote every config to a path that will
// not exist tomorrow (#2684).
func TestTheRepairRefusesToAdoptATempBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	codex := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codex), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codex, []byte("[tools]\nweb = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The state written by hand, the way TestWiringRefreshTouchesOnlyRecorded-
	// Targets does: what install records depends on which harnesses a machine
	// happens to have, and this test is about the guard, not about detection.
	if err := os.MkdirAll(filepath.Dir(wiringStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"version":"0.0.1","targets":["codex"],"exe":"/opt/deja/bin/deja","home":` +
		strconv.Quote(homeDir()) + `}`
	if err := os.WriteFile(wiringStatePath(), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(codex)
	if err != nil {
		t.Fatal(err)
	}

	restore := exeIsTemporary
	t.Cleanup(func() { exeIsTemporary = restore })

	// A scratch build runs the session hook once.
	exeIsTemporary = func(string) bool { return true }
	if rewired := refreshWiringAfterUpgrade(); len(rewired) != 0 {
		t.Fatalf("a build in a temp directory was adopted: %v", rewired)
	}
	after, err := os.ReadFile(codex)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("the config was rewritten for a build in a temp directory:\nwas:\n%s\nnow:\n%s", before, after)
	}

	// And a real upgrade still repairs itself.
	exeIsTemporary = func(string) bool { return false }
	rewired := refreshWiringAfterUpgrade()
	if len(rewired) == 0 {
		t.Fatalf("a binary that moved to a real location was not adopted (stuck: %v)", stuckWiring)
	}
	repaired, err := os.ReadFile(codex)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, repaired) {
		t.Fatalf("the repair reported %v and changed nothing:\n%s", rewired, repaired)
	}
}
