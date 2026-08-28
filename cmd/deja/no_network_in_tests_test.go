package main

import (
	"strings"
	"sync/atomic"
	"testing"
)

// offlineLookupCalls counts the times a test took the stubbed version lookup
// TestMain installs. It is the evidence that the doctor path did not reach
// api.github.com, which the suite did eleven times per run (#2206).
var offlineLookupCalls atomic.Int64

// offlineLookup is what TestMain puts in doctorLookup's place: the answer a
// machine with no answer gives, which is the state doctor already handles.
func offlineLookup() (string, bool) {
	offlineLookupCalls.Add(1)
	return "", false
}

// Removing the stub from TestMain puts the network back under every test that
// runs doctor, and this is what notices: the counter stays where it was.
func TestDoctorDoesNotReachTheNetwork(t *testing.T) {
	hermeticEnv(t)
	before := offlineLookupCalls.Load()
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: this really ran the report, so a lookup that did not happen
	// is a lookup that went somewhere else.
	if !strings.Contains(out, "Harness stores:") {
		t.Fatalf("doctor printed something else entirely:\n%s", out)
	}
	if got := offlineLookupCalls.Load(); got == before {
		t.Errorf("doctor asked for the latest version without going through the stub, so it asked GitHub")
	}
}
