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

// Cherry Studio's row named a relative placeholder when the app was not
// installed, and a relative path has no disk to lose: walked up from the
// working directory it read as `unplugged` on every machine without the app.
// No row on a bare home may say it, whatever its location looks like.
func TestDoctorCallsNoStoreOnABareHomeUnplugged(t *testing.T) {
	tmp := hermeticEnv(t)
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	// hermeticEnv points zed at a root outside the home; give it its parent
	// so the row reads as an uninstalled store, which is what it is here.
	if err := os.MkdirAll(filepath.Join(tmp, "zed"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	var out bytes.Buffer
	doctorHarnesses(&out, filepath.Join(tmp, "index.db"))
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "unplugged") {
			t.Errorf("row on a bare home: %q", line)
		}
	}
	if row := harnessRow(t, out.String(), "cherrystudio"); !strings.Contains(filepath.ToSlash(row), "CherryStudio/Data/Agents/.claude") {
		t.Errorf("cherrystudio row does not name the app's default store: %q", row)
	}
	if storeDiskGone(filepath.Join("CherryStudio", "Data", "Agents", ".claude")) {
		t.Error("a relative location was called unplugged")
	}
}
