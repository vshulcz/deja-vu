package index

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/query"
)

// DidYouMean respells each word of q that no session uses as the nearest
// word some session does, and returns "" when there is nothing to respell.
// The interactive screen offers it over an empty answer. The close tier has
// already tried one edit on short words by then, so this reaches two on any
// word of five letters or more: "schedlr" is two edits from "scheduler".
func DidYouMean(dir, q string) string {
	if dir == "" {
		dir = DefaultDir()
	}
	idx, err := tokenIndexCached(dir)
	if err != nil {
		return ""
	}
	words := strings.Fields(q)
	changed := false
	for i, w := range words {
		toks := query.Tokens(w)
		if len(toks) != 1 || toks[0] != strings.ToLower(w) {
			continue
		}
		t := toks[0]
		n := len([]rune(t))
		if n < 4 || idx.set[t] {
			continue
		}
		limit := 1
		if n >= 5 {
			limit = 2
		}
		var best []string
		bestD := limit + 1
		idx.candidates(n, limit, func(tok string) {
			switch d := damerauDistance(t, tok, limit); {
			case d < bestD:
				best, bestD = []string{tok}, d
			case d == bestD && d <= limit:
				best = append(best, tok)
			}
		})
		if len(best) == 0 {
			continue
		}
		words[i], changed = mostUsed(dir, best), true
	}
	if !changed {
		return ""
	}
	return strings.Join(words, " ")
}

// mostUsed picks the word more sessions hold, so a typo of a common word is
// not answered with a rare neighbour of it. Past a handful of ties the read
// costs more than the pick is worth, and the first in order stands.
func mostUsed(dir string, words []string) string {
	pick, most := words[0], -1
	if len(words) > 16 {
		return pick
	}
	for _, w := range words {
		posts, err := postingsFor(dir, "t"+w)
		if err == nil && len(posts) > most {
			pick, most = w, len(posts)
		}
	}
	return pick
}
