package search

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Keep the reviewed 0.23.0 predicate as a differential oracle.
func referenceHoldsLastTurn(holder, other model.Session) bool {
	key := func(m model.Message) string {
		return m.Role + "\x00" + strconv.FormatInt(m.Time.UnixNano(), 10) + "\x00" + m.Text
	}
	for i := len(other.Messages) - 1; i >= 0; i-- {
		m := other.Messages[i]
		text := strings.TrimSpace(m.Text)
		if m.Time.IsZero() || text == "" || m.Role == roleSummary || (m.Role == "assistant" && text == "No response requested.") {
			continue
		}
		want := key(m)
		for _, h := range holder.Messages {
			if key(h) == want {
				return true
			}
		}
		return false
	}
	return false
}

func TestHoldsLastTurnMatchesReviewedPredicate(t *testing.T) {
	at := time.Date(2026, 10, 19, 12, 0, 0, 123, time.UTC)
	times := []time.Time{{}, at, at.In(time.FixedZone("offset", 3600)), at.Add(time.Nanosecond)}
	roles := []string{"user", "assistant", roleSummary, "tool"}
	texts := []string{"same", "own", "", " \t", "No response requested.", "nul\x00text"}
	rng := rand.New(rand.NewSource(5320))
	for i := range 1000 {
		pair := make([]model.Session, 2)
		for j := range pair {
			for range rng.Intn(8) {
				pair[j].Messages = append(pair[j].Messages, model.Message{
					Role: roles[rng.Intn(len(roles))], Time: times[rng.Intn(len(times))], Text: texts[rng.Intn(len(texts))],
				})
			}
		}
		if got, want := holdsLastTurn(pair[0], pair[1]), referenceHoldsLastTurn(pair[0], pair[1]); got != want {
			t.Fatalf("case %d: got %v, want %v for %#v", i, got, want, pair)
		}
	}
}
