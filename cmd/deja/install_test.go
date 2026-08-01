package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Removing something must not leave more behind than it found. On the
// uninstall path every target computes "the config without deja in it", which
// for a file that does not exist is an empty config it then creates (#676).
func TestUninstallCreatesNothingOnAMachineThatNeverHadDeja(t *testing.T) {
	hermeticEnv(t)
	// The harnesses are installed; deja never was.
	for _, d := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A config that exists but never mentioned deja: the empty mcpServers block
	// used to be added to it on the way out, with a .bak of it besides.
	claudeJSON := filepath.Join(os.Getenv("HOME"), ".claude.json")
	const untouched = "{\n  \"editorMode\": \"vim\"\n}\n"
	if err := os.WriteFile(claudeJSON, []byte(untouched), 0o600); err != nil {
		t.Fatal(err)
	}
	before := filesUnder(t, os.Getenv("HOME"))
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatal(err)
	}
	after := filesUnder(t, os.Getenv("HOME"))
	for p := range after {
		if !before[p] {
			t.Errorf("uninstall created %s", p)
		}
	}
	got, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != untouched {
		t.Errorf("uninstall rewrote a config that never mentioned deja:\n%s", got)
	}
}

func filesUnder(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	if err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// The other direction still has to work: what install wired, uninstall removes.
func TestUninstallStillRemovesWhatInstallWrote(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	for _, d := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "install", "--all", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if n := len(filesMentioning(t, home, "deja")); n == 0 {
		t.Fatal("install wrote nothing that mentions deja")
	}
	if _, err := captureRun(t, "uninstall", "--all"); err != nil {
		t.Fatal(err)
	}
	if left := filesMentioning(t, home, "\"deja\""); len(left) > 0 {
		t.Errorf("uninstall left deja wired in %v", left)
	}
}

func filesMentioning(t *testing.T, root, needle string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || strings.HasSuffix(p, ".bak") {
			return nil
		}
		// The store's own state is not wiring: notes, the index, the record of
		// what was installed.
		if strings.Contains(filepath.ToSlash(p), "/.config/deja/") || strings.Contains(filepath.ToSlash(p), "/deja/notes") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err == nil && strings.Contains(string(b), needle) {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}
