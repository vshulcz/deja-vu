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
// strong is how many of the terms this session matched are rare ones. It is
// what the hook has always had and the benchmark always discarded, which is how
// the two came to measure different rules twice. Taking it here makes that
// impossible rather than merely noticeable.
//
// It is taken and not yet read, on purpose. The obvious first use —
// "a session that matched a rare term is worth showing" — looked equivalent to
// the rule below and is not: strong counts rareness by corpus frequency and
// HasIdentifierTerm judges the shape of the word, so a short ordinary-looking
// word can be rare in a small corpus. Wired in as a shortcut it put two false
// fires on the benchmark's negative controls. Threading it changes nothing
// today; using it is the next rule's decision, and it will now land in every
// instrument at once.
func RecallWorthShowing(terms []string, matched, strong int, known map[string]float64) bool {
	_ = strong // see the note above: taken so the instruments cannot drift
	if matched < 1 {
		return false
	}
	// Either the session's match rested on a rare word, or the question itself
	// named something identifiable. The second is what the benchmark has been
	// applying all along while the hook applied only the first — measured on
	// the corpus, the pair answers 11 of 12 real questions with no false fire,
	// where the session-only rule answers 7 and fires on 2 controls.
	if HasIdentifierTermKnown(terms, known) {
		return true
	}
	return matched >= 2
}

// identifierKnownFrom is how many words a question needs before its subject is
// checked against the store.
const identifierKnownFrom = 3

// identifierIDFFloor is how rare a word has to be in this store before its
// shape may carry a match on its own. Shape alone admits any word of three
// letters that is not on the working-word list, and that list is written by
// hand — "passed", "rows", "delay" are not on it and name nothing.
//
// Asking the store instead of the list: measured by cross-pairing 400 real
// prompts against a project that cannot hold their answer, where every fire is
// a false one, this takes them from 129 to 89 while the same questions asked at
// home still fire 398 times out of 400. Higher costs the home rate — at 5.5 it
// is 396 — and buys nothing: 91.
const identifierIDFFloor = 4.0

// HasIdentifierTermKnown is HasIdentifierTerm over the words the corpus
// actually holds.
//
// A word is judged a term of art by its shape, which says nothing about whether
// this store has ever seen it. So a question naming something that lives in
// another project entirely — "which branch was the oauth rotation on when the
// suite passed" — cleared the bar on "oauth", and then a single match on
// "branch" was enough to answer it. The subject contributed nothing: what
// matched was the vocabulary every working session is made of, and the session
// that says those words most often is whichever session is longest.
//
// known is the idf of each term the corpus contains; a term missing from it
// appears nowhere in the store. Nil means the caller has no such map and every
// term is taken as known, which is the behaviour this had before.
func HasIdentifierTermKnown(terms []string, known map[string]float64) bool {
	// A short question is all subject: "как там pr, смержился?" names the thing
	// and one working verb, and there is nothing else in it for a candidate to
	// have matched instead. The vocabulary a session is carried by only exists
	// in a question long enough to hold it.
	if known == nil || len(terms) <= identifierKnownFrom {
		return HasIdentifierTerm(terms)
	}
	kept := make([]string, 0, len(terms))
	for _, t := range terms {
		if v, ok := known[t]; ok && v >= identifierIDFFloor {
			kept = append(kept, t)
		}
	}
	return HasIdentifierTerm(kept)
}

// IdentifyingTerms is the question's words that are rare enough in this store
// to say what a session is about. The caller that has the ranking's own idf
// gets that verdict; without it there is nothing to judge by and the whole
// question stands.
func IdentifyingTerms(terms []string, known map[string]float64) []string {
	if known == nil {
		return terms
	}
	kept := make([]string, 0, len(terms))
	for _, t := range terms {
		if v, ok := known[t]; ok && v >= identifierIDFFloor {
			kept = append(kept, t)
		}
	}
	return kept
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
