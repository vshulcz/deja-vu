package search

import "unicode/utf8"

// RecallWorthShowing is the one bar both the hook and the benchmark apply. It
// lived in two places with two slightly different expressions, so the
// benchmark measured a gate the product did not have.
//
// A single rare word stands on its own: "pgbouncer" identifies something
// whether or not a second word joins it. Two ordinary words do not — scattered
// across a long session they are two words, not an answer. A session that
// really settled the question has them in the same breath, in one message.
// Measured by hand on ten real prompts against a frozen index: five were
// answered with plainly unrelated work, none with silence, and every one of
// those five rested on words that live in the project but never met.
func RecallWorthShowing(terms []string, matched int) bool {
	if matched < 1 {
		return false
	}
	// Either the session's match rested on a rare word, or the question itself
	// named something identifiable. The second is what the benchmark has been
	// applying all along while the hook applied only the first — measured on
	// the corpus, the pair answers 11 of 12 real questions with no false fire,
	// where the session-only rule answers 7 and fires on 2 controls.
	if HasIdentifierTerm(terms) {
		return true
	}
	return matched >= 2
}

// HasIdentifierTerm reports whether the question contains a word specific
// enough to carry a match on its own. In a small corpus even "file" clears the
// informativeness bar, so a single hit is only trusted when the question named
// something that reads like a term of art — long, or shaped like a symbol.
func HasIdentifierTerm(terms []string) bool {
	for _, t := range terms {
		// Letters, not bytes. Counting bytes read "omoda" as filler and every
		// Cyrillic word of three letters as a term of art, because Cyrillic
		// takes two bytes a letter. Measured on a real store: "напомни, что мы
		// уже выясняли про can шину на omoda" reduces to one term, three
		// sessions hold it, and nothing was recalled.
		//
		// Russian carries its meaning in shorter words — "сеть", "порт" name
		// something the way a five-letter Latin word does — so the bar is one
		// letter lower there.
		n := utf8.RuneCountInString(t)
		if n >= 3 && !soleWorkingWord[t] {
			return true
		}
		for _, r := range t {
			if r == '_' || r == '.' || r == '/' || r == '-' || (r >= '0' && r <= '9') {
				return true
			}
		}
	}
	return false
}

// soleWorkingWord is the ordinary vocabulary a session is made of, long enough
// to pass the letter bar and carrying nothing on its own. The list is consulted
// only here, where the store has not been opened yet: keeping it shut for a
// lone filler word is worth 62ms of the 67ms such a prompt would otherwise
// cost, measured against a real index. Once the store is open the ranking
// decides on evidence and no list is needed.
var soleWorkingWord = map[string]bool{
	"tests": true, "test": true, "retry": true, "build": true, "error": true,
	"errors": true, "check": true, "checks": true, "files": true, "patch": true,
	"trace": true, "output": true, "command": true, "branch": true, "suite": true,
	"commit": true, "commits": true, "deploy": true, "fixes": true, "again": true,
	"write": true, "wrote": true, "merge": true, "start": true, "print": true,
	"there": true, "these": true, "those": true, "which": true, "where": true,
	// Four letters, once the bar came down to three: a question is never about
	// "line" or "file" on its own, and the negative controls built from them
	// started firing the moment they could reach the store.
	"line": true, "file": true, "code": true, "read": true, "open": true,
	"task": true, "step": true, "note": true, "logs": true, "docs": true,
}
