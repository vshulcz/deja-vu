package prompt

import (
	"slices"
	"testing"
)

// People name a version or a pull request in two characters and nothing else in
// the question identifies anything: "как там pr, смержился?" reduces to a verb
// once the length floor drops the subject, and the answer is never found.
func TestTermsKeepShortSubjects(t *testing.T) {
	for _, tc := range []struct{ prompt, want string }{
		{"как там pr, смержился?", "pr"},
		{"ну что там v3 показал", "v3"},
		{"а ci на этой ветке зелёный?", "ci"},
	} {
		got := Terms(tc.prompt)
		if !slices.Contains(got, tc.want) {
			t.Errorf("Terms(%q) = %v, want it to keep %q", tc.prompt, got, tc.want)
		}
	}
}

// The floor still holds for two letters that name nothing.
func TestTermsDropTwoLetterFiller(t *testing.T) {
	for _, tc := range []struct{ prompt, drop string }{
		{"же он опять упал", "же"},
		{"do it again please", "do"},
	} {
		if got := Terms(tc.prompt); slices.Contains(got, tc.drop) {
			t.Errorf("Terms(%q) = %v, must not keep %q", tc.prompt, got, tc.drop)
		}
	}
}
