package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The digest cache lives beside the index directory, not inside it, so wiping
// the index left the hook answering from a snapshot of a store that no longer
// exists — and asking for nothing, so it kept answering from that snapshot
// forever. Caches do get wiped: ~/.cache is fair game for cleanup tools and CI
// images (#874).
func TestHookAsksForARebuildWhenTheIndexIsGone(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	cwd := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	// hermeticEnv marks every test in this package as the warmup child, which
	// is exactly the process that must not ask for another warmup.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")

	entry := hookCacheEntry{At: time.Now(), CWD: cwd, Gate: hookGate(), Digest: "pool exhausted under load", Sessions: 1, Raw: 42}
	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookCachePath(dir, cwd), b, 0o600); err != nil {
		t.Fatal(err)
	}
	// No index at all: the directory the cache was built from is gone.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	clearWarmupSentinel()

	spawned := 0
	saved := spawnWarmup
	spawnWarmup = func(exe, sentinel string) error { spawned++; return nil }
	t.Cleanup(func() { spawnWarmup = saved })

	digest, sessions, _, _, _ := cachedHookDigest(dir)
	if !strings.Contains(digest, "pool exhausted") || sessions != 1 {
		t.Errorf("the cached digest was dropped rather than served: %q (%d sessions)", digest, sessions)
	}
	if spawned == 0 {
		t.Error("the hook served a store that is gone and asked for no rebuild")
	}

	// A healthy index asks for nothing: the sentinel exists so a session start
	// does not spawn a build on every hook (#825).
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	clearWarmupSentinel()
	spawned = 0
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	clearWarmupSentinel()
	cachedHookDigest(dir)
	if spawned != 0 {
		t.Errorf("a healthy index still asked for %d rebuild(s)", spawned)
	}
}
