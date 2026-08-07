package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// "Unplugged" is a claim about the disk, and it was made for every store
// buried more than two levels under a home that is right there: goose, cline,
// pi and deja's own notes read as a vanished volume on any machine that never
// installed them. Cursor's row never said anything else — its location is two
// roots joined for display, which is no path to walk up from.
func TestDoctorDoesNotCallADeepUninstalledStoreUnplugged(t *testing.T) {
	tmp := hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join(home, ".local", "share", "goose", "sessions"),
		filepath.Join(home, ".cline", "data", "sessions"),
		filepath.Join(home, ".pi", "agent", "sessions"),
		filepath.Join(home, ".local", "share", "deja", "notes.jsonl"),
	} {
		if storeDiskGone(path) {
			t.Errorf("an uninstalled store was called unplugged: %s", path)
		}
	}
	// Cursor hands doctor two roots joined with ", " — not a path, but still
	// under the home that is there.
	if storeDiskGone(doctorCursorLocation()) {
		t.Error("cursor's joined location was called unplugged")
	}

	var out bytes.Buffer
	doctorHarnesses(&out, filepath.Join(tmp, "index.db"))
	for _, name := range []string{"goose", "cline", "pi", "cursor", "deja"} {
		if row := harnessRow(t, out.String(), name); strings.Contains(row, "unplugged") {
			t.Errorf("row for an uninstalled %s store: %q", name, row)
		}
	}
}
