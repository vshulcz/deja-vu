package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A session whose own text reports the approach was backed out is a dead end,
// not the answer — and it is usually the louder one, because it repeated the
// terms while flailing. Term frequency alone hands the agent that reverted
// attempt first, which invites redoing what already failed. GaveUp must demote
// it below a quieter session that held. decisionbench measures the lift on a
// corpus (25% -> 100% held-first); this pins the behaviour in CI.
func TestGaveUpSessionRanksBelowOneThatHeld(t *testing.T) {
	day := time.Now().Add(-24 * time.Hour)
	msg := func(role, text string) model.Message { return model.Message{Role: role, Text: text, Time: day} }

	held := model.Session{
		ID: "held", Harness: "claude", Project: "p", Updated: day,
		Messages: []model.Message{
			msg("user", "retry budget failing"),
			msg("assistant", "We capped retries at 3 with jitter; the pool stopped saturating."),
		},
	}
	// The reverted attempt is the louder match — it repeats the terms — and is
	// equally recent, so on the text alone it wins.
	reverted := model.Session{
		ID: "reverted", Harness: "claude", Project: "p", Updated: day, GaveUp: true,
		Messages: []model.Message{
			msg("user", "retry budget retry budget again"),
			msg("user", "retry budget still"),
			msg("assistant", "We tried capping retries at 3 but reverted it — it did not help and we backed it out."),
		},
	}

	hits, err := Run([]model.Session{reverted, held}, Options{Query: "retry budget", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Session.ID != "held" {
		top := "—"
		if len(hits) > 0 {
			top = hits[0].Session.ID
		}
		t.Fatalf("a backed-out session outranked the one that held: top=%s", top)
	}
	// It is demoted, not hidden: the reverted attempt still appears, second.
	if len(hits) != 2 {
		t.Fatalf("the reverted session was dropped, not demoted: got %d hits", len(hits))
	}
}

// GaveUp is a session-level flag: one line reporting a reversal marks the whole
// session, even when a later line lands the real fix ("reverted the pool cap;
// the fix was pgx"). Such a session reached a conclusion, so it must keep the
// decision boost and take no give-up penalty — otherwise the outcome signal
// buries the very answer it should surface, below a louder session that only
// talked about the topic.
func TestRevertedThenSolvedKeepsTheDecision(t *testing.T) {
	day := time.Now().Add(-24 * time.Hour)
	msg := func(role, text string) model.Message { return model.Message{Role: role, Text: text, Time: day} }

	// Louder on the query terms and decides nothing — on term frequency alone it
	// wins, and if the solved session were wrongly penalised it would stay on top.
	plain := model.Session{
		ID: "plain", Harness: "claude", Project: "p", Updated: day,
		Messages: []model.Message{
			msg("user", "connection pool connection pool question"),
			msg("assistant", "Some background on the connection pool and connection pool sizing."),
		},
	}
	// Reverted one attempt, landed another. GaveUp is true, but it decided
	// something, so it must outrank the quiet-topic session above.
	solved := model.Session{
		ID: "solved", Harness: "claude", Project: "p", Updated: day, GaveUp: true,
		Messages: []model.Message{
			msg("user", "connection pool saturating"),
			msg("assistant", "We reverted the connection pool cap; the fix was switching to pgx pooling."),
		},
	}

	hits, err := Run([]model.Session{plain, solved}, Options{Query: "connection pool", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Session.ID != "solved" {
		top := "—"
		if len(hits) > 0 {
			top = hits[0].Session.ID
		}
		t.Fatalf("a reverted-then-solved session was buried by a quiet-topic one: top=%s", top)
	}
}

// The give-up penalty reads the transcript; a lifecycle state is a later human
// judgement about the same session. Only "accepted" overrides the penalty — a
// person vouched for the outcome. "rejected" agrees the session was a dead end,
// so a rejected give-up must stay demoted even when it is the louder match;
// otherwise marking a session rejected would rank it higher, not lower.
func TestLifecycleGatesTheGiveUpPenalty(t *testing.T) {
	day := time.Now().Add(-24 * time.Hour)
	msg := func(role, text string) model.Message { return model.Message{Role: role, Text: text, Time: day} }

	// The same reverted, undecided transcript recorded twice, differing only in
	// the lifecycle state a person later attached. Identical text means identical
	// term frequency, so the ONLY thing that can separate them is the give-up
	// penalty: the accepted copy waives it (score S), the rejected copy takes it
	// (0.5·S), so accepted must rank strictly first. Before the fix both states
	// skipped the penalty, leaving them tied — the id "aaa-rejected" then sorted
	// ahead and this failed, which is the behaviour it pins.
	body := []model.Message{
		msg("user", "cache stampede cache stampede"),
		msg("assistant", "We tried a mutex but reverted it — it did not help and we backed it out."),
	}
	accepted := model.Session{
		ID: "zzz-accepted", Harness: "claude", Project: "p", Updated: day,
		GaveUp: true, Lifecycle: "accepted", Messages: body,
	}
	rejected := model.Session{
		ID: "aaa-rejected", Harness: "claude", Project: "p", Updated: day,
		GaveUp: true, Lifecycle: "rejected", Messages: body,
	}

	hits, err := Run([]model.Session{rejected, accepted}, Options{Query: "cache stampede", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Session.ID != "zzz-accepted" {
		top := "—"
		if len(hits) > 0 {
			top = hits[0].Session.ID
		}
		t.Fatalf("a rejected give-up was not ranked below the accepted copy of the same session: top=%s", top)
	}
}
