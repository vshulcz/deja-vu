package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The hook's digest is computed from the index, so refreshing it off a stale
// index re-served the same snapshot: a session that reversed an earlier
// decision stayed invisible to the agent for as long as the user went without
// running the CLI (#913).
func TestHookRefreshRebuildsTheIndexBeforeTheDigest(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, ts, text string) []byte {
		return []byte(`{"type":"user","message":{"role":"user","content":"` + text + `"},"timestamp":"` + ts + `","sessionId":"` + sid + `","cwd":"/proj"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(store, "old.jsonl"), line("old", "2026-06-01T10:00:00Z", "poll interval set to 30 seconds"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// The store moves on after the build: the decision above is reversed.
	if err := os.WriteFile(filepath.Join(store, "new.jsonl"), line("new", "2026-07-25T10:00:00Z", "reverted, the poll interval is back to 5 seconds"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := index.SessionCount(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before != 1 {
		t.Fatalf("index holds %d sessions before the refresh, want 1", before)
	}

	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")
	runHookRefresh(dir)

	after, err := index.SessionCount(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after != 2 {
		t.Errorf("index holds %d sessions after the refresh, want 2 — the reversal is still invisible", after)
	}
}
