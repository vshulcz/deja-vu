package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An -auto target writes several configs; the kept-snapshot line after an
// uninstall has to count every one of them, not only the file the folded
// result is named for.
func TestKeptSnapshotsCountEveryFileTheAutoTargetTouched(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "claude.json")
	b := filepath.Join(dir, "settings.json")
	for _, p := range []string{a + ".bak", b + ".bak"} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := wroteAll(installResult{Path: a, Action: "updated"}, installResult{Path: b, Action: "updated"})
	line := keptSnapshotsLine(r.touched())
	if !strings.Contains(line, "kept 2 snapshots") {
		t.Fatalf("line = %q", line)
	}
}
