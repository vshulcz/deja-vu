package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bench writes its corpus under the working directory, and the ignore rule
// covers paths rather than configuration — so a run from a covered tree indexes
// its corpus and then finds none of it. Every arm reads zero, which is what a
// dead surface looks like: `deja bench prompt` printed 0 of 13 real questions
// from a directory under `~/.claude/jobs/`, and 13 of 13 from one beside it.
func TestBenchRefusesToMeasureACorpusItCannotSee(t *testing.T) {
	work := filepath.Join(t.TempDir(), "home", ".claude", "jobs", "job-1", "tmp")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	// t.Chdir rather than a cleanup of our own: cleanups run last-registered
	// first, so a manual restore would race t.TempDir's removal.
	t.Chdir(work)

	got, err := benchmarkTempDir()
	if err == nil {
		t.Fatalf("the bench prepared %q inside a tree the ignore rule hides", got)
	}
	for _, want := range []string{"ignore rule", "read zero"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if entries, rerr := os.ReadDir(work); rerr == nil && len(entries) > 0 {
		t.Errorf("a refused run still left %d entries behind", len(entries))
	}
}

// Anywhere else it prepares the run tree as before.
func TestBenchPreparesItsRunTreeElsewhere(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	got, err := benchmarkTempDir()
	if err != nil {
		t.Fatalf("bench refused an ordinary directory: %v", err)
	}
	if !strings.HasPrefix(got, work) {
		t.Errorf("run tree %q is not under the working directory %q", got, work)
	}
}
