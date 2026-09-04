package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The first session start that finds a manifest says what the build found,
// once; the next one does not (#3073).
func TestTheFirstSessionAfterTheBuildHearsWhatWasIndexed(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	q := `{"type":"user","message":{"role":"user","content":"why does the retry loop drop the last attempt"},"timestamp":"2026-03-01T10:00:00Z","sessionId":"%s","cwd":"/proj"}`
	for _, id := range []string{"s1", "s2"} {
		line := strings.Replace(q, "%s", id, 1)
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")

	read := func() string {
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
		return resp.SystemMessage
	}
	first := read()
	if !strings.Contains(first, "deja indexed 2 sessions from 1 agent") {
		t.Fatalf("the first session after the build was not told what was indexed: %q", first)
	}
	if !strings.Contains(first, "1 question asked more than once") {
		t.Fatalf("the repeat count is missing: %q", first)
	}
	if second := read(); strings.Contains(second, "deja indexed") {
		t.Fatalf("the built note repeated: %q", second)
	}
}

func TestTheBuiltNoteNeverReachesTheModel(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
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
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")
	out, err := captureRun(t, "hook-context", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "deja indexed") {
		t.Fatalf("the built note went into the model's context:\n%s", out)
	}
}
