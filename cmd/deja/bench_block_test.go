package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/bench"
	"github.com/vshulcz/deja-vu/internal/model"
)

// The corpus has to be able to say no. A bench whose baseline scores full
// marks measures nothing, which is how bench context's coverage column ended
// up unable to move (#2931) — so the shape of this corpus is asserted, not
// assumed.
func TestTheBlockCorpusSettlesOnceAndNotLast(t *testing.T) {
	corpus := bench.GenerateBlock(1)
	if len(corpus.Chains) != bench.BlockChainCount {
		t.Fatalf("chains = %d, want %d", len(corpus.Chains), bench.BlockChainCount)
	}
	chain := corpus.Chains[0]
	if len(chain.Sessions) != bench.BlockPriorCount {
		t.Fatalf("sessions = %d, want %d", len(chain.Sessions), bench.BlockPriorCount)
	}
	settled := 0
	for i, s := range chain.Sessions {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, chain.SettledMarker()) {
				settled++
				if i != bench.BlockSettledAt {
					t.Errorf("session %d carries the answer; only %d should", i, bench.BlockSettledAt)
				}
			}
		}
	}
	if settled != 1 {
		t.Errorf("the answer appears %d times in the chain, want once", settled)
	}
	// Not the last thing said, or "take the newest turn" scores full marks
	// without choosing anything.
	last := chain.Sessions[bench.BlockSettledAt].Messages
	if strings.Contains(last[len(last)-1].Text, chain.SettledMarker()) {
		t.Error("the answer is the last message of its session; recency alone would win")
	}
}

// The baseline is what says the metric is real: the newest turns of the right
// session do not carry the answer, so an arm that scores above zero had to
// find it.
func TestTheNewestTurnBaselineMissesTheAnswer(t *testing.T) {
	corpus := bench.GenerateBlock(1)
	chain := corpus.Chains[0]
	got := scoreBlock(blockArmNewest([]model.Session{chain.Sessions[bench.BlockSettledAt]}), chain)
	if got.carries {
		t.Error("the newest two turns carry the answer, so the baseline cannot fail")
	}
}

// And scoring itself: the settled sentence counts, the question's words do not.
func TestScoringCountsTheAnswerAndNotTheSubject(t *testing.T) {
	chain := bench.GenerateBlock(1).Chains[0]
	if got := scoreBlock(chain.Settled, chain); !got.carries || !got.settled {
		t.Errorf("the settled sentence scored %+v", got)
	}
	subjectOnly := strings.Repeat(chain.Terms[0]+" ", 40)
	if got := scoreBlock(subjectOnly, chain); got.carries {
		t.Error("saying the subject over and over counted as carrying the answer")
	}
	if got := scoreBlock("", chain); got.carries || got.settled || !got.armEmpty {
		t.Errorf("an empty arm scored %+v", got)
	}
}

func TestBlockReportPrintsEveryArm(t *testing.T) {
	var b bytes.Buffer
	printBlockReport(&b, blockReport{
		Chains: 30, Priors: 8,
		Arms: map[string]blockArmReport{
			"deja-block":  {Carries: 1, ReadsSettled: 1, MedianTokens: 665},
			"deja-digest": {Carries: 1, ReadsSettled: 1, MedianTokens: 1656},
			"newest-turn": {},
			"cold":        {},
		},
	})
	for _, want := range []string{"deja-block", "deja-digest", "newest-turn", "cold", "carries the answer"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the report does not mention %q:\n%s", want, b.String())
		}
	}
}

// End to end, the same shape the context bench is guarded with: the report
// parses, every arm is present, the baseline is beaten, and nothing is written
// outside the scratch directory.
func TestBlockJSONAndIsolation(t *testing.T) {
	outside := t.TempDir()
	t.Setenv("HOME", outside)
	t.Setenv("USERPROFILE", outside)
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1")
	out, err := captureRun(t, "bench", "block", "--json", "--seed", "7")
	if err != nil {
		t.Fatal(err)
	}
	var report blockReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid block JSON %q: %v", out, err)
	}
	if report.Chains != bench.BlockChainCount || report.Priors != bench.BlockPriorCount {
		t.Fatalf("unexpected block report: %#v", report)
	}
	for _, arm := range []string{"deja-block", "deja-digest", "newest-turn", "cold"} {
		if _, ok := report.Arms[arm]; !ok {
			t.Fatalf("missing arm %q", arm)
		}
	}
	// The whole point: deja's surfaces carry the answer and the baseline does
	// not. A run where the baseline scores is a corpus that stopped asking.
	if report.Arms["deja-block"].Carries <= report.Arms["newest-turn"].Carries {
		t.Errorf("the block did not beat the baseline: %+v", report.Arms)
	}
	if report.Arms["cold"].Carries != 0 {
		t.Errorf("the empty arm carried something: %+v", report.Arms["cold"])
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("benchmark wrote outside scratch: %v", entries)
	}
}

func TestBlockArgs(t *testing.T) {
	if _, err := captureRun(t, "bench", "block", "--bad"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatal("invalid block flag did not fail")
	}
	if _, err := captureRun(t, "bench", "block", "--seed"); err == nil {
		t.Fatal("missing seed did not fail")
	}
}
