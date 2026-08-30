package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The MCP entry goes into a commented gemini config through the text path, and
// this used to refuse the same file afterwards for having comments — after it
// had been edited (#2744).
func TestGeminiHooksReadPastComments(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	// Already on: nothing to write, and no complaint about the comment.
	on := "{ // mine\n  \"hooksConfig\": {\"enabled\": true}\n}\n"
	if err := os.WriteFile(path, []byte(on), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := enableGeminiHooks(); err != nil {
		t.Errorf("refused a commented config that already has hooks on: %v", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != on {
		t.Errorf("rewrote a file it had nothing to change:\n%s", b)
	}

	// Not on: still a refusal, but one that says what to switch on rather than
	// quoting a parser at the reader's own comment.
	off := "{ // mine\n  \"hooksConfig\": {\"enabled\": false}\n}\n"
	if err := os.WriteFile(path, []byte(off), 0o644); err != nil {
		t.Fatal(err)
	}
	err := enableGeminiHooks()
	if err == nil {
		t.Fatal("silently rewrote a commented config")
	}
	if !strings.Contains(err.Error(), "by hand") || !strings.Contains(err.Error(), "hooksConfig") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != off {
		t.Errorf("the file changed on the refusing path:\n%s", b)
	}
}
