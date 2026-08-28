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
		var answer, tie int
		for _, s := range c.Sessions {
			text := ""
			for _, m := range s.Messages {
				text += " " + strings.ToLower(m.Text)
			}
			saysTerm := strings.Contains(text, strings.ToLower(c.Topic))
			saysOther := strings.Contains(text, strings.ToLower(c.Paraphrase))
			switch {
			case saysTerm && saysOther:
				tie++
				// The tie explains; it must not be able to satisfy the arm on
				// its own, and the probe counts only the chain's own ids.
				if strings.HasPrefix(s.ID, c.ID) {
					t.Errorf("%s: the tying session can pass the arm by itself", c.ID)
				}
			case saysTerm:
				answer++
			}
		}
		if answer == 0 {
			t.Errorf("%s: no session settles anything in the term's own words", c.ID)
		}
		if tie == 0 {
			t.Errorf("%s: no session ties the ordinary words to the term", c.ID)
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
