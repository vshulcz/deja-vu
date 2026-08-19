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

// A term is a mention where a word begins. Traced by instrumenting the matcher
// on a real store: "mini" scored a hit on "cron minimum granularity is 1
// minute", and that row of a table then won the slot the reader sees first.
//
// Short terms must also end the word. Requiring that of everything under seven
// characters was measured and cost more than it saved — the benchmark went
// 14/14 to 13/14 and the live rate 88% to 86%, because "hermes", "score" and
// "tick" belong inside longer words. Four characters is where the trade turns.
func TestTermIsAMentionWhereAWordBegins(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		term string
		want bool
	}{
		{"a short term inside a longer word", "cron minimum granularity is 1 minute", "mini", false},
		{"a short term as its own word", "подними mini и проверь", "mini", true},
		{"a short term before punctuation", "файл mini.yaml на месте", "mini", true},
		// Long enough to be trusted as a prefix: these read as one topic.
		{"a longer term running on", "the scoreboard was rebuilt", "score", true},
		{"a longer term after a separator", "смотри ~/.hermes/config", "hermes", true},
		// The stem may run on to the right; it still has to start a word.
		{"an inflected ending", "индексацию перенесли", "индексация", true},
		{"a stem inside a longer word", "переиндексацию отложили", "индексация", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TextCarriesTerm(tc.text, tc.term); got != tc.want {
				t.Errorf("TextCarriesTerm(%q, %q) = %v, want %v", tc.text, tc.term, got, tc.want)
			}
		})
	}
}
