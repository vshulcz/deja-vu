package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stagingsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".deja-update-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func updateInto(t *testing.T, dir string) {
	t.Helper()
	destination := filepath.Join(dir, "deja")
	if err := os.WriteFile(destination, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installUpdateBinary(destination, []byte("new binary")); err != nil {
		t.Fatal(err)
	}
}

func strandStaging(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
	return p
}

// An update killed between staging and rename strands a binary's worth of bytes
// beside the real one, and every later update added another rather than
// clearing it (#1109).
func TestUpdateClearsStagingsLeftByAnInterruptedRun(t *testing.T) {
	dir := t.TempDir()
	strandStaging(t, dir, ".deja-update-123456", 48*time.Hour)
	updateInto(t, dir)
	if left := stagingsIn(t, dir); len(left) != 0 {
		t.Errorf("staging files survived a successful update: %v", left)
	}
}

// A staging file written moments ago may belong to an update running right now,
// and deleting it would break that run.
func TestUpdateLeavesAFreshStagingAlone(t *testing.T) {
	dir := t.TempDir()
	strandStaging(t, dir, ".deja-update-999999", time.Minute)
	updateInto(t, dir)
	if left := stagingsIn(t, dir); len(left) != 1 {
		t.Errorf("a staging file from a run in flight was touched: %v", left)
	}
}

// The sweep knows only its own litter: anything else beside the binary stays.
func TestUpdateSweepsOnlyItsOwnFiles(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(keep, old, old); err != nil {
		t.Fatal(err)
	}
	updateInto(t, dir)
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("a file that is not deja's was removed: %v", err)
	}
}
