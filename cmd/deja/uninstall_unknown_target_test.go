package main

import (
	"strings"
	"testing"
)

// install refuses a target it does not know and names the near miss; uninstall
// printed nothing at all and exited 0, so removing deja by a half-remembered
// name read as done while the wiring stayed (#2273).
func TestUninstallRefusesATargetItDoesNotKnow(t *testing.T) {
	withTempStores(t)

	for _, target := range []string{"nosuchagent", "claude-cod"} {
		out, err := captureRun(t, "uninstall", target)
		if err == nil {
			t.Errorf("uninstall %s succeeded, printing %q", target, out)
			continue
		}
		if !strings.Contains(err.Error(), target) {
			t.Errorf("uninstall %s said %v — it should name what it did not recognise", target, err)
		}
	}
	// The near miss is named, as install does.
	if _, err := captureRun(t, "uninstall", "claude-cod"); err == nil ||
		!strings.Contains(err.Error(), "claude-code") {
		t.Errorf("uninstall claude-cod does not point at claude-code: %v", err)
	}
	// A target it does know still uninstalls, whether or not anything is wired.
	if _, err := captureRun(t, "uninstall", "claude-code", "--no-index"); err != nil {
		t.Errorf("uninstall claude-code: %v", err)
	}
}
