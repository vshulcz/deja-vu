package bench

import "testing"

func TestGenerateIsDeterministic(t *testing.T) {
	a := Generate(Seed)
	b := Generate(Seed)
	if a.Hash != b.Hash || len(a.Sessions) != SessionCount || len(a.Queries) != QueryCount {
		t.Fatalf("generator changed: hash %q/%q sessions=%d queries=%d", a.Hash, b.Hash, len(a.Sessions), len(a.Queries))
	}
	if a.Hash == Generate(2).Hash {
		t.Fatal("different seeds produced the same corpus")
	}
}

func TestQueriesHaveRelevantSessions(t *testing.T) {
	c := Generate(Seed)
	byID := make(map[string]bool, len(c.Sessions))
	for _, s := range c.Sessions {
		byID[s.ID] = true
	}
	for _, q := range c.Queries {
		if len(q.Relevant) != 1 || !byID[q.Relevant[0]] {
			t.Fatalf("invalid query relevance: %#v", q)
		}
	}
}
