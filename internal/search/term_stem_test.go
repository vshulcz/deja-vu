package search

import "testing"

// A term long enough to survive losing its ending matches the inflected forms
// of itself; a short one does not, because a trimmed prefix of it would match
// half the language. The ranking reaches sessions through a stem fold and the
// digest did not, so a question about "индексация" found no line in the session
// that says "индексацию" and the block fell back to the top of the transcript.
func TestTermStemMatchesInflectionAndNotEverything(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		term string
		want bool
	}{
		{"the same form", "индексацию перенесли в воркер", "индексацию", true},
		{"an inflected ending", "индексацию перенесли в воркер", "индексация", true},
		{"another case ending", "решение про индексации приняли вчера", "индексация", true},
		// Short terms keep the exact rule: "index" trimmed to "ind" would sit
		// inside "indeed", "independent" and a hundred others.
		{"a short term is not trimmed", "indeed we shipped it", "index", false},
		{"a short term still matches exactly", "the index was rebuilt", "index", true},
		// Same root, different ending, is the point of the trim: "migrating"
		// and "migration" are one topic.
		{"a long term matches its own root", "the migration ran", "migrating", true},
		// Trimming must not reach so far that a shorter unrelated word inside
		// the same family collides.
		{"a long term does not match a stranger", "документ подписан вчера", "документация", false},
		{"an empty term matches nothing", "anything at all", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TextCarriesTerm(tc.text, tc.term); got != tc.want {
				t.Errorf("TextCarriesTerm(%q, %q) = %v, want %v", tc.text, tc.term, got, tc.want)
			}
		})
	}
}
