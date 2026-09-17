package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Gemini keeps one flat command namespace and lists the MCP server's own
// prompt in it, which deja names `deja` on every host. Two entries under one
// name and Gemini renames both, so the name the receipt tells people to type
// belongs to nothing. The file gets a name of its own (#3655).
func TestGeminiCommandDoesNotCollideWithTheServerPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GEMINI_CLI_HOME", "")
	t.Setenv("DEJA_GEMINI_ROOT", "")

	path := commandFilePath("gemini")
	if filepath.Base(path) != "deja-search.toml" {
		t.Errorf("command file = %q, want a name the server's `deja` prompt cannot claim", path)
	}

	// The file an older deja wrote under the colliding name is dropped by the
	// install, or the clash survives the fix for everyone who installed before.
	legacy := filepath.Join(sources.GeminiHome(), "commands", "deja.toml")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(commandFileText("gemini", "/usr/local/bin/deja")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCommandFile("gemini", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Errorf("the colliding file is still there: %s", legacy)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the new command file was not written: %v", err)
	}

	// But a /deja of the reader's own at that path is theirs, and stays.
	mine := filepath.Join(sources.GeminiHome(), "commands", "deja.toml")
	if err := os.WriteFile(mine, []byte("prompt = \"my own thing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCommandFile("gemini", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(mine); err != nil {
		t.Errorf("the reader's own command file was removed: %v", err)
	} else if string(b) != "prompt = \"my own thing\"\n" {
		t.Errorf("the reader's own command file was rewritten: %q", b)
	}
}
