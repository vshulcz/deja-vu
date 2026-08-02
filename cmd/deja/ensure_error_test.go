package main

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A full disk arrived as `ensure: write /…/index.db.tmp/records.bin: no space
// left on device` — an internal path nobody can act on, and the same shape
// #798 replaced for permissions (#888).
func TestEnsureErrorNamesWhatToFix(t *testing.T) {
	dir := filepath.Join("/tmp", "store", "index.db")

	full := ensureError(dir, fmt.Errorf("write %s: %w", filepath.Join(dir+".tmp", "records.bin"), syscall.ENOSPC))
	got := full.Error()
	if !strings.Contains(got, "no space left where the index is built") || !strings.Contains(got, filepath.Dir(dir)) {
		t.Errorf("full disk: %q", got)
	}
	if strings.Contains(got, "records.bin") || strings.Contains(got, ".tmp") {
		t.Errorf("full disk names an internal path: %q", got)
	}

	denied := ensureError(dir, fmt.Errorf("mkdir: %w", fs.ErrPermission)).Error()
	if !strings.Contains(denied, "check the directory's permissions") || !strings.Contains(denied, dir) {
		t.Errorf("permission: %q", denied)
	}

	// Anything else still comes through whole rather than being guessed at.
	other := errors.New("some other failure")
	if got := ensureError(dir, other).Error(); !strings.Contains(got, "some other failure") {
		t.Errorf("unknown cause was swallowed: %q", got)
	}
}
