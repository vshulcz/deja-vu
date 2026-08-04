package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The one state an agent cannot ask a human about: the index is not there yet
// because it is being built. The tool call failed with `manifest: open
// /…/manifest.gob: no such file or directory` — an internal path and an errno,
// handed to a model as a broken tool (#972).
func TestMCPSaysTheIndexIsBeingBuiltInsteadOfFailing(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A build asked for just now, nothing published yet.
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := buildingNowForAgent(dir)
	if !strings.Contains(got, "indexing") || !strings.Contains(got, "ask again") {
		t.Errorf("an agent calling recall mid-build is told: %q", got)
	}

	// With a real index in place the answer comes from the index, not from
	// this: a hand-made manifest is not one, which is what HasManifest checks.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"a session"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := buildingNowForAgent(dir); got != "" {
		t.Errorf("a built index still claims to be building: %q", got)
	}
}
