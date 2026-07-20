package main

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
		{"history found", true, true, "claude-code", "no agent history"},
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
	if got := installIndexHint(index.DefaultDir()); got != "" {
		t.Fatalf("hint = %q, want empty", got)
	}
}

func TestRequestWarmupUsesRecentSentinel(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	dir := filepath.Join(t.TempDir(), "index")
	t.Setenv("DEJA_INDEX_DIR", dir)
	var calls int
	oldSpawn := spawnWarmup
	spawnWarmup = func(exe, sentinel string) error {
		calls++
		if exe == "" || sentinel != filepath.Join(dir, "warmup.sentinel") {
			t.Fatalf("warmup args = %q, %q", exe, sentinel)
		}
		return nil
	}
	defer func() { spawnWarmup = oldSpawn }()
	requestWarmup(dir)
	requestWarmup(dir)
	if calls != 1 {
		t.Fatalf("warmup calls = %d, want 1", calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "warmup.sentinel")); err != nil {
		t.Fatalf("sentinel missing: %v", err)
	}
}

func TestRequestWarmupRetriesStaleAndRecordsFailure(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	dir := filepath.Join(t.TempDir(), "index")
	t.Setenv("DEJA_INDEX_DIR", dir)
	sentinel := filepath.Join(dir, "warmup.sentinel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error {
		calls++
		return os.ErrPermission
	}
	defer func() { spawnWarmup = oldSpawn }()
	requestWarmup(dir)
	if calls != 1 {
		t.Fatalf("stale sentinel calls = %d, want 1", calls)
	}
	b, err := os.ReadFile(sentinel)
	if err != nil || string(b) == "1\n" {
		t.Fatalf("failed warmup sentinel = %q, err=%v", b, err)
	}
}

func TestHookMissingManifestRequestsWarmup(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	dir := filepath.Join(t.TempDir(), "index")
	t.Setenv("DEJA_INDEX_DIR", dir)
	called := false
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { called = true; return nil }
	defer func() { spawnWarmup = oldSpawn }()
	if digest, sessions := hookDigestResult(index.DefaultDir()); digest != "" || sessions != 0 {
		t.Fatalf("missing-manifest digest = %q, sessions=%d", digest, sessions)
	}
	if !called {
		t.Fatal("missing manifest did not request warmup")
	}
}

func TestHookContextMissingManifestStaysSilent(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index"))
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	defer func() { spawnWarmup = oldSpawn }()
	if err := runHookContext(index.DefaultDir(), true); err != nil {
		t.Fatal(err)
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

func TestRequestWarmupBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "warm-index")
	spawned := 0
	oldSpawn := spawnWarmup
	spawnWarmup = func(exe, sentinel string) error { spawned++; return nil }
	defer func() { spawnWarmup = oldSpawn }()

	// Inside a warmup child: no recursion.
	t.Setenv("DEJA_WARMUP_SENTINEL", filepath.Join(dir, "warmup.sentinel"))
	requestWarmup(dir)
	if spawned != 0 {
		t.Fatal("warmup child must not spawn again")
	}
	t.Setenv("DEJA_WARMUP_SENTINEL", "")

	// First call spawns and leaves a sentinel.
	requestWarmup(dir)
	if spawned != 1 {
		t.Fatalf("spawned=%d", spawned)
	}
	// Second call within the retry window is suppressed by the sentinel.
	requestWarmup(dir)
	if spawned != 1 {
		t.Fatalf("sentinel did not suppress: spawned=%d", spawned)
	}
	// A stale sentinel is replaced and spawns again.
	stale := time.Now().Add(-2 * warmupRetryAfter).UnixNano()
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"), []byte(strconv.FormatInt(stale, 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requestWarmup(dir)
	if spawned != 2 {
		t.Fatalf("stale sentinel not replaced: spawned=%d", spawned)
	}
	// A corrupt sentinel is also replaced.
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	requestWarmup(dir)
	if spawned != 3 {
		t.Fatalf("corrupt sentinel not replaced: spawned=%d", spawned)
	}
	// The index command clears the sentinel it was born with.
	sentinel := filepath.Join(dir, "warmup.sentinel")
	t.Setenv("DEJA_WARMUP_SENTINEL", sentinel)
	clearWarmupSentinel()
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("sentinel not cleared")
	}
}
