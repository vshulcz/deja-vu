package main

import (
	"strings"
	"testing"
)

// `deja help` listed eleven of the thirty-one install targets, so the README's
// own `deja install aider` and `deja install openclaw-auto` read as invalid —
// and the "install needs a target" refusal sent people to that same short list
// (#1106). Help is now built from installTargetNames, and this holds it there.
func TestHelpNamesEveryInstallTarget(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t, "help")
	if err != nil {
		t.Fatal(err)
	}
	names := installTargetNames()
	if len(names) == 0 {
		t.Fatal("no install targets, wrong fixture")
	}
	for _, n := range names {
		if !strings.Contains(out, n) {
			t.Errorf("`deja help` does not name install target %q:\n%s", n, out)
		}
	}
	// The refusal points at help; help must not point back with a shorter list.
	for _, n := range []string{"aider", "openclaw-auto", "roo", "copilot", "goose"} {
		if !strings.Contains(out, n) {
			t.Errorf("target %q named in the README is missing from help", n)
		}
	}
}

// The listing is wrapped, so a name must not be split across a line break: a
// reader copying `openclaw-auto` out of help has to get the whole word.
func TestHelpTargetListWraps(t *testing.T) {
	names := installTargetNames()
	got := wrapTargets(names, "      ", 76)
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 76 {
			t.Errorf("target line is %d columns wide: %q", len(line), line)
		}
		if !strings.HasPrefix(line, "      ") {
			t.Errorf("target line is not indented: %q", line)
		}
	}
	flat := strings.Join(strings.Fields(strings.ReplaceAll(got, ",", " ")), " ")
	for _, n := range names {
		if !strings.Contains(" "+flat+" ", " "+n+" ") {
			t.Errorf("target %q did not survive wrapping: %q", n, got)
		}
	}
}
