package search

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
)

// termSet answers which of a query's terms a lowercased message holds.
//
// The relevance tier matches on every surface form of every word, and a Russian
// word brings around forty of them: «сервер», «сервера», «серверами», ... A
// three-word Russian question is 110 to 170 terms against 20 to 50 for an
// English one, and asking strings.Contains for each over every message of fifty
// sessions was where a Cyrillic recall spent its extra half second (#5378).
//
// Almost every form starts with a shorter term of the same list, its stem or
// the word itself, and a form can only occur where that prefix occurs. So the
// prefix is searched for once, and each longer form is checked at the places
// the prefix was found. The answer is the same set strings.Contains gives.
type termSet struct {
	terms []string
	// roots are the terms searched for in the text; kids[i] are the indexes of
	// the terms that start with roots[i], checked only where it occurs.
	roots []int
	kids  [][]int
	// folded is the CJK fold of a term that has one, which is matched against
	// the folded text as before and never grouped under a prefix.
	folded []string
}

func newTermSet(terms []string) *termSet {
	ts := &termSet{terms: terms, folded: make([]string, len(terms))}
	parent := make([]int, len(terms))
	for i, t := range terms {
		parent[i] = -1
		if ft := cjkfold.String(t); ft != t || cjkfold.HasCJK(t) {
			ts.folded[i] = ft
		}
	}
	for i, t := range terms {
		if ts.folded[i] != "" || t == "" {
			continue
		}
		// The shortest other term this one starts with, so a word's forms all
		// hang from its stem rather than from each other.
		for j, p := range terms {
			if j == i || ts.folded[j] != "" || p == "" || len(p) >= len(t) || !strings.HasPrefix(t, p) {
				continue
			}
			if parent[i] < 0 || len(p) < len(terms[parent[i]]) {
				parent[i] = j
			}
		}
	}
	at := make(map[int]int, len(terms))
	for i := range terms {
		if parent[i] < 0 {
			at[i] = len(ts.roots)
			ts.roots = append(ts.roots, i)
			ts.kids = append(ts.kids, nil)
		}
	}
	for i, p := range parent {
		if p >= 0 {
			r := at[p]
			ts.kids[r] = append(ts.kids[r], i)
		}
	}
	return ts
}

// match fills found with which terms low holds; found has len(terms). pos is
// scratch for the places a root occurs and is returned for reuse. probes is how
// many substring searches ran over the text, so a test can see the grouping
// without timing it.
func (ts *termSet) match(low string, found []bool, pos []int) (_ []int, probes int) {
	var foldedLow string
	for ri, i := range ts.roots {
		t := ts.terms[i]
		if ft := ts.folded[i]; ft != "" {
			found[i] = strings.Contains(low, t)
			probes++
			if !found[i] {
				if foldedLow == "" {
					foldedLow = cjkfold.String(low)
				}
				found[i] = strings.Contains(foldedLow, ft)
				probes++
			}
			continue
		}
		kids := ts.kids[ri]
		if len(kids) == 0 {
			found[i] = strings.Contains(low, t)
			probes++
			continue
		}
		pos = pos[:0]
		for from := 0; ; {
			probes++
			k := strings.Index(low[from:], t)
			if k < 0 {
				break
			}
			pos = append(pos, from+k)
			// One byte on, not past the match: a prefix can overlap itself.
			from += k + 1
		}
		found[i] = len(pos) > 0
		for _, c := range kids {
			found[c] = false
			rest := ts.terms[c][len(t):]
			for _, p := range pos {
				if strings.HasPrefix(low[p+len(t):], rest) {
					found[c] = true
					break
				}
			}
		}
	}
	return pos, probes
}
