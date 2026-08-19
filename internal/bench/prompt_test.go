package bench

import (
	"testing"
	"time"
)

// This corpus is what makes changes to per-prompt recall arguable instead of
// opinionated, so its shape is the thing to pin. Every property below was
// wrong at some point while it was being written, and each mistake made the
// benchmark score zero or lie.
func TestGeneratePromptShape(t *testing.T) {
	corpus := GeneratePrompt(Seed)
	if corpus.Hash == "" {
		t.Fatal("no hash: two runs cannot be compared")
	}
	topics := map[string]bool{}
	projects := map[string]bool{}
	var real, negative, marathon, fresh int
	for _, c := range corpus.Chains {
		// One project is shared on purpose. Everywhere else a shared project
		// would make a query match two chains and blur the measurement; the
		// bucket exists to measure exactly that condition — a scope holding
		// several unrelated pieces of work and not the answer.
		// The concluded pair shares a project on purpose too: the whole case is
		// that two sessions of the same project hold the subject and only one
		// of them settled it.
		if c.Kind != "bucket" && c.Kind != "haystack" && c.Kind != "haystack-noise" && c.Kind != "russian" &&
			c.Kind != "concluded" && c.Kind != "concluded-noise" {
			if projects[c.Project] {
				t.Fatalf("project %q shared by two chains; a query would match both", c.Project)
			}
			projects[c.Project] = true
		}
		if c.Negative {
			negative++
			if len(c.Sessions) == 0 {
				t.Fatalf("negative chain %s has no sessions to rule out", c.ID)
			}
			continue
		}
		real++
		// One topic per chain: the context corpus gives them all the same
		// vocabulary, and a term in every chain identifies none of them.
		// Filler for the catch-all scope carries no question of its own: it is
		// asked about through the bucket-answer chain, whose question is the
		// one that must go unanswered. A chain nobody asks about still has to
		// earn its place, and this one earns it by being the wrong answer.
		if c.Kind == "bucket" || c.Kind == "haystack-noise" || c.Kind == "concluded-noise" || c.Kind == "background" {
			continue
		}
		if topics[c.Topic] {
			t.Fatalf("topic %q appears twice", c.Topic)
		}
		topics[c.Topic] = true
		if c.Question == "" {
			t.Fatalf("chain %s has no question", c.ID)
		}
		switch c.Kind {
		case "marathon":
			marathon++
		case "fresh":
			fresh++
		}
		for _, s := range c.Sessions {
			if len(s.Messages) == 0 {
				t.Fatalf("chain %s has an empty session", c.ID)
			}
			// A corpus dated in the future reads as newer than now, and the
			// freshness gate withholds all of it.
			if s.Updated.After(time.Now()) {
				t.Fatalf("chain %s is dated in the future: %v", c.ID, s.Updated)
			}
			if s.Project != c.Project {
				t.Fatalf("chain %s has a session filed under %q", c.ID, s.Project)
			}
		}
	}
	if real < 12 || negative != PromptNegativeCount {
		t.Fatalf("chains: %d real, %d negative", real, negative)
	}
	if marathon != 1 || fresh != 1 {
		t.Fatalf("gated shapes: %d marathon, %d fresh — both are needed to measure the gates", marathon, fresh)
	}
}

// The gated shapes have to actually be gated, or the arms measure nothing.
func TestGeneratePromptGatedShapesAreExtreme(t *testing.T) {
	corpus := GeneratePrompt(Seed)
	for _, c := range corpus.Chains {
		switch c.Kind {
		case "marathon":
			if n := len(c.Sessions[0].Messages); n <= 300 {
				t.Fatalf("marathon chain has %d messages, under the cap it exists to cross", n)
			}
		case "fresh":
			if age := time.Since(c.Sessions[0].Updated); age > 15*time.Minute {
				t.Fatalf("fresh chain is %v old, past the window it exists to test", age)
			}
		}
	}
}

// Same seed, same corpus: a benchmark whose inputs drift cannot be compared
// across runs, which is the whole point of reporting a hash.
func TestGeneratePromptIsDeterministic(t *testing.T) {
	a := GeneratePrompt(Seed)
	b := GeneratePrompt(Seed)
	if a.Hash != b.Hash {
		t.Fatalf("hashes differ across runs: %s vs %s", a.Hash, b.Hash)
	}
	if len(a.Chains) != len(b.Chains) {
		t.Fatalf("chain counts differ: %d vs %d", len(a.Chains), len(b.Chains))
	}
	// A different seed must produce a different corpus, or the seed is decoration.
	if c := GeneratePrompt(Seed + 1); c.Hash == a.Hash {
		t.Fatal("the seed changes nothing")
	}
}

func TestPromptTopicsAreUsableTerms(t *testing.T) {
	for _, topic := range promptTopics() {
		if topic.word == "" || topic.fact == "" || topic.question == "" {
			t.Fatalf("incomplete topic: %+v", topic)
		}
		// The corpus exists to test what the extractor keeps, so the topics
		// have to be the short identifiers people actually type.
		if len(topic.word) > 12 {
			t.Fatalf("topic %q is not the short kind this corpus is for", topic.word)
		}
	}
}
