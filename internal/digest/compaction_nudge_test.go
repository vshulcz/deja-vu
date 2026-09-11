package digest

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The packet's objective is the first line a resuming agent reads, and the turn
// before a compaction is as often "carry on" as it is the task. The English
// exact-match list let every other language through: measured on a real store,
// the objective came back as "продолжай".
func TestTheObjectiveSkipsACarryOnInAnyLanguage(t *testing.T) {
	task := "the exporter retries are hammering the server, cap them"
	for _, nudge := range []string{
		"продолжай", "давай", "давай дальше", "дальше", "ок", "спасибо",
		"ok", "okay", "continue", "go on", "thanks", "proceed",
	} {
		got := ExtractCompactionContext(session(task, nudge), ExtractOptions{})
		if got.Objective.Text != task {
			t.Errorf("tail %q made the objective %q", nudge, got.Objective.Text)
		}
	}
}

// And a terse instruction is the task, not a nudge: the length bound that keeps
// an acknowledgement out of a handover would have dropped it.
func TestATerseInstructionIsStillTheObjective(t *testing.T) {
	for _, short := range []string{"Repair the parser.", "cap the retries", "revert that"} {
		got := ExtractCompactionContext(session("something earlier and longer than this", short), ExtractOptions{})
		if got.Objective.Text != short {
			t.Errorf("the objective dropped %q and took %q", short, got.Objective.Text)
		}
	}
}

func session(first, last string) model.Session {
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
