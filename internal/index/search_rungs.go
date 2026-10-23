package index

import (
	"fmt"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// postingsHit is what the postings rungs hand on to the record check.
type postingsHit struct {
	posts    []posting
	variants map[string][]string
	tier     string
	used     bool
}

// postingsRung intersects the query's postings, and the substring postings
// when the exact ones are empty. served is set when relevance answered instead.
func postingsRung(dir string, m Manifest, o query.Options, ranking *relevanceOnce) (postingsHit, *SearchResult, error) {
	hit := postingsHit{tier: query.TierExact}
	if o.Regex {
		return hit, nil, nil
	}
	keys := queryKeys(o.Query)
	if len(keys) == 0 {
		return hit, nil, nil
	}
	hit.used = true
	var err error
	hit.posts, err = intersectPostings(dir, retrievalKeys(keys))
	if err != nil {
		return hit, nil, fmt.Errorf("postings: %w", err)
	}
	if len(hit.posts) > 0 {
		return hit, nil, nil
	}
	// grep expectation: "code" should find "opencode". Expand each query
	// token to all indexed tokens containing it (bucket directories only,
	// no record scan), then intersect.
	var variants map[string][]string
	bare, spelledApart := compoundQueryTokens(tokens(o.Query))

	hit.posts, variants, err = intersectSubstringPostingsDetailed(dir, bare)
	if err != nil {
		return hit, nil, fmt.Errorf("substr postings: %w", err)
	}
	// The postings above found the parts; the record check below
	// counts what the reader typed, and the text does not hold the
	// compound. Hand it the words spelled apart as this term's
	// other spelling, which is what the store actually says (#2125).
	if len(spelledApart) > 0 {
		if variants == nil {
			variants = map[string][]string{}
		}
		for tok, apart := range spelledApart {
			variants[tok] = append(variants[tok], apart)
		}
	}
	if len(hit.posts) == 0 {
		return hit, nil, nil
	}
	hit.variants = variants
	hit.tier = query.TierClose
	// A natural-language query that degraded to substring
	// intersection often lands on one incidental session and
	// the ladder used to stop there. When the query carries
	// enough informative words to rank by relevance, prefer
	// that ranking and keep the substring hits as the tail.
	if rel, rerr := ranking.get(dir, m, o); rerr == nil && len(rel.Sessions) > 0 {
		if res, ok := relevanceOverClose(dir, m, o, rel, hit.posts, variants); ok {
			return hit, &res, nil
		}
	}
	return hit, nil, nil
}

// relevanceOverClose serves the relevance ranking with the close tier as its
// tail when the substring hits do not hold relevance's best candidate.
func relevanceOverClose(dir string, m Manifest, o query.Options, rel SearchResult, posts []posting, variants map[string][]string) (SearchResult, bool) {
	// With the variants too, for the reason the scan below
	// takes them: a compound spelled apart is not a
	// substring of what the store wrote, so this
	// comparison would call the close tier empty and hand
	// the answer to relevance (#2125).
	//
	// Only when a candidate survives the filters: no offsets
	// hands the scan the whole record log, and a recall whose
	// project or session filters left none of the hits read
	// all of it, 13 s on a 4050-session store (#5370).
	var closeSS []model.Session
	var serr error
	if cut := cutPostingsBySession(posts, m, o); len(cut) > 0 {
		closeSS, serr = scanRecordsWithVariants(dir, m, o, postingOffsets(cut), variants)
	}
	agree := false
	if serr == nil {
		top := rel.Sessions[0].Harness + ":" + rel.Sessions[0].ID
		for _, c := range closeSS {
			if c.Harness+":"+c.ID == top {
				agree = true
				break
			}
		}
	}
	// Substring hits that contain relevance's own best
	// candidate are trustworthy — keep the close tier
	// (and its variant annotations). When they disagree,
	// the intersection landed on an incidental session;
	// serve the relevance ranking with close as a tail.
	if serr != nil || len(closeSS) == 0 || agree {
		return SearchResult{}, false
	}
	seen := map[string]bool{}
	for _, r := range rel.Sessions {
		seen[r.Harness+":"+r.ID] = true
	}
	merged := rel.Sessions
	for _, c := range closeSS {
		if !seen[c.Harness+":"+c.ID] {
			merged = append(merged, c)
		}
	}
	// The tail carries sessions the relevance pool did
	// not hold, so they are matches too and count
	// toward the total. Adding them on both sides
	// leaves `capped` reading the relevance window,
	// which is still the only thing that withheld
	// anything here.
	tail := len(merged) - len(rel.Sessions)
	return relevanceResult(merged, rel.Total+tail, rel.TermIDF), true
}

// noPostingsLadder answers a query whose words matched no postings at all.
func noPostingsLadder(ranking *relevanceOnce, dir string, m Manifest, o query.Options) (SearchResult, error) {
	if res, done, err := wordLadder(ranking, dir, m, o); done {
		return res, err
	}
	ranked, rerr := ranking.get(dir, m, o)
	if rerr != nil || len(ranked.Sessions) > 0 {
		return ranked, rerr
	}
	// Nothing anywhere. The neighbour map holds the one link left to
	// try — the project's own word for what the reader asked about —
	// and asking it over the question's identifying words is what
	// makes it readable by a sentence (#2331).
	return cooccurNarrowedSearch(dir, m, o)
}

// wordLadder tries the stem, fuzzy, co-occurrence and error-signature rungs in
// turn; done says one of them answered or failed.
func wordLadder(ranking *relevanceOnce, dir string, m Manifest, o query.Options) (SearchResult, bool, error) {
	if result, ferr := stemSearch(dir, m, o); ferr != nil {
		return SearchResult{}, true, fmt.Errorf("stem postings: %w", ferr)
	} else if result.Stemmed {
		return withRelevanceTail(ranking, dir, m, o, result), true, nil
	}
	if result, ferr := fuzzySearch(dir, m, o); ferr != nil {
		return SearchResult{}, true, fmt.Errorf("fuzzy postings: %w", ferr)
	} else if result.Fuzzy {
		return withRelevanceTail(ranking, dir, m, o, result), true, nil
	}
	// Two words that never appear together is the state the
	// co-occurrence rescue was written for — "login" next to "jwks"
	// — and it was called only in the other zero-result branch, so
	// the case it exists for never reached it (#1786).
	if result, ferr := cooccurSearch(dir, m, o); ferr != nil {
		return SearchResult{}, true, fmt.Errorf("cooccur postings: %w", ferr)
	} else if len(result.Sessions) > 0 {
		return withRelevanceTail(ranking, dir, m, o, result), true, nil
	}
	// A pasted error that matched no postings is the case the word
	// ladder cannot win: its identifiers are line numbers and hashes.
	// The friction hashes the store already keeps answer it exactly.
	if result, ferr := errorSigSearch(dir, m, o); ferr != nil {
		return SearchResult{}, true, fmt.Errorf("error signature: %w", ferr)
	} else if len(result.Sessions) > 0 {
		return result, true, nil
	}
	return SearchResult{}, false, nil
}

// withPoolScope reads what o.PoolScope asked for onto a settled exact answer.
func withPoolScope(dir string, m Manifest, o query.Options, res SearchResult, unread *query.Unread, scoped []posting, variants map[string][]string) SearchResult {
	// No offsets would scan the whole record log.
	var extra []model.Session
	if len(scoped) > 0 {
		var serr error
		if extra, serr = scanRecordsWithVariants(dir, m, o, postingOffsets(scoped), variants); serr != nil {
			return res
		}
	}
	for _, s := range extra {
		if unread.Scoped == nil {
			unread.Scoped = map[string]bool{}
		}
		unread.Scoped[s.Harness+":"+s.ID] = true
	}
	res.Sessions = append(res.Sessions, extra...)
	unread.ScopeWhole = true
	return res
}
