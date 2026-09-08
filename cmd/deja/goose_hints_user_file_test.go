package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deja used to write ~/.config/goose/.goosehints and now writes AGENTS.md; the
// retired file goes only when it holds nothing but deja's block. A person's
// own hints there were deleted on every turn (#3196).
func TestRetiredGooseHintsKeepsThePersonsOwnFile(t *testing.T) {
	hermeticEnv(t)
	path := retiredGooseHintsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	own := "my own hints, written by the user\n"
	if err := os.WriteFile(path, []byte(own+gooseRecallStart+"\nrecall\n"+gooseRecallEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropRetiredGooseHints(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the person's file is gone: %v", err)
	}
	if strings.TrimRight(string(got), "\n") != strings.TrimRight(own, "\n") {
		t.Fatalf("file = %q, want the person's hints alone", got)
	}
	// A file with no deja block at all is not touched either.
	if err := os.WriteFile(path, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropRetiredGooseHints(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != own {
		t.Fatalf("a file with no deja block was changed: %q", got)
	}
	// deja's block alone: the file goes, as before.
	if err := os.WriteFile(path, []byte(gooseRecallStart+"\nrecall\n"+gooseRecallEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropRetiredGooseHints(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a file holding only deja's block was kept")
	}
}
