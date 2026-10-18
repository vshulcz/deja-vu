package prompt

import "testing"

// The hook shares query.IsStopWord, so inflected Russian filler must not
// become a term of its own.
func TestTermsDropInflectedRussianFiller(t *testing.T) {
	for _, term := range Terms("покажите, как делали давайте посмотрите") {
		t.Errorf("filler became a term: %q", term)
	}
}
