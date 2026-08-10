package index

import (
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// Searching a pasted error is the commonest thing anyone does with a memory
// tool — "why is this broken" is 22.4% of user turns on a real 1165-session
// store — and it is the case the lexical ladder is worst at. A stack trace is
// not a question: it carries paths, line numbers, goroutine ids and a hash or
// two, so an AND over its words matches nothing, and the relevance tier then
// ranks by whichever of those words happen to be rare.
//
// The store already fingerprints errors: every session carries the hashes of
// the specific lines it tripped over (SessionMeta.Hit, built for `deja
// friction`). Matching on that hash finds the sessions that hit this error
// even when not one word of the paste appears in them the same way.
//
// This runs after the exact/stem/fuzzy rungs and before relevance, so it never
// takes a case the exact tier could answer.
func errorSigSearch(dir string, m Manifest, o query.Options) (SearchResult, error) {
	if o.Regex || strings.Contains(o.Query, "\"") {
		return SearchResult{}, nil
	}
	sigs := querySigs(o.Query)
	if len(sigs) == 0 {
		return SearchResult{}, nil
	}
	type scored struct {
		meta SessionMeta
		hits int
	}
	var cand []scored
	for _, meta := range m.Sessions {
		if !sessionMetaMatches(meta, o) {
			continue
		}
		hits := 0
		for _, h := range meta.Hit {
			if sigs[h] {
				hits++
			}
		}
		if hits > 0 {
			cand = append(cand, scored{meta, hits})
		}
	}
	if len(cand) == 0 {
		return SearchResult{}, nil
	}
	// Rank by how much of the paste a session hit, not by recency alone. A long
	// trace carries several error lines; the session that tripped over more of
	// them is the closer match, and newest-first only decided ties before this.
	sort.SliceStable(cand, func(i, j int) bool {
		if cand[i].hits != cand[j].hits {
			return cand[i].hits > cand[j].hits
		}
		return newestFirstMeta(cand[i].meta, cand[j].meta)
	})
	metas := make([]SessionMeta, len(cand))
	for i := range cand {
		metas[i] = cand[i].meta
	}
	total := len(metas)
	if len(metas) > relevanceWindow {
		metas = metas[:relevanceWindow]
	}
	ss, err := sessionsForMetas(dir, metas)
	if err != nil {
		return SearchResult{}, err
	}
	// The sessions come back whole, and what the caller needs is the part that
	// hit this error: keep the records carrying the signature plus the two
	// around each, the same neighbourhood rule the prompt hook uses.
	for i := range ss {
		ss[i].Messages = aroundErrorLines(ss[i].Messages, sigs)
	}
	kept := ss[:0]
	for _, s := range ss {
		if len(s.Messages) > 0 {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		return SearchResult{}, nil
	}
	// Capped is about the window, not the neighbourhood filter. relevanceResult
	// would set Capped = total > len(kept), but kept also shrinks when
	// aroundErrorLines drops a session whose stored Hit hash no longer appears
	// in its records — that is not the window hiding a retrievable session, so
	// it must not read as "showing N of M". Cap only when the window trimmed.
	return SearchResult{
		Sessions: kept,
		Tier:     query.TierError,
		Total:    total,
		Capped:   total > relevanceWindow,
	}, nil
}

// querySigs hashes every line of the query that names something specific that
// went wrong. A paste usually carries one; a long trace can carry several.
func querySigs(q string) map[uint64]bool {
	out := map[uint64]bool{}
	for _, raw := range strings.Split(q, "\n") {
		if line, ok := FrictionLine(raw); ok {
			out[frictionHash(line)] = true
		}
	}
	return out
}

// aroundErrorLines narrows a session to the messages that carry one of the
// signatures, with two either side so the fix that followed is visible.
func aroundErrorLines(ms []model.Message, sigs map[uint64]bool) []model.Message {
	const window = 2
	keep := map[int]bool{}
	for i, m := range ms {
		hit := false
		for _, raw := range strings.Split(m.Text, "\n") {
			if line, ok := FrictionLine(raw); ok && sigs[frictionHash(line)] {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		for j := i - window; j <= i+window; j++ {
			if j >= 0 && j < len(ms) {
				keep[j] = true
			}
		}
	}
	if len(keep) == 0 {
		return nil
	}
	out := make([]model.Message, 0, len(keep))
	for i, m := range ms {
		if keep[i] {
			out = append(out, m)
		}
	}
	return out
}
