package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What an uninstall leaves in deja's own state file, and what the next session
// does with it.
//
// The file survives — it is deja's, not a config in someone else's tree, so the
// rule #840 states does not apply to it — and the field anything acts on is
// emptied. The regression this guards is unpleasant: a refresh that still knew
// the old targets would rewire harnesses the reader had just removed, on the
// next session start, without being asked (#2674).
func TestUninstallLeavesNoTargetsToRewire(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, d := range []string{".claude", ".codex", ".cursor", ".qwen"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if wiringTargets(t) == 0 {
		t.Fatal("install recorded no targets, so this pins nothing")
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if n := wiringTargets(t); n != 0 {
		t.Fatalf("uninstall left %d target%s recorded for a refresh to rewire", n, pluralS(n))
	}
}

// And the whole point of that: a binary that moves afterwards rewires nothing
// and says nothing, because there is nothing recorded to rewire.
func TestAMovedBinaryRewiresNothingAfterAnUninstall(t *testing.T) {
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
	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	// The upgrade: the recorded binary is not where it was.
	writeWiringExe(t, filepath.Join(home, "moved", "deja"))

	rewired := refreshWiringAfterUpgrade()

	if note := rewireNote(rewired); note != "" {
		t.Fatalf("a machine with nothing installed was told deja rewired it: %q", note)
	}
	for _, gone := range []string{".claude.json.deja", ".codex/config.toml", ".cursor/mcp.json"} {
		if b, err := os.ReadFile(filepath.Join(home, gone)); err == nil && strings.Contains(string(b), "deja") {
			t.Fatalf("%s was wired again after the reader removed it:\n%s", gone, b)
		}
	}
}

func wiringTargets(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(wiringStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	var st struct {
		Targets []string `json:"targets"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("the state file is not readable: %v\n%s", err, b)
	}
	return len(st.Targets)
}

func writeWiringExe(t *testing.T, exe string) {
	t.Helper()
	b, err := os.ReadFile(wiringStatePath())
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	st["exe"] = exe
	out, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), out, 0o644); err != nil {
		t.Fatal(err)
	}
}
