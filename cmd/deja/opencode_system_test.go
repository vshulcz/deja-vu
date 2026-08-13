package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The opencode plugin must fold its recall into the first system block. An
// OpenAI-compatible endpoint that requires the system message to come first
// rejects a request carrying a second one, and installing deja then made every
// turn fail: "Not Found: System message must be at the beginning." Reproduced
// against a local model, and fixed by merging rather than appending.
func TestOpencodePluginDoesNotAppendASecondSystemBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	if _, err := installOpencodePlugin("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "opencode", "plugins", "deja.js")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "output.system.push(ctx)\n") &&
		!strings.Contains(src, "output.system[0] = ctx") {
		t.Fatal("plugin appends a second system block; providers that require system-first reject the request")
	}
	if !strings.Contains(src, "output.system[0] = ctx") {
		t.Fatal("plugin no longer folds the recall into the first system block")
	}
}
