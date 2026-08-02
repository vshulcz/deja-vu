package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The heading is separated from what came before by the leading newline of the
// section, which assumes the file ends with one. A hand-written file whose last
// line has no newline — a list, most often — got the heading glued to it (#871).
func TestExportAppendsAfterAFileWithNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	glued := filepath.Join(dir, "nonl.md")
	if err := os.WriteFile(glued, []byte("# Notes\n\n- a line without a newline"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := exportPromoted(glued, "pool exhausted", "raised it", "claude:s1", "accepted", time.Time{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(glued)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "a line without a newline\n\n## pool exhausted") {
		t.Errorf("heading is not separated from the file's last line:\n%q", string(got))
	}

	// A file that does end with a newline keeps exactly one blank line.
	normal := filepath.Join(dir, "norm.md")
	if err := os.WriteFile(normal, []byte("# Notes\n\ntext\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := exportPromoted(normal, "pool exhausted", "raised it", "claude:s1", "accepted", time.Time{}); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(normal)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "text\n\n## pool exhausted") || strings.Contains(string(got), "text\n\n\n") {
		t.Errorf("normal append changed shape:\n%q", string(got))
	}
}

// A failed export said "open …: no such file or directory" and nothing else,
// so a reader whose note had already been written was told their decision was
// lost (#871).
func TestExportFailureSaysTheNoteIsKept(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission behaviour differs here")
	}
	dir := t.TempDir()
	for _, tc := range []struct{ path, reason string }{
		{filepath.Join(dir, "nope", "out.md"), "its directory does not exist"},
		{dir, "that path is a directory"},
	} {
		_, err := exportPromoted(tc.path, "t", "b", "claude:s1", "accepted", time.Time{})
		if err == nil {
			t.Fatalf("%s: export succeeded", tc.path)
		}
		if got := exportFailureReason(err); got != tc.reason {
			t.Errorf("%s: reason = %q, want %q", tc.path, got, tc.reason)
		}
	}
	ro := filepath.Join(dir, "ro.md")
	if err := os.WriteFile(ro, []byte("x\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	_, err := exportPromoted(ro, "t", "b", "claude:s1", "accepted", time.Time{})
	if err == nil {
		t.Fatal("export into a read-only file succeeded")
	}
	if got := exportFailureReason(err); got != "permission denied" {
		t.Errorf("reason = %q", got)
	}
}
