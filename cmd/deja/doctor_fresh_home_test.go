package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On a new machine ~/.cache is not there yet, and doctor read that as a disk
// that may have been unmounted, with the only absolute path in the section.
// It is an index that has not been built.
func TestDoctorOnAFreshHomeSaysNotBuiltNotUnmounted(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".cache", "deja")
	var out bytes.Buffer
	doctorIndex(&out, doctorIndexReport{State: "missing", Path: dir}, dir)
	got := out.String()
	if strings.Contains(got, "unmounted") || !strings.Contains(got, "not built") {
		t.Fatalf("fresh home reported as:\n%s", got)
	}
}

// A vanished mount point still reads as one, and its path is said the way the
// rest of the section says paths.
func TestDoctorUnreachableIndexUsesTheTildeForm(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// Under home, but below a directory that cannot be written: nothing deja
	// runs could create it, so it is not a fresh home.
	locked := filepath.Join(home, "mnt")
	if err := os.MkdirAll(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if dirWritable(locked) {
		t.Skip("cannot make a read-only directory here")
	}
	dir := filepath.Join(locked, "disk", "deja")
	var out bytes.Buffer
	doctorIndex(&out, doctorIndexReport{State: "missing", Path: dir}, dir)
	got := out.String()
	if !strings.Contains(got, "not reachable") || !strings.Contains(got, "~"+string(filepath.Separator)+filepath.Join("mnt", "disk")) {
		t.Fatalf("unreachable index reported as:\n%s", got)
	}
}
