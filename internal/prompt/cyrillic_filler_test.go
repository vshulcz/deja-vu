package prompt

import (
	"strings"
	"testing"
)

// The English filler list covers its own language: a question built from
// "can you run the tests again" recalls nothing. The Russian one did not, so
// the same question asked in Russian carried its filler into ranking — and a
// filler term matches a filler line, which then takes the slot the reader sees
// first.
//
// In live prompts, about a third of the terms
// extracted from a Russian question were words like these.
func TestCyrillicFillerLeavesOnlyTheSubject(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prompt  string
		want    []string
		notWant []string
	}{
		{
			name:    "a question with one real subject",
			prompt:  "напомни, что мы уже выясняли про офсеты в kafka через cli, и что делать дальше",
			want:    []string{"kafka"},
			notWant: []string{"напомни", "выясняли", "через", "дальше"},
		},
		{
			name:    "an interjection and a request verb",
			prompt:  "подожди worker42 и покажи замер RSS",
			want:    []string{"worker42"},
			notWant: []string{"подожди", "покажи"},
		},
		{
			// Adverbs of repetition and time ("снова", "теперь", "потом", "опять")
			// should be stripped so only the technical subject remains.
			name:    "adverbs of repetition and time",
			prompt:  "нужно снова заняться балансировкой envoy роутинга, теперь потом опять проверим",
			want:    []string{"envoy"},
			notWant: []string{"снова", "теперь", "потом", "опять"},
		},
		{
			name:    "attention words at the front",
			prompt:  "погоди, смотри, а в envoy можно такой же роутинг сделать",
			want:    []string{"envoy"},
			notWant: []string{"погоди", "смотри"},
		},
		{
			name:    "meta discussion words",
			prompt:  "давай по порядку разобрать проблемы и улучшения в pgbouncer, структуру и архитектуру",
			want:    []string{"pgbouncer"},
			notWant: []string{"порядку", "разобрать", "проблемы", "улучшения", "структуру", "архитектуру"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Terms(tc.prompt)
			joined := " " + strings.Join(got, " ") + " "
			for _, w := range tc.want {
				if !strings.Contains(joined, " "+w+" ") {
					t.Errorf("the subject %q was dropped: %v", w, got)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(joined, " "+w+" ") {
					t.Errorf("filler %q survived as a term: %v", w, got)
				}
			}
		})
	}
}

// A word at the end of a sentence arrives with its full stop attached. The dot
// then does two things at once: it marks the token as an identifier, so the
// term counts as one, and it stops the term from ever matching the text, which
// spells the word without it. Measured on a live prompt: "retry budget. с чего
// начать" produced the term "budget.", which matched nothing anywhere while
// still earning the recall its place.
func TestTermsTrimSentencePunctuationButKeepPaths(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prompt  string
		want    []string
		notWant []string
	}{
		{
			name:    "a full stop is not part of the word",
			prompt:  "нужно заняться балансировкой envoy роутинга по retry budget. с чего начать",
			want:    []string{"budget"},
			notWant: []string{"budget."},
		},
		{
			// The dot inside is what makes these one word, and trimming the
			// edges must not reach them.
			name:   "an address keeps its dots",
			prompt: "4 поды подняли 203.0.113.223 подключай ее в кластер",
			want:   []string{"203.0.113.223"},
		},
		{
			// A directory named mid-sentence keeps a trailing separator, and a
			// flag quoted mid-sentence keeps a trailing dash. Neither belongs
			// to the word.
			name:    "a trailing separator is not part of the word",
			prompt:  "посмотри в cmd/deja/, там баг с флагом --verbose-",
			want:    []string{"cmd/deja"},
			notWant: []string{"cmd/deja/"},
		},
		{
			name:   "a path keeps its slashes and extension",
			prompt: "посмотри cmd/deja/hook_prompt.go и main.go, там баг",
			want:   []string{"cmd/deja/hook_prompt.go", "main.go"},
		},
		{
			name:    "workflow preamble without technical subject yields no terms",
			prompt:  "сначала проверим что происходит в проекте, посмотрим последние коммиты и разберемся что сломалось, ничего не пушить",
			notWant: []string{"сначала", "проверим", "происходит", "проекте", "посмотрим", "последние", "коммиты", "разберемся", "сломалось", "ничего", "пушить"},
		},
		{
			name:    "workflow preamble preserves real technical subject",
			prompt:  "сначала посмотрим последние изменения в pgbouncer и разберемся что сломалось",
			want:    []string{"pgbouncer"},
			notWant: []string{"сначала", "посмотрим", "последние", "изменения", "разберемся", "сломалось"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Terms(tc.prompt)
			joined := " " + strings.Join(got, " ") + " "
			for _, w := range tc.want {
				if !strings.Contains(joined, " "+w+" ") {
					t.Errorf("%q is missing from the terms: %v", w, got)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(joined, " "+w+" ") {
					t.Errorf("%q survived as a term: %v", w, got)
				}
			}
		})
	}
}
