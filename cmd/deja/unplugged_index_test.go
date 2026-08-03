package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A volume that was ejected takes its mount point with it, and creating that
// point back fails with EPERM on macOS — so the errno said permissions while
// the disk was simply gone, and doctor called the index missing and told the
// reader to rebuild it onto a path that is not there (#931).
func TestAnUnpluggedIndexDiskIsNotPermissionsOrAMissingIndex(t *testing.T) {
	tmp := hermeticEnv(t)
	// The parent stands in for the mount point: it is not there at all.
	dir := filepath.Join(tmp, "gone-volume", "index.db")

	err := ensureError(dir, os.ErrPermission)
	if err == nil || !strings.Contains(err.Error(), "is not there") || !strings.Contains(err.Error(), "unmounted") {
		t.Errorf("a vanished mount point was reported as: %v", err)
	}

	var out bytes.Buffer
	doctorIndex(&out, doctorComponent{State: "missing", Path: dir}, dir)
	if !strings.Contains(out.String(), "not reachable") {
		t.Errorf("doctor on an unreachable index:\n%s", out.String())
	}
	if strings.Contains(out.String(), "run `deja warmup`") {
		t.Errorf("doctor told the reader to rebuild onto a path that is not there:\n%s", out.String())
	}

	// A directory that is there and merely locked is still a permissions
	// problem, and an index that is simply absent is still "not built".
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	real := filepath.Join(tmp, "here", "index.db")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureError(real, os.ErrPermission); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Errorf("a locked directory stopped being a permissions problem: %v", err)
	}
	out.Reset()
	doctorIndex(&out, doctorComponent{State: "missing", Path: real}, real)
	if !strings.Contains(out.String(), "not built") {
		t.Errorf("an absent index stopped saying so:\n%s", out.String())
	}
}
