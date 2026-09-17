package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Zed's entry can name a binary or defer to an extension. Deferring to an
// extension that is not installed — a dangling symlink into a scratch
// directory is how it happened — leaves an enabled server with nothing behind
// it, and the row said `wired` because the id was in the file (#3660).
func TestZedRowSaysWhenNothingCanStart(t *testing.T) {
	if runtime.GOOS != "darwin" {
		// The extension directory is per-platform and the point of the test is
		// the pair of answers, not the path: check it where it was found.
		t.Skip("extension path is platform-specific; exercised on darwin")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	settings := filepath.Join(home, "settings.json")
	deferring := `{
  "context_servers": {
    "deja-context-server": {
      "enabled": true,
      "remote": false,
      "settings": {}
    }
  }
}
`
	if err := os.WriteFile(settings, []byte(deferring), 0o600); err != nil {
		t.Fatal(err)
	}
	note := zedUnreachableNote(settings)
	if note == "" {
		t.Fatal("an entry deferring to a missing extension was reported as fine")
	}
	if !strings.Contains(note, "deja install zed") {
		t.Errorf("the note does not say how to fix it: %q", note)
	}

	// A dangling symlink is the case this exists for: it is present in a
	// listing and resolves to nothing.
	installed := filepath.Join(home, "Library", "Application Support", "Zed", "extensions", "installed")
	if err := os.MkdirAll(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "gone"), filepath.Join(installed, zedServerID)); err != nil {
		t.Fatal(err)
	}
	if zedUnreachableNote(settings) == "" {
		t.Error("a dangling extension symlink was read as installed")
	}

	// With the extension actually there, nothing to say.
	if err := os.Remove(filepath.Join(installed, zedServerID)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(installed, zedServerID), 0o755); err != nil {
		t.Fatal(err)
	}
	if note := zedUnreachableNote(settings); note != "" {
		t.Errorf("an installed extension was reported as missing: %q", note)
	}

	// And an entry that names its own command answers for itself, extension or
	// not — that is what `deja install zed` writes.
	own := filepath.Join(home, "own.json")
	if err := os.WriteFile(own, []byte(`{"context_servers":{"deja-context-server":{"command":"/usr/local/bin/deja","args":["mcp"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(installed, zedServerID)); err != nil {
		t.Fatal(err)
	}
	if note := zedUnreachableNote(own); note != "" {
		t.Errorf("an entry with its own command was reported as needing an extension: %q", note)
	}
}
