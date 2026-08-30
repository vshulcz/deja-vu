package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Measured on a real index: of 120 sessions read at the tool hook's 200-byte
// budget, 29 yielded no conclusion — and 16 of those had one, a sentence that
// did not fit. The rule is "whole sentences or nothing" (#1336), and the trade
// it made was silence where every other surface cuts and marks the cut (#2518).
func TestASingleConclusionIsCutRatherThanDropped(t *testing.T) {
	long := "DONE: restored the retry precedence so an exact-key failure retries once more, " +
		"which is what the ticket asked for and what the earlier change had quietly reversed " +
		"when it moved the check above the cache lookup instead of below it"
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "what did we settle on?"},
		{Role: "assistant", Text: long},
	}}

	got := Conclusions(s, 200, 1)
	if len(got) == 0 {
		t.Fatalf("a session whose conclusion is one long sentence answered with nothing")
	}
	if len(got[0]) > 200 {
		t.Errorf("the line is %d bytes against a 200-byte budget: %q", len(got[0]), got[0])
	}
	if !strings.HasSuffix(got[0], "…") {
		t.Errorf("the cut is not marked, so the line reads as a finished thought: %q", got[0])
	}
	if !strings.HasPrefix(got[0], "DONE: restored the retry precedence") {
		t.Errorf("the line does not start where the conclusion does: %q", got[0])
	}
}

// A caller asking for several keeps the old rule: there the cut marker would
// have text after it, which is the one thing the digest's own invariant forbids.
func TestSeveralConclusionsKeepWholeSentences(t *testing.T) {
	long := strings.Repeat("this sentence is long enough to overflow a small budget on its own. ", 3)
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "and?"},
		{Role: "assistant", Text: long},
		{Role: "assistant", Text: "we kept the pool change."},
	}}
	for _, line := range Conclusions(s, 60, 3) {
		if strings.HasSuffix(line, "…") {
			t.Errorf("a multi-line request produced a marked cut: %q", line)
		}
	}
}
