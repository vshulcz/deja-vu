package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Profiling is off unless asked for, and asking for it must not be able to
// break the command: a bad path is a reason to say so on stderr and carry on
// indexing, not a reason to fail the run someone is trying to profile.
func TestProfilingWritesWhenAskedAndStaysOutOfTheWayOtherwise(t *testing.T) {
	dir := t.TempDir()

	t.Setenv("DEJA_CPUPROFILE", "")
	t.Setenv("DEJA_MEMPROFILE", "")
	stop := startProfiling()
	stop()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("profiling wrote %d files without being asked", len(entries))
	}

	cpu := filepath.Join(dir, "cpu.out")
	mem := filepath.Join(dir, "mem.out")
	t.Setenv("DEJA_CPUPROFILE", cpu)
	t.Setenv("DEJA_MEMPROFILE", mem)
	stop = startProfiling()
	// Something for the CPU profile to sample; an empty profile is still a
	// valid file, so the assertion below is only that both were written.
	sum := 0
	for i := 0; i < 200000; i++ {
		sum += i % 7
	}
	_ = sum
	stop()
	for _, path := range []string{cpu, mem} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(path), err)
		}
		if fi.Size() == 0 {
			t.Fatalf("%s is empty", filepath.Base(path))
		}
	}

	// A directory that does not exist is a typo, not a crash.
	t.Setenv("DEJA_CPUPROFILE", filepath.Join(dir, "no", "such", "cpu.out"))
	t.Setenv("DEJA_MEMPROFILE", filepath.Join(dir, "no", "such", "mem.out"))
	stop = startProfiling()
	stop()
}
