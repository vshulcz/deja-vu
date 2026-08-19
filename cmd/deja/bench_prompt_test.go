package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/bench"
)

// The benchmark is what makes changes to per-prompt recall arguable instead of
// opinionated, so it needs to be right itself. These pin the parts that were
// wrong when it was written: a corpus dated in the future, gates the probe did
// not apply, and arms that scored the wrong chains.
func TestPromptBenchCorpusIsMeasurable(t *testing.T) {
	corpus := bench.GeneratePrompt(bench.Seed)
	kinds := map[string]int{}
	topics := map[string]bool{}
	for _, c := range corpus.Chains {
		kinds[c.Kind]++
		if c.Negative {
			kinds["negative"]++
			continue
		}
		// One topic per chain: the context corpus gives them all the same
		// vocabulary, which scores zero no matter what the extractor does.
		// Filler for the catch-all scope carries no question of its own: it is
		// asked about through the bucket-answer chain, whose question is the
		// one that must go unanswered. A chain nobody asks about still has to
		// earn its place, and this one earns it by being the wrong answer.
		if c.Kind == "bucket" {
			continue
		}
		if topics[c.Topic] {
			t.Fatalf("topic %q appears in more than one chain", c.Topic)
		}
		topics[c.Topic] = true
		if c.Question == "" {
			t.Fatalf("chain %s has no question to ask", c.ID)
		}
		for _, s := range c.Sessions {
			// A corpus dated in the future reads as newer than now and the
			// freshness gate withholds all of it.
			if s.Updated.After(time.Now()) {
				t.Fatalf("chain %s is dated in the future: %v", c.ID, s.Updated)
			}
		}
	}
	if kinds["marathon"] == 0 || kinds["fresh"] == 0 {
		t.Fatalf("corpus has no gated shapes to measure: %v", kinds)
	}
	if kinds["negative"] == 0 {
		t.Fatal("no negative controls: a run could buy coverage with noise unnoticed")
	}
}

func TestPromptBenchScoresAndReports(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an index")
	}
	report, err := measurePrompt(bench.Seed)
	if err != nil {
		t.Fatal(err)
	}
	if report.Real.Cases == 0 || report.Marathon.Cases == 0 || report.Fresh.Cases == 0 {
		t.Fatalf("arms not populated: %+v", report)
	}
	// The numbers this run reports are the ones the PR quoted; a drop below
	// them means recall got worse without anyone saying so.
	if report.Real.Fired < 8 {
		t.Fatalf("real questions answered dropped to %d/%d", report.Real.Fired, report.Real.Cases)
	}
	if report.Negative.FalseFires != 0 {
		t.Fatalf("%d false fires on negative controls", report.Negative.FalseFires)
	}
	if report.Real.Precision < 1 {
		t.Fatalf("precision fell to %.2f", report.Real.Precision)
	}
	if report.CorpusHash == "" {
		t.Fatal("report carries no corpus hash, so results cannot be compared across runs")
	}
}

func TestRunBenchPromptWritesJSONAndText(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an index")
	}
	out := captureStdout(t, func() {
		if err := runBenchPrompt([]string{"--json"}); err != nil {
			t.Fatal(err)
		}
	})
	var got promptReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json is not json: %v\n%s", err, out)
	}
	if got.Real.Cases == 0 {
		t.Fatalf("json report is empty: %s", out)
	}
	text := captureStdout(t, func() {
		if err := runBenchPrompt(nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"real questions", "long sessions", "recent sessions", "negative controls"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text report missing %q:\n%s", want, text)
		}
	}
}

func TestFinishPromptArmHandlesEmptyArms(t *testing.T) {
	var arm promptArmReport
	finishPromptArm(&arm, nil)
	if arm.FireRate != 0 || arm.Precision != 0 {
		t.Fatalf("empty arm reported rates: %+v", arm)
	}
	arm = promptArmReport{Cases: 4, Fired: 2, Correct: 1}
	finishPromptArm(&arm, []int{2, 3})
	if arm.FireRate != 0.5 || arm.Precision != 0.5 {
		t.Fatalf("rates wrong: %+v", arm)
	}
	if arm.MedianTerm == 0 {
		t.Fatal("median term count not computed")
	}
}

// The bucket arm measures a scope that cannot hold the answer, so nothing it
// returns is ever right. Until it existed the benchmark handed the probe the
// correct project on every case and could not see a scope failure at all —
// which is how `bench prompt` reported precision 1.00 while the hook was
// injecting a stranger's session on a real machine (2026-08-19).
func TestBucketArmCannotBeSatisfied(t *testing.T) {
	report, err := measurePrompt(1)
	if err != nil {
		t.Fatal(err)
	}
	b := report.Bucket
	if b.Cases == 0 {
		t.Fatal("the bucket case is not in the corpus, so a wrong scope goes unmeasured")
	}
	if b.Correct != 0 {
		t.Errorf("the bucket arm scored %d correct — nothing in a catch-all scope is the answer, so the question is being asked against the right project instead", b.Correct)
	}
	if b.Fired != b.FalseFires {
		t.Errorf("fired %d but counted %d false — every fire out of the bucket is a false one", b.Fired, b.FalseFires)
	}
}
