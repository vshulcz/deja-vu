package index

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// On a large store the relevance pool is fused with a per-message reading:
// a session whose single user turn carries the query's words moves up past
// one that scatters fewer of the same words over many assistant turns. It is
// a fusion, so the pool leader keeps its place — the gain measured on a
// 19k-session pile is hit@1 15 -> 18, not a new ranking (#3016).
func TestBestMessageFusionLiftsTheTurnThatHoldsTheFact(t *testing.T) {
	terms := []string{"degree", "graduated", "university"}
	idf := map[string]float64{"degree": 2.0, "graduated": 2.5, "university": 1.5}
	scattered := model.Session{ID: "scattered", Messages: []model.Message{
		{Role: "assistant", Text: "a degree in anything is useful"},
		{Role: "assistant", Text: "many people graduated last year"},
		{Role: "assistant", Text: "the university opens in autumn"},
	}}
	thin := model.Session{ID: "thin", Messages: []model.Message{
		{Role: "assistant", Text: "a degree is a degree"},
	}}
	focused := model.Session{ID: "focused", Messages: []model.Message{
		{Role: "user", Text: "I graduated with a degree in physics from the university of Michigan"},
	}}
	// Pool order as the session-level ranking hands it over: focused last.
	got := sessionIDs(rerankByBestMessage([]model.Session{scattered, thin, focused}, terms, idf))
	want := []string{"scattered", "focused", "thin"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// Fusion, not replacement: two sessions the messages cannot tell apart keep
// the pool's order, so a ranking that was right stays right.
func TestBestMessageFusionKeepsThePoolOrderOnTies(t *testing.T) {
	terms := []string{"bike", "commute"}
	idf := map[string]float64{"bike": 2.0, "commute": 2.0}
	a := model.Session{ID: "a", Messages: []model.Message{{Role: "user", Text: "my bike commute"}}}
	b := model.Session{ID: "b", Messages: []model.Message{{Role: "user", Text: "the bike commute again"}}}
	got := rerankByBestMessage([]model.Session{a, b}, terms, idf)
	if got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("order changed on a tie: %v", sessionIDs(got))
	}
}

func sessionIDs(ss []model.Session) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.ID
	}
	return out
}
