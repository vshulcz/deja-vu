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
	var metas []SessionMeta
	for _, meta := range m.Sessions {
		if !sessionMetaMatches(meta, o) {
			continue
		}
		for _, h := range meta.Hit {
			if sigs[h] {
				metas = append(metas, meta)
				break
			}
		}
	}
	if len(metas) == 0 {
		return SearchResult{}, nil
	}
	sort.SliceStable(metas, func(i, j int) bool { return newestFirstMeta(metas[i], metas[j]) })
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
	return SearchResult{Sessions: kept, Tier: query.TierRelevance, Total: total}, nil
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
