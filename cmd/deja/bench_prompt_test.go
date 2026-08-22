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
		if c.Kind == "bucket" || c.Kind == "haystack-noise" || c.Kind == "concluded-noise" || c.Kind == "background" {
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
	// Five, not zero, and that is the point of the background sessions: on the
	// thirty-chain corpus every word was rare, so the gate looked perfect and
	// nothing about rarity could be measured here. With ordinary working talk
	// behind it the corpus reads the defect the product actually has. The
	// number is recorded so it cannot grow quietly while the fix is measured
	// (#1534); real questions went 11/12 -> 13/13 in the same move.
	if report.Negative.FalseFires > 5 {
		t.Fatalf("%d false fires on negative controls, was 5", report.Negative.FalseFires)
	}
	if report.Real.Precision < 1 {
		t.Fatalf("precision fell to %.2f", report.Real.Precision)
	}
	if report.CorpusHash == "" {
		t.Fatal("report carries no corpus hash, so results cannot be compared across runs")
	}
	// Every other arm was reported and nothing read it. Over one night three
	// separate changes moved shown_line, haystack and russian_questions and the
	// suite stayed green — the numbers only appeared in a PR description, where
	// they are worth exactly as much as whoever remembered to look.
	//
	// Floors, not equalities: an arm that improves must not need this file
	// edited, and the three arms that still read false fires are held at their
	// current count so the known defects cannot quietly grow.
	if report.Shown.Correct < report.Shown.Cases {
		t.Fatalf("shown_line %d/%d: a block opened on a line carrying none of the question",
			report.Shown.Correct, report.Shown.Cases)
	}
	if report.Haystack.Correct < 3 {
		t.Fatalf("haystack %d/%d: a long session that mentions everything won again",
			report.Haystack.Correct, report.Haystack.Cases)
	}
	if report.Russian.Correct < 3 {
		t.Fatalf("russian_questions %d/%d", report.Russian.Correct, report.Russian.Cases)
	}
	if report.Marathon.Fired < 1 || report.Fresh.Fired < 1 {
		t.Fatalf("marathon %d/%d, fresh %d/%d: a shape the gates turn away",
			report.Marathon.Fired, report.Marathon.Cases, report.Fresh.Fired, report.Fresh.Cases)
	}
	// Known defects, held where they are. Each has its own entry in the loop
	// journal and none has a fix that survived measurement on a real store.
	for _, k := range []struct {
		name string
		arm  promptArmReport
		max  int
	}{
		{"absent_subject", report.AbsentSubject, 3},
		{"off_topic", report.OffTopic, 3},
		{"bucket_scope", report.Bucket, 1},
	} {
		if k.arm.FalseFires > k.max {
			t.Fatalf("%s false fires rose to %d of %d, was %d", k.name, k.arm.FalseFires, k.arm.Cases, k.max)
		}
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
