package main

import (
	"strings"
	"testing"
)

// The command takes nothing, and --json exists on seven of its neighbours — a
// script that reached for it here got the tab-separated table back and parsed
// it as JSON (#747).
func TestSourcesRejectsArguments(t *testing.T) {
	withTempStores(t)
	for _, arg := range []string{"--json", "--nosuchflag", "extra", "-j"} {
		_, err := captureRun(t, "sources", arg)
		if err == nil {
			t.Errorf("%q was accepted", arg)
			continue
		}
		if !strings.Contains(err.Error(), "takes no arguments") {
			t.Errorf("%q: %v", arg, err)
		}
	}
	// With nothing to reject it still prints the table.
	out, err := captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude") {
		t.Errorf("sources printed %q", out)
	}
}
