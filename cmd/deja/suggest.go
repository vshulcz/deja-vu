package main

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
)

// suggestFirstQuery picks a short phrase from the user's own recent history to
// print after the first index build: counts impress, but seeing your own
// three-week-old problem come back is the argument. Empty string means "use
// the generic hint" — a thin corpus never gets a made-up suggestion.
// Both halves read the user role only. That is what the suggestion is made of
// — a phrase someone would type — and it is also what makes this affordable on
// the first screen: tokenizing every message in the store to score a two-word
// phrase cost 2.2 s of the brief's 3.2 s, against 0.19 s for the turns a person
// actually wrote (#625).
func suggestFirstQuery(dir string) string {
	ss, err := index.SearchWithRecovery(dir, query.Options{All: true, Role: "user"}, nil)
	if err != nil {
		return ""
	}
	// "Your own three-week-old problem" has to be yours. On a machine whose
	// history arrived by sync, an unfiltered pick printed a phrase out of a
	// peer's session on the first screen — while search, the recent block and
	// the status line all refuse those sessions (#1352, the shape #1026 and
	// #1350 fixed elsewhere). Browsing gate, as on those surfaces.
	ss, _ = policyFilterSessionsCounted(policy.ActivationSearch, ss)
	if len(ss) < 3 {
		return ""
	}
	// Document frequency over what was typed in the sessions above — the ones a
	// rule lets this reader see — and candidate phrases only from the recent
	// part of them. Counting frequency over the whole store instead would let
	// how often a word appears in withheld sessions decide which of the
	// reader's own phrases gets shown.
	df := map[string]int{}
	for _, s := range ss {
		seen := map[string]bool{}
		for _, m := range s.Messages {
			if digest.IsAgentArtifact(m.Text) {
				continue
			}
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
