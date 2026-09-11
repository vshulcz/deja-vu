package digest

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A nudge that carries an instruction is the instruction. The rule bounded the
// tail by length — a nudge "and a word or two" — and on this store an
// instruction is usually given exactly that way: "давай починим экспортер".
// Measured against the bounded rule, every tail below read as "carry on", so the
// packet went on naming the task the reader had just replaced.
func TestANudgeWithAnInstructionIsTheObjective(t *testing.T) {
	earlier := "the exporter retries are hammering the server, cap them"
	for _, tail := range []string{
		"ok cap the retries at three",
		"давай починим экспортер",
		"давай вернём прежний таймаут",
		"continue with the parser fix",
		"yes revert it",
		"no do not touch the cache",
		"go fix the pool",
	} {
		got := ExtractCompactionContext(nudgeSession(earlier, tail), ExtractOptions{})
		if got.Objective.Text != tail {
			t.Errorf("tail %q was read as a nudge; the objective stayed %q", tail, got.Objective.Text)
		}
	}
}

// And a turn that is nudges all the way down is still a nudge, in any mixture.
func TestNudgesAllTheWayDownAreStillANudge(t *testing.T) {
	earlier := "the exporter retries are hammering the server, cap them"
	for _, tail := range []string{
		"ok", "продолжай", "давай дальше", "ok go on", "да давай дальше",
		"yes please continue", "thanks, continue", "ага, давай",
	} {
		got := ExtractCompactionContext(nudgeSession(earlier, tail), ExtractOptions{})
		if got.Objective.Text != earlier {
			t.Errorf("tail %q became the objective: %q", tail, got.Objective.Text)
		}
	}
}

func nudgeSession(first, last string) model.Session {
	now := time.Now().UTC()
	return model.Session{
		Harness: "claude", ID: "s1", Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: first, Time: now},
			{Role: "assistant", Text: "capped it at three attempts because retrying past that spread the outage", Time: now.Add(time.Minute)},
			{Role: "user", Text: last, Time: now.Add(2 * time.Minute)},
		},
	}
}
