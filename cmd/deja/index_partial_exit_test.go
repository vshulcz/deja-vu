package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A run that wrote the index but left part of its input unread exited 0, the
// same as a clean one, so cron could not tell them apart (#5396).
func TestIndexExitsThreeWhenItLeftInputUnread(t *testing.T) {
	hermeticEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	seedClaude(t, claude, "app", "s1", "the pgbouncer pool kept timing out", "we fixed the retry")
	seedClaude(t, claude, "app", "s2", "the redis pool too", "same fix")
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("a clean build: %v", err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("a clean run with nothing new: %v", err)
	}

	// A line it could not use, in what this run read.
	f, err := os.OpenFile(filepath.Join(claude, "-app", "s2.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{not json\n")
	_ = f.Close()
	if _, err := captureRunStderr(t, "index"); !isExitStatus(err, exitPartial) {
		t.Fatalf("a run that skipped a line returned %v, want exit status %d", err, exitPartial)
	}
	// The next run read nothing new, so it skipped nothing.
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("a run after the skipped line returned %v", err)
	}

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	locked := filepath.Join(claude, "-app", "s1.jsonl")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	for _, run := range []string{"the run that finds it", "the run after"} {
		if _, err := captureRunStderr(t, "index"); !isExitStatus(err, exitPartial) {
			t.Fatalf("%s with an unreadable transcript returned %v, want exit status %d", run, err, exitPartial)
		}
	}
	// --quiet says nothing about success; the status still says partial.
	if _, err := captureRunStderr(t, "index", "--quiet"); !isExitStatus(err, exitPartial) {
		t.Fatalf("--quiet returned %v, want exit status %d", err, exitPartial)
	}
	if err := os.Chmod(locked, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatalf("readable again, the run returned %v", err)
	}
}

func isExitStatus(err error, code int) bool { //nolint:unparam // the code is what it checks for
	var s exitStatus
	return errors.As(err, &s) && int(s) == code
}

// Piped or under cron there is no live display: one line per phase, with
// counts, so a long run can be told from a stuck one (#5396).
func TestIndexPrintsItsPhasesWhenNotOnATerminal(t *testing.T) {
	hermeticEnv(t)
	// hermeticEnv marks the run a warmup, which publishes its phases to the
	// status file instead.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	saved := logoWanted
	logoWanted = func(*os.File) bool { return false }
	t.Cleanup(func() { logoWanted = saved })
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	seedClaude(t, claude, "app", "s1", "the pgbouncer pool kept timing out", "we fixed the retry")
	out, err := captureRunStderr(t, "index", "--rebuild")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"deja: finding transcripts",
		"deja: reading sessions — 1 file",
		"deja: indexing messages — 2 messages",
		"deja: writing index",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q line:\n%s", strings.TrimSpace(want), out)
		}
	}
	if strings.Count(out, "deja: finding transcripts") != 1 {
		t.Errorf("a phase printed more than once:\n%s", out)
	}
	quiet, _ := captureRunStderr(t, "index", "--rebuild", "--quiet")
	if strings.Contains(quiet, "reading sessions") {
		t.Errorf("--quiet printed phases:\n%s", quiet)
	}
}
