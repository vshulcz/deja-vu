package digest

import "testing"

// A marker that is also a word of the question makes the check circular: the
// line counts as a conclusion because the asker used the phrase. Measured on
// the benchmark, "по чему у нас в итоге шардирование" promoted a session about
// something else entirely on the words "в итоге".
//
// Removing the skip left the whole suite green.
func TestCarriesDecisionIgnoresAMarkerTheAskerUsed(t *testing.T) {
	line := "в итоге посмотрим на это позже, пока ничего не трогаем"

	if !CarriesDecision(line) {
		t.Fatalf("premise: the line carries the marker %q", line)
	}
	if CarriesDecisionExcept(line, []string{"итоге"}) {
		t.Error("a marker the asker used still counted as a conclusion")
	}
	// A different marker in the same line must still count, or the skip would
	// silence real conclusions whenever a question happens to share a word.
	if !CarriesDecisionExcept("в итоге решили откатить", []string{"итоге"}) {
		t.Error("another marker in the line no longer counts")
	}
}
