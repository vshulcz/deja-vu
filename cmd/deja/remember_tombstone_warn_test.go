package main

import (
	"strings"
	"testing"
	"time"
)

// A day-note the reader forgot keeps its tombstone until unforget, so a later
// `remember` on the same day and project writes into a note that stays hidden.
// remember now names the restore command instead of reporting a silent success;
// an ordinary remember stays quiet.
func TestRememberWarnsWhenTheDayNoteIsTombstoned(t *testing.T) {
	hermeticEnv(t)
	dayNote := "deja-" + time.Now().Local().Format("2006-01-02") + "-p"

	if _, err := captureRunStderr(t, "remember", "first thing", "--project", "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", dayNote); err != nil {
		t.Fatal(err)
	}
	warn, err := captureRunStderr(t, "remember", "second thing", "--project", "p")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn, "stays hidden") || !strings.Contains(warn, "unforget deja:"+dayNote) {
		t.Errorf("remember into a forgotten day-note did not warn:\n%s", warn)
	}

	quiet, err := captureRunStderr(t, "remember", "clean thing", "--project", "fresh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet, "stays hidden") {
		t.Errorf("remember into a fresh project warned about a tombstone it has none of:\n%s", quiet)
	}
}
