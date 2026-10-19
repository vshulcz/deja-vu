package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `deja index` from cron runs the incremental pass, and it said "index is up
// to date" about a store with a transcript it could not read; only --rebuild
// named it (#5393). Every run says so while the path stays locked.
func TestIncrementalIndexNamesAPathItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny reads here")
	}
	hermeticEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	seedClaude(t, claude, "app", "s1", "the pgbouncer pool kept timing out", "we fixed the retry")
	seedClaude(t, claude, "app", "s2", "the redis pool too", "same fix")
	if out, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("first build: %v\n%s", err, out)
	}
	locked := filepath.Join(claude, "-app", "s1.jsonl")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	// doctor names it before any pass has: it opened only the newest file.
	if out, errOut := captureBoth(t, "doctor"); !strings.Contains(out+errOut, "permission denied on "+locked) {
		t.Errorf("doctor does not name the locked transcript:\n%s%s", out, errOut)
	}
	for _, run := range []string{"the run that finds it", "the run after"} {
		out, _ := captureRunStderr(t, "index")
		if !strings.Contains(out, "claude: 1 path could not be read") {
			t.Errorf("%s does not name the unreadable path:\n%s", run, out)
		}
	}
}
