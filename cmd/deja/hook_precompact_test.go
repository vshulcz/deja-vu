package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withHookStdin(t *testing.T, payload string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; _ = r.Close() })
}

func TestHookContextCompactLead(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "idx"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"compact survivor fact"}}`,
	})
	if err := run([]string{"index"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/tmp/p")

	withHookStdin(t, `{"source":"compact","session_id":"x"}`)
	out := captureStdout(t, func() { _ = runHookContext(true) })
	if !strings.Contains(out, "Context was just compacted") {
		t.Fatalf("compact lead missing: %q", out)
	}
	if !strings.Contains(out, "✓ recalled from claude session") {
		t.Fatalf("provenance marker missing: %q", out)
	}

	withHookStdin(t, `{"source":"startup"}`)
	out = captureStdout(t, func() { _ = runHookContext(true) })
	if strings.Contains(out, "Context was just compacted") || !strings.Contains(out, "recent history") {
		t.Fatalf("startup lead wrong: %q", out)
	}

	// Malformed stdin must not break the hook.
	withHookStdin(t, `{not json`)
	out = captureStdout(t, func() { _ = runHookContext(true) })
	if out == "" {
		t.Fatal("hook produced nothing on malformed stdin")
	}
}

func TestRunHookPrecompactParsesAndGuards(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "idx"))
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	spawned := 0
	oldSpawn := spawnWarmup
	spawnWarmup = func(exe, sentinel string) error { spawned++; return nil }
	defer func() { spawnWarmup = oldSpawn }()

	payload, _ := json.Marshal(map[string]any{"session_id": "abc", "trigger": "auto"})
	withHookStdin(t, string(payload))
	runHookPrecompact()
	if spawned != 1 {
		t.Fatalf("spawned=%d, want 1", spawned)
	}
	// Malformed stdin still triggers the guarded warmup path.
	withHookStdin(t, "garbage")
	runHookPrecompact()
	if spawned != 1 {
		t.Fatalf("sentinel must suppress the second spawn, got %d", spawned)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}
