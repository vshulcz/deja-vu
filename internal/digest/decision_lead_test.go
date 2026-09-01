package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func conclusionSession(text string) model.Session {
	return model.Session{
		Harness:  "claude",
		Project:  "p",
		ID:       "s1",
		Messages: []model.Message{{Role: "assistant", Text: text}},
	}
}

// A reply is diagnosis-first, and the block quoted its opening. When the
// outcome is further in, that handed an agent the symptom and dropped what the
// session settled — 211 of 490 sessions on a real store (#2243).
func TestTheBlockQuotesTheConclusionAndNotOnlyTheDiagnosis(t *testing.T) {
	got := Conclusions(conclusionSession(
		"The pool kept dropping connections under load. "+
			"Both replicas showed the same pattern in the logs. "+
			"The fix was raising pgbouncer default_pool_size to 40."), 600, 3)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "raising pgbouncer default_pool_size to 40") {
		t.Errorf("the outcome is not in the block:\n%s", joined)
	}
	if !CarriesDecision(joined) {
		t.Errorf("the block does not read as a conclusion:\n%s", joined)
	}
}

// The opening stays the default. A head that already concludes must not be
// traded for some later sentence that also happens to carry a marker — the
// first statement of the outcome is the one worth having.
func TestAHeadThatAlreadyConcludesIsKept(t *testing.T) {
	got := Conclusions(conclusionSession(
		"Connections were dropping under load. "+
			"The fix was pinning pgx to 5.4.3. "+
			"Later we also merged the CI image switch."), 600, 3)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	// Both sentences of the head, not the decision sentence lifted out of it:
	// the opening is what makes the outcome make sense, and a later sentence
	// carrying a marker of its own must not displace it.
	for _, want := range []string{"Connections were dropping under load", "pinning pgx to 5.4.3"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("the head was traded for a later sentence, %q is gone:\n%s", want, got[0])
		}
	}
}

// A message that settles nothing must not acquire a conclusion: the fallback
// is still its opening.
func TestAMessageWithNoOutcomeStillQuotesItsOpening(t *testing.T) {
	got := Conclusions(conclusionSession(
		"Looking at the pool metrics now. "+
			"The graph covers the last six hours. "+
			"I will check the replica logs next."), 600, 3)
	if len(got) == 0 {
		t.Fatal("a message that settles nothing was dropped entirely; its opening is still the answer")
	}
	if !strings.Contains(got[0], "Looking at the pool metrics") {
		t.Errorf("the opening was dropped for something else:\n%s", got[0])
	}
}

// The splitter has to agree with firstSentences on what a sentence is, or the
// two disagree about where a conclusion starts. A version number is not a
// sentence end; a CJK stop is, with no space after it.
func TestSentencesSplitWhereTheDigestAlreadySplits(t *testing.T) {
	got := sentencesOf("Pinned pgx to v5.4.3 and it held. Left a note.")
	if len(got) != 2 {
		t.Fatalf("split into %d sentences, want 2: %q", len(got), got)
	}
	if !strings.Contains(got[0], "v5.4.3 and it held") {
		t.Errorf("the version number was read as a sentence end: %q", got[0])
	}
	if n := len(sentencesOf("已固定 pgx 5.4.3。问题消失了。")); n != 2 {
		t.Errorf("CJK sentences split into %d, want 2", n)
	}
}
