package main

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
)

// suggestFirstQuery picks a short phrase from the user's own recent history to
// print after the first index build: counts impress, but seeing your own
// three-week-old problem come back is the argument. Empty string means "use
// the generic hint" — a thin corpus never gets a made-up suggestion.
func suggestFirstQuery(dir string) string {
	ss, err := index.SearchWithRecovery(dir, query.Options{All: true}, nil)
	if err != nil || len(ss) < 3 {
		return ""
	}
	// Document frequency over every session; candidate phrases only from
	// recent, human-typed messages.
	df := map[string]int{}
	for _, s := range ss {
		seen := map[string]bool{}
		for _, m := range s.Messages {
			for _, tok := range suggestTokens(m.Text) {
				if !seen[tok] {
					seen[tok] = true
					df[tok]++
				}
			}
		}
	}
	cut := time.Now().AddDate(0, -2, 0)
	total := float64(len(ss))
	best := ""
	bestScore := 0.0
	// Newest first, so an IDF tie resolves to the most recent phrase instead
	// of map iteration order.
	sort.SliceStable(ss, func(i, j int) bool { return ss[i].Updated.After(ss[j].Updated) })
	for _, s := range ss {
		if s.Updated.Before(cut) {
			continue
		}
		for _, m := range s.Messages {
			if m.Role != "user" || digest.IsAgentArtifact(m.Text) {
				continue
			}
			toks := suggestTokens(m.Text)
			for i := 0; i+1 < len(toks); i++ {
				a, b := toks[i], toks[i+1]
				// Both words must recur (a search should hit more than this
				// one session) yet stay rare enough to be distinctive.
				if df[a] < 2 || df[b] < 2 {
					continue
				}
				score := math.Log(total/float64(df[a])) + math.Log(total/float64(df[b]))
				if score > bestScore+1e-9 {
					bestScore = score
					best = a + " " + b
				}
			}
		}
	}
	if bestScore < 1.0 {
		return ""
	}
	return best
}

// suggestTokens keeps the informative words of a message: lowercase, no stop
// words, no redaction markers, no digits-only noise.
func suggestTokens(text string) []string {
	if strings.Contains(text, "[redacted:") {
		return nil
	}
	raw := query.Tokens(text)
	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		tok = strings.Trim(tok, "*#`~_-")
		if len(tok) < 4 || len(tok) > 24 || query.IsStopWord(tok) {
			continue
		}
		letters := 0
		for _, r := range tok {
			if r >= 'a' && r <= 'z' || r >= 'а' && r <= 'я' {
				letters++
			}
		}
		if letters*2 < len(tok) {
			continue
		}
		out = append(out, tok)
	}
	return out
}
