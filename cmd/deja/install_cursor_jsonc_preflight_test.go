package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// cursor-auto checks its status line before mcp.json is written. On a
// commented cli-config.json that check wrote the status line, so a target
// refused for an unreadable mcp.json still left deja's status line wired.
func TestCursorAutoRefusalLeavesACommentedCLIConfigAlone(t *testing.T) {
	hermeticEnv(t)
	cfg := cursorCLIConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := "{\n  // mine\n  \"version\": 1\n}\n"
	if err := os.WriteFile(cfg, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	mcp := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(mcp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcp, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor-auto", "--no-index"); err == nil {
		t.Fatal("install over an unreadable mcp.json succeeded")
	}
	if b, _ := os.ReadFile(cfg); string(b) != orig {
		t.Errorf("a refused target changed cli-config.json:\n%s", b)
	}
}
