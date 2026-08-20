package prompt

import (
	"strings"
	"testing"
)

// Russian keeps short subjects — кеш, хук, бот, сеть, порт — and the floor for
// Cyrillic terms stood at five letters, so none of them could become a search
// term. The English floor came down to three for the same reason; the words
// that are work are named rather than measured.
//
// Measured over live questions from a real machine, dropping the floor added
// nine distinct words across forty-one prompts, six of them closed-class words
// missing from the list. Naming those six is what makes the floor safe: without
// it, blocks opening on a subject word fell from 88% to 53% on a store of
// ordinary short sessions.
func TestShortRussianSubjectsSurvive(t *testing.T) {
	for _, subject := range []string{"кеш", "хук", "бот", "сеть", "порт"} {
		got := Terms("напомни, что там было с " + subject)
		if !contains(got, subject) {
			t.Fatalf("Terms(%q) = %v, dropped the subject", subject, got)
		}
	}
	for _, filler := range []string{"ну вот про это раз", "и еще чтоб при том"} {
		if got := Terms(filler); len(got) > 0 {
			t.Fatalf("Terms(%q) = %v, want nothing: these are closed-class words", filler, got)
		}
	}
}

func contains(all []string, want string) bool {
	for _, s := range all {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}
