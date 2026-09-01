package bench

import (
	"strings"
	"testing"
)

// The corpus is the benchmark: if it can be generated two different ways from
// one seed, or if the answer is reachable without choosing, the numbers mean
// nothing. Both are asserted here rather than assumed.
func TestGenerateBlockIsSeeded(t *testing.T) {
	a, b := GenerateBlock(7), GenerateBlock(7)
	if a.Hash != b.Hash {
		t.Errorf("same seed, different hash: %s vs %s", a.Hash, b.Hash)
	}
	if len(a.Chains) != BlockChainCount {
		t.Fatalf("chains = %d, want %d", len(a.Chains), BlockChainCount)
	}
	if c := GenerateBlock(8); c.Hash == a.Hash {
		t.Error("a different seed produced the same corpus hash")
	}
}

func TestEachChainSettlesOnceInTheMiddleOfOneSession(t *testing.T) {
	for _, chain := range GenerateBlock(1).Chains {
		if len(chain.Sessions) != BlockPriorCount {
			t.Fatalf("%s: %d sessions, want %d", chain.ID, len(chain.Sessions), BlockPriorCount)
		}
		found := 0
		for i, s := range chain.Sessions {
			for j, m := range s.Messages {
				if !strings.Contains(m.Text, chain.SettledMarker()) {
					continue
				}
				found++
				if i != BlockSettledAt {
					t.Errorf("%s: session %d carries the answer", chain.ID, i)
				}
				if j == len(s.Messages)-1 {
					t.Errorf("%s: the answer is the last message, so recency alone finds it", chain.ID)
				}
				if j == 0 {
					t.Errorf("%s: the answer opens its session, so it never has to be found", chain.ID)
				}
			}
		}
		if found != 1 {
			t.Errorf("%s: the answer appears %d times, want once", chain.ID, found)
		}
	}
}

// The sessions that settle nothing have to say the subject more, or the
// corpus is not the case ranking finds hard.
func TestTheUnsettledSessionsSayTheSubjectMore(t *testing.T) {
	chain := GenerateBlock(1).Chains[0]
	count := func(i int) int {
		n := 0
		for _, m := range chain.Sessions[i].Messages {
			n += strings.Count(m.Text, chain.Terms[0])
		}
		return n
	}
	settled := count(BlockSettledAt)
	for i := range chain.Sessions {
		if i == BlockSettledAt {
			continue
		}
		if count(i) <= settled {
			t.Errorf("session %d says the subject %d times, the settled one %d", i, count(i), settled)
		}
	}
}

// SettledMarker is the decision without the sentence around it, so a
// legitimate cut of the tail still counts as carrying the answer.
func TestSettledMarkerIsTheDecisionAlone(t *testing.T) {
	chain := GenerateBlock(1).Chains[0]
	marker := chain.SettledMarker()
	if !strings.Contains(chain.Settled, marker) {
		t.Fatalf("marker %q is not part of the settled sentence %q", marker, chain.Settled)
	}
	if strings.Contains(marker, "The fix was ") || strings.Contains(marker, ", decided") {
		t.Errorf("marker still carries the sentence around it: %q", marker)
	}
	// Both fallbacks: a sentence with neither half of the frame comes back
	// whole rather than empty, which is the only safe answer for a scorer.
	plain := BlockChain{Settled: "we moved to a read replica"}
	if got := plain.SettledMarker(); got != plain.Settled {
		t.Errorf("unframed sentence = %q, want it whole", got)
	}
	noTail := BlockChain{Settled: "The fix was moving to a read replica"}
	if got := noTail.SettledMarker(); got != "moving to a read replica" {
		t.Errorf("sentence with no tail = %q", got)
	}
}
