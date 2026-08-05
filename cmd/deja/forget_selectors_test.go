package main

import (
	"strings"
	"testing"
)

// Forgetting 100 sessions one id at a time took 10.5s against 0.2s for one
// `--project` call, and the bare refusal named no selector at all — so the
// expensive way was the discoverable one (#1022).
func TestForgetRefusalNamesItsSelectors(t *testing.T) {
	hermeticEnv(t)
	_, err := captureRun(t, "forget")
	if err == nil {
		t.Fatal("forget with no selector was accepted")
	}
	for _, want := range []string{"--session", "--project", "--before", "--dry-run", "--list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %s: %v", want, err)
		}
	}
	// And it must not advertise a selector forget does not have.
	for _, absent := range []string{"--query", "--harness"} {
		if strings.Contains(err.Error(), absent) {
			t.Errorf("the refusal offers %s, which forget does not accept: %v", absent, err)
		}
	}
}
