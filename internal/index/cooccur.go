package index

import (
	"path/filepath"
	"sort"
	"strings"

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
	// cooccurShards is how many passes the pair count is split into. Eight holds
	// an eighth of the pairs at a time for eight walks of the capped id lists,
	// which are two orders of magnitude cheaper than the map they feed.
	cooccurShards = 8
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

	// The tokens each session contributes pairs from: rarest first, capped, so a
	// ubiquitous session cannot explode the map. Computed once and kept — at
	// most cooccurTokensPerSn ids per session — while the full token sets go, so
	// the pair passes below walk something small.
	keptPerSession := make([][]uint32, 0, len(ss))
	for _, list := range perSession {
		kept := make([]uint32, 0, cooccurTokensPerSn)
		for _, v := range list {
			if band(v) {
				kept = append(kept, v)
			}
		}
		sort.Slice(kept, func(i, j int) bool {
			if df[kept[i]] == df[kept[j]] {
				return toks[kept[i]] < toks[kept[j]]
			}
			return df[kept[i]] < df[kept[j]]
		})
		if len(kept) > cooccurTokensPerSn {
			kept = kept[:cooccurTokensPerSn]
		}
		keptPerSession = append(keptPerSession, kept)
	}
	perSession = nil

	type nc struct {
		t string
		c int
	}
	cand := map[string][]nc{}
	// One packed key per unordered pair, canonical by id, counted a shard at a
	// time. Holding every pair at once was the larger half of a build's peak
	// footprint on a store of a few thousand sessions (#1137): the map is one
	// entry per distinct pair of co-occurring tokens, which grows far faster
	// than the corpus does. A shard costs one more walk of the capped id lists,
	// which is cheap, and the counts are exact either way — each pair lands in
	// exactly one shard.
	//
	// Each canonical pair (a,b) is a neighbour of both a and b, so expanding it
	// to both sides here rebuilds the same candidate lists the single-map
	// version produced; the final sort is by count then token, so the order
	// shards are visited in cannot reach the output.
	for shard := uint64(0); shard < cooccurShards; shard++ {
		pairs := map[uint64]int{}
		for _, kept := range keptPerSession {
			for i := 0; i < len(kept); i++ {
				for j := i + 1; j < len(kept); j++ {
					a, b := kept[i], kept[j]
					if a > b {
						a, b = b, a
					}
					key := uint64(a)<<32 | uint64(b)
					if key%cooccurShards != shard {
						continue
					}
					pairs[key]++
				}
			}
		}
		for key, c := range pairs {
			if c < cooccurMinPair {
				continue
			}
			a, b := toks[uint32(key>>32)], toks[uint32(key)]
			cand[a] = append(cand[a], nc{b, c})
			cand[b] = append(cand[b], nc{a, c})
		}
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
	return cooccurSearchOver(dir, m, o, terms, true)
}

// cooccurNarrowedSearch is the same rescue asked of a sentence, and it runs
// last — after the ranked tier has come back empty.
//
// The whole-AND rung above can only fire on a query short enough not to need
// it (#2331). Narrowing the AND to the words that identify something makes it
// fire on a sentence, and putting that in the ladder where the whole-AND rung
// sits made things worse: measured on LongMemEval, hit@1 79.5% to 73.5% and
// MRR .857 to .795, because it answered questions the ranked tier answers
// better. Below the ranked tier it costs nothing and reaches what nothing else
// does.
func cooccurNarrowedSearch(dir string, m Manifest, o query.Options) (SearchResult, error) {
	terms, _ := query.QueryParts(o.Query)
	if len(terms) < 2 {
		return SearchResult{}, nil
	}
	neighbors := readCooccur(dir)
	if neighbors == nil {
		return SearchResult{}, nil
	}
	// A sentence never matches an AND after one substitution: the map holds
	// the link a reworded question needs and the only query short enough to
	// read it is one that did not need it (#2331). So ask again over the words
	// that identify something — a question's ordinary words are what the AND
	// was failing on, and they are not what the answer is found by.
	//
	// Narrower, then narrower still. Three identifying words is already a
	// sharp question and often one word too many: the session that settled
	// something says it in its own vocabulary, and every word of the reader's
	// that it does not use fails the AND on its own.
	// Every attempt, not the first that lands. The widest subset is the one
	// the *explaining* session matches — it holds both vocabularies by
	// definition — while the session that settled something holds the
	// project's word and little else of the question, so it is found by a
	// narrower pair. Returning on the first hit therefore returned the
	// explanation and never the answer, which is what #2331 says this rung
	// cannot reach.
	var found []model.Session
	seen := map[string]bool{}
	variants := map[string][]string{}
	for _, narrowed := range narrowedAttempts(dir, m, terms, neighbors) {
		res, err := cooccurSearchOver(dir, m, o, narrowed, false)
		if err != nil {
			return SearchResult{}, err
		}
		for _, s := range res.Sessions {
			key := s.Harness + ":" + s.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			found = append(found, s)
		}
		for term, values := range res.Variants {
			if _, ok := variants[term]; !ok {
				variants[term] = values
			}
		}
		if len(found) >= cooccurNarrowedMax {
			break
		}
	}
	if len(found) == 0 {
		return SearchResult{}, nil
	}
	return SearchResult{Sessions: found, Stemmed: true, Neighbour: true, Variants: variants, Tier: query.TierClose}, nil
}

// cooccurNarrowedMax bounds what the narrowed attempts collect. This is the
// last rung and the reader is waiting; a handful of sessions is a lead, and
// more than that is the ranked tier's job.
const cooccurNarrowedMax = 10

// narrowedAttempts is the identifying subsets worth trying: the widest first,
// then the pairs.
//
// A pair is what usually works, and which pair matters. The session that
// settled something says it in its own vocabulary — "the pgbouncer pool ran
// out of connections" — so the reader's word for the subject has to be in the
// subset (it is what the substitution is made on) and the rest of the subset
// has to be a word that session also uses. Trying the bridgeable word against
// each of the other identifying words is a handful of intersections and finds
// the answering session rather than the sentence that explains the term.
func narrowedAttempts(dir string, m Manifest, terms []string, neighbours map[string][]string) [][]string {
	kept := identifyingTerms(dir, m, terms, neighbours)
	if len(kept) == 0 {
		return nil
	}
	inQueryOrder := func(subset []string) []string {
		out := append([]string(nil), subset...)
		sort.Slice(out, func(i, j int) bool { return indexOf(terms, out[i]) < indexOf(terms, out[j]) })
		return out
	}
	var out [][]string
	seen := map[string]bool{}
	add := func(subset []string) {
		subset = inQueryOrder(subset)
		key := strings.Join(subset, "\x00")
		if seen[key] || len(subset) > len(terms) {
			return
		}
		seen[key] = true
		out = append(out, subset)
	}
	if len(kept) >= cooccurNarrowTerms {
		add(kept[:cooccurNarrowTerms])
	}
	for i, b := range kept {
		if len(neighbours[b]) == 0 || i >= cooccurBridgeHeads {
			continue
		}
		for j, other := range kept {
			if i == j || j >= cooccurPairsPerHead {
				continue
			}
			add([]string{b, other})
		}
	}
	// And the word on its own. A session that settled something in the
	// project's own vocabulary often shares nothing else with the question —
	// "quarto was moved to the first of the month" answers "when did we last
	// change the zephyrine schedule" and holds none of its words. This is the
	// last rung of the last tier, so the alternative to swapping the subject
	// and looking for that alone is silence.
	for i, b := range kept {
		if len(neighbours[b]) == 0 || i >= cooccurBridgeHeads {
			continue
		}
		add([]string{b})
	}
	return out
}

// cooccurBridgeHeads and cooccurPairsPerHead bound the pairs tried. Each is an
// intersection of two posting lists, and the reader is waiting: two words to
// bridge from against the four rarest words of the question is the width at
// which this stops paying.
const (
	cooccurBridgeHeads  = 2
	cooccurPairsPerHead = 4
)

// identifyingTerms is the query's words that are rare enough in this store to
// say what a session is about, bridgeable ones first and then rarest first.
// The order is what the narrowing cuts against.
func identifyingTerms(dir string, m Manifest, terms []string, neighbours map[string][]string) []string {
	type scored struct {
		term      string
		df        int
		bridgable bool
	}
	var kept []scored
	for _, t := range terms {
		if query.IsStopWord(t) {
			continue
		}
		posts, err := postingsFor(dir, "t"+t)
		if err != nil || len(posts) == 0 {
			continue
		}
		in := make(map[uint32]bool, len(posts))
		for _, p := range posts {
			in[p.Sid] = true
		}
		if len(in) > 2 && rankIDF(len(m.Sessions), len(in)) < dejaVuIDFFloor {
			continue
		}
		kept = append(kept, scored{t, len(in), len(neighbours[t]) > 0})
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].bridgable != kept[j].bridgable {
			return kept[i].bridgable
		}
		if kept[i].df == kept[j].df {
			return kept[i].term < kept[j].term
		}
		return kept[i].df < kept[j].df
	})
	out := make([]string, 0, len(kept))
	for _, k := range kept {
		out = append(out, k.term)
	}
	return out
}

func indexOf(terms []string, want string) int {
	for i, t := range terms {
		if t == want {
			return i
		}
	}
	return len(terms)
}

// cooccurNarrowTerms is how many identifying words the narrowed AND is built
// from. Three is the width at which a rephrased question still names one
// subject; wider and the AND fails for the same reason the sentence did.
const cooccurNarrowTerms = 3

// cooccurSearchOver tries each neighbour of each term in turn. stopAtFirst is
// what the whole-AND rung wants — one swap that matches is the answer — while
// the narrowed rung takes them all, because the first neighbour to match is
// usually the sentence that explains the word and the one after it is the
// session that used it.
func cooccurSearchOver(dir string, m Manifest, o query.Options, terms []string, stopAtFirst bool) (SearchResult, error) {
	var found []model.Session
	seen := map[string]bool{}
	gathered := map[string][]string{}
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
			// Verified against the words this attempt was built from, not
			// against the whole sentence. The scan re-checks the record
			// against the query, and a session that settled something says it
			// in its own words — asking it to also carry "why", "would" and
			// "exhaust" is the AND this rung was trying to get out from under
			// (#2331).
			narrowed := o
			narrowed.Query = strings.Join(terms, " ")
			ss, serr := scanRecordsWithVariants(dir, m, narrowed, postingOffsets(posts), variants)
			if serr != nil || len(ss) == 0 {
				continue
			}
			if stopAtFirst {
				return SearchResult{Sessions: ss, Stemmed: true, Neighbour: true, Variants: variants, Tier: query.TierClose}, nil
			}
			for _, s := range ss {
				key := s.Harness + ":" + s.ID
				if seen[key] {
					continue
				}
				seen[key] = true
				found = append(found, s)
			}
			for t, values := range variants {
				if _, ok := gathered[t]; !ok {
					gathered[t] = values
				}
			}
			if len(found) >= cooccurNarrowedMax {
				return SearchResult{Sessions: found, Stemmed: true, Neighbour: true, Variants: gathered, Tier: query.TierClose}, nil
			}
		}
	}
	if len(found) == 0 {
		return SearchResult{}, nil
	}
	return SearchResult{Sessions: found, Stemmed: true, Neighbour: true, Variants: gathered, Tier: query.TierClose}, nil
}
