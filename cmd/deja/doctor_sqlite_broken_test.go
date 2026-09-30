package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A sqlite3 that is on PATH and prints nothing made doctor say "sqlite3 found"
// above an opencode row that parsed to zero. It now names the binary as broken
// in the tools line, the JSON component, and the store's own state.
func TestDoctorNamesABrokenSQLite3(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	problem := "sqlite3 at " + stub + " did not answer a probe query (no output)"

	var tools strings.Builder
	doctorTools(&tools)
	if !strings.Contains(tools.String(), "found but not working") || !strings.Contains(tools.String(), "warning      "+problem) {
		t.Errorf("tools line:\n%s", tools.String())
	}
	if c := doctorSQLite3(); c.State != "broken" || c.Path != stub || c.Error != problem {
		t.Errorf("json component = %+v", c)
	}

	var store doctorStore
	for _, check := range doctorStoreChecks() {
		if check.name == "opencode" {
			store, _ = inspectDoctorStore(check)
		}
	}
	if store.State != "needs-sqlite3" || store.Skipped != problem {
		t.Fatalf("opencode store = %+v", store)
	}
	var warn strings.Builder
	printDoctorStoreWarnings(&warn, []doctorStore{store})
	if !strings.Contains(warn.String(), "opencode store needs a working sqlite3 CLI: "+problem) {
		t.Errorf("warning:\n%s", warn.String())
	}
	if got := doctorSQLiteDetail(db, false); !strings.Contains(got, "sqlite3 CLI not working") {
		t.Errorf("row detail = %q", got)
	}
}
