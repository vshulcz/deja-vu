package main

import (
	"strings"
	"testing"
)

// A repeated selector used to take the last one and drop the first without a
// word, so a command meant to delete two sessions deleted one and exited 0 —
// leaving behind a session the reader believes is gone (#2271).
func TestForgetRefusesARepeatedSelector(t *testing.T) {
	withTempStores(t)
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"forget", "--session", "a1b2", "--session", "c3d4", "--dry-run"},
		{"forget", "--project", "alpha", "--project", "beta", "--dry-run"},
		{"forget", "--before", "30d", "--before", "10d", "--dry-run"},
	} {
		out, err := captureRun(t, args...)
		if err == nil {
			t.Errorf("%v was accepted, printing %q", args[1:], out)
			continue
		}
		if !strings.Contains(err.Error(), "twice") {
			t.Errorf("%v said %v — the message should say the flag was given twice", args[1:], err)
		}
	}

	// One of each still works, including the combination that narrows.
	if _, err := captureRun(t, "forget", "--session", "a1b2", "--dry-run"); err != nil {
		t.Errorf("a single --session: %v", err)
	}
	if _, err := captureRun(t, "forget", "--project", "alpha", "--before", "30d", "--dry-run"); err != nil {
		t.Errorf("--project with --before: %v", err)
	}
}
