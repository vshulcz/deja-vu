package search

import (
	"strings"
	"testing"
)

// denseAround builds a message that repeats one query word before and after the
// phrase that answers the question, so the phrase sits wherever the caller puts
// it inside a text far longer than any occurrence cap.
func denseAround(before, after int) string {
	var b strings.Builder
	for i := 0; i < before; i++ {
		b.WriteString("alpha ")
	}
	b.WriteString("alpha beta")
	for i := 0; i < after; i++ {
		b.WriteString(" alpha")
	}
	return b.String()
}

// The phrase is at the very end, past thousands of repetitions. Sampling places
// across the text can still land just short of the final one, so the last place
// is always kept.
func TestTokenWindowSeesPhraseAtTheEnd(t *testing.T) {
	got := tokenWindow(denseAround(6097, 0), []string{"alpha", "beta"})
	if got == 0 {
		t.Fatal("tokenWindow found nothing, so the probe measured nothing")
	}
	if want := len("alpha beta"); got != want {
		t.Errorf("window %d, want %d: the phrase at the end was not measured", got, want)
	}
}

// The phrase sits in the middle, past the point where enumerating occurrences
// from the front used to stop. Taking the first N places measured this message
// against a repetition thousands of characters from the phrase.
func TestTokenWindowSeesPhraseInTheMiddle(t *testing.T) {
	got := tokenWindow(denseAround(6000, 6000), []string{"alpha", "beta"})
	if got == 0 {
		t.Fatal("tokenWindow found nothing, so the probe measured nothing")
	}
	if want := len("alpha beta"); got != want {
		t.Errorf("window %d, want %d: the phrase in the middle was not measured", got, want)
	}
}

// Both query words run past the cap, so there is no rare token to anchor on and
// places get sampled across the text instead. Every place then has a neighbour
// close by, and the window must still come out as that neighbour rather than as
// the distance to whichever place the sampling kept.
func TestTokenWindowWhenEveryQueryWordIsEverywhere(t *testing.T) {
	// Both words appear five thousand times, always a few words apart, and the
	// one place they sit together is at the very end. The lone leading "alpha"
	// puts the first occurrences far enough apart that the cheap
	// first-occurrence bound cannot answer, so the sampling runs.
	low := "alpha " + strings.Repeat("gap ", 100) +
		strings.Repeat("alpha gap gap gap gap beta gap gap gap gap ", 5000) +
		"alpha beta"
	got := tokenWindow(low, []string{"alpha", "beta"})
	if want := len("alpha beta"); got != want {
		t.Errorf("window %d, want %d: the pair at the end was outside the sampled places", got, want)
	}
}

// The phrase at the start has always worked, and must keep working — otherwise
// a fix could pass the tests above by only ever looking at the tail.
func TestTokenWindowStillSeesPhraseAtTheStart(t *testing.T) {
	got := tokenWindow("alpha beta "+strings.Repeat("alpha ", 6000), []string{"alpha", "beta"})
	if want := len("alpha beta"); got != want {
		t.Errorf("window %d, want %d: the phrase at the start was not measured", got, want)
	}
}
