package index

import (
	"path/filepath"
	"testing"
)

// The same index has to answer the same way twice. last_error was whichever
// failing file Go's map iteration reached last, so `doctor --json` reported a
// different error each run and a script diffing the report saw a change where
// nothing changed (#2245).
func TestTheReportedErrorIsTheSameOnEveryRun(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	proj := filepath.Join(root, "projects", "-work-a")
	files := map[string]FileIngest{
		filepath.Join(proj, "aaa.jsonl"): {Error: "open aaa.jsonl: permission denied"},
		filepath.Join(proj, "bbb.jsonl"): {Error: "unexpected end of JSON input"},
		filepath.Join(proj, "ccc.jsonl"): {Error: "open ccc.jsonl: is a directory"},
	}

	first := healthFromFiles(files)["claude"]
	// The premise: three failures folded into one store, or one answer proves
	// nothing.
	if first.FailedFiles != 3 {
		t.Fatalf("%d failed files counted, want 3", first.FailedFiles)
	}
	for i := 0; i < 200; i++ {
		if got := healthFromFiles(files)["claude"].LastError; got != first.LastError {
			t.Fatalf("run %d reported %q where the first run reported %q", i+2, got, first.LastError)
		}
	}
	// And it is the one the field claims: the first failing path, so two
	// machines reading the same index quote the same file.
	if want := "open aaa.jsonl: permission denied"; first.LastError != want {
		t.Errorf("reported %q, want the first failing path's error %q", first.LastError, want)
	}
}
