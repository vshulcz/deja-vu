package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Every "memory is on its way" surface reads <index>.warmup, and the file did
// not exist until the build proper began — the walk over the stores comes
// first and, on a slow volume, takes the longest. Measured on 6000 sessions:
// first published at 0.76s before, 0.02s after (#1021).
func TestProgressIsPublishedBeforeTheFreshnessWalk(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")

	stop := publishBuildProgress(dir)
	if _, err := os.Stat(dir + ".warmup"); err != nil {
		t.Fatalf("nothing published before the walk: %v", err)
	}
	if st := readWarmupStatus(dir); st == nil {
		t.Error("the published file does not read back as a build in flight")
	}
	stop()
	if _, err := os.Stat(dir + ".warmup"); err == nil {
		t.Error("the progress file outlived the command")
	}

	// Without the sentinel this is an ordinary foreground run and publishes
	// nothing, as before.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	stop = publishBuildProgress(dir)
	if _, err := os.Stat(dir + ".warmup"); err == nil {
		t.Error("a foreground run published a warmup status")
	}
	stop()
}

// The walk itself reports, so the phase name is not "starting" for the seconds
// it takes on a network volume (#1021).
func TestTheStoreWalkReportsItsPhase(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"a session"},"timestamp":"2026-08-01T10:00:00Z","sessionId":"s","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &phaseRecorder{}
	index.SetProgress(p)
	t.Cleanup(func() { index.SetProgress(nil) })
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	if !p.seen("finding transcripts") {
		t.Errorf("the store walk published no phase: %v", p.phases)
	}
}

type phaseRecorder struct{ phases []string }

func (p *phaseRecorder) Phase(name string, _ int) { p.phases = append(p.phases, name) }
func (p *phaseRecorder) Advance(int)              {}
func (p *phaseRecorder) Harness(string, int, int) {}
func (p *phaseRecorder) seen(name string) bool {
	for _, s := range p.phases {
		if s == name {
			return true
		}
	}
	return false
}
