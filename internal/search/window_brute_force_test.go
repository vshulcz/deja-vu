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
