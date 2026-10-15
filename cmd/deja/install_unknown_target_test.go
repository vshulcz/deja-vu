package main

import (
	"os"
	"strings"
	"testing"
)

// A target nobody has heard of wired nothing, exited 1 — and left the CLI skill
// behind on the way, so a typo created a file the reader never asked for
// (#3583).
func TestAnUnknownTargetWritesNothing(t *testing.T) {
	hermeticEnv(t)
	said, err := captureRunStderr(t, "install", "frobnicate")
	if err == nil {
		t.Fatalf("an unknown target exited 0: %s", said)
	}
	if _, statErr := os.Stat(cliSkillPath()); statErr == nil {
		t.Fatalf("the skill was written for a target that does not exist: %s", cliSkillPath())
	}
	// Nor the install lock: it made ~/.config/deja for a run that wires
	// nothing (#5052).
	if _, statErr := os.Stat(installLockPath()); statErr == nil {
		t.Fatalf("an unknown target created the install lock: %s", installLockPath())
	}
}

// A target that exists still gets it, which is what the file is for.
func TestAKnownTargetStillWritesTheSkill(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRunStderr(t, "install", "claude"); err != nil {
		t.Fatalf("install claude: %v", err)
	}
	if _, err := os.Stat(cliSkillPath()); err != nil {
		t.Fatalf("the skill is missing after a real install: %v", err)
	}
}

// And a run where one target of two is unknown keeps the skill, because the
// other one was wired.
func TestOneGoodTargetAmongBadOnesKeepsTheSkill(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRunStderr(t, "install", "claude"); err != nil {
		t.Fatalf("install claude: %v", err)
	}
	before, err := os.ReadFile(cliSkillPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "deja") {
		t.Fatalf("the skill does not mention deja: %.80s", before)
	}
}
