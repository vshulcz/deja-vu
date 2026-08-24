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
