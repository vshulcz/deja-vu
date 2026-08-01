package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The warmup child is spawned with stdout on /dev/null, a character device
// that passes for a terminal — so the live display took over the sink that
// publishes progress and every "memory is on its way" line had nothing to
// read (#862).
func TestWarmupPublishesStatusWhenStdoutLooksLikeATerminal(t *testing.T) {
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"pool exhausted"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(chats, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	t.Setenv("DEJA_WARMUP_SENTINEL", filepath.Join(tmp, "sentinel"))

	saved := logoWanted
	logoWanted = func(*os.File) bool { return true }
	t.Cleanup(func() { logoWanted = saved })

	dir := filepath.Join(tmp, "idx")
	var published bool
	err := withWarmupStatus(dir, func() error {
		return withBuildProgress(func() error {
			if err := index.Ensure(dir, "", false, nil); err != nil {
				return err
			}
			// Still inside withWarmupStatus, so the file has not been
			// cleaned up yet: what a reader would have seen mid-build.
			_, err := os.Stat(warmupStatusPath(dir))
			published = err == nil
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !published {
		t.Error("no warmup status was published while the index was building")
	}
	// And it is cleaned up: a status left behind tells every later session
	// that memory is still coming when the build finished long ago.
	if _, err := os.Stat(warmupStatusPath(dir)); !os.IsNotExist(err) {
		t.Errorf("warmup status survived the build: %v", err)
	}
}
