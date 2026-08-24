package main

import (
	"strings"
	"testing"
)

// A lone mistyped flag was reported as an unknown target, and the remedy then
// handed the reader thirty-eight harness names when what they needed was the
// flag list. The same binary already said "unknown flag" when a real target
// came with it — the guard from #1078 only ran at two arguments or more
// (#1680).
func TestInstallNamesALoneUnknownFlagAsAFlag(t *testing.T) {
	hermeticEnv(t)
	for _, arg := range []string{"--nosuch", "--al", "-all"} {
		_, err := captureRun(t, "install", arg)
		if err == nil {
			t.Fatalf("install accepted %q", arg)
		}
		msg := err.Error()
		if strings.Contains(msg, "unknown target") {
			t.Errorf("install %s is reported as a target: %s", arg, msg)
		}
		if !strings.Contains(msg, "unknown flag") {
			t.Errorf("install %s does not name the flag: %s", arg, msg)
		}
	}
	// The control: a real mistyped target still gets the target treatment.
	_, err := captureRun(t, "install", "claude_code")
	if err == nil {
		t.Fatal("install accepted claude_code")
	}
	if !strings.Contains(err.Error(), `did you mean "claude-code"`) {
		t.Errorf("a mistyped target lost its suggestion: %s", err)
	}
}

// One target refused, and the sentence said "fix what each one reports".
func TestInstallRefusalReadsSingularAtOne(t *testing.T) {
	hermeticEnv(t)
	_, err := captureRun(t, "install", "nosuch")
	if err == nil {
		t.Fatal("install accepted nosuch")
	}
	if strings.Contains(err.Error(), "each one") {
		t.Errorf("one refusal is reported in the plural: %s", err)
	}
	if !strings.Contains(err.Error(), "1 target refused") {
		t.Errorf("the count itself is wrong: %s", err)
	}
}
