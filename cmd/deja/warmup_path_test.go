package main

import (
	"os"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Everything the user sees about a cold build — the status bar line, the
// hook's systemMessage, the opencode toast, the MCP instructions note — reads
// one status file. If the detached build stops writing it, all of them go
// quiet at once and nothing else fails, so this pins the whole path rather
// than fileProgress in isolation.
func TestWarmupPublishesStatusDuringBuild(t *testing.T) {
	withTempStores(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	if dir == "" {
		t.Fatal("DEJA_INDEX_DIR not set by withTempStores")
	}
	t.Setenv("DEJA_WARMUP_SENTINEL", dir+"/warmup.sentinel")
	seen := false
	err := withWarmupStatus(dir, func() error {
		if err := index.Ensure(dir, "", true, os.Stderr); err != nil {
			return err
		}
		_, statErr := os.Stat(warmupStatusPath(dir))
		seen = statErr == nil
		return nil
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !seen {
		t.Fatal("no status file during the build: the status bar and hooks have nothing to report")
	}
	// And it must not outlive the build, or the agent claims forever that
	// memory is on its way.
	if _, err := os.Stat(warmupStatusPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("status file survived the build: %v", err)
	}
}
