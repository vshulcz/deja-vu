package bench

import (
	"strings"
	"testing"
)

// The reworded arm has a ceiling nobody had measured: for all five questions it
// fails, no message anywhere in the corpus says both the ordinary words and the
// term of art, so nothing lexical can bridge them. The tied chains are the half
// that is reachable — a store where somebody wrote the sentence tying the two,
// which is what every real store has.
func TestTiedChainsCarryTheSentenceThatTiesTheVocabularies(t *testing.T) {
	corpus := GeneratePrompt(1)
	tied := 0
	for _, c := range corpus.Chains {
		if !c.Tied {
			continue
		}
		tied++
		if c.Paraphrase == "" {
			t.Fatalf("%s is tied and asks nothing in other words", c.ID)
		}
		answers, ties := 0, 0
		for _, s := range c.Sessions {
			text := ""
			for _, m := range s.Messages {
				text += " " + strings.ToLower(m.Text)
			}
			if !strings.Contains(text, strings.ToLower(c.Topic)) {
				continue
			}
			// The tying sessions sit outside the chain's id prefix: the arm
			// counts the chain's own sessions, and a session that explains the
			// words while settling nothing must not pass it by itself.
			if strings.HasPrefix(s.ID, c.ID) {
				answers++
				continue
			}
			ties++
			// It says the term beside the question's ordinary words, not the
			// whole question: a session repeating the question verbatim is the
			// best lexical match there can be, and an arm it always wins
			// measures the fixture rather than the search.
			shared := 0
			for _, w := range strings.Fields(strings.ToLower(c.Paraphrase)) {
				if len(w) > 4 && strings.Contains(text, w) {
					shared++
				}
			}
			if shared < 2 {
				t.Errorf("%s: the tying session shares too little of the wording", s.ID)
			}
			if strings.Contains(text, strings.ToLower(c.Paraphrase)) {
				t.Errorf("%s: the tying session restates the whole question", s.ID)
			}
		}
		if answers == 0 {
			t.Errorf("%s: no session settles anything in the term's own words", c.ID)
		}
		// The map links two words only once they have shared three sessions.
		if ties < cooccurTieCopies {
			t.Errorf("%s: the tie is said %d times, below what the map learns from", c.ID, ties)
		}
	}
	if tied != PromptTiedCount {
		t.Fatalf("tied chains: %d, want %d", tied, PromptTiedCount)
	}
}

// Tied chains have their own subjects. Sharing them with the plain topics would
// let a plain chain's sessions answer the tied question, and the arm would
// measure the corpus rather than the bridge.
func TestTiedTopicsAreTheirOwn(t *testing.T) {
	plain := map[string]bool{}
	for _, tp := range promptTopics() {
		plain[tp.word] = true
	}
	for _, tp := range tiedTopics() {
		if plain[tp.word] {
			t.Errorf("tied topic %q is also a plain topic", tp.word)
		}
	}
}
