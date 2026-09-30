package index

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A sqlite3 on PATH that prints nothing read the opencode store as empty and
// the index pass said nothing about it. It is now skipped by name, is not
// recorded as a tool the index had, and repairing it counts as gaining one.
func TestIndexNamesABrokenSQLite3(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub")
	}
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	db := filepath.Join(home, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	stub := filepath.Join(bin, "sqlite3")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	sources.ResetSQLite3Probe()
	t.Cleanup(sources.ResetSQLite3Probe)

	var out bytes.Buffer
	loadProgress("opencode", &out)
	want := "deja: opencode: skipped — sqlite3 at " + stub + " did not answer a probe query (no output)"
	if !strings.Contains(out.String(), want) {
		t.Errorf("index narration:\n%s\nwant %q", out.String(), want)
	}

	if fp := mergedToolFingerprint(""); !strings.Contains(fp, "sqlite3=false") {
		t.Errorf("broken sqlite3 recorded as present: %s", fp)
	}
	if toolsChanged(Manifest{ToolFingerprint: toolFingerprint(false, true)}) {
		t.Error("a broken sqlite3 read as a tool newly gained")
	}
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho '{\"deja\":1}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sources.ResetSQLite3Probe()
	if !toolsChanged(Manifest{ToolFingerprint: toolFingerprint(false, true)}) {
		t.Error("a repaired sqlite3 did not ask for a pass")
	}
}
