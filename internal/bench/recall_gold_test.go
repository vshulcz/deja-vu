package bench

import "testing"

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
