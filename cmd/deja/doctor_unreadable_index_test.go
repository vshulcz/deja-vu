package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A built index whose directory cannot be read (permissions, a restricted
// mount) was reported as "not built (run `deja warmup`)" — advice that fails
// the same way and hides a build sitting behind a closed door (#1116).
func TestDoctorNamesAnUnreadableIndex(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	dir := filepath.Join(t.TempDir(), "idx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file makes it a real, non-empty index directory.
	if err := os.WriteFile(filepath.Join(dir, "manifest.gob"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	var out bytes.Buffer
	doctorIndex(&out, doctorIndexReport{State: "missing", Path: dir}, dir)
	got := out.String()
	if !strings.Contains(got, "unreadable") || !strings.Contains(got, "permission denied") {
		t.Errorf("a blocked index was not named unreadable:\n%s", got)
	}
	// And what to do about it. doctor is the command someone runs to find out
	// why nothing is recalled, so the half that names the fix is the half that
	// earns the line — measured, it could be deleted with every test still
	// passing, because "unreadable" alone appears in more than one of doctor's
	// lines. The variable is the unambiguous part of that advice: "permission"
	// appears in the diagnosis too.
	if !strings.Contains(got, "DEJA_INDEX_DIR") {
		t.Errorf("the line does not say what to do about it:\n%s", got)
	}
	if strings.Contains(got, "not built (run `deja warmup`)") {
		t.Errorf("a blocked index was called not-built:\n%s", got)
	}
}

// The same shape one branch over: an index that is behind and cannot be
// written says so, and says what to do. Nothing covered that line either.
func TestDoctorNamesAnIndexItCannotWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorIndex(&out, doctorIndexReport{State: "stale-readonly", Path: dir, StaleStores: 2}, dir)
	got := out.String()

	if !strings.Contains(got, "cannot be written") {
		t.Errorf("a read-only index was not named:\n%s", got)
	}
	if !strings.Contains(got, "DEJA_INDEX_DIR") {
		t.Errorf("the line does not say what to do about it:\n%s", got)
	}
	// And it still says what is stale, or the reader cannot tell whether it
	// matters: two stores behind is a different sentence from none.
	if !strings.Contains(got, "2 stores") {
		t.Errorf("the line does not say how much is behind:\n%s", got)
	}
}
