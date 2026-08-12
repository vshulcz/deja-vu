package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// opencode moved from a block in AGENTS.md to a skill. Install has to take the
// old block away, or the harness reads both and the replaced copy sits in every
// session forever — and a file that held only our block should not be left
// behind empty.
func TestOpencodeGuidanceMigratesOffAgentsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	agents := retiredGuidancePaths("opencode")[0]
	if err := os.MkdirAll(filepath.Dir(agents), 0o755); err != nil {
		t.Fatal(err)
	}

	// Our block among the user's own notes: the block goes, the notes stay.
	mixed := "# My rules\n\nrun make lint\n\n" + guidanceStart + "\nold\n" + guidanceEnd + "\n\nkeep me\n"
	if err := os.WriteFile(agents, []byte(mixed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("opencode", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), guidanceStart) {
		t.Fatalf("old block survived install: %s", b)
	}
	if !strings.Contains(string(b), "run make lint") || !strings.Contains(string(b), "keep me") {
		t.Fatalf("user content lost: %s", b)
	}
	if _, err := os.Stat(guidancePath("opencode")); err != nil {
		t.Fatalf("skill not written: %v", err)
	}

	// A file that was only ever ours is removed rather than left empty.
	if err := os.WriteFile(agents, []byte(guidanceStart+"\nold\n"+guidanceEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("opencode", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agents); !os.IsNotExist(err) {
		left, _ := os.ReadFile(agents)
		t.Fatalf("empty AGENTS.md left behind: %q err=%v", left, err)
	}
}
