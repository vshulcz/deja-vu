package main

import (
	"strconv"
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

// The list is the one block in help whose layout is computed, and it was
// computed to a fixed 76 columns — the same six lines came out of a 30-column
// pane and a 120-column one, while the brief, files, restore and search all
// tracked the terminal (#1660).
func TestHelpTargetListFollowsTheTerminal(t *testing.T) {
	hermeticEnv(t)
	for _, width := range []int{40, 120} {
		t.Setenv("COLUMNS", strconv.Itoa(width))
		out, err := captureRun(t, "help")
		if err != nil {
			t.Fatal(err)
		}
		var widest int
		for _, line := range strings.Split(out, "\n") {
			if !strings.HasPrefix(line, "      ") || !strings.Contains(line, "claude-code") && !strings.Contains(line, ",") {
				continue
			}
			if !isTargetLine(line) {
				continue
			}
			if n := len(line); n > widest {
				widest = n
			}
			if len(line) > width {
				t.Errorf("COLUMNS=%d: target line is %d columns: %q", width, len(line), line)
			}
		}
		if widest == 0 {
			t.Fatalf("COLUMNS=%d: found no target lines in help", width)
		}
		// A wide pane must actually use it, or "fits" is met by never growing.
		if width == 120 && widest <= 76 {
			t.Errorf("COLUMNS=120: widest target line is %d columns, still laid out for 76", widest)
		}
	}
}

// isTargetLine reports whether an indented help line is part of the install
// target listing rather than some other indented block.
func isTargetLine(line string) bool {
	fields := strings.Split(strings.TrimSpace(line), ", ")
	if len(fields) < 2 {
		return false
	}
	known := map[string]bool{}
	for _, n := range installTargetNames() {
		known[n] = true
	}
	for _, f := range fields {
		if !known[strings.TrimSuffix(f, ",")] {
			return false
		}
	}
	return true
}
