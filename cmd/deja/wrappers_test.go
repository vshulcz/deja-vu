package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `deja goose` and `deja aider` refresh recall and then become the harness.
// Both were shipped without tests, and one of them started an agent from a
// one-word search before that was caught.
func TestGooseHookWritesTheRecallFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "idx"))
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")

	if err := cmdGooseHook(filepath.Join(home, "idx"), nil); err != nil {
		t.Fatalf("hook: %v", err)
	}
	hints := gooseHintsPath()
	b, err := os.ReadFile(hints)
	if err != nil {
		t.Fatalf("hook wrote no hints: %v", err)
	}
	// An empty index still has to leave a file: Goose refuses to start when a
	// configured hints file is missing.
	if len(b) == 0 {
		t.Fatal("hints file is empty")
	}

	// With MOIM set the digest goes there instead, so the wrapper does not
	// inject the same text twice.
	moim := filepath.Join(home, "recall.md")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)
	if err := cmdGooseHook(filepath.Join(home, "idx"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(moim); err != nil {
		t.Fatalf("MOIM file not written: %v", err)
	}
}

func TestRecallCountsReadTheRightFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")

	if n := gooseRecallCount(); n != 0 {
		t.Fatalf("count on a missing file = %d", n)
	}
	if n := aiderRecallCount(); n != 0 {
		t.Fatalf("aider count on a missing file = %d", n)
	}
	digest := "<deja-recall>\n  - Session: **a** `1`\n  - Session: **b** `2`\n</deja-recall>\n"
	for _, path := range []string{gooseHintsPath(), aiderContextPath()} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(digest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n := gooseRecallCount(); n != 2 {
		t.Fatalf("goose count = %d, want 2", n)
	}
	if n := aiderRecallCount(); n != 2 {
		t.Fatalf("aider count = %d, want 2", n)
	}
}

// The wrappers must not start a harness that is not installed, and must say
// which one is missing rather than failing obscurely.
func TestWrappersReportAMissingHarness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "idx"))
	t.Setenv("PATH", filepath.Join(home, "empty"))
	for name, run := range map[string]func(string, []string, string) error{"aider": cmdAider, "goose": cmdGoose} {
		err := run(filepath.Join(home, "idx"), []string{"--version"}, "")
		if err == nil {
			t.Fatalf("%s: no error with the harness absent", name)
		}
		if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "PATH") {
			t.Fatalf("%s: unhelpful error %v", name, err)
		}
	}
}

func TestAiderContextRefreshLeavesAFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "idx"))
	if err := refreshAiderContext(filepath.Join(home, "idx")); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	b, err := os.ReadFile(aiderContextPath())
	if err != nil {
		t.Fatalf("no context file: %v", err)
	}
	// aider fails to start when a file in read: does not exist, so an empty
	// index must still produce something.
	if strings.TrimSpace(string(b)) == "" {
		t.Fatal("context file is empty; aider would refuse to start")
	}
}
