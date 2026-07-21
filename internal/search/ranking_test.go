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
