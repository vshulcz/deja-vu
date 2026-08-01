package main

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An index left behind by an older format answers every prompt with nothing,
// which is indistinguishable from a user with no history — and neither hook
// asked for the rebuild that would fix it (#777).
func TestStaleFormatHooksAskForARebuild(t *testing.T) {
	hermeticEnv(t)
	// hermeticEnv suppresses the request entirely; the point here is whether
	// the hooks ask for one at all, so let it through and count the spawn
	// instead of starting a real child.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	spawned := 0
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { spawned++; return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
	dir := filepath.Join(t.TempDir(), "idx")
	writeStaleFormatIndex(t, dir)
	if !index.HasManifest(dir) {
		t.Fatal("fixture has no manifest")
	}
	if index.IsCurrentVersion(dir) {
		t.Fatal("fixture claims the current format version")
	}

	if err := runHookPromptMode(dir, strings.NewReader(`{"session_id":"s","prompt":"zeppelin throttle regulator"}`), &bytes.Buffer{}, false); err != nil {
		t.Fatal(err)
	}
	if spawned != 1 {
		t.Errorf("hook-prompt requested %d rebuilds on a stale-format index, want 1", spawned)
	}
	if err := os.Remove(filepath.Join(dir, "warmup.sentinel")); err != nil {
		t.Fatal(err)
	}

	// A cached digest returns before the guard inside hookDigestResult, so the
	// request has to happen ahead of the cache read — and the cached digest is
	// the user's own history, so it must still be served.
	cwd, _ := os.Getwd()
	writeHookCache(dir, cwd, "an earlier digest", 1, 10, nil)
	if d, _, _, _ := cachedHookDigest(dir); d == "" {
		t.Error("a cache hit stopped serving the user's own history")
	}
	if spawned != 2 {
		t.Errorf("a cache hit swallowed the rebuild request: %d spawns, want 2", spawned)
	}
}

// writeStaleFormatIndex writes what an upgrade leaves behind: a readable
// manifest whose format version is not this build's.
func writeStaleFormatIndex(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, v any) {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(v); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.gob", struct {
		Version int
		BuiltAt time.Time
	}{Version: 1, BuiltAt: time.Now()})
	write("sessions.gob", map[string]index.SessionMeta{})
}
