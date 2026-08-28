package index

import "testing"

// PrefixMatches has always answered 0 for an empty prefix — "not a question
// worth answering" — while FindByPrefix resolved it to whichever session sorted
// first, because every id has "" as a prefix. #853 is the rule that the count
// and the resolver must agree; this is that same disagreement with the sign
// flipped, and it reached a user through the MCP resource URI in #1728.
func TestEmptyPrefixResolvesToNothing(t *testing.T) {
	dir := askedFixture(t,
		map[string][]string{
			"aa1": {"why does the pool exhaust under load?"},
			"bb1": {"why is the build slow?"},
		},
		map[string]string{
			"aa1": "2026-03-01T10:00:00Z",
			"bb1": "2026-03-03T10:00:00Z",
		})

	s, ok, err := FindByPrefix(dir, "")
	if err != nil {
		t.Fatalf("empty prefix: %v", err)
	}
	if ok {
		t.Fatalf("an empty prefix opened session %q; every id has \"\" as a prefix, so this is whichever one sorted first", s.ID)
	}

	// The two answers agree, which is the whole point.
	if n := PrefixMatches(dir, ""); n != 0 {
		t.Fatalf("PrefixMatches = %d, want 0", n)
	}

	// A real prefix is untouched: the guard must not be a refusal of everything.
	if got, ok, err := FindByPrefix(dir, "aa"); err != nil || !ok || got.ID != "aa1" {
		t.Fatalf("got %q ok=%v err=%v, want aa1", got.ID, ok, err)
	}
}
