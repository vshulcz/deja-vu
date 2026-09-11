package bench

import (
	"strings"
	"testing"
)

// A query the corpus answers five times over is a tie, not a ranking question.
//
// The topic came from i%10 and the variant from i%5, which made the variant a
// function of the topic: each topic's five gold sessions carried the same text,
// and the five queries built from them were one string with five different
// "correct" answers. recall@1 was then capped at 0.20 and MRR at
// (1 + 1/2 + 1/3 + 1/4 + 1/5)/5 = 0.457 — the numbers the bench printed — so the
// two columns that exist to see the order could not move.
func TestEachBenchQueryNamesOneSession(t *testing.T) {
	c := Generate(Seed)
	asked := map[string][]string{}
	for _, q := range c.Queries {
		if len(q.Relevant) != 1 {
			t.Fatalf("query %q names %d sessions; the metric assumes one", q.Text, len(q.Relevant))
		}
		asked[q.Text] = append(asked[q.Text], q.Relevant[0])
	}
	for text, gold := range asked {
		if len(gold) > 1 {
			t.Errorf("%q is asked %d times with a different answer each time: %v", text, len(gold), gold)
		}
	}

	// And the session it names has to be the only one that says it: a second
	// session with the same text is the same tie one level down.
	text := map[string]int{}
	for _, s := range c.Sessions {
		if len(s.Messages) > 0 {
			text[s.Messages[0].Text]++
		}
	}
	for _, q := range c.Queries {
		for _, s := range c.Sessions {
			if s.ID != q.Relevant[0] || len(s.Messages) == 0 {
				continue
			}
			if n := text[s.Messages[0].Text]; n > 1 {
				t.Errorf("%d sessions carry the text of %s, the answer to %q", n, s.ID, q.Text)
			}
		}
	}
}

// And one session, not five, holds the words the query asks with. The subject
// alone sat in four of a topic's five texts, so the exact-phrase query matched
// all four and the label named one: recall@1 could not pass 0.74 however the
// ranker behaved. The occasion in the query is what makes the answer singular.
func TestOneSessionHoldsEachQuerysWords(t *testing.T) {
	c := Generate(Seed)
	for _, q := range c.Queries {
		want := queryWords(q.Text)
		var holders []string
		for _, s := range c.Sessions {
			if len(s.Messages) == 0 {
				continue
			}
			if holdsAll(s.Messages[0].Text, want) {
				holders = append(holders, s.ID)
			}
		}
		if len(holders) != 1 {
			t.Errorf("%q is carried by %d sessions (%v); the metric credits one", q.Text, len(holders), holders)
			continue
		}
		if holders[0] != q.Relevant[0] {
			t.Errorf("%q is carried by %s but credited to %s", q.Text, holders[0], q.Relevant[0])
		}
	}
}

// queryWords are the words a query has to be found by, with the quoting and the
// punctuation a phrase query carries taken off.
func queryWords(text string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = strings.Trim(w, `"'.,;:`)
		if len(w) > 2 {
			out = append(out, w)
		}
	}
	return out
}

func holdsAll(text string, words []string) bool {
	low := strings.ToLower(text)
	for _, w := range words {
		if !strings.Contains(low, w) {
			return false
		}
	}
	return true
}

// The ranking this corpus gates is used mostly on stores that do not read like
// the first half of the topic table. #2734 found the store Russian-dominant and
// changed the decision recogniser for it; the bench stayed English, so a
// regression on Cyrillic passed every column at 1.00. Both halves ask, and each
// asks in its own language — a query that mixes two is nobody's question.
func TestTheRecallCorpusAsksInBothLanguages(t *testing.T) {
	c := Generate(Seed)
	cyrillic := func(s string) bool {
		for _, r := range s {
			if r >= 0x400 && r <= 0x4FF {
				return true
			}
		}
		return false
	}
	ru, en := 0, 0
	for _, q := range c.Queries {
		if cyrillic(q.Text) {
			ru++
			// The occasion travels with the subject: "под нагрузкой", never
			// "under load" after three Russian words.
			for _, word := range []string{"under load", "after a deploy", "on cold start", "nightly job", "replica lag"} {
				if strings.Contains(q.Text, word) {
					t.Errorf("%q asks in two languages at once", q.Text)
				}
			}
			continue
		}
		en++
	}
	if ru < QueryCount/4 || en < QueryCount/4 {
		t.Errorf("the corpus asks %d queries in Russian and %d in English; both halves have to be there", ru, en)
	}
}
