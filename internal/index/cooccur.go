package index

import (
	"path/filepath"
	"sort"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// Co-occurrence rescue: the corpus itself knows that this user's "login"
// lives next to "jwks" and "rotation". A compact neighbor map built at full
// rebuild lets a zero-result query swap one token for a proven neighbor —
// personal, deterministic, no models. The map regenerates on every full
// rebuild and is intentionally left stale by incremental appends.

const (
	cooccurFile        = "cooccur.gob"
	cooccurMinDF       = 3  // a pattern, not a one-off
	cooccurTokensPerSn = 64 // rarest informative tokens per session
	cooccurNeighbors   = 6  // kept per token
	cooccurMinPair     = 3  // sessions two tokens must share
	cooccurMaxSessions = 20000
)

func cooccurPath(dir string) string { return filepath.Join(dir, cooccurFile) }

// buildCooccur writes the neighbor map into the build directory. Failures are
// swallowed: rescue is an extra, never a reason to fail an index build.
func buildCooccur(tmp string, ss []model.Session) {
	if len(ss) < cooccurMinDF || len(ss) > cooccurMaxSessions {
		return
	}
	// Tokens become dense ids once, so the pair pass hashes integers instead
	// of strings. That inner loop runs for every pair of every session's kept
	// tokens — thousands of sessions times up to 64 tokens each — and string
	// hashing there was most of what a cold build spent on this map.
	ids := map[string]uint32{}
	var toks []string
	id := func(t string) uint32 {
		if v, ok := ids[t]; ok {
			return v
		}
		v := uint32(len(toks))
		ids[t] = v
		toks = append(toks, t)
		return v
	}
	var df []int
	perSession := make([][]uint32, 0, len(ss))
	seen := map[uint32]bool{}
	for _, s := range ss {
		clear(seen)
		for _, m := range s.Messages {
			for _, tok := range tokens(m.Text) {
				if len(tok) < 4 || query.IsStopWord(tok) {
					continue
				}
				v := id(tok)
				if seen[v] {
					continue
				}
				seen[v] = true
			}
		}
		list := make([]uint32, 0, len(seen))
		for v := range seen {
			list = append(list, v)
			for len(df) <= int(v) {
				df = append(df, 0)
			}
			df[v]++
		}
		perSession = append(perSession, list)
	}
	maxDF := len(ss) / 4
	if maxDF < 8 {
		maxDF = 8
	}
	band := func(v uint32) bool { return df[v] >= cooccurMinDF && df[v] <= maxDF }

	// One packed key per unordered pair, canonical by id. The neighbour pass
	// below expands it back to both sides, so which side is canonical never
	// reaches the output.
	pairs := map[uint64]int{}
	kept := make([]uint32, 0, cooccurTokensPerSn)
	for _, list := range perSession {
		kept = kept[:0]
		for _, v := range list {
			if band(v) {
				kept = append(kept, v)
			}
		}
		// rarest first, capped: ubiquitous sessions must not explode the map
		sort.Slice(kept, func(i, j int) bool {
			if df[kept[i]] == df[kept[j]] {
				return toks[kept[i]] < toks[kept[j]]
			}
			return df[kept[i]] < df[kept[j]]
		})
		if len(kept) > cooccurTokensPerSn {
			kept = kept[:cooccurTokensPerSn]
		}
		for i := 0; i < len(kept); i++ {
			for j := i + 1; j < len(kept); j++ {
				a, b := kept[i], kept[j]
				if a > b {
					a, b = b, a
				}
				pairs[uint64(a)<<32|uint64(b)]++
			}
		}
	}
	type nc struct {
		t string
		c int
	}
	// Each canonical pair (a,b) is a neighbor of both a and b, so one pass over
	// the half-map rebuilds the full candidate lists. Filtering at threshold
	// here keeps the transient cand map to the pairs that can actually survive.
	cand := map[string][]nc{}
	for key, c := range pairs {
		if c < cooccurMinPair {
			continue
		}
		a, b := toks[uint32(key>>32)], toks[uint32(key)]
		cand[a] = append(cand[a], nc{b, c})
		cand[b] = append(cand[b], nc{a, c})
	}
	neighbors := map[string][]string{}
	for tok, list := range cand {
		sort.Slice(list, func(i, j int) bool {
			if list[i].c == list[j].c {
				return list[i].t < list[j].t
			}
			return list[i].c > list[j].c
		})
		if len(list) > cooccurNeighbors {
			list = list[:cooccurNeighbors]
		}
		out := make([]string, len(list))
		for i, e := range list {
			out[i] = e.t
		}
		neighbors[tok] = out
	}
	if len(neighbors) == 0 {
		return
	}
	_ = writeGob(cooccurPath(tmp), neighbors)
}

func readCooccur(dir string) map[string][]string {
	var m map[string][]string
	if err := readGob(cooccurPath(dir), &m); err != nil {
		return nil
	}
	return m
}

// cooccurSearch is the last lexical resort: every stem/fuzzy avenue came up
// empty, so try replacing exactly one query token with a corpus-proven
// neighbor. The swap is narrated through the variants channel and results
// land in the close tier like every other recovery.
func cooccurSearch(dir string, m Manifest, o query.Options) (SearchResult, error) {
	terms, _ := query.QueryParts(o.Query)
	if len(terms) < 2 {
		return SearchResult{}, nil
	}
	neighbors := readCooccur(dir)
	if neighbors == nil {
		return SearchResult{}, nil
	}
	catalog, err := tokenCatalogCached(dir)
	if err != nil {
		return SearchResult{}, err
	}
	for i, term := range terms {
		for _, n := range neighbors[term] {
			if !catalog[n] {
				continue
			}
			perToken := make([]map[int64]posting, 0, len(terms))
			variants := map[string][]string{}
			ok := true
			for j, other := range terms {
				tok := other
				if j == i {
					tok = n
				}
				posts, perr := postingsFor(dir, "t"+tok)
				if perr != nil || len(posts) == 0 {
					ok = false
					break
				}
				set := map[int64]posting{}
				for _, p := range posts {
					set[p.Off] = p
				}
				perToken = append(perToken, set)
				if j == i {
					variants[other] = []string{n}
				} else {
					variants[other] = []string{other}
				}
			}
			if !ok {
				continue
			}
			posts := intersectPostingMaps(perToken)
			if len(posts) == 0 {
				continue
			}
			posts = cutPostingsBySession(posts, m, o)
			if len(posts) == 0 {
				continue
			}
			ss, serr := scanRecordsWithVariants(dir, m, o, postingOffsets(posts), variants)
			if serr != nil || len(ss) == 0 {
				continue
			}
			return SearchResult{Sessions: ss, Stemmed: true, Variants: variants, Tier: query.TierClose}, nil
		}
	}
	return SearchResult{}, nil
}
