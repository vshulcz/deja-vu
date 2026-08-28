package main

import (
	"strings"
	"testing"
)

// brief threw its arguments away at the dispatch table, so `deja brief --json`
// printed the human screen and exited 0 — the shape #2253 fixed for friction
// and restore (#2265).
func TestBriefRefusesArgumentsItDoesNotTake(t *testing.T) {
	withTempStores(t)

	// The premise: it still prints the screen when asked for nothing.
	out, err := captureRun(t, "brief")
	if err != nil || !strings.Contains(out, "deja-vu") {
		t.Fatalf("bare brief: %q err=%v", out, err)
	}

	for _, arg := range []string{"--json", "extra"} {
		out, err := captureRun(t, "brief", arg)
		if err == nil {
			t.Errorf("brief %s was accepted and printed %q", arg, out)
			continue
		}
		if !strings.Contains(err.Error(), arg) {
			t.Errorf("brief %s said %v — the message should name what it could not take", arg, err)
		}
	}
}
