package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestInstallIndexHintsTTYOnly(t *testing.T) {
	cases := []struct {
		name     string
		history  bool
		tty      bool
		want     string
		unwanted string
	}{
		{"no history", false, true, "no agent history found on this machine", "next: run"},
		{"history found", true, true, "next: run `deja index` to index 1 agent stores", "no agent history"},
		{"non tty", true, false, "claude-code: created", "next: run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			if err := os.MkdirAll(sourcesClaudeConfigDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.history {
				writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "session.jsonl"), "session", []string{
					`{"type":"user","sessionId":"session","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"history"}}`,
				})
			}
			oldLogoWanted := logoWanted
			logoWanted = func(*os.File) bool { return tc.tty }
			defer func() { logoWanted = oldLogoWanted }()

			out, err := captureRun(t, "install", "--all")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.want) || strings.Contains(out, tc.unwanted) {
				t.Fatalf("output=%q, want %q without %q", out, tc.want, tc.unwanted)
			}
		})
	}
}

func TestInstallHintSkippedWhenIndexExists(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.gob", "sessions.gob"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("present"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := installIndexHint(); got != "" {
		t.Fatalf("hint = %q, want empty", got)
	}
}

func TestFirstIndexGreetingIncludesParsedZeroWarning(t *testing.T) {
	hermeticEnv(t)
	bad := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "bad.jsonl")
	writeFileMkdir(t, bad, "not json\n")
	oldLogoWanted := logoWanted
	logoWanted = func(*os.File) bool { return true }
	defer func() { logoWanted = oldLogoWanted }()
	oldBuild := index.LastBuild
	index.LastBuild = index.BuildSummary{
		Initial: true, Messages: 1, Sessions: 1, Harnesses: 1,
		PerHarness: []index.HarnessCount{{Name: "codex", Messages: 1, Sessions: 1}},
	}
	defer func() { index.LastBuild = oldBuild }()

	out := captureStdoutCall(t, maybeFirstIndexGreeting)
	if !strings.Contains(out, "warning: claude files found but newest parsed to zero") {
		t.Fatalf("greeting missing parsed-zero warning: %q", out)
	}
}

func captureStdoutCall(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	// Drain concurrently: windows anonymous pipes buffer only a few KB, so
	// callbacks that print more would block a sequential read-after-call.
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	out := <-done
	_ = r.Close()
	return out
}

func sourcesClaudeConfigDir() string {
	return filepath.Join(homeDir(), ".claude")
}
