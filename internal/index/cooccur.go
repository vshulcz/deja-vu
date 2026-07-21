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
	df := map[string]int{}
	perSession := make([][]string, 0, len(ss))
	for _, s := range ss {
		seen := map[string]bool{}
		for _, m := range s.Messages {
			for _, tok := range tokens(m.Text) {
				if len(tok) < 4 || query.IsStopWord(tok) || seen[tok] {
					continue
				}
				seen[tok] = true
			}
		}
		list := make([]string, 0, len(seen))
		for tok := range seen {
			list = append(list, tok)
			df[tok]++
		}
		perSession = append(perSession, list)
	}
	maxDF := len(ss) / 4
	if maxDF < 8 {
		maxDF = 8
	}
	band := func(tok string) bool { return df[tok] >= cooccurMinDF && df[tok] <= maxDF }

	pairs := map[string]map[string]int{}
	for _, list := range perSession {
		kept := make([]string, 0, len(list))
		for _, tok := range list {
			if band(tok) {
				kept = append(kept, tok)
			}
		}
		// rarest first, capped: ubiquitous sessions must not explode the map
		sort.Slice(kept, func(i, j int) bool {
			if df[kept[i]] == df[kept[j]] {
				return kept[i] < kept[j]
			}
			return df[kept[i]] < df[kept[j]]
		})
		if len(kept) > cooccurTokensPerSn {
			kept = kept[:cooccurTokensPerSn]
		}
		for i := 0; i < len(kept); i++ {
			for j := i + 1; j < len(kept); j++ {
				a, b := kept[i], kept[j]
				if pairs[a] == nil {
					pairs[a] = map[string]int{}
				}
				if pairs[b] == nil {
					pairs[b] = map[string]int{}
				}
				pairs[a][b]++
				pairs[b][a]++
			}
		}
	}
	neighbors := map[string][]string{}
	for tok, ns := range pairs {
		type nc struct {
			t string
			c int
		}
		list := make([]nc, 0, len(ns))
		for n, c := range ns {
			if c >= cooccurMinPair {
				list = append(list, nc{n, c})
			}
		}
		if len(list) == 0 {
			continue
		}
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
	catalog, err := tokenCatalog(dir)
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
