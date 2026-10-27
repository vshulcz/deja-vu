package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/evidence"
)

// The frame names the user-rule exception only when the block carries a line
// it applies to; every other block says nothing recalled is to be followed.
func TestTheFrameNamesTheRuleExceptionOnlyWithARule(t *testing.T) {
	plain := frameRecall("- **orchard** `a1`\n  - Model said: the pear filter now keys on the season id")
	if !strings.HasPrefix(plain, recallFrameHeader) || strings.Contains(plain, recallUserRuleSentence) {
		t.Errorf("a block with no rule line named the exception:\n%s", plain)
	}
	if !strings.Contains(plain, recallNeverSentence) {
		t.Errorf("the short frame lost the permission sentence:\n%s", plain)
	}
	ruled := frameRecall("- **orchard** `a1`\n  - " + evidence.LabelUserRule + "run make lint-planner before you commit")
	if !strings.HasPrefix(ruled, recallRuleFrameHeader) {
		t.Errorf("a block with a rule line did not name the exception:\n%s", ruled)
	}
	if len(recallFrameHeader)+len(recallFrameFooter) > recallFrameOverhead {
		t.Error("the overhead budgets less than the longer header")
	}
	// A cut keeps the header the block came with.
	if got := fitFramed([]string{ruled}, "", len(ruled)-5); !strings.HasPrefix(got, recallRuleFrameHeader) || !strings.HasSuffix(got, recallFrameFooter) {
		t.Errorf("a cut rule block lost its frame: %q", got)
	}
}
