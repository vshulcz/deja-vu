package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// `deja bench` builds its corpus under .deja-bench/run-* in the working
// directory — the user's repo — removes the run tree on success and never
// touches the parent. So a ^C leaves megabytes there that nothing ever looks at
// again, and a clean run still leaves an empty directory behind (#2560).
func TestBenchSweepsWhatEarlierRunsLeft(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	work := t.TempDir()
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(work, ".deja-bench")
	stale := filepath.Join(parent, "run-interrupted")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "corpus.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	// A run that started a minute ago is somebody else's, still going.
	fresh := filepath.Join(parent, "run-live")
	if err := os.MkdirAll(fresh, 0o700); err != nil {
		t.Fatal(err)
	}

	dir, err := benchmarkTempDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the interrupted run's tree is still there: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a live run's tree was swept: %v", err)
	}

	// And the parent goes when this run's own tree is the last thing in it.
	if err := os.RemoveAll(fresh); err != nil {
		t.Fatal(err)
	}
	releaseBenchTempDir(dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the run tree survived: %v", err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf(".deja-bench was left in the working directory: %v", err)
	}
}

// A parent still holding another run's tree stays where it is.
func TestBenchKeepsTheParentWhileAnotherRunIsInIt(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	work := t.TempDir()
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	dir, err := benchmarkTempDir()
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(dir)
	other := filepath.Join(parent, "run-other")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	releaseBenchTempDir(dir)
	if _, err := os.Stat(parent); err != nil {
		t.Errorf("the parent was removed while another run was in it: %v", err)
	}
}
