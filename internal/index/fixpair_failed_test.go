package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A repaired remedy is the failing command corrected, so what makes it the
// answer is the command before it. Without that command stored the remedy is a
// long line naming nothing of the error: 107 of the 360 pairs served on a real
// store are that shape.
func TestARepairedPairCarriesTheCommandThatFailed(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "command", Text: `grep -rn "deja" --include=*.go .`, Time: now},
		{Role: "tool-output", Text: "zsh:1: no matches found: --include=*.go", Time: now.Add(time.Second)},
		{Role: "command", Text: `grep -rn "deja" --include=\*.go .`, Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "3 matches", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || !pairs[0].Repaired {
		t.Fatalf("want one repaired pair, got %+v", pairs)
	}
	if pairs[0].Failed != `grep -rn "deja" --include=*.go .` {
		t.Errorf("the pair does not say what it repaired: %q", pairs[0].Failed)
	}
}

// Every other remedy is a command someone ran after the error, not that
// command corrected, and saying "after this failed" about it would be a claim
// the store cannot make.
func TestAnOrdinaryPairCarriesNoFailingCommand(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "command", Text: "make build", Time: now},
		{Role: "tool-output", Text: "zsh: command not found: shellcheck", Time: now.Add(time.Second)},
		{Role: "command", Text: "brew install shellcheck", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "installed", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || pairs[0].Repaired {
		t.Fatalf("want one ordinary pair, got %+v", pairs)
	}
	if pairs[0].Failed != "" {
		t.Errorf("an ordinary remedy claims to repair %q", pairs[0].Failed)
	}
}
