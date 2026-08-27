package index

import (
	"slices"
	"testing"
)

// stemMatches keeps eight forms, in the order they were built: the English
// suffix forms first, the developer synonyms, then the Cyrillic ones. The
// fullest thing a store can hold is a Russian paradigm, and that is seven
// forms — one slot under the cap, with the single-letter endings that carry
// the lemma last in the order, so the lemma is what a tighter cap would cut.
// Nothing said so, and for a Russian query those forms are the whole answer.
func TestAFullRussianParadigmFitsUnderTheFormCap(t *testing.T) {
	for _, tc := range []struct {
		term  string
		lemma string
		forms []string
	}{
		{"настройки", "настройка", []string{
			"настройка", "настройки", "настройке", "настройку", "настройкой", "настройками", "настройках"}},
		{"миграции", "миграция", []string{
			"миграция", "миграции", "миграцию", "миграцией", "миграциях", "миграциями", "миграций"}},
		{"обработчика", "обработчик", []string{
			"обработчик", "обработчика", "обработчику", "обработчиком", "обработчике", "обработчики", "обработчиков"}},
	} {
		// A catalog like a real one: what a transcript would contain, and none
		// of the stubs the expander invents — indexKeys stores tokens, never
		// expansions, so "настройкиed" is in no catalog anywhere.
		catalog := map[string]bool{}
		for _, w := range tc.forms {
			catalog[w] = true
		}
		got := stemMatches(tc.term, catalog)
		if len(got) != len(tc.forms) {
			t.Errorf("stemMatches(%q) kept %d of %d forms: %q", tc.term, len(got), len(tc.forms), got)
		}
		// The lemma above all: it is what the reader would search next, and it
		// is last in the build order.
		if !slices.Contains(got, tc.lemma) {
			t.Errorf("stemMatches(%q) does not reach the lemma %q: %q", tc.term, tc.lemma, got)
		}
	}
}
