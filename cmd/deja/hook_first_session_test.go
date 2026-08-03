package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The session that asks for the rebuild is the one that hears nothing about
// it: the warmup child has not written its first progress line yet. That is
// the first session after an upgrade or a damaged store — the moment deja most
// looks broken (#878).
func TestTheSessionThatAsksForTheBuildIsTold(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	// Never the real thing: on Windows a detached child holds deja.test.exe
	// open and `go test` fails to remove it after the run.
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
	dir := os.Getenv("DEJA_INDEX_DIR")
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// A build was just requested and has published nothing yet.
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(warmupStatusPath(dir))
	// Nothing to recall for this cwd, which is the state the message is for.
	t.Setenv("CLAUDE_PROJECT_DIR", filepath.Join(tmp, "elsewhere"))

	out, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output is not JSON: %q", out)
	}
	if !strings.Contains(resp.SystemMessage, "indexing your history") {
		t.Errorf("the session that asked for the build was told nothing: %q", out)
	}

	// The very first build speaks too: requiring a manifest left a machine
	// with ten thousand transcripts and no index yet in silence (#909). The
	// line appears once and is true; the next session has a manifest.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "indexing your history") {
		t.Errorf("the first build said nothing: %q", out)
	}
}
