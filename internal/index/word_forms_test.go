package index

import (
	"slices"
	"testing"
)

// The exact tier wins and stops: `retry` returns what wrote "retry" and never
// reaches the sessions that wrote "retries", because the stem tier below it
// only runs when exact came up empty. OtherWordForms is what lets the caller
// name the rung that was skipped.
func TestOtherWordFormsNamesFormsTheCorpusHolds(t *testing.T) {
	dir := nlIndex(t,
		"we decided the uploader will retry once on 5xx",
		"we decided to cap uploader retries at 3",
		"the uploader is retrying the same chunk forever",
	)
	got := OtherWordForms(dir, []string{"retry"})
	if !slices.Contains(got["retry"], "retries") || !slices.Contains(got["retry"], "retrying") {
		t.Fatalf("retry did not reach its other forms: %v", got)
	}
	if slices.Contains(got["retry"], "retry") {
		t.Errorf("the term itself is not another form: %v", got)
	}
	// A word whose forms the corpus does not hold gets no entry, so the
	// caller has nothing to print. A line on every search is noise.
	if forms := OtherWordForms(dir, []string{"uploader"}); len(forms) != 0 {
		t.Errorf("uploader = %v, want no other forms", forms)
	}
}
