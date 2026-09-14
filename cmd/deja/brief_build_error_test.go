package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `deja` with nothing indexed builds the index first, and a build it cannot run
// reported the syscall: `open /…/index.db.lock: permission denied` — an internal
// lock file nobody can act on, on the first screen a new install shows. Every
// other command routes the same failure through ensureError.
func TestBriefNamesWhyABuildCouldNotRun(t *testing.T) {
	// The refusal is produced by a directory mode, and a mode is not how
	// windows refuses a write: 0500 there leaves the directory writable and the
	// build succeeds, so the test measured the platform rather than the message.
	if runtime.GOOS == "windows" {
		t.Skip("directory modes do not refuse writes on windows")
	}
	tmp := hermeticEnv(t)
	ro := filepath.Join(tmp, "ro")
	if err := os.MkdirAll(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(ro, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })

	err := runBrief(dir, io.Discard)
	if err == nil {
		t.Fatal("a build that could not run was reported as a success")
	}
	msg := err.Error()
	if strings.Contains(msg, ".lock") || strings.Contains(msg, "permission denied") {
		t.Errorf("the refusal is still the syscall: %v", err)
	}
	if !strings.Contains(msg, "permissions") || !strings.Contains(msg, "DEJA_INDEX_DIR") {
		t.Errorf("the refusal does not say what to change: %v", err)
	}
}
