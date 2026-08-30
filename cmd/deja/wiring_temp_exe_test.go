package main

import (
	"bytes"
	"os"
	"path/filepath"
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
	for _, d := range []string{".claude", ".codex", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(home, "opt", "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	// The wiring was recorded for a binary that lives somewhere real, and the
	// configs say whatever this in-process install wrote.
	writeWiringExe(t, installed)
	before, err := os.ReadFile(filepath.Join(home, ".claude.json"))
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
	after, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("the config was rewritten for a build in a temp directory:\nwas:\n%s\nnow:\n%s", before, after)
	}

	// And a real upgrade still repairs itself. If the environment stopped the
	// repair for a reason of its own — a target that cannot be written — say so
	// rather than blame the guard, which is what this test is about.
	exeIsTemporary = func(string) bool { return false }
	rewired := refreshWiringAfterUpgrade()
	if len(stuckWiring) > 0 && len(rewired) == 0 {
		t.Skipf("the repair could not write here: %v", stuckWiring)
	}
	if len(rewired) == 0 {
		t.Fatal("a binary that moved to a real location was not adopted")
	}
}
