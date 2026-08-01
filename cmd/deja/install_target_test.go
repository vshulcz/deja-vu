package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The first command a new machine runs, and it answered with a word the reader
// has to go look up (#830).
func TestInstallWithoutATargetNamesWhatIsHere(t *testing.T) {
	hermeticEnv(t)

	// Nothing installed: point at the list rather than at nothing.
	err := runInstall(t.TempDir(), nil, false)
	if err == nil {
		t.Fatal("install with no target succeeded")
	}
	if !strings.Contains(err.Error(), "deja help") {
		t.Errorf("a bare machine gets no pointer: %v", err)
	}

	// Agents present: name them, and the two bulk flags the README uses.
	home := os.Getenv("HOME")
	for _, d := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	err = runInstall(t.TempDir(), nil, false)
	if err == nil {
		t.Fatal("install with no target succeeded")
	}
	msg := err.Error()
	for _, want := range []string{"claude-code", "codex", "--all", "--auto"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not name %q: %v", want, err)
		}
	}

	// uninstall says uninstall, not install.
	err = runInstall(t.TempDir(), nil, true)
	if err == nil || !strings.HasPrefix(err.Error(), "uninstall") {
		t.Errorf("uninstall error = %v", err)
	}
}
