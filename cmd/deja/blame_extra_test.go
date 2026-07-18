package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func TestBlameErrorAndEmptyBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	// Index dir squatted by a file: ensure/search must surface the error.
	squat := filepath.Join(tmp, "squatted")
	if err := os.WriteFile(squat, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(squat, "index.db"))
	if err := runBlame([]string{"parser.go"}); err == nil {
		t.Fatal("expected blame error for squatted index dir")
	}
	if _, err := blameTextResult(search.BlameOptions{}, "parser.go", 5); err == nil {
		t.Fatal("expected MCP blame error for squatted index dir")
	}
	// Healthy empty index: the no-mentions message path.
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "empty-index"))
	out, err := captureRun(t, "blame", "never-mentioned.go")
	if err != nil || out != "" {
		t.Fatalf("blame on empty index: %v (out=%q)", err, out)
	}
	if s, err := blameTextResult(search.BlameOptions{}, "never-mentioned.go", 5); err != nil || s == "" {
		t.Fatalf("mcp empty = %q err=%v", s, err)
	}
}

func TestEmbedCommandBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"embed me"}}`,
	})
	if err := runEmbed([]string{"--bogus"}); err == nil {
		t.Fatal("expected unknown flag error")
	}
	// Dead endpoint with real records: the embed call must surface the error.
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1/api/embed")
	if err := runEmbed(nil); err == nil {
		t.Fatal("expected embed endpoint error")
	}
	// Squatted index dir: Ensure error path.
	squat := filepath.Join(tmp, "sq")
	if err := os.WriteFile(squat, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(squat, "i"))
	if err := runEmbed(nil); err == nil {
		t.Fatal("expected ensure error")
	}
}
