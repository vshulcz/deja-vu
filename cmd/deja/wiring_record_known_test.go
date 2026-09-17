package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The record is written from the arguments, before any of them has been
// through installTarget, so a refused name was kept beside the ones that
// worked — and every repair afterwards retried it, failed, and counted it in
// the line a person sees at session start (#3695).
func TestAMistypedTargetIsNotRecorded(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	// Refused, and the run reports it — what matters is what it leaves behind.
	_, _ = captureRun(t, "install", "nosuchtarget", "--no-index")
	if got := readWiringState().Targets; slices.Contains(got, "nosuchtarget") {
		t.Errorf("a target deja does not know was recorded: %v", got)
	}
	if got := readWiringState().Targets; !slices.Contains(got, "claude-code") {
		t.Errorf("the target that worked was dropped: %v", got)
	}
}

// And one already in a record written by an older build goes on the next
// install, rather than being retried until somebody edits the file by hand.
func TestARecordedTargetDejaDoesNotKnowIsDropped(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	st := readWiringState()
	st.Targets = append(st.Targets, "nosuchtarget")
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if got := readWiringState().Targets; slices.Contains(got, "nosuchtarget") {
		t.Errorf("an unknown target survived an install: %v", got)
	}
}
