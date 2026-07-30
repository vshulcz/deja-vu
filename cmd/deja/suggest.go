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
			toks := suggestPhraseTokens(m.Text)
			for i := 0; i+1 < len(toks); i++ {
				a, b := toks[i], toks[i+1]
				// Adjacent in the text, not merely adjacent after filtering.
				// Dropping stop words joins words that were sentences apart, and
				// the suggestion then reads as a fragment rather than as
				// something anyone would type — "приятной фишечек" came from two
				// unrelated halves of one message.
				if a == "" || b == "" {
					continue
				}
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

// suggestPhraseTokens is suggestTokens with a gap marker: an empty string
// wherever a word was dropped, so callers can tell a real phrase from two words
// that only became neighbours after filtering.
func suggestPhraseTokens(text string) []string {
	if strings.Contains(text, "[redacted:") {
		return nil
	}
	raw := query.Tokens(text)
	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		if keep := suggestToken(tok); keep != "" {
			out = append(out, keep)
		} else {
			out = append(out, "")
		}
	}
	return out
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
		if keep := suggestToken(tok); keep != "" {
			out = append(out, keep)
		}
	}
	return out
}

// suggestToken returns the word if it carries information, or "" if it is a
// stop word, a marker, or mostly punctuation and digits.
func suggestToken(tok string) string {
	tok = strings.Trim(tok, "*#`~_-")
	if len(tok) < 4 || len(tok) > 24 || query.IsStopWord(tok) {
		return ""
	}
	// Identifiers and code fragments are rare by nature, so IDF loves them and
	// the suggestion becomes something nobody would type: a struct literal, a
	// field name from a pasted payload.
	if isCodeToken(tok) {
		return ""
	}
	letters := 0
	for _, r := range tok {
		if r >= 'a' && r <= 'z' || r >= 'а' && r <= 'я' {
			letters++
		}
	}
	if letters*2 < len(tok) {
		return ""
	}
	return tok
}

// isCodeToken spots a word that came out of source rather than out of a
// sentence: internal punctuation a person does not type mid-word.
func isCodeToken(tok string) bool {
	return strings.ContainsAny(tok, "_{}[]()<>/\\:;=|@$")
}
