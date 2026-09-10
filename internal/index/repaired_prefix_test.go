package index

import "testing"

// The commands come out of a transcript with the shell prompt some harnesses
// store in front of them, and with it in place `$` was the program of every
// command ever recorded — so the same-program test compared `$` with `$` and
// passed for any two lines. On a real store 21 of the 163 repaired answers ran
// a different program and three were `cd` somewhere else.
func TestAPromptPrefixDoesNotMakeEveryCommandTheSameProgram(t *testing.T) {
	if repairedVariant("$ go test ./cmd/deja -run TestPasses", "$ rm cmd/deja/zz_probe_test.go; go test ./cmd/deja") {
		t.Error("removing a probe file counts as a repair of a failing test")
	}
	if repairedVariant("$ cd ~/one-place && make check", "$ cd ~/another-place && make check") {
		t.Error("going somewhere else counts as a repair")
	}
	// And the shape the bound was chosen for still holds with the prompt there.
	if !repairedVariant(`$ grep -rn "quokkabloom" --include=*.go internal`, `$ grep -rn "quokkabloom" --include="*.go" internal`) {
		t.Error("the corrected glob is no longer a repair")
	}
}

// Dropping the wrapper the shell could not find is the repair for the wall this
// machine hits most: 19 sessions have run into a missing `timeout`.
func TestDroppingAMissingWrapperIsARepair(t *testing.T) {
	if !repairedVariant("$ timeout 12 launchctl kickstart -k system/dev.example.svc",
		"$ launchctl kickstart -k system/dev.example.svc") {
		t.Error("the same command without the missing wrapper is not recognised as the repair")
	}
	if got := commandProgram(commandTokens("$ timeout 90 ssh -o ConnectTimeout=10 host uptime")); got != "ssh" {
		t.Errorf("the program behind the wrapper is %q", got)
	}
	// A duration is what timeout takes; a command that happens to be named
	// after it is still the program.
	if got := commandProgram(commandTokens("$ timeout --help")); got != "timeout" {
		t.Errorf("timeout with no command after it is %q", got)
	}
}
