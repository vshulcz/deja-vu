package main

import (
	"strings"
	"testing"
)

// A restore that restored nothing exited 0, so `deja forget --unforget "$id" &&
// echo back` printed "back" for a typo, for an id that was never forgotten, and
// for a session that is still gone (#2263).
func TestUnforgetRefusesWhenItRestoredNothing(t *testing.T) {
	withTempStores(t)

	out, err := captureRun(t, "forget", "--unforget", "nothing-like-this")
	if err == nil {
		t.Errorf("a prefix matching no tombstone succeeded, printing %q", out)
	}
	if err != nil && !strings.Contains(err.Error(), "no tombstone matches") {
		t.Errorf("the refusal reads %q — it should still say what it could not find", err)
	}

	// The dry run reports the same miss and answers the same way.
	out, err = captureRun(t, "forget", "--unforget", "nothing-like-this", "--dry-run")
	if err == nil {
		t.Errorf("a dry run against no tombstone succeeded, printing %q", out)
	}
}
