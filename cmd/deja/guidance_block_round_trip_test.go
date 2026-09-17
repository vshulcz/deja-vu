package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// grok's guidance is a marked block inside a file the reader owns, so a round
// trip has to give that file back exactly. It came back one blank line longer:
// install trims the trailing newlines and appends `\n\n` + block, and the
// uninstall cut the block alone, leaving the separator behind (#3703).
func TestAGuidanceBlockRoundTripLeavesTheFileAsItWas(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".grok", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file that ends the way text files end comes back byte for byte. The
	// install normalises the trailing run — it trims and appends exactly one
	// blank line before its block — so a file with two blank lines at the end,
	// or none, comes back ending in a single newline; that change happens at
	// install time and is the same in the file while deja is wired, which is
	// why it is the contract here rather than a second bug.
	for _, theirs := range []string{
		"# My own notes\n\nAlways run `make check` before pushing.\n",
		"one line, no blank at the end\n",
		"windows endings\r\n\r\nand a second paragraph\r\n",
	} {
		if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "install", "grok", "--no-index"); err != nil {
			t.Fatalf("install grok: %v", err)
		}
		mid, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !containsBlock(string(mid)) {
			t.Fatalf("install wrote no block into %q", theirs)
		}
		if _, err := captureRun(t, "uninstall", "grok"); err != nil {
			t.Fatalf("uninstall grok: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != theirs {
			t.Errorf("round trip changed the reader's file:\n given %q\n back  %q", theirs, string(got))
		}
	}
}

func containsBlock(s string) bool {
	return strings.Contains(s, guidanceStart) && strings.Contains(s, guidanceEnd)
}

// And the normalisation the install does state itself, so nobody reads it as
// the bug above: a trailing run of blank lines becomes one.
func TestAGuidanceBlockNormalisesTheTrailingBlankLines(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, ".grok", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("two blank lines after this\n\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "grok", "--no-index"); err != nil {
		t.Fatalf("install grok: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "grok"); err != nil {
		t.Fatalf("uninstall grok: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "two blank lines after this\n"; string(got) != want {
		t.Errorf("trailing blank lines came back as %q, want %q", string(got), want)
	}
}
