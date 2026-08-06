package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A full disk arrived as `ensure: write /…/index.db.tmp/records.bin: no space
// left on device` — an internal path nobody can act on, and the same shape
// #798 replaced for permissions (#888).
func TestEnsureErrorNamesWhatToFix(t *testing.T) {
	// A directory that is there: one whose parent is gone means the disk was
	// unmounted, which is its own message (#931).
	dir := filepath.Join(t.TempDir(), "index.db")

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

// A volume that went away mid-write — an unmounted disk, a network share that
// dropped — arrived as `write /…/index.db.tmp/records.bin: input/output
// error`, which tells the reader neither that the disk is gone nor what to do
// (#899).
func TestEnsureErrorNamesAnUnreachableDisk(t *testing.T) {
	dir := filepath.Join("/Volumes", "somewhere", "index.db")
	for _, errno := range []syscall.Errno{syscall.EIO, syscall.ENXIO, syscall.ENODEV} {
		got := ensureError(dir, fmt.Errorf("write %s: %w", filepath.Join(dir+".tmp", "records.bin"), errno)).Error()
		if !strings.Contains(got, "not reachable") || !strings.Contains(got, filepath.Dir(dir)) {
			t.Errorf("%v: %q", errno, got)
		}
		if strings.Contains(got, "records.bin") {
			t.Errorf("%v names an internal path: %q", errno, got)
		}
	}
	// A full disk keeps its own answer: the room is there, it is just used up.
	full := ensureError(dir, fmt.Errorf("write x: %w", syscall.ENOSPC)).Error()
	if !strings.Contains(full, "no space left") {
		t.Errorf("full disk lost its message: %q", full)
	}
}

// A volume ejected cleanly takes the index directory with it but leaves the
// mount point, so the build fails with ENOENT rather than the EIO of #899.
// That fell through to `ensure: open /…/idx.tmp/buckets/tm.bin.tmp: no such
// file or directory` — an internal path, a syscall, and no word about the
// index still sitting there intact (#1068).
func TestEnsureErrorNamesAnEjectedVolume(t *testing.T) {
	mount := t.TempDir()
	gone := filepath.Join(mount, "idx")
	got := ensureError(gone, fmt.Errorf("open %s: %w", filepath.Join(gone+".tmp", "buckets", "tm.bin.tmp"), syscall.ENOENT)).Error()
	if !strings.Contains(got, "went away mid-build") || !strings.Contains(got, gone) {
		t.Errorf("ejected volume: %q", got)
	}
	if strings.Contains(got, "tm.bin.tmp") || strings.Contains(got, ".tmp/") {
		t.Errorf("ejected volume names an internal path: %q", got)
	}
	if !strings.Contains(got, "unharmed") {
		t.Errorf("ejected volume says nothing about the index that survived: %q", got)
	}

	// A missing file under an index directory that is still there is an
	// ordinary failure, not a disconnected disk: guessing at it would hide
	// every real ENOENT behind a story about unplugged drives.
	here := filepath.Join(t.TempDir(), "idx")
	if err := os.MkdirAll(here, 0o700); err != nil {
		t.Fatal(err)
	}
	still := ensureError(here, fmt.Errorf("open %s: %w", filepath.Join(here, "manifest.gob"), syscall.ENOENT)).Error()
	if strings.Contains(still, "went away mid-build") {
		t.Errorf("a present index directory was reported as unmounted: %q", still)
	}
}
