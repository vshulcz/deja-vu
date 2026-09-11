package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A packet over its budget used to drop the conclusions first and keep the
// command list. A resuming agent can re-run a command; it cannot re-derive what
// the last session settled, which is the whole reason the packet exists.
// Measured on the local model with an over-budget packet: conclusions-first
// answered the question 3 of 3 against 1 of 3.
func TestAnOverBudgetPacketKeepsItsConclusions(t *testing.T) {
	c := model.CompactionContext{
		Objective: model.ContextFact{Text: "stop the exporter from retrying without a pause"},
	}
	// Enough of each to go well past the 24 KB cap: forty of each was inside it,
	// so nothing shrank and the test proved nothing.
	for i := range 120 {
		c.Conclusions = append(c.Conclusions, model.ContextFact{
			Text: "the backoff counted from zero, so the fourth attempt went out immediately (" +
				strings.Repeat("detail ", 40) + string(rune('a'+i%26)) + ")",
		})
		c.Tests = append(c.Tests, model.ContextTest{
			Command: "go test ./internal/exporter/ -run TestBackoff -count=1 " + strings.Repeat("-v ", 40) + string(rune('a'+i%26)),
		})
	}
	bounded := boundCompactionContext(c)
	if len(bounded.Conclusions) == 0 {
		t.Fatalf("the packet kept %d commands and no conclusion at all", len(bounded.Tests))
	}
	if len(bounded.Tests) >= len(bounded.Conclusions) {
		t.Errorf("the command list (%d) was not what shrank; conclusions left: %d",
			len(bounded.Tests), len(bounded.Conclusions))
	}

	// And the render budget cuts from the bottom, so the conclusions have to be
	// printed before the commands to survive a tight one.
	out := RenderCompactionContext(bounded, 700)
	conclusions := strings.Index(out, "conclusions")
	commands := strings.Index(out, "go test ./internal/exporter/")
	if conclusions < 0 {
		t.Errorf("a 700-byte packet carried no conclusion:\n%s", out)
	}
	if commands >= 0 && commands < conclusions {
		t.Errorf("the command list was printed ahead of the conclusions:\n%s", out)
	}
}
