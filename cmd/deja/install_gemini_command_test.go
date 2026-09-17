package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Gemini keeps one flat command namespace and everything deja installs lands in
// it: the MCP server's prompt is `/deja`, and every skill is a command too. A
// file of deja's collided with the prompt first (#3655) and, renamed to
// `deja-search`, collided with deja's own CLI skill — "Skill command
// '/deja-search' was renamed to '/deja-search1'", which is Gemini saying it out
// loud. So Gemini gets no command file of ours, and both names one used to have
// are taken back out (#3665).
func TestGeminiHasNoCommandFileOfItsOwn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GEMINI_CLI_HOME", "")
	t.Setenv("DEJA_GEMINI_ROOT", "")

	if path := commandFilePath("gemini"); path != "" {
		t.Errorf("command file = %q, want none — the skill is the command there", path)
	}

	// Both files an older deja wrote are dropped by the install, or the clash
	// outlives the fix for everyone who installed before.
	dir := filepath.Join(sources.GeminiHome(), "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := []string{filepath.Join(dir, "deja.toml"), filepath.Join(dir, "deja-search.toml")}
	for _, p := range legacy {
		if err := os.WriteFile(p, []byte("description = \"(deja-vu)\"\nprompt = '''\nsearch\n'''\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := installCommandFile("gemini", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	for _, p := range legacy {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("a command file deja used to write is still there: %s", p)
		}
	}

	// But a command of the reader's own under either name is theirs, and stays.
	mine := filepath.Join(dir, "deja.toml")
	body := "prompt = \"my own thing\"\n"
	if err := os.WriteFile(mine, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCommandFile("gemini", "/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(mine); err != nil {
		t.Errorf("the reader's own command file was removed: %v", err)
	} else if string(b) != body {
		t.Errorf("the reader's own command file was rewritten: %q", b)
	}
}
