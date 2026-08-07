package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// warmup was the one build path that returned the raw error: a read-only
// index directory came back as `open /…/idx.lock: permission denied`, an
// internal lock file and a syscall, while `index` and `search` on the same
// directory said what to change (#798).
func TestWarmupNamesTheDirectoryWhenTheIndexCannotBeWritten(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	parent := filepath.Join(tmp, "ro")
	idx := filepath.Join(parent, "idx")
	if err := os.MkdirAll(idx, 0o700); err != nil {
		t.Fatal(err)
	}
	// The build stages beside the index, so the parent is what has to deny it.
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	t.Setenv("DEJA_INDEX_DIR", idx)

	_, err := captureRun(t, "warmup")
	if err == nil {
		t.Fatal("warmup on an unwritable index directory succeeded")
	}
	got := err.Error()
	if strings.Contains(got, ".lock") || strings.Contains(got, "permission denied") {
		t.Errorf("leaks the lock file and the syscall: %q", got)
	}
	if !strings.Contains(got, "cannot write the index at "+idx) {
		t.Errorf("does not name the index directory: %q", got)
	}
}

// doctor is what someone runs when memory looks absent. On a location that
// cannot be written it answered "not built (run `deja warmup`)" — advice that
// fails the same way, so the reader learns nothing.
func TestDoctorSaysTheIndexLocationIsNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	parent := filepath.Join(tmp, "ro")
	idx := filepath.Join(parent, "idx")
	if err := os.MkdirAll(idx, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	t.Setenv("DEJA_INDEX_DIR", idx)

	out, err := captureRun(t, "doctor", "--offline")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "not built (run `deja warmup`)") {
		t.Errorf("sends the reader to a command that cannot run here:\n%s", out)
	}
	if !strings.Contains(out, parent+" is not writable") {
		t.Errorf("does not name the unwritable directory:\n%s", out)
	}
}
