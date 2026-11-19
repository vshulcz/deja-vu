package search

import (
	"strings"
	"testing"
)

// bruteWindow is the definition tokenWindow implements, computed the slow way:
// the tightest span over every combination of one place per token.
func bruteWindow(low string, toks []string) int {
	places := make([][]int, len(toks))
	for ti, tok := range toks {
		for i := 0; i < len(low); {
			j := strings.Index(low[i:], tok)
			if j < 0 {
				break
			}
			places[ti] = append(places[ti], i+j)
			i += j + 1
		}
		if len(places[ti]) == 0 {
			return 0
		}
	}
	best := -1
	var walk func(ti, lo, hi int)
	walk = func(ti, lo, hi int) {
		if ti == len(toks) {
			if span := hi - lo; best < 0 || span < best {
				best = span
			}
			return
		}
		for _, p := range places[ti] {
			nlo, nhi := lo, hi
			if p < nlo || ti == 0 {
				nlo = p
			}
			if end := p + len(toks[ti]); end > nhi || ti == 0 {
				nhi = end
			}
			walk(ti+1, nlo, nhi)
		}
	}
	walk(0, 0, 0)
	if best < 0 {
		return 0
	}
	return best
}

// Anchoring on the rarest token and taking its nearest neighbours on each side
// has to give the same answer as trying every combination — including with more
// than two tokens, and with tokens of different lengths, where which token is
// the anchor changes what the neighbour search sees.
func TestTokenWindowMatchesBruteForce(t *testing.T) {
	// Every case puts the tokens' first occurrences further apart than
	// proximityNear, so tokenWindow does the real work instead of returning its
	// cheap first-occurrence bound.
	far := strings.Repeat("gap ", 100)
	cases := []struct {
		text string
		toks []string
	}{
		{"alpha " + far + "beta alpha", []string{"alpha", "beta"}},
		{strings.Repeat("alpha ", 60) + "beta " + strings.Repeat("alpha ", 30), []string{"alpha", "beta"}},
		{"beta " + strings.Repeat("alpha ", 60) + "beta", []string{"alpha", "beta"}},
		{"aa " + far + strings.Repeat("bbbbbbbb aa ", 20) + "cccccccccccc", []string{"aa", "bbbbbbbb", "cccccccccccc"}},
		{"cccccccccccc " + far + strings.Repeat("bbbbbbbb aa ", 20), []string{"aa", "bbbbbbbb", "cccccccccccc"}},
		{"one " + far + strings.Repeat("one ", 25) + "two three " + strings.Repeat("one ", 25) + "two", []string{"one", "two", "three"}},
		{strings.Repeat("x ", 200) + "needle x haystack", []string{"needle", "haystack"}},
	}
	for _, c := range cases {
		want := bruteWindow(c.text, c.toks)
		got := tokenWindow(c.text, c.toks)
		if got == 0 || want == 0 {
			t.Errorf("toks %v: got %d, brute force %d", c.toks, got, want)
			continue
		}
		if got != want {
			t.Errorf("toks %v: window %d, tightest is %d", c.toks, got, want)
		}
	}
}

// denseAround builds a message that repeats one query word before and after the
// phrase that answers the question, so the phrase sits wherever the caller puts
// it inside a text far longer than any occurrence cap.
func denseAround(before, after int) string {
	var b strings.Builder
	for range before {
		b.WriteString("alpha ")
	}
	b.WriteString("alpha beta")
	for range after {
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
