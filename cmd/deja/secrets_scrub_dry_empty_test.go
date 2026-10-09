package main

import (
	"bytes"
	"strings"
	"testing"
)

// A dry run where every file was skipped — the transcript still being written
// — ended "0 values in 0 files — `deja secrets --scrub` to rewrite them",
// sending the reader to rewrite nothing.
func TestScrubDryRunWithNothingToRewriteSaysSo(t *testing.T) {
	var out bytes.Buffer
	printScrub(&out, []scrubOutcome{{
		target:  scrubTarget{path: "/tmp/x/s.jsonl", found: 1},
		skipped: "still being written",
	}}, nil, true)
	got := out.String()
	if strings.Contains(got, "to rewrite them") || !strings.Contains(got, "nothing would be rewritten") {
		t.Fatalf("dry run with nothing to rewrite said:\n%s", got)
	}
}
