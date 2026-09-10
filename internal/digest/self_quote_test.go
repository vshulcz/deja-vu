package digest

import "testing"

// deja asks an agent to credit the recall it used, and that sentence reports
// what an earlier session decided — so every decision marker fires on it and
// the line came back as a decision of its own. Found on a real store at the
// file line: `doctor_auto.go has been worked on in 2 sessions — prior decision:
// deja-vu recalled: …`, which is deja quoting itself quoting a session.
func TestDejaQuotingItselfIsNotADecision(t *testing.T) {
	quotes := []string{
		"déjà vu: the retry budget stays at four (claude, Aug 11, deja:2fc1d1ef) — reusing it.",
		"deja-vu recalled: earlier experiments already fixed the ranking, so that is settled",
		"Recalled from this machine's history: the deploy key was rotated in March",
		"deja found sessions whose wording matches this request",
	}
	for _, q := range quotes {
		if CarriesDecision(q) {
			t.Errorf("deja's own line read as a decision:\n  %s", q)
		}
	}
}

// And a person's own decision about deja still is one — the guard is about who
// is speaking, not about the word.
func TestADecisionAboutDejaIsStillADecision(t *testing.T) {
	decisions := []string{
		"решили: deja больше не трогает живой индекс, только копию",
		"we settled on keeping the deja hook out of the release build",
	}
	for _, d := range decisions {
		if !CarriesDecision(d) {
			t.Errorf("a real decision was dropped:\n  %s", d)
		}
	}
}
