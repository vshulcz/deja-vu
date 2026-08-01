package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A rebuild recreates the index directory, so a reader can land in a window
// where the manifest does not exist and get the syscall for it — a moment
// mistaken for a broken store (#822).
func TestRebuildWindowErrorNamesTheRebuild(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	missing := &fs.PathError{Op: "open", Path: filepath.Join(dir, "manifest.gob"), Err: fs.ErrNotExist}

	// Nobody holding the lock: the error is what it says it is.
	if got := rebuildWindowError(missing); got != error(missing) {
		t.Errorf("with no rebuild running: %v", got)
	}

	// A rebuild running: the same error is that window.
	old := rebuildInProgress
	rebuildInProgress = func(string) bool { return true }
	got := rebuildWindowError(missing)
	if !strings.Contains(got.Error(), "being rebuilt") {
		t.Errorf("during a rebuild: %v", got)
	}
	if strings.Contains(got.Error(), "manifest.gob") {
		t.Errorf("the internal file is still named: %v", got)
	}

	// Any other failure passes through untouched — including while a rebuild
	// is running, which is the case that catches a check on the wrong half.
	other := errors.New("i/o error")
	rebuildInProgress = func(string) bool { return true }
	defer func() { rebuildInProgress = old }()
	if got := rebuildWindowError(other); got != other {
		t.Errorf("an unrelated error was rewritten during a rebuild: %v", got)
	}
}
