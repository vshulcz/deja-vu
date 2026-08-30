package main

import (
	"strings"
	"testing"
)

// The help page is one hand-written string 148 columns wide, and nothing in it
// consults the terminal: twelve of its eighty-four lines wrap on a default
// 80-column one, and `deja last --help` quotes the widest of them (#1661).
func TestHelpFitsAnOrdinaryTerminal(t *testing.T) {
	for _, width := range []int{80, 100, 120} {
		wrapped := wrapUsage(usageText(), width)
		for _, line := range strings.Split(wrapped, "\n") {
			if len([]rune(line)) > width {
				t.Errorf("at %d columns a line is %d wide:\n%s", width, len([]rune(line)), line)
			}
		}
	}
}

// A wrapped usage line stays readable: the flags land under the command with a
// hanging indent, and a bracketed group is never split down the middle.
func TestAWrappedUsageLineKeepsItsGroupsWhole(t *testing.T) {
	line := "  deja last [n] [--json] [--project name] [--harness name] [--from macro] [--since 7d] [--before 2026-01-01] [--path p] [--tool t] [--full] [--stats]"
	out := wrapUsage(line, 80)
	rows := strings.Split(out, "\n")
	if len(rows) < 2 {
		t.Fatalf("the widest line in the page was not wrapped:\n%s", out)
	}
	if !strings.HasPrefix(rows[0], "  deja last ") {
		t.Errorf("the command left its own line: %q", rows[0])
	}
	for _, r := range rows[1:] {
		if !strings.HasPrefix(r, "      ") {
			t.Errorf("a continuation is not indented under the command: %q", r)
		}
	}
	for _, r := range rows {
		if strings.Count(r, "[") != strings.Count(r, "]") {
			t.Errorf("a bracketed group was split across lines: %q", r)
		}
	}
	// Nothing is lost or reordered: the words come back in the same order.
	if strings.Join(strings.Fields(out), " ") != strings.Join(strings.Fields(line), " ") {
		t.Errorf("wrapping changed the line:\n%s\n%s", line, out)
	}
}

// A terminal deja cannot measure — a pipe, a file — gets the page as written.
func TestAPipeGetsThePageAsWritten(t *testing.T) {
	if got := wrapUsage(usageText(), 0); got != usageText() {
		t.Error("the page was reflowed for a reader with no screen to fit it to")
	}
}

// `deja last --help` quotes the widest line on the page, so it is the one that
// wrapped worst.
func TestASingleCommandsHelpFitsToo(t *testing.T) {
	for _, name := range []string{"last", "search", "install", "sync"} {
		h := helpForCommand(name)
		if h == "" {
			t.Fatalf("no help for %q", name)
		}
		for _, line := range strings.Split(wrapUsage(h, 80), "\n") {
			if len([]rune(line)) > 80 {
				t.Errorf("%s --help has a line %d wide:\n%s", name, len([]rune(line)), line)
			}
		}
	}
}

// The wiring, not just the wrapper: `deja help` and `deja <cmd> --help` are
// the two surfaces that have to ask the terminal how wide it is.
func TestBothHelpSurfacesAreWrappedForTheTerminal(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	for _, args := range [][]string{{"help"}, {"last", "--help"}} {
		out, err := captureRun(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		for _, line := range strings.Split(out, "\n") {
			if len([]rune(line)) > 80 {
				t.Errorf("%v printed a line %d wide:\n%s", args, len([]rune(line)), line)
			}
		}
	}
}
