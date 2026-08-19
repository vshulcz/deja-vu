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
// Measured on live prompts from this machine: about a third of the terms
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
			prompt:  "напомни, что мы уже выясняли про can шину на omoda через adb, и что делать дальше",
			want:    []string{"omoda"},
			notWant: []string{"напомни", "выясняли", "через", "дальше"},
		},
		{
			name:    "an interjection and a request verb",
			prompt:  "подожди impl1084 и покажи замер RSS",
			want:    []string{"impl1084"},
			notWant: []string{"подожди", "покажи"},
		},
		{
			// Straight from a live prompt: "нужно снова заняться фильтрацией
			// telegram прокси по quality score".
			name:    "adverbs of repetition and time",
			prompt:  "нужно снова заняться фильтрацией telegram прокси, теперь потом опять проверим",
			want:    []string{"telegram"},
			notWant: []string{"снова", "теперь", "потом", "опять"},
		},
		{
			name:    "attention words at the front",
			prompt:  "погоди, смотри, а в singbox можно такой же роутинг сделать",
			want:    []string{"singbox"},
			notWant: []string{"погоди", "смотри"},
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
