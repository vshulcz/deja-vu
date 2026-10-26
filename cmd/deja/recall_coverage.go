package main

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// recallCoverage is how much of a query the best session on a page holds.
//
// The page used to be judged on one word or on all of them. An agent's recall
// query is a list of six to ten words, a path and a flag among them, and no
// session holds every one: of 137 such queries on a real store none matched,
// and the page was headed "No session is about this" above the session that
// held the path and four of the other words. One rule set for that file sat in
// a page headed like that and the agent skipped it.
type recallCoverage struct {
	at     int                // the hit on the page that holds the most
	held   []string           // the query words it holds
	terms  []string           // the query's words
	idf    map[string]float64 // what the store says each word is worth
	weight float64            // the idf of the words held
}

// bestCoverage finds the served session holding the most of the query by what
// its words are worth on this store; the earlier one wins a tie, since the
// page is already in ranked order. A word the store does not hold (a typo, a
// flag about to be added, "640" where the store says "640px") is worth
// nothing here: no session can hold it, and it used to make every one fail.
func bestCoverage(hits []search.Hit, q string, idf map[string]float64) recallCoverage {
	c := recallCoverage{at: -1, terms: dedupTerms(index.RelevanceTerms(q)), idf: idf}
	for i, h := range hits {
		held := sessionHolds(h.Session, c.terms)
		w := 0.0
		for _, t := range held {
			w += idf[t]
		}
		if c.at < 0 || w > c.weight {
			c.at, c.held, c.weight = i, held, w
		}
	}
	return c
}

// aboutIt reports whether the page answers the query in part. One session
// holds at least two of the query's words and at least half of them, at least
// half of what the words the store knows are worth, and one word rare enough
// on this store to identify something. Without the last two a question about
// a subject the store never held reads as answered by any session sharing its
// filler ("final", "verification").
//
// A query that names a path, a flag or an identifier is about that thing, so
// the session also has to hold one of them: ".gitignore site/ final
// verification" is not answered by a session that says "final verification".
// A bare number or version is a value, not a name.
//
// Only for a query of four words or more. In a shorter one the word nothing
// holds is a third of the question or more, and most likely its subject:
// "pgbouncer pool quasar" is a question about quasar.
func (c recallCoverage) aboutIt() bool {
	k, n := len(c.held), len(c.terms)
	if c.at < 0 || n < 4 || k < 2 || 2*k < n {
		return false
	}
	known, rare := 0.0, false
	for _, t := range c.terms {
		known += c.idf[t]
	}
	for _, t := range c.held {
		rare = rare || c.idf[t] >= index.InformativeIDF
	}
	if !rare || 2*c.weight < known {
		return false
	}
	named := false
	for _, t := range c.terms {
		if symbolShaped(t) && hasLetter(t) {
			named = true
			if slices.Contains(c.held, t) {
				return true
			}
		}
	}
	return !named
}

func hasLetter(s string) bool {
	return strings.IndexFunc(s, unicode.IsLetter) >= 0
}

// line says what the best session holds and which words it does not, so the
// reader can tell an incidental word from a missing subject.
func (c recallCoverage) line() string {
	var missing []string
	for _, t := range c.terms {
		if !slices.Contains(c.held, t) {
			missing = append(missing, t)
		}
	}
	s := fmt.Sprintf("No session matched every word; session %d holds %d of %d (%s)",
		c.at+1, len(c.held), len(c.terms), recallListingLine(strings.Join(c.held, ", ")))
	if len(missing) > 0 {
		s += ", not " + recallListingLine(strings.Join(missing, ", "))
	}
	return s + ". Check that it describes what is happening now before acting on it."
}

// sessionHolds returns the terms a session says anywhere, by the matcher the
// excerpts use, so a word counted here is one the reader can find below.
func sessionHolds(s model.Session, terms []string) []string {
	var out []string
	for _, t := range terms {
		if search.TextCarriesTerm(s.Title, t) {
			out = append(out, t)
			continue
		}
		for _, m := range s.Messages {
			if search.TextCarriesTerm(m.Text, t) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

func dedupTerms(terms []string) []string {
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}
