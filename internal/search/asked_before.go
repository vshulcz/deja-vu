package search

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/prompt"
)

// A question asked twice is the case this tool exists for, and deja could not
// see most of them. `stats.RepeatQuestions` counts a repeat only when the
// words match exactly, so the same thing asked in other words is two different
// questions to it: measured over this machine's own sessions, 5 by that rule
// and 23 by meaning — 6.5% of every substantial question a person asked.
//
// It is also a different claim than the one the block makes today. "Prior
// sessions matching this request" is about a subject; "you asked this before,
// and here is what came of it" is about the question, and an agent can act on
// the second without deciding whether the first is relevant.

// askedAgainOverlap is how much of the shorter question's terms the two have
// to share. Three quarters keeps "why is the scheduler retrying" with "what
// made the scheduler retry" and separates both from "how does the scheduler
// pick a shard", which shares one word out of three.
const askedAgainOverlap = 0.75

// askedAgainMinTerms is below what a repeat means nothing: two words overlap
// by chance often enough that a claim resting on them reads as noise.
const askedAgainMinTerms = 3

// AskedBefore returns the question this session already asked that the current
// one repeats, or "" when it asked nothing of the sort. Terms rather than
// words: the same question in other words is the case worth catching, and it
// is the only one the exact-match counter misses.
func AskedBefore(s model.Session, terms []string) string {
	if len(terms) < askedAgainMinTerms {
		return ""
	}
	want := make(map[string]bool, len(terms))
	for _, t := range terms {
		want[t] = true
	}
	best, bestShared := "", 0
	for _, m := range s.Messages {
		if m.Role != "user" {
			continue
		}
		past := prompt.Terms(m.Text)
		if len(past) < askedAgainMinTerms {
			continue
		}
		shared := 0
		for _, t := range past {
			if want[t] {
				shared++
			}
		}
		small := len(past)
		if len(terms) < small {
			small = len(terms)
		}
		if float64(shared)/float64(small) < askedAgainOverlap {
			continue
		}
		if shared > bestShared {
			best, bestShared = strings.TrimSpace(firstSentence(m.Text)), shared
		}
	}
	return best
}

// firstSentence keeps the question and drops whatever the person appended to
// it — a pasted log, a second thought. The block quotes this back, so it has
// to read as the question that was asked.
func firstSentence(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "\n"); i > 0 {
		text = text[:i]
	}
	const max = 120
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !isRuneStart(text[cut]) {
		cut--
	}
	return strings.TrimSpace(text[:cut]) + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
