package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
	// A directory in the temp file's place: the write cannot succeed.
	if err := os.Mkdir(notes+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
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
