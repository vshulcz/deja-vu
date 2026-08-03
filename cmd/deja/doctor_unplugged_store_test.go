package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A store whose disk went away is not a store that was deleted, and "missing"
// on a row of transcripts reads as the second thing (#933). What separates the
// two is how much of the way there is gone: a harness that was never installed
// loses one directory and its home is right there.
func TestDoctorTellsAnUnpluggedStoreFromAnUninstalledHarness(t *testing.T) {
	tmp := hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}

	// Never installed: one level missing under a home that is there.
	if storeDiskGone(filepath.Join(home, ".kimi-code", "sessions")) {
		t.Error("an uninstalled harness was called unplugged")
	}
	// Present: nothing missing at all.
	present := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(present, 0o755); err != nil {
		t.Fatal(err)
	}
	if storeDiskGone(present) {
		t.Error("a store that is there was called unplugged")
	}
	// Ejected volume: the whole chain is gone.
	if !storeDiskGone(filepath.Join(tmp, "gone-volume", "home", ".claude", "projects")) {
		t.Error("a store on a vanished volume was not called unplugged")
	}

	// And the row says it.
	t.Setenv("HOME", filepath.Join(tmp, "gone-volume", "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "gone-volume", "home", ".claude", "projects"))
	var out bytes.Buffer
	doctorHarnesses(&out, filepath.Join(tmp, "index.db"))
	row := harnessRow(t, out.String(), "claude")
	if !strings.Contains(row, "unplugged") {
		t.Errorf("row for a store on a vanished volume: %q", row)
	}
}
