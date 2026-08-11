package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func rankSession(id, title, text string) model.Session {
	now := time.Now()
	return model.Session{
		ID: id, Harness: "claude", Project: "p", Title: title, Updated: now,
		Messages: []model.Message{{Role: "user", Text: text, Time: now}},
	}
}

func runRank(t *testing.T, q string, ss ...model.Session) []Hit {
	t.Helper()
	hits, err := Run(ss, Options{Query: q, All: true})
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

func TestProximityBoostRanksAdjacentTermsFirst(t *testing.T) {
	pad := " filler words that stretch the message out considerably here "
	spread := rankSession("spread", "", "connection"+pad+pad+pad+pad+pad+"pool"+pad+pad+pad+pad+"exhausted end")
	tight := rankSession("tight", "", "the connection pool exhausted error came back"+pad+pad+pad+pad+pad+pad+pad+pad+pad+pad)
	hits := runRank(t, "connection pool exhausted", spread, tight)
	if len(hits) != 2 || hits[0].Session.ID != "tight" {
		t.Fatalf("proximity ignored: %v", []string{hits[0].Session.ID, hits[1].Session.ID})
	}
}

func TestTokenWindow(t *testing.T) {
	if w := tokenWindow("alpha beta gamma", []string{"alpha", "gamma"}); w != 16 {
		t.Fatalf("window = %d, want 16", w)
	}
	if w := tokenWindow("alpha only", []string{"alpha", "gamma"}); w != 0 {
		t.Fatalf("missing token window = %d, want 0", w)
	}
	if w := tokenWindow("alpha beta", []string{"alpha"}); w != 0 {
		t.Fatalf("single-token window = %d, want 0", w)
	}
}

func TestTitleBoostOutranksBodyMatch(t *testing.T) {
	body := rankSession("body", "unrelated title", "jwt refresh rotation discussed once here")
	titled := rankSession("titled", "jwt refresh rotation broke login", "we discussed jwt refresh rotation and fixed it")
	hits := runRank(t, "jwt refresh rotation", body, titled)
	if len(hits) != 2 || hits[0].Session.ID != "titled" {
		t.Fatalf("title boost missing: %v", []string{hits[0].Session.ID, hits[1].Session.ID})
	}
}

func TestBoostsAreBounded(t *testing.T) {
	if b := proximityBoost(1, 5); b > 1.36 {
		t.Fatalf("proximity boost unbounded: %f", b)
	}
	if b := titleBoost(5, 5); b > 1.41 {
		t.Fatalf("title boost unbounded: %f", b)
	}
	if proximityBoost(0, 5) != 1 || titleBoost(0, 5) != 1 {
		t.Fatal("neutral cases must not boost")
	}
}

func TestWornBoostBreaksTiesButIsCapped(t *testing.T) {
	a := rankSession("cold", "", "jwt refresh rotation fix applied")
	b := rankSession("worn", "", "jwt refresh rotation fix applied")
	hits, err := Run([]model.Session{a, b}, Options{Query: "jwt refresh rotation", All: true, RecallWorn: map[string]int{"worn": 4}})
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].Session.ID != "worn" || hits[0].Reused != 4 {
		t.Fatalf("worn tie-break missing: %+v", hits[0])
	}
	if wornBoost(1000) != 1.5 {
		t.Fatalf("worn boost uncapped: %f", wornBoost(1000))
	}
	// A clearly better match must beat a worn weak one: relevance > popularity.
	strong := rankSession("strong", "jwt refresh rotation", "jwt refresh rotation everywhere jwt refresh rotation again")
	weak := rankSession("weakworn", "", "jwt mentioned once, refresh later, rotation at the end of a long unrelated story about deployments")
	hits2, err := Run([]model.Session{strong, weak}, Options{Query: "jwt refresh rotation", All: true, RecallWorn: map[string]int{"weakworn": 50}})
	if err != nil {
		t.Fatal(err)
	}
	if hits2[0].Session.ID != "strong" {
		t.Fatalf("popularity outranked relevance: %+v", hits2)
	}
}

// Reuse must reach past a dead tie: a recurring answer the user keeps pulling
// back should surface even when a louder session repeats the query terms a
// couple more times. At the old 0.05/1.2 shape it did not — a single extra term
// buried the reused answer, so the signal was nearly inert. This pins the reach.
func TestWornBoostRescuesAnOutmatchedRecurringAnswer(t *testing.T) {
	// The answer names the topic and says it was settled; the distractor repeats
	// the terms twice more and was never reused — louder on the text alone.
	answer := rankSession("answer", "", "retry budget issue; we resolved retry budget before and it held")
	distractor := rankSession("distractor", "", "retry budget retry budget retry budget keeps failing; raw logs about retry budget")
	hits, err := Run([]model.Session{distractor, answer}, Options{Query: "retry budget", All: true, RecallWorn: map[string]int{"answer": 6}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Session.ID != "answer" {
		top := "—"
		if len(hits) > 0 {
			top = hits[0].Session.ID
		}
		t.Fatalf("reuse did not rescue an out-matched recurring answer: top=%s", top)
	}
}
