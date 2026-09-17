package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// pi scans the shared skills directory as well as its own, so a copy in both
// is one skill shipped twice — and pi prints the collision on every start
// (#3657). One file, and the old copy taken with it.
func TestPiReadsTheSharedSkillAndTheOldCopyGoes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_PI_ROOT", "")

	if got := guidancePath("pi"); got != sharedSkillPath() {
		t.Errorf("pi guidance = %q, want the shared skill %q", got, sharedSkillPath())
	}

	// A copy an older deja wrote in pi's own directory is what collides.
	own := filepath.Join(sources.PiConfigDir(), "skills", "deja-history", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte(skillFile(skillBody)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installGuidance("pi", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(own); err == nil {
		t.Errorf("the colliding copy is still in pi's own directory: %s", own)
	}
	if _, err := os.Stat(sharedSkillPath()); err != nil {
		t.Errorf("the shared skill was not written: %v", err)
	}
}
