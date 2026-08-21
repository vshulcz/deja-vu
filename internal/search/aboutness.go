package search

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// SessionIsAbout reports how strong a single-term match is, by asking whether
// the session keeps returning to that word. It replaces the language-wide
// rarity test, which admitted a stranger's passing mention of an unusual word
// and refused a person's own question about an ordinary one — "what is the name
// of my hamster" was measured silent while the answer sat in the store.
//
// Measured on LongMemEval: blocks carrying an identifying word of the answer go
// from 60 to 73 of 500, silence from 86 to 47, and injections on prompts whose
// answer is absent from 346 to 339.
const (
	aboutMentions     = 16
	aboutScanMessages = 400
)

func SessionIsAbout(s model.Session, terms []string) int {
	lead := terms
	if len(lead) > 3 {
		lead = lead[:3]
	}
	for _, t := range lead {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		mentions := 0
		// Folding every message of a marathon session costs more than this hook
		// may spend on a keystroke, and a session that is about a word says it
		// early: measured on a real store, capping the scan keeps the median at
		// 44 ms instead of 52 and changes no gate decision on the benchmark.
		for i, m := range s.Messages {
			if i >= aboutScanMessages {
				break
			}
			mentions += strings.Count(strings.ToLower(m.Text), t)
			if mentions >= aboutMentions {
				return 1
			}
		}
	}
	return 0
}
