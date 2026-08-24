package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// DeepVerify compares per-session message counts and the structure around
// them; it does not compare content, so a same-length edit inside a record
// passes every check while the session becomes unreachable. The status line
// claimed more than that — "index matches sources — no memory lost" is what a
// reader takes to the bank when deciding whether to rebuild (#1712).
func TestDeepStatusSaysWhatItCompared(t *testing.T) {
	var buf bytes.Buffer
	doctorDeep(&buf, index.DeepReport{FilesChecked: 3, SessionsIndexed: 3, SampledFiles: 3, SampledPostings: 10})
	out := buf.String()
	if strings.Contains(out, "no memory lost") {
		t.Errorf("the status promises more than a count comparison can support:\n%s", out)
	}
	if !strings.Contains(out, "count") {
		t.Errorf("the status does not say what was compared:\n%s", out)
	}
}

// A clean report that sampled nothing must not claim a comparison happened:
// nothing is sampled when every source is stale, or when the sampled tokens
// carry no postings (found in review on #1713).
func TestDeepStatusDoesNotClaimAnUnsampledComparison(t *testing.T) {
	cases := []struct {
		name    string
		report  index.DeepReport
		mustNot string
		must    string
	}{
		{"nothing sampled at all", index.DeepReport{FilesChecked: 3, SessionsIndexed: 3},
			"matches its source", "nothing to compare"},
		{"no file re-parsed", index.DeepReport{FilesChecked: 3, SessionsIndexed: 3, SampledPostings: 10},
			"message count matches", "no source was in sync"},
		{"no posting carried", index.DeepReport{FilesChecked: 3, SessionsIndexed: 3, SampledFiles: 3},
			"postings resolve;", "no sampled token carried postings"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			doctorDeep(&buf, c.report)
			out := buf.String()
			if strings.Contains(out, c.mustNot) {
				t.Errorf("claims a comparison that did not happen:\n%s", out)
			}
			if !strings.Contains(out, c.must) {
				t.Errorf("does not say what was skipped:\n%s", out)
			}
		})
	}
}

// The control: a report with findings still leads with them, unchanged.
func TestDeepStatusStillLeadsWithFindings(t *testing.T) {
	var buf bytes.Buffer
	doctorDeep(&buf, index.DeepReport{
		FilesChecked: 3,
		Findings:     []index.DeepFinding{{Kind: "torn-log", Detail: "records.bin unreadable"}},
	})
	out := buf.String()
	if !strings.Contains(out, "torn-log") || !strings.Contains(out, "records.bin unreadable") {
		t.Errorf("a finding was dropped:\n%s", out)
	}
	if strings.Contains(out, "count") {
		t.Errorf("the clean-status wording leaked into a report with findings:\n%s", out)
	}
}
