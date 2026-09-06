package stats

import "testing"

// The new shape counts only with the session id: "déjà vu" alone is an
// ordinary phrase, and a session about the tool says it without crediting
// anything. The old shape still counts, because the transcripts that say it
// are still on disk.
func TestCreditedAloudNeedsTheIDForTheNewShape(t *testing.T) {
	cases := map[string]bool{
		"déjà vu: you asked this on Aug 26 in opencode; it was settled as a vault lease (deja:ses_fc145)": true,
		"Déjà vu: \"why did we drop the trigram index\" — size vs traffic (codex, May 1, deja:cx0052)":    true,
		"deja-vu recalled: the webhook retry cap — reusing it.":                                           true,
		"the déjà vu line is rate-limited per session":                                                    false,
		"deja:cx0052 is the session to read":                                                              false,
		"":                                                                                                false,
	}
	for text, want := range cases {
		if got := CreditedAloud(text); got != want {
			t.Errorf("CreditedAloud(%q) = %v, want %v", text, got, want)
		}
	}
}
