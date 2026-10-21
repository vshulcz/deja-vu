//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// stallStub serves a Hermes store through a sqlite3 on PATH whose message
// read DEJA_TEST_SQLITE steers: ok answers, stall prints a row and stops,
// fail exits 1.
func stallStub(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
*"json_object('deja',1)"*) echo '{"deja":1}'; exit 0;;
*"from messages"*"order by session_id"*) ;;
*) exit 0;;
esac
row() { printf '{"session_id":"hs%s","role":"user","content":"stallneedle turn %s","timestamp":%s}\n' "$1" "$1" "$((1790000000+$1))"; }
case "$DEJA_TEST_SQLITE" in
fail) echo "Error: disk I/O error" >&2; exit 1;;
stall) row 1; exec sleep 30;;
*) for i in 1 2 3; do row $i; done;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "sqlite3"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	sources.ResetSQLite3Probe()
	t.Cleanup(sources.ResetSQLite3Probe)
	// Where Hermes keeps it, so the run files the path under Hermes.
	hermes := t.TempDir()
	if err := os.WriteFile(filepath.Join(hermes, "state.db"), []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_HERMES_HOME", hermes)
	t.Setenv("DEJA_HERMES_DB", "")
	t.Setenv("DEJA_STORE_TIMEOUT", "300ms")
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	saved := logoWanted
	logoWanted = func(*os.File) bool { return false }
	t.Cleanup(func() { logoWanted = saved })
}

// A store given up for making no progress, or whose read failed, was left out
// of a first build with no line about it and exit 0: 1,683 Hermes sessions
// missing from an index of 16k, and nothing to tell (#5517). It is named, the
// run exits 3, and doctor names it.
func TestIndexNamesAStoreItGaveUpOnAndExitsThree(t *testing.T) {
	for _, c := range []struct{ mode, line string }{
		{"stall", "deja: hermes: skipped — no progress for 300ms; DEJA_STORE_TIMEOUT=0 waits"},
		{"fail", "deja: hermes: nothing indexed — 1 path could not be read"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			hermeticEnv(t)
			stallStub(t)
			t.Setenv("DEJA_TEST_SQLITE", c.mode)
			out, err := captureRunStderr(t, "index")
			if !isExitStatus(err, exitPartial) {
				t.Fatalf("a build that left Hermes unread returned %v, want exit status %d\n%s", err, exitPartial, out)
			}
			for _, want := range []string{c.line, "deja: hermes: 1 path could not be read"} {
				if !strings.Contains(out, want) {
					t.Errorf("index output lacks %q:\n%s", want, out)
				}
			}
			doc, _ := captureRun(t, "doctor")
			if !strings.Contains(doc, "hermes: 0 unusable lines skipped, 1 path unreadable") {
				t.Errorf("doctor does not name the unread store:\n%s", doc)
			}
		})
	}
}

// A rebuild that cannot read a store keeps what the last build held from it:
// the store was skipped, not emptied. Its row is left out, so the next pass
// reads it again.
func TestARebuildKeepsTheSessionsOfAStoreItCouldNotRead(t *testing.T) {
	for _, mode := range []string{"stall", "fail"} {
		t.Run(mode, func(t *testing.T) {
			hermeticEnv(t)
			stallStub(t)
			t.Setenv("DEJA_TEST_SQLITE", "ok")
			if out, err := captureRunStderr(t, "index"); err != nil {
				t.Fatalf("the first build: %v\n%s", err, out)
			}
			t.Setenv("DEJA_NO_AUTO_INDEX", "1")
			if out, _ := captureRun(t, "search", "stallneedle"); !strings.Contains(out, "stallneedle turn 3") {
				t.Fatalf("the first build did not index the store:\n%s", out)
			}
			t.Setenv("DEJA_TEST_SQLITE", mode)
			out, err := captureRunStderr(t, "index", "--rebuild")
			if !isExitStatus(err, exitPartial) {
				t.Fatalf("the rebuild returned %v, want exit status %d\n%s", err, exitPartial, out)
			}
			if strings.Contains(out, "no longer in") {
				t.Errorf("a store it could not read is called emptied:\n%s", out)
			}
			for _, n := range []string{"1", "2", "3"} {
				if got, _ := captureRun(t, "search", "stallneedle"); !strings.Contains(got, "stallneedle turn "+n) {
					t.Errorf("turn %s is gone after a rebuild that could not read its store:\n%s", n, got)
				}
			}
			t.Setenv("DEJA_TEST_SQLITE", "ok")
			if out, err := captureRunStderr(t, "index"); err != nil || !strings.Contains(out, "updated 1 file") {
				t.Errorf("the next pass did not read the store again: %v\n%s", err, out)
			}
		})
	}
}
