package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The advice line named permissions unconditionally, so on a full disk it sent
// the reader to chmod a file that was already writable (#808).
func TestForgetNamesTheCauseItCouldNotClearTheTitle(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"permission", fs.ErrPermission, "fix that file's permissions"},
		{"no space", syscall.ENOSPC, "free some space on that filesystem"},
		{"anything else", errors.New("i/o error"), "clear the problem above"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := forgetTitleFix(tc.err); got != tc.want {
				t.Errorf("fix = %q, want %q", got, tc.want)
			}
		})
	}
}

// A failed rewrite must not damage the notes file. The leftover temp file the
// same failure used to leave behind needs a full filesystem to reproduce, so
// it is verified by hand rather than here (#808).
func TestForgetPromotedTitlesKeepsTheNotesFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	line := `{"kind":"promoted","session":"claude:s1","project":"p","title":"acme yard job","text":"t","src":"claude","state":"accepted","ts":"2026-07-01T10:00:00Z"}`
	if err := os.WriteFile(notes, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A read-only directory: the rewrite cannot create its temp file at all.
	// This used to put a directory where the temp file would go, which worked
	// while the temp name was derived from the destination — it is unique per
	// writer now, so two `deja remember` calls cannot land on one name (#1319).
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop a write here")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := sources.ForgetPromotedTitles(func(src string) bool { return src == "claude:s1" }); err == nil {
		t.Fatal("the rewrite reported success")
	}
	after, err := os.ReadFile(notes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "acme yard job") {
		t.Error("the notes file lost content on a failed rewrite")
	}
}
