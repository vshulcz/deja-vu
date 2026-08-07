package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

var sgrRe = regexp.MustCompile("\x1b\\[([0-9;]*)m")

// sgrOpenAtEnd reports whether the line leaves a colour or attribute switched
// on. Anything still open bleeds into the lines that follow it.
func sgrOpenAtEnd(line string) bool {
	open := false
	for _, m := range sgrRe.FindAllStringSubmatch(line, -1) {
		open = m[1] != "0" && m[1] != ""
	}
	return open
}

func TestStatHarnessTagClosesItsEscapes(t *testing.T) {
	for _, h := range []string{"claude", "codex", "opencode", "cursor", "gemini", "aider", "antigravity", "other"} {
		tag := statHarnessTag(h, true)
		if !strings.Contains(tag, "["+h+"]") {
			t.Fatalf("%s: tag lost its name: %q", h, tag)
		}
		if sgrOpenAtEnd(tag) {
			t.Fatalf("%s: tag ends with an attribute still open: %q", h, tag)
		}
	}
}

// The two stats lines that embed a harness tag: the counts after it and the
// line under it used to render bold because the tag re-armed bold with no
// closer.
func TestStatsHarnessLinesLeaveNothingOpen(t *testing.T) {
	byHarness := fmt.Sprintf("  %s%s %4d sessions  %5d messages", statHarnessTag("claude", true), strings.Repeat(" ", 7), 1, 24)
	longest := fmt.Sprintf("  Longest session  %d messages · %s · %s", 24, statHarnessTag("claude", true), "retry budget reset")
	for _, line := range []string{byHarness, longest} {
		if sgrOpenAtEnd(line) {
			t.Fatalf("line ends with an attribute still open: %q", line)
		}
	}
}
