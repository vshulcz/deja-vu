package index

import (
	"fmt"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The ranker scores sessions on many cores now, because lowercasing and
// tokenising every turn of every ranked session was 1.6 s of a 2.4 s recall
// answer on a real store. Spreading work changes nothing about the answer only
// if the order it comes back in is the same, so both paths are run.
func TestRankingIsTheSameOnOneCoreAndMany(t *testing.T) {
	var ss []model.Session
	for i := range 24 {
		msgs := []model.Message{
			{Role: "user", Text: fmt.Sprintf("session %d asks about the retry budget", i)},
			{Role: "assistant", Text: fmt.Sprintf("we capped it at %d attempts", i%5)},
		}
		if i%3 == 0 {
			msgs = append(msgs, model.Message{Role: "assistant", Text: "the scheduler stayed single-writer"})
		}
		ss = append(ss, model.Session{Harness: "claude", ID: fmt.Sprintf("s%02d", i), Messages: msgs})
	}
	terms := []string{"retry", "budget", "scheduler"}
	idf := map[string]float64{"retry": 1.5, "budget": 2, "scheduler": 3}

	rerankWorkers = func() int { return 1 }
	one := rerankByBestMessage(cloneSessions(ss), terms, idf)
	rerankWorkers = func() int { return 8 }
	many := rerankByBestMessage(cloneSessions(ss), terms, idf)
	t.Cleanup(func() { rerankWorkers = defaultRerankWorkers })

	if len(one) != len(many) {
		t.Fatalf("one core ranked %d sessions, eight ranked %d", len(one), len(many))
	}
	for i := range one {
		if one[i].ID != many[i].ID {
			t.Fatalf("position %d: one core says %s, eight say %s", i, one[i].ID, many[i].ID)
		}
	}
}

func cloneSessions(ss []model.Session) []model.Session {
	out := make([]model.Session, len(ss))
	copy(out, ss)
	return out
}
