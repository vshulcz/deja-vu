package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A volume that was unmounted fails with EACCES on macOS, because deja is then
// trying to create a directory under /Volumes — and "check that file's
// permissions" sends the reader after a problem they do not have (#907).
func TestAVanishedDirectoryIsNotAPermissionProblem(t *testing.T) {
	tmp := hermeticEnv(t)
	gone := filepath.Join(tmp, "not-mounted", "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", gone)

	err := notesWriteError(os.ErrPermission)
	if err == nil {
		t.Fatal("no error")
	}
	got := err.Error()
	if !strings.Contains(got, "is not there") || !strings.Contains(got, "unmounted") {
		t.Errorf("a missing directory was described as: %q", got)
	}
	if strings.Contains(got, "check that file and its directory's permissions") {
		t.Errorf("still sends the reader after permissions: %q", got)
	}

	// A directory that is there keeps the permission wording.
	live := filepath.Join(tmp, "live")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(live, "notes.jsonl"))
	if got := notesWriteError(os.ErrPermission).Error(); !strings.Contains(got, "permissions") {
		t.Errorf("a live directory lost the permission wording: %q", got)
	}
	if notesWriteError(nil) != nil {
		t.Error("a nil error was turned into one")
	}
	if !dirExists(live) || dirExists(filepath.Join(tmp, "nope")) {
		t.Error("dirExists disagrees with the filesystem")
	}
}
