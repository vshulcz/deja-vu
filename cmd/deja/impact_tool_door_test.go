package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// A machine served only through the PreToolUse hook: no counter named that
// door, and the bytes line wanted a raw size the tool event never records, so
// the report opened and printed two zeros about 945 bytes of served lines
// (#2309).
func TestImpactNamesTheToolDoor(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	for i := 0; i < 4; i++ {
		usage.RecordResult(dir, usage.KindTool, 236, 1, false)
	}

	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "4") {
		t.Errorf("the report never counts the four tool lines:\n%s", got)
	}
	if !strings.Contains(got, "944 B") && !strings.Contains(got, "945 B") {
		t.Errorf("the report never says what was served:\n%s", got)
	}
}

// The ratio line still reads as before when both sides are there.
func TestImpactKeepsTheRatioWhenRawIsKnown(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	usage.RecordServedSessions(dir, usage.KindRecall, 1000, 2, false, 8000, []string{"a", "b"})

	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "8× less") {
		t.Errorf("the distilled ratio is gone:\n%s", got)
	}
}
