package index

import (
	"cmp"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Search returns the sessions in the index at dir that answer o.
func Search(dir string, o query.Options) ([]model.Session, error) {
	r, err := SearchDetailed(dir, o)
	return r.Sessions, err
}

// SearchDetailed is Search with how the answer was found.
func SearchDetailed(dir string, o query.Options) (SearchResult, error) {
	r, err := searchDetailedOnce(dir, o)
	if err != nil || o.Regex {
		return r, err
	}
	if len(r.Sessions) > 0 {
		if r.Tier != query.TierExact {
			return withDirectorySessions(dir, o, r), nil
		}
		return r, nil
	}
	// A question that embeds a quoted phrase ("when did I read \"x y\"?")
	// and matched nothing anywhere: the phrase kept its exactness contract,
	// now retry once with the quotes dropped, served under the relevance
	// tier so the loosening is visible. A bare quoted phrase with no words
	// of its own stays silent — reversed or misremembered phrases must not
	// dissolve into bag-of-words matches.
	if strings.Contains(o.Query, "\"") {
		outside := quotedSpanRE.ReplaceAllString(o.Query, " ")
		if len(RelevanceTerms(outside)) >= 2 {
			o2 := o
			o2.Query = strings.ReplaceAll(o.Query, "\"", " ")
			r2, err2 := searchDetailedOnce(dir, o2)
			if err2 == nil && len(r2.Sessions) > 0 {
				// The retry can land on the exact tier — the words are all
				// there, only the phrase was not — and the label about to be
				// put on it means nothing matched. Say how many did, the same
				// way a thin strict answer does (#3815).
				if r2.Strict == 0 && r2.Tier != query.TierRelevance {
					r2.Strict, r2.StrictIDs = len(r2.Sessions), sessionKeySet(r2.Sessions)
				}
				r2.Tier = query.TierRelevance
				// Found for another query than the caller's.
				r2.Fell = 0
				return r2, nil
			}
		}
	}
	return withDirectorySessions(dir, o, r), nil
}

var quotedSpanRE = regexp.MustCompile(`"[^"]*"`)

// searchDetailedOnce answers from one generation of the index. A search that
// runs without the lock, while a rebuild swaps the directory or a pass
// rewrites the manifest under it, could take the manifest of one generation
// and the postings and records of the next: interned ids then resolved
// through the wrong table and a hit came back labelled with its neighbour's
// session, or a record read across the swap looked corrupt and forced a
// second rebuild. The manifest's identity before and after the read says
// whether that happened; when it did, the read is simply done again.
func searchDetailedOnce(dir string, o query.Options) (SearchResult, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	return inOneGeneration(dir, func() (SearchResult, error) { return searchDetailedRead(dir, o) })
}

// inOneGeneration is read, done again while the manifest after it is not the
// file it was before it, at most three times in all: see searchDetailedOnce.
func inOneGeneration[T any](dir string, read func() (T, error)) (T, error) {
	var r T
	var err error
	for range 3 {
		before := manifestGeneration(dir)
		r, err = read()
		if sameGeneration(before, manifestGeneration(dir)) {
			return r, err
		}
	}
	return r, err
}

func searchDetailedRead(dir string, o query.Options) (SearchResult, error) {
	// Non-blocking: while a detached rebuild holds the lock, read the
	// current snapshot lock-free — the directory swap is atomic and a torn
	// read fails recordsIntact, which SearchWithRecoveryDetailed retries.
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return SearchResult{}, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return SearchResult{}, fmt.Errorf("manifest: %w", err)
	}
	if !recordsReadable(dir, m) {
		return SearchResult{}, fmt.Errorf("%w: records.bin size does not match the manifest (crash-truncated or uncommitted tail)", errCorruptIndex)
	}
	// One ranking per pass: the part-of-word step and the tail under a thin
	// answer both ask for it with the same query, and each read and scored
	// fifty sessions (#5378).
	ranking := &relevanceOnce{}
	if o.FellThrough != 0 {
		return rankedTail(ranking, dir, m, o, o.FellThrough)
	}
	hit, served, err := postingsRung(dir, m, o, ranking)
	if err != nil {
		return SearchResult{}, err
	}
	if served != nil {
		return *served, nil
	}
	posts, fallbackVariants, fallbackTier := hit.posts, hit.variants, hit.tier
	if len(posts) == 0 {
		if hit.used {
			return noPostingsLadder(ranking, dir, m, o)
		}
		ss, scanErr := scanRecords(dir, m, o, nil)
		return SearchResult{Sessions: ss, Tier: fallbackTier, Variants: fallbackVariants}, scanErr
	}
	// When every session holding the words is filtered out — most often the
	// agent's own live session, which holds them because the question came
	// from it — the answer is still the rest of the ladder, not nothing. The
	// early return here served empty to 23 of 255 real recall calls (#4766).
	holding := len(posts)
	posts = cutPostingsBySession(posts, m, o)
	var ss []model.Session
	var unread *query.Unread
	var scoped []posting
	var scopeWhole bool
	if len(posts) > 0 {
		pool := poolPostings(posts, m, o, holding)
		posts, unread, scoped, scopeWhole = pool.read, pool.unread, pool.scoped, pool.whole
		// With the variants the rung above collected, not without them. A
		// substring variant needs none — the text holding "opencode" holds
		// "code" too — but a compound spelled apart is not a substring of
		// anything the store wrote, so the postings found the session and
		// this check dropped it again (#2125).
		var misses int
		ss, misses, err = scanRecordsTextMisses(dir, m, o, postingOffsets(posts), fallbackVariants)
		if err == nil && unread != nil {
			var more []model.Session
			if more, err = verifyUnread(dir, m, o, fallbackVariants, ss, pool, misses == 0); len(more) > 0 {
				// Read now, so ranked with the rest; the scope's own matches
				// among them need no second read.
				ss, scoped = append(ss, more...), nil
			}
		}
	}
	if err == nil && len(ss) == 0 {
		if res, done, ferr := wordLadder(ranking, dir, m, o); done {
			return res, ferr
		}
		// Settled for any narrower search only when every candidate was
		// filtered out or checked: a pool that left sessions unread is not.
		fell := 0
		if len(posts) == 0 || unread == nil {
			fell = query.FellNoText
		}
		return rankedTail(ranking, dir, m, o, fell)
	}
	if err != nil {
		return SearchResult{}, err
	}
	res := withRelevanceTail(ranking, dir, m, o, SearchResult{Sessions: ss, Tier: fallbackTier, Variants: fallbackVariants, Unread: unread})
	// What o.PoolScope asked for is read after this search settled its own
	// answer, and only onto a plain exact one: that is the only answer a
	// narrower one is cut from.
	if scopeWhole && res.Unread == unread && res.Tier == query.TierExact {
		return withPoolScope(dir, m, o, res, unread, scoped, fallbackVariants), nil
	}
	return res, nil
}

// sessionKeySet keys sessions the way StrictIDs is keyed, for the paths that
// know every session they hand back matched.
func sessionKeySet(ss []model.Session) map[string]bool {
	out := make(map[string]bool, len(ss))
	for _, s := range ss {
		out[s.Harness+":"+s.ID] = true
	}
	return out
}

// thinAND is how few sessions an AND has to return before its own strictness
// becomes the suspect. A question asked in plain words requires every content
// word to be present, so on a large history it lands on a handful of sessions
// or on nothing — measured over a 1910-session corpus, the queries whose answer
// never came back returned between one and eight. Above that the intersection
// is describing a real cluster and needs no help.
const thinAND = 10

// tailFloor is the smallest store that gets a relevance tail under a thin
// strict answer. Below it the tail is a handful of sessions out of a handful,
// and the one it would add arrives on filler words rather than on the subject:
// on a five-session store a precise query came back with an unrelated session
// attached.
//
// It was twenty, and two LoCoMo conversations hold nineteen sessions each —
// 302 of the 1982 questions, answered with no tail at all. Twelve: R@10 94.9%
// -> 95.6%, R@20 97.0% -> 97.7%, questions the ranking never reaches 60 -> 45,
// R@1 unchanged either way (nine gained, ten lost). A fifth of twelve is two
// places, which is a tail; below that it is the store.
const tailFloor = 12

// strictPromotion is what satisfying the strict AND is worth, counted in places
// on the relevance ranking.
//
// It used to be worth everything: the strict head was concatenated in front of
// the ranked tail, so one session that happened to carry every query word led
// an answer even when the ranking put it fortieth. That is the case this tail
// exists for — a thin AND on a large store — so the head is usually one or two
// incidental sessions.
//
// Worth nothing is no better: the AND is real evidence, and #1226 measured that
// keeping strict matches first is why hit@1 is what it is.
const strictPromotion = 10

// fusedPlace is where a session sits once both tiers have had their say: its
// place in the relevance ranking, moved up by strictPromotion if it also
// satisfied the strict AND.
//
// The result is deliberately allowed to go negative. Clamping it at zero was
// the first version, and it collapsed every promoted session onto the same
// place, where the tie-break was posting order — so the strict head kept its
// absolute lead and lost the ranking it did have. day0bench read that as hit@1
// 21/40 -> 17/40.
func fusedPlace(relevanceRank int, strict bool) int {
	if strict {
		return relevanceRank - strictPromotion
	}
	return relevanceRank
}

// withRelevanceTail keeps a thin AND result and hangs the relevance ranking
// underneath it, so a question whose wording excluded the answer can still
// reach it. Strict sessions keep a bounded lead rather than an absolute one —
// see strictPromotion.
//
// Order is the contract here. RelevanceHits scores by arrival (len-rank), so
// the merged order survives to the caller untouched; anything that re-sorts
// this by exact-match scoring would undo the point. once is the pass's ranking,
// shared with the part-of-word step that may already have run it.
func withRelevanceTail(once *relevanceOnce, dir string, m Manifest, o query.Options, res SearchResult) SearchResult {
	ss := res.Sessions
	if len(ss) == 0 || len(ss) >= thinAND {
		return res
	}
	// A store no bigger than the window the tail is drawn from used to get no
	// tail at all, on the reading that ranking a store that size hands back
	// most of it rather than ranking it. The tail is bounded there instead —
	// see tailCap below.
	small := len(m.Sessions) <= relevanceWindow
	// relevanceSearch declines quoted phrases, regex, and queries with fewer
	// than two informative words, which is exactly the set that wants its
	// strict answer left alone.
	rel, err := once.get(dir, m, o)
	if err != nil || len(rel.Sessions) == 0 {
		// A tail is an improvement, not a requirement: if ranking the whole
		// store fails, the strict answer is still a correct answer.
		return res
	}
	seen := make(map[string]bool, len(ss))
	for _, s := range ss {
		seen[s.Harness+":"+s.ID] = true
	}
	// The head arrives in posting order, not in any ranking: the tier that
	// produced it expected the caller to rank it, and the caller cannot once
	// this is labelled relevance. Order it by where the same sessions sit in
	// the relevance pool, which is a real ranking over the same query, and
	// leave anything the pool never scored at the back of the head.
	relRank := make(map[string]int, len(rel.Sessions))
	for i, r := range rel.Sessions {
		relRank[r.Harness+":"+r.ID] = i
	}
	// Both lists, ordered by one rule. The strict head used to sit in front of
	// everything whatever the ranking thought of it, which made a thin AND —
	// often one incidental session — the whole top of the answer. Satisfying the
	// AND is strong evidence and stays worth something definite: a fixed number
	// of places, not an unconditional lead. A strict session the ranking scored
	// badly can now be passed by a relevance session it scored well.
	unranked := len(rel.Sessions)
	// A strict session the pool never scored sat behind all of it, so ten
	// places could not bring it near the page however well its message
	// answered: on a store where every query word is common, the pool fills
	// with sessions holding some form of each word in one message, and the one
	// that said the query in its own words ranked 41st (#5369). It is placed
	// instead by how many pool sessions say the query, in the words it was
	// asked in, at least as well as its best message does, without the strict
	// lead on top: placed ten ahead of that, an incidental strict session
	// passed answers the pool had ranked first. Never lower than before. Not
	// for a long query, which is ranked by its sessions
	// rather than their best message, nor for a head whose AND dropped a word.
	byMsg := map[string]int{}
	var outside []model.Session
	if !isLongQuery(RelevanceTermsWithTime(o.Query, o.Now)) && !droppedAWord(res.Variants) {
		for _, s := range ss {
			if _, ok := relRank[s.Harness+":"+s.ID]; !ok {
				outside = append(outside, s)
			}
		}
	}
	if len(outside) > 0 {
		mt := newMessageTerms(RelevanceTermsWithTime(o.Query, o.Now), rel.TermIDF, true)
		pool := make([]float64, len(rel.Sessions))
		parallelForRanked(len(pool), func(i int) { pool[i] = mt.best(rel.Sessions[i]) })
		for _, s := range outside {
			sc, r := mt.best(s), 0
			if sc <= 0 {
				continue
			}
			for _, p := range pool {
				if p >= sc {
					r++
				}
			}
			byMsg[s.Harness+":"+s.ID] = r
		}
	}
	// Doubled, so a session placed by its message sits just behind the pool
	// session it ties with rather than ahead of it.
	place := func(s model.Session, strict bool) int {
		key := s.Harness + ":" + s.ID
		r, ok := relRank[key]
		if !ok {
			if m, ok := byMsg[key]; ok {
				return min(2*m+1, 2*fusedPlace(unranked, true))
			}
			r = unranked
		}
		return 2 * fusedPlace(r, strict)
	}
	merged := append([]model.Session(nil), ss...)
	added := 0
	// How many ranked sessions may hang under the strict head on a small
	// store, as a share of the store: a fifth of it, and never more than the
	// thin-AND bound. On a store of two sessions that is nothing, which is
	// right — ranking two sessions is not a ranking, and the other one would
	// arrive on filler words. On the fifty a LongMemEval haystack holds, or a
	// store a few weeks after an install, it is ten places, and those ten are
	// worth 1.8pp of hit@5 there and 5.2pp on LoCoMo. An unbounded tail scored
	// the same, so this is the bounded version of the same win.
	tailCap := 0
	if small {
		if len(m.Sessions) < tailFloor {
			return res
		}
		tailCap = min(thinAND, len(m.Sessions)/5)
	}
	for _, r := range rel.Sessions {
		if seen[r.Harness+":"+r.ID] {
			continue
		}
		if tailCap > 0 && added >= tailCap {
			break
		}
		merged = append(merged, r)
		added++
	}
	// A head the word-form ladder got by dropping words is a partial match like
	// the rest, and promoting it put sessions holding only the query's filler
	// ("flags", "tests") above the one holding its path: on a long query the
	// ladder drops whichever words leave anything at all, often the ones that
	// named the subject. The ranking orders it with the rest instead.
	dropped := droppedAWord(res.Variants)
	at := make(map[string]int, len(merged))
	for _, s := range merged {
		key := s.Harness + ":" + s.ID
		at[key] = place(s, seen[key] && !dropped)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return at[merged[i].Harness+":"+merged[i].ID] < at[merged[j].Harness+":"+merged[j].ID]
	})
	if added == 0 {
		return res
	}
	// A session that satisfies the AND carries every query key, so it is
	// already inside the pool relevance scored, and adding the strict count on
	// top would count it twice — that pool size is the total. The exception is
	// a term the ranking derives rather than reads, a date from a relative
	// word, which the AND never had to match; the floor keeps the total from
	// claiming fewer sessions than were handed back.
	out := relevanceResult(merged, max(rel.Total, len(merged)), rel.TermIDF)
	// Keep the word-form annotations the ladder earned: the caller still tells
	// the user it fell back to stems or close spellings, even though the order
	// is now this tier's to keep.
	out.Stemmed, out.Fuzzy, out.Variants = res.Stemmed, res.Fuzzy, res.Variants
	// And keep the size of the head. Without it the label is the only thing
	// the caller has, and the label says nothing matched while len(ss)
	// sessions hold every word — which is what both the CLI sentence and the
	// agent-facing lead went on to claim (#3815).
	out.Strict, out.StrictIDs = len(ss), seen
	// Except where the AND it came from was not the query. A word the ladder
	// dropped because no session matches it with the rest is a word nothing
	// here holds, so "holds every word of the query" is false for every one
	// of these sessions — and it is the strongest thing any surface says.
	// Measured on this store: eight invented subjects, all eight answered
	// with a strict count of 1 to 5 and three or four sessions tagged as
	// holding the whole question, directly under a line saying the subject
	// was ignored.
	if droppedAWord(out.Variants) {
		out.Strict, out.StrictIDs = 0, nil
	}
	return out
}

// relevanceOnce is relevanceSearch for one pass of the ladder, run the first
// time it is asked for and handed back after that.
type relevanceOnce struct {
	done bool
	res  SearchResult
	err  error
}

func (r *relevanceOnce) get(dir string, m Manifest, o query.Options) (SearchResult, error) {
	if !r.done {
		r.res, r.err = relevanceSearch(dir, m, o)
		r.done = true
	}
	return r.res, r.err
}

// DroppedAWord reports that the word-form ladder gave up on one of the query's
// words: an empty variant is how it records "no session matches it with the
// rest of the query".
func DroppedAWord(variants map[string][]string) bool {
	for _, forms := range variants {
		if slices.Contains(forms, "") {
			return true
		}
	}
	return false
}

func droppedAWord(variants map[string][]string) bool { return DroppedAWord(variants) }

// RelevanceTerms extracts the rankable tokens of a natural-language query:
// lowercased, stopwords dropped. Exported so callers and the benchmark can
// mirror exactly what the relevance tier scores against. The implementation
// lives in query so that packages below index can reduce a question to its
// words without importing index.
func RelevanceTerms(q string) []string { return query.RelevanceTerms(q) }

// RelevanceMatchTerms returns the query's relevance terms plus the surface
// forms the relevance tier actually matches on. Callers count and snippet
// with these: a session surfaced through the fold ("camped" ranking a session
// that says "camping") otherwise rendered as "0 matches" with no snippet,
// because the raw term appears nowhere in its text.
func RelevanceMatchTerms(q string) []string {
	terms := RelevanceTerms(q)
	out := make([]string, 0, len(terms))
	seen := map[string]bool{}
	for _, t := range terms {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range terms {
		for _, f := range stemMatchForms(t) {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// relevanceWindow bounds how many ranked sessions the relevance tier serves.
// It belongs to the ranking rather than to output: --limit and --all act on the
// result set, which is downstream of this. That is also why this tier has to
// report Total and Capped — the pool the window was taken from does not survive
// the return, so a caller counting sessions is counting the window.
const relevanceWindow = 50

// relevanceRuns counts relevance rankings, for the test that pins one to a
// pass of the ladder.
var relevanceRuns atomic.Int64

// relevanceSearch is the ladder's last resort: no AND survived, so rank every
// session by IDF-weighted overlap with the query's informative words. Order
// carries the ranking; callers must not re-sort by exact-match BM25 (the whole
// point is that exact matching already failed). A session must match at least
// two informative terms — one lucky word is noise, the same bar the déjà vu
// hook applies.
func relevanceSearch(dir string, m Manifest, o query.Options) (SearchResult, error) {
	relevanceRuns.Add(1)
	// A quoted phrase is an explicit exactness request; loosening it into
	// bag-of-words relevance would betray what the user asked for.
	if strings.Contains(o.Query, "\"") || o.Regex {
		return SearchResult{}, nil
	}
	terms := RelevanceTermsWithTime(o.Query, o.Now)
	if len(terms) < 2 {
		return SearchResult{}, nil
	}
	rank, rerr := relevantMetasCounts(dir, m, nil, terms, relevanceWindow, func(meta SessionMeta) bool {
		return sessionMetaMatches(meta, o)
	})
	metas, anyMatched, strong := rank.metas, rank.any, rank.strong
	termsKnown, matched := rank.termsKnown, rank.total
	if rerr != nil {
		// A corrupt or unreadable bucket: surface it so the recovery path
		// rebuilds, rather than serving a silently short-ranked answer.
		return SearchResult{}, rerr
	}
	if len(metas) == 0 {
		if bridged := crossScriptBridgeTerms(terms); len(bridged) > 0 {
			rank, rerr = relevantMetasCounts(dir, m, nil, append(append([]string{}, terms...), bridged...), relevanceWindow, func(meta SessionMeta) bool {
				return sessionMetaMatches(meta, o)
			})
			if rerr != nil {
				return SearchResult{}, rerr
			}
			metas, anyMatched, strong = rank.metas, rank.any, rank.strong
			termsKnown, matched = rank.termsKnown, rank.total
		}
		if len(metas) == 0 {
			return SearchResult{}, nil
		}
	}
	keep := make([]SessionMeta, 0, len(metas))
	var weak []SessionMeta
	// A lone rare term is only trustworthy when the words it is alone against
	// are real words the corpus knows and the session simply does not use. On
	// "zzqx wwvv limiter" the others are typos, and one surviving anchor is
	// noise however rare it is — which is the silence the tier owes a query it
	// cannot honestly answer.
	decisive := len(terms) >= 3 && termsKnown >= 2
	for i, meta := range metas {
		// Two ordinary words, or one word rare enough to identify something on
		// its own. Counting terms alone was the whole of it, and it is the wrong
		// question on a spoken sentence: "How many bikes do I own?" carries
		// `many` and `own` alongside `bikes`, so every session holding the two
		// filler words counted as a real match and the one session that says
		// bikes — twenty of nineteen hundred do — rode behind all of them.
		if anyMatched[i] >= 2 || (decisive && strong[i] > 0) {
			keep = append(keep, meta)
		} else {
			weak = append(weak, meta)
		}
	}
	if len(keep) == 0 {
		// No multi-term session at all. For a real question (three or more
		// informative words) serving nothing teaches the user the tool is
		// deaf — serve the single-term candidates ranked by idf as an
		// explicitly weak tail. Short queries keep the silence contract:
		// one lucky word on a two-word query is noise, not an answer.
		// Guard: at least two of the query's words must exist in the corpus
		// at all — one known anchor among typos is noise, not a question.
		if len(weak) == 0 || len(terms) < 3 || termsKnown < 2 {
			return SearchResult{}, nil
		}
		if len(weak) > relevanceWindow {
			weak = weak[:relevanceWindow]
		}
		ss, err := sessionsAtOffsets(dir, m, o, weak, rank.offsets)
		if err != nil {
			return SearchResult{}, err
		}
		return relevanceResult(ss, matched, rank.idf), nil
	}
	// Single-term sessions ride BEHIND every strong candidate: they widen
	// deep recall without letting a lucky word outrank a real match.
	if len(keep)+len(weak) > relevanceWindow {
		weak = weak[:relevanceWindow-len(keep)]
	}
	keep = append(keep, weak...)
	ss, err := sessionsAtOffsets(dir, m, o, keep, rank.offsets)
	// A long query was ranked by its sessions, not by their best message, and
	// re-reading the best message would put that ranking back: forced on, it
	// takes the SWE-ContextBench R@1 above from 51.9% to 47.0%.
	if err == nil && !isLongQuery(terms) && (len(m.Sessions) >= bestMessageStore || rankingIsDoubtful(rank)) {
		ss = rerankByBestMessage(ss, terms, rank.idf)
	}
	if err != nil {
		return SearchResult{}, err
	}
	if day, ok := pointInTime(o.Query, o.Now); ok {
		ss = rerankByDayProximity(ss, day)
	}
	return relevanceResult(ss, matched, rank.idf), nil
}

// relevanceResult labels a relevance answer with the pool it was drawn from:
// matched is every session the ranking scored, counted before the window
// trimmed it, so capped can say plainly whether the caller is holding all of
// them. Reporting len(ss) as the total was the bug in #497 — the window is
// exactly the thing the number was supposed to see past.
func relevanceResult(ss []model.Session, matched int, idf map[string]float64) SearchResult {
	return SearchResult{
		Sessions: ss,
		Tier:     query.TierRelevance,
		Total:    matched,
		Capped:   matched > len(ss),
		TermIDF:  idf,
	}
}

// coverageCount picks which count of matched terms coverage is paid on.
//
// When something in the query identifies on its own, coverage is counted over
// those terms alone: the ordinary words a question is phrased with stop earning
// a session credit for covering the query. When nothing does — a question made
// entirely of ordinary words — counting them is all there is, and the generous
// reading of the gate stands.
//
// Measured: LoCoMo 69.8% to 70.2% R@1 and MRR .768 to .770; on LongMemEval-S
// the preference questions, which are ordinary words around one that matters,
// go 33.3% to 36.7% hit@1 with the total unmoved.
func coverageCount(all, identifying int32, identifyingTerms int) int32 {
	if identifyingTerms == 0 {
		return all
	}
	return identifying
}

// marathonShare is how much a session's score is lifted by the match covering
// it rather than brushing it. Swept on the prompt and recall benches: at 0.4
// and below the marathon still takes the top place on two of the four
// specific-vs-marathon cases, at 0.5 it takes none, and from there to 3.0
// nothing else moves — no arm of the prompt bench and no recall number
// (#3214).
const marathonShare = 0.5

// toolMatchWeight is what a match inside a tool record — a file read, a
// command's output, a JSON dump — is worth against the same match in speech.
// At full weight one opencode session of 4 MB of tool output ranked 1st, 3rd
// and 3rd on three unrelated questions over a real store of 1705 sessions; at
// 0.5 it ranks 8th, 14th and 10th, at 0.25 12th, 17th and 10th, at 0.75 2nd,
// 3rd and 6th (#4780). LongMemEval, LoCoMo and the bench arms hold no tool
// records and do not move.
const toolMatchWeight = 0.5

// subjectShare is how much of the question's subject a session has to reach:
// the rarest naming word the question holds, halved. Measured by sweeping the
// whole prompt bench — at 0.5 and 0.6 the cross-paired arm loses a false fire
// and no other arm moves; at 0.9 it loses two more and takes nine of twenty
// rewordings, four of five Russian questions and eighteen pasted-preamble
// questions with it (#3351).
const subjectShare = 0.5

// longQueryTerms is where a query stops being a question and becomes a pasted
// document, counted in informative terms. The longest LongMemEval question has
// 32 and the agents' recall queries a handful; a GitHub issue has a median of
// 69. Past it, scoring is BM25-shaped: summed over the session and divided by
// its length against the average with longQueryLengthB. On 362 SWE-ContextBench
// tasks searching the same repo's past tasks by issue text, R@1 goes from 42.5%
// to 51.9% and R@10 from 78.2% to 83.7% (#4925). Applied to every query
// instead, LongMemEval hit@1 fell from 87.4% to 82.8%.
const longQueryTerms = 40

const longQueryLengthB = 0.75

// isLongQuery counts the query in words: a CJK bigram is half of one, since a
// run of n characters expands to n-1 bigrams over about n/2 words, and a
// forty-character Chinese question is not a pasted document.
func isLongQuery(terms []string) bool {
	n := 0.0
	for _, t := range terms {
		if r := []rune(t); len(r) > 0 && cjkfold.Unspaced(r[0]) {
			n += 0.5
		} else {
			n++
		}
	}
	return n >= longQueryTerms
}

// rankIDF is what a match is WORTH: documents counted in sessions, the unit
// ranking has always used. Weighting by the gate's number instead lifts every
// term a few long sessions happen to repeat, which reorders the top of the
// result — measured on LongMemEval-S, 1.7 points of hit@1, with preference
// questions falling from 36.7% to 26.7%.
func rankIDF(sessions, minSess int) float64 {
	return math.Log(float64(sessions+1) / float64(minSess+1))
}

// gateIDF is whether a term is worth speaking up about at all: the rarer of the
// two verdicts, so a word has to read as ordinary counted in sessions AND
// counted in capped messages before it is treated as filler. Sessions alone
// call a subject word common, because the marathons that hold most of a store
// all mention it; capped messages alone call the topic of one long session
// filler.
func gateIDF(totalDocs float64, minDF int, rank float64) float64 {
	return math.Max(math.Log(totalDocs/float64(minDF+1)), rank)
}

// TopIDF is the idf every word said in only one session reaches on this store,
// however many of its messages say it: gateIDF never goes below rankIDF, and
// that is rankIDF's highest value. A store too small to reach a fixed rareness
// floor still has words that belong to one session. Counted in messages
// instead, a word the user and the agent both said in a one-session store
// scores 0 against a ceiling of 0.4 and is never rare. An unreadable store
// answers +Inf, so a floor taken as min(floor, TopIDF) stays where it was.
func TopIDF(dir string) float64 {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return math.Inf(1)
	}
	return topIDF(len(m.Sessions))
}

// topIDF is rankIDF for a word in one session, from the same inputs scoreTerm
// uses, so the two agree to the bit.
func topIDF(sessions int) float64 {
	return rankIDF(sessions, 1)
}

// dejaVuIDFFloor is the informativeness bar for a term to count toward a
// déjà vu match: ln(N/df) >= 2 keeps terms present in at most ~13% of
// sessions. Conversational filler ("post", "text", "claude") is frequent in
// any large corpus and clears nothing. Terms living in one or two sessions
// are informative regardless — small corpora never reach the ratio bar.
const dejaVuIDFFloor = 2.0

// InformativeIDF is dejaVuIDFFloor for callers reading SearchResult.TermIDF.
const InformativeIDF = dejaVuIDFFloor

// dejaVuStrongIDFFloor is the bar for a term rare enough to justify an
// UNPROMPTED recall on its own. The ordinary floor admits words that merely
// beat the average — in a corpus of a few thousand sessions that includes
// "session", "problem", "думать" — and the auto-recall hook fires on every
// message, so one such word was enough to inject on nearly any prompt.
// Measured on cross-paired prompts (the answer never present), the hook would
// have injected on 94% of them; requiring a strong term for the single-match
// case removes the half that rests on one ordinary word.
const dejaVuStrongIDFFloor = 3.0

// ProjectRelevant ranks the current project's sessions by how well they match
// the prompt terms — without reconstructing an AND query, which poisons on
// filler words. Each session scores the IDF-weighted sum of prompt terms it
// contains (rare topical terms dominate; common filler barely moves it), from
// bucket postings only. The best sessions are materialized with transcripts.

// ProjectRelevant ranks the project's sessions by IDF-weighted overlap with
// the prompt terms. matched reports, per returned session, how many distinct
// INFORMATIVE terms hit (idf >= dejaVuIDFFloor) — callers gate on it so
// generic words cannot manufacture a confident "you have been here".
func ProjectRelevant(dir string, projects, terms []string, n int) ([]model.Session, []int, []int, map[string]float64, error) {
	return ProjectRelevantSkipping(dir, projects, terms, n, nil)
}

// ProjectRelevantSkipping is ProjectRelevant without the sessions the caller
// has already dealt with. The per-prompt hook discards a candidate it injected
// earlier in the same agent session, and measured on a real store that is 15 of
// the 26 it ranks — every one read from disk in full first, only to be dropped
// on its id.
func ProjectRelevantSkipping(dir string, projects, terms []string, n int, skip map[string]bool) ([]model.Session, []int, []int, map[string]float64, error) {
	out, matched, strong, _, idf, err := ProjectRelevantNaming(dir, projects, terms, n, skip)
	return out, matched, strong, idf, err
}

// ProjectRelevantNaming is ProjectRelevantSkipping plus, per session, how many
// of the question's naming words it matched. The gate needs that to tell a
// session that matched the subject from one that matched the verb beside it,
// which rarity cannot answer in a small store (#3351). It is nil when the
// bridged retry answered instead: that path ranks on neighbouring terms, so a
// count of the question's own words would be a claim about the wrong query.
func ProjectRelevantNaming(dir string, projects, terms []string, n int, skip map[string]bool) ([]model.Session, []int, []int, []int, map[string]float64, error) {
	return ProjectRelevantNamingCut(dir, projects, terms, n, skip, nil)
}

// ProjectRelevantNamingCut is ProjectRelevantNaming with the records cut keeps
// out, by session id, left out of what is served: a fork asking gets its
// source from the fork point on, and a source whose only match was in the
// turns the fork copied is not served at all (#5335).
func ProjectRelevantNamingCut(dir string, projects, terms []string, n int, skip map[string]bool, cut map[string]time.Time) ([]model.Session, []int, []int, []int, map[string]float64, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	// Non-blocking, for the same reason searchDetailedOnce is: this runs on
	// the session-start hook, and waiting here stalls the agent for the whole
	// rebuild — which every user hits on an index-format upgrade.
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if ok {
		defer unlock()
	}
	// From one generation, as a search is. A pass that swapped in between a
	// bucket and its overlays left the ranking the old bucket, where the
	// session's postings were the dead ones, and no overlay with its live
	// ones: the session the question named fell out (#5459).
	r, err := inOneGeneration(dir, func() (projectRelevantRead, error) {
		return projectRelevantIn(dir, projects, terms, n, skip, cut)
	})
	return r.out, r.matched, r.strong, r.naming, r.idf, err
}

// projectRelevantRead is what ProjectRelevantNamingCut hands back.
type projectRelevantRead struct {
	out                     []model.Session
	matched, strong, naming []int
	idf                     map[string]float64
}

// projectRelevantIn is the read ProjectRelevantNamingCut makes, without the
// lock.
func projectRelevantIn(dir string, projects, terms []string, n int, skip map[string]bool, cut map[string]time.Time) (projectRelevantRead, error) {
	m, err := readManifestCached(dir)
	if err != nil {
		return projectRelevantRead{}, err
	}
	var keep func(SessionMeta) bool
	if len(skip) > 0 {
		keep = func(meta SessionMeta) bool { return !skip[meta.ID] }
	}
	rank, rerr := relevantMetasCounts(dir, m, projects, terms, n, keep)
	if rerr != nil {
		// A corrupt or unreadable bucket. The hook never rebuilds, so surface
		// it rather than inject a silently short-ranked déjà vu; the caller
		// stays quiet on an error.
		return projectRelevantRead{}, rerr
	}
	metas, matched, strong, naming, idf := rank.metas, rank.informative, rank.strong, rank.naming, rank.idf
	if bm, bmatched, bstrong, bidf, ok := bridgedRetry(dir, m, projects, terms, n, skip, strong, idf); ok {
		metas, matched, strong, idf, naming = bm, bmatched, bstrong, bidf, nil
	}
	if len(metas) == 0 {
		return projectRelevantRead{}, nil
	}
	out, err := sessionsServable(dir, metas, query.Options{ExcludeUpTo: cut})
	if err != nil {
		return projectRelevantRead{}, err
	}
	if len(cut) > 0 {
		out = stillMatching(out, terms, cut)
	}
	// Paired by identity, not by position. The counts belong to the ranking,
	// which works on metas; the sessions come back from sessionsServable, which
	// drops what the policy ignores and what the query may not be served. Every
	// caller reads the three positionally, so a single drop had the per-prompt
	// hook judging each session by its neighbour's terms (#2546).
	matched, strong = countsFor(out, metas, matched, strong)
	if naming != nil {
		naming, _ = countsFor(out, metas, naming, naming)
	}
	return projectRelevantRead{out, matched, strong, naming, idf}, nil
}

// stillMatching drops a cut session none of whose served messages holds a
// query term: it ranked on the turns the cut left out.
func stillMatching(ss []model.Session, terms []string, cut map[string]time.Time) []model.Session {
	out := ss[:0]
	for _, s := range ss {
		if _, ok := cut[s.ID]; !ok || holdsAnyTerm(s, terms) {
			out = append(out, s)
		}
	}
	return out
}

func holdsAnyTerm(s model.Session, terms []string) bool {
	for _, m := range s.Messages {
		text := strings.ToLower(m.Text)
		for _, t := range terms {
			if t != "" && strings.Contains(text, strings.ToLower(t)) {
				return true
			}
		}
	}
	return false
}

// countsFor re-pairs the per-session counts with the sessions that survived
// loading, keeping their order.
func countsFor(out []model.Session, metas []SessionMeta, matched, strong []int) ([]int, []int) {
	if len(out) == len(metas) {
		return matched, strong
	}
	at := make(map[string]int, len(metas))
	for i, meta := range metas {
		at[meta.Harness+":"+meta.ID] = i
	}
	m := make([]int, len(out))
	st := make([]int, len(out))
	for j, s := range out {
		i, ok := at[s.Harness+":"+s.ID]
		if !ok {
			continue
		}
		if i < len(matched) {
			m[j] = matched[i]
		}
		if i < len(strong) {
			st[j] = strong[i]
		}
	}
	return m, st
}

func relevantMetasMatched(dir string, m Manifest, projects, terms []string, n int, skip map[string]bool) ([]SessionMeta, []int, []int, map[string]float64, error) {
	var keep func(SessionMeta) bool
	if len(skip) > 0 {
		keep = func(meta SessionMeta) bool { return !skip[meta.ID] }
	}
	rank, err := relevantMetasCounts(dir, m, projects, terms, n, keep)
	return rank.metas, rank.informative, rank.strong, rank.idf, err
}

// relevanceRanking is what one ranking pass produced. It was seven return
// values and a naked error, which is how the idf it computes came to be thrown
// away at both call sites — there was nowhere to put it that did not make the
// signature worse.
type relevanceRanking struct {
	metas []SessionMeta
	// informative, any and strong count, per session, the query terms it holds
	// that clear the idf floor, that it holds at all, and that are rare enough
	// to identify something on their own.
	informative, any, strong []int
	// naming counts the question's naming words a session actually matched,
	// judged by shape. Rarity cannot answer that in a small store, so the gate
	// had nothing to distinguish the subject from the verb beside it (#3351).
	naming []int
	// termsKnown is how many of the query's terms the corpus contains at all,
	// and total how many sessions the ranking scored before n truncated it.
	termsKnown, total int
	// idf is what each query term was worth, so a caller choosing which message
	// to show can weigh it the same way the ranking weighed the session rather
	// than approximating it.
	idf map[string]float64
	// scores is the whole-query score the ranking gave each kept session, in
	// the order of metas. The focused view is not here: what reads this is the
	// doubt gate, and the two views already meet in fuseFocus.
	scores []float64
	// offsets are the records that matched, per session ordinal. The ranking
	// collects them anyway — the per-message signals are keyed by offset — and
	// they are what lets the tier read and fold the messages that matched
	// instead of every message of every candidate (#3491).
	offsets map[uint32][]int64
}

// relevanceScored is one session's standing after the ranking pass: its score
// over the whole query, and its score over only the words that distinguish.
type relevanceScored struct {
	place   int32
	matched int32
	any     int32
	strong  int32
	// naming is how many of the question's naming words this session matched,
	// judged by shape. The gate needs it to tell a match on the subject from a
	// match on the verb beside it (#3351).
	naming int32
	score  float64
	focus  float64
}

// rrfK damps how much a top place is worth against the ranking's tail. Sixty is
// the constant the fusion paper used and what search engines ship with.
const rrfK = 60

// fuseFocus reorders a ranking by the better of two views of the same query.
//
// A question asked in plain speech carries words that are filler to a reader
// and content to the index: "how many bikes do I own" ranks on many, bikes and
// own, and the session that answers it holds only the rare one while sessions
// about how many people own things hold two of them. Scored on the whole query
// the answer loses. Scored on the rare part alone it wins — and that view is
// wrong on other queries, where the common words are the question.
//
// So both are kept and each session takes its better place, the focused one at
// a price. Ordering by the fused place rather than by a blended score keeps the
// two views from having to be on the same scale, which they are not.
func fuseFocus(scored []relevanceScored, order []int32) []int32 {
	if len(order) < 2 {
		return order
	}
	// The rows stay where they are; places are kept by index.
	byFocusOrder := slices.Clone(order)
	slices.SortStableFunc(byFocusOrder, func(a, b int32) int { return cmp.Compare(scored[b].focus, scored[a].focus) })
	// Reciprocal rank fusion instead of "the better of the two places, with the
	// focused view paying three of them". RRF sums 1/(k+rank) over the
	// rankings and needs no common scale between them (Cormack, Clarke &
	// Buettcher, SIGIR 2009); measured against a convex combination of
	// normalised scores it is both more accurate and steadier (Bruch et al.,
	// "An Analysis of Fusion Functions for Hybrid Retrieval", 2022). What it
	// replaces was two numbers picked by hand.
	fused := make([]float64, len(scored))
	for i, idx := range order {
		fused[idx] = 1 / float64(rrfK+i+1)
	}
	for place, idx := range byFocusOrder {
		fused[idx] += 1 / float64(rrfK+place+1)
	}
	slices.SortStableFunc(order, func(a, b int32) int { return cmp.Compare(fused[b], fused[a]) })
	return order
}

// projectInScope says whether a session belongs to the project a caller is
// working in, given one of the name forms that project is known by.
//
// Harnesses record the same project under different forms — on the machine this
// was written on, the same repository appears as "deja-vu" in 69 sessions and
// "goprojects/deja-vu" in 25 — so the caller supplies several and any of them
// matching is the project. What decides is whole path segments.
//
// It used to be any substring, which joined 41 pairs of names on that machine
// and was wrong about a third of them: "deja" pulled in both deja-push and
// deja-vu, two different repositories, and a session whose project had parsed
// as "-" matched every hyphenated project on the disk — deja-vu, pin-manifests,
// mpc-autoscaler, telegram-mtproxy-forwarder. Recall in one project then ranks
// against another's history, which is the one thing project scoping exists to
// prevent.
//
// The explicit --project filter is left as a substring on purpose: that one is
// documented as a substring and is the user asking, not deja guessing.
func projectInScope(project, want string) bool {
	if want == "" {
		return false
	}
	return lowerInScope(strings.ToLower(project), strings.ToLower(want))
}

// projectScope is projectInScope for a walk over the whole manifest: the
// candidates are lowered once, and the answer is kept per project name, since
// a store holds tens of thousands of sessions under a few hundred names. The
// per-call lowering and concatenation was a third of the per-prompt hook's
// ranking time on a 19k-session store.
type projectScope struct {
	wants []string
	memo  map[string]bool
}

func newProjectScope(wants ...string) *projectScope {
	s := &projectScope{memo: map[string]bool{}}
	for _, w := range wants {
		if w != "" {
			s.wants = append(s.wants, strings.ToLower(w))
		}
	}
	return s
}

// has reports whether project is in scope for any of the candidates.
func (s *projectScope) has(project string) bool {
	if v, ok := s.memo[project]; ok {
		return v
	}
	p := strings.ToLower(project)
	v := false
	for _, w := range s.wants {
		if lowerInScope(p, w) {
			v = true
			break
		}
	}
	s.memo[project] = v
	return v
}

// lowerInScope is the rule of projectInScope on names already lowered.
func lowerInScope(p, w string) bool {
	imported := false
	if rest, ok := strings.CutPrefix(p, "imported:"); ok {
		// A synced session carries the peer's prefix and is otherwise the same
		// project under the same name.
		p, imported = rest, true
	}
	if p == w {
		return true
	}
	// A candidate with no separator is a bare directory name, and as a suffix
	// against a LOCAL project it cannot tell two directories apart: working in
	// /work/api, the candidate "api" matched a client's acme/api and the
	// session-start hook injected it "from this project" (#2333). Nothing is
	// lost by insisting on more here — a claude project is recorded as
	// parent/base and the candidates carry that form, and the stores that
	// record a bare project name match it exactly on the line above.
	//
	// A peer's project keeps the peer's path, which is not this machine's, so
	// the loose rule stays for imported work: matching it by the name this
	// machine knows it by is the point of scoping a synced index.
	if !imported && !strings.ContainsAny(w, `/\`) {
		return false
	}
	// Both separators, because a project name is built from a path and windows
	// builds it with backslashes. Matching only "/" made every scoped ranking on
	// windows return nothing — the substring rule this replaced happened not to
	// care which separator it was, and taking the loose rule out took the
	// platform's separator with it.
	return endsWithSegment(p, w)
}

// endsWithSegment is strings.HasSuffix(p, "/"+w) || strings.HasSuffix(p, `\`+w)
// without building either string.
func endsWithSegment(p, w string) bool {
	n := len(p) - len(w) - 1
	return n >= 0 && (p[n] == '/' || p[n] == '\\') && p[n+1:] == w
}

// ProjectInScopeStrict is ProjectInScope without the allowance synced work
// gets. The loose rule is right for recall — a peer's parent path is not this
// machine's, so `imported:goprojects/svc` has to answer to a local `svc` — and
// wrong for a command that packages one session's content for another agent:
// `deja handoff`, run from a directory named api, picked a teammate's
// `clients/acme/api` because it was the newest thing ending in /api (#2347).
func ProjectInScopeStrict(project, want string) bool {
	if want == "" {
		return false
	}
	p := strings.TrimPrefix(strings.ToLower(project), "imported:")
	w := strings.ToLower(want)
	if p == w {
		return true
	}
	if !strings.ContainsAny(w, `/\`) {
		return false
	}
	return strings.HasSuffix(p, "/"+w) || strings.HasSuffix(p, `\`+w)
}

// ProjectInScope reports whether a session's project is the one a caller is
// standing in. Exported so the automatic surfaces share one rule: the same
// question answered three different ways is how a client's project reached a
// session start (#2333), a handoff (#2336) and a tool-time line (#2339).
func ProjectInScope(project, want string) bool { return projectInScope(project, want) }

// relevantMetasCounts additionally reports how many terms of ANY frequency
// each session matched — the noise gate for full-index relevance search,
// where demanding two rare terms also rejects real answers that pair one
// rare word with one ordinary one.
// keep, when non-nil, restricts the candidate pool before ranking. The
// search tier used to rank the whole index, take the top n and only then
// apply the scope, so a --harness or --project search came back empty
// whenever the unfiltered head was full of other sessions.
// The last return value is how many sessions the ranking scored in total,
// counted before n truncated the slice. Everything above it is already
// computed and sorted by then, so the count is free — and it is the only
// place it exists: after the return, the pool the top n came out of is gone.
// Returns, in order: the ranked metas, their informative-term counts, their
// any-frequency term counts, how many query terms the corpus knows at all,
// and that pre-truncation total.
func relevantMetasCounts(dir string, m Manifest, projects, terms []string, n int, keep func(SessionMeta) bool) (relevanceRanking, error) {
	views := viewsFor(m)
	all := views.allMeta()
	if len(all) == 0 {
		return relevanceRanking{}, nil
	}
	var metaIdx []int32
	var place []int32
	nMetas := len(all)
	if len(projects) == 0 && keep == nil {
		place = make([]int32, views.maxOrd+1)
		for i := range place {
			place[i] = -1
		}
		for i := range all {
			place[all[i].Ord] = int32(i)
		}
	} else {
		scope := newProjectScope(projects...)
		metaIdx = make([]int32, 0, len(all))
		maxOrd := -1
		for i := range all {
			if keep != nil && !keep(all[i]) {
				continue
			}
			// An empty scope is the whole index.
			if len(projects) == 0 || scope.has(all[i].Project) {
				metaIdx = append(metaIdx, int32(i))
				maxOrd = max(maxOrd, int(all[i].Ord))
			}
		}
		if len(metaIdx) == 0 {
			return relevanceRanking{}, nil
		}
		nMetas = len(metaIdx)
		place = make([]int32, maxOrd+1)
		for i := range place {
			place[i] = -1
		}
		for p, idx := range metaIdx {
			place[all[idx].Ord] = int32(p)
		}
	}
	br := newBucketReader(dir)
	defer br.close()
	a := newRelevanceAcc(m, all, metaIdx, place, br)
	for _, term := range terms {
		a.addTerm(term)
	}
	a.bestMessages()
	// A query this long is a pasted document — an issue, a log, a spec — and
	// ranking it is finding the documents most like it, so a session's score
	// pays for its length the way BM25 does and is summed over the session
	// rather than read off its best message.
	long := isLongQuery(terms)
	var sumWords, nWords float64
	if long {
		for p := range nMetas {
			if w := a.meta(int32(p)).Words; w > 0 {
				sumWords += float64(w)
				nWords++
			}
		}
	}
	scored := make([]relevanceScored, 0, len(a.credited))
	for _, p := range a.credited {
		if a.sess[p].score <= 0 {
			continue
		}
		scored = append(scored, a.scoreSession(p, long, sumWords, nWords))
	}
	if len(scored) == 0 {
		return relevanceRanking{termsKnown: a.termsKnown}, a.readErr
	}
	// Sorted by index: a row is a whole SessionMeta, and moving rows was most
	// of what the sort cost.
	order := make([]int32, len(scored))
	for i := range order {
		order[i] = int32(i)
	}
	slices.SortFunc(order, func(i, j int32) int {
		x, y := &scored[i], &scored[j]
		if x.score != y.score {
			return cmp.Compare(y.score, x.score)
		}
		xm, ym := a.meta(x.place), a.meta(y.place)
		if !xm.Updated.Equal(ym.Updated) {
			return ym.Updated.Compare(xm.Updated)
		}
		// Total order even on full ties: the order sessions were collected
		// in must never decide what the user sees first.
		return strings.Compare(xm.ID, ym.ID)
	})
	ranked := fuseFocus(scored, order)
	// Counted here, one line above the truncation, because this is the last
	// moment the pool exists.
	matchedTotal := len(ranked)
	if n > 0 && len(ranked) > n {
		ranked = ranked[:n]
	}
	out := make([]SessionMeta, 0, len(ranked))
	matched := make([]int, 0, len(ranked))
	anyMatched := make([]int, 0, len(ranked))
	strong := make([]int, 0, len(ranked))
	naming := make([]int, 0, len(ranked))
	scores := make([]float64, 0, len(ranked))
	kept := make([]bool, nMetas)
	for _, idx := range ranked {
		r := &scored[idx]
		out = append(out, *a.meta(r.place))
		scores = append(scores, r.score)
		matched = append(matched, int(r.matched))
		anyMatched = append(anyMatched, int(r.any))
		strong = append(strong, int(r.strong))
		naming = append(naming, int(r.naming))
		kept[r.place] = true
	}
	// The records that matched, for the sessions that survived the window.
	// The per-message signals need them by offset; handing them on is what
	// lets the caller read those records instead of every record of every
	// candidate (#3491).
	offsets := make(map[uint32][]int64, len(ranked))
	for _, msg := range a.msgs {
		if msg.matched && kept[msg.place] {
			ord := a.meta(msg.place).Ord
			offsets[ord] = append(offsets[ord], msg.off)
		}
	}
	return relevanceRanking{
		metas:       out,
		informative: matched,
		any:         anyMatched,
		strong:      strong,
		naming:      naming,
		termsKnown:  a.termsKnown,
		total:       matchedTotal,
		idf:         a.idfOf,
		offsets:     offsets,
		scores:      scores,
	}, a.readErr
}

// sessionsAtOffsets loads the records at the given offsets and groups them by
// session, in the order the metas are given.
//
// It is scanRecordsWithVariants without the record matcher: that one re-checks
// the query against every record it reads, which is right for the exact tier
// and wrong here — a relevance match is a stem or a rare-word hit the raw query
// does not appear in. The withholding rules are the same, and so is the ignore
// rule, because this is the other place a tier turns manifest entries into
// sessions (#3491).
func sessionsAtOffsets(dir string, m Manifest, o query.Options, metas []SessionMeta, offsets map[uint32][]int64) ([]model.Session, error) {
	var flat []int64
	rank := make(map[string]int, len(metas))
	for i, meta := range metas {
		rank[meta.Harness+":"+meta.ID] = i
		flat = append(flat, offsets[meta.Ord]...)
	}
	if len(flat) == 0 {
		return nil, nil
	}
	f, err := openIndexFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	by := map[string]*model.Session{}
	if err := eachRecordAtInRoles(f, sortedUniqueOffsets(flat), tablesFromManifest(m), servableRoles(o), func(r Record) {
		meta, ok := m.Sessions[r.Key]
		if !ok {
			return
		}
		if _, wanted := rank[r.Key]; !wanted {
			return
		}
		if !recordServable(r.Role, o) {
			return
		}
		sess := by[r.Key]
		if sess == nil {
			cp := sessionFromMeta(meta)
			sess = &cp
			by[r.Key] = sess
		}
		sess.Messages = append(sess.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}); err != nil {
		return nil, err
	}
	out := make([]model.Session, 0, len(by))
	for _, sess := range by {
		orderPromotedNote(sess)
		out = append(out, *sess)
	}
	// Back into the ranked order: the records came off disk in log order, and
	// the order the ranking chose is what the caller serves.
	sort.SliceStable(out, func(i, j int) bool {
		return rank[out[i].Harness+":"+out[i].ID] < rank[out[j].Harness+":"+out[j].ID]
	})
	return ignoredByPolicy(out), nil
}

// FirstMatch tries candidate queries in order under ONE lock and manifest
// read, probes each via exact posting intersection (bucket reads only), and
// materializes sessions for the first query that matches. Built for the
// per-prompt hook, which fires on every user message and must stay fast: the
// full Search pipeline per candidate would re-read the manifest each time.
func FirstMatch(dir string, queries []string, limit int) ([]model.Session, string, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, "", err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, "", fmt.Errorf("manifest: %w", err)
	}
	if !recordsReadable(dir, m) {
		return nil, "", fmt.Errorf("%w: records.bin size does not match the manifest (crash-truncated or uncommitted tail)", errCorruptIndex)
	}
	for _, q := range queries {
		keys := queryKeys(q)
		if len(keys) == 0 {
			continue
		}
		posts, err := intersectPostings(dir, retrievalKeys(keys))
		if err != nil {
			return nil, "", fmt.Errorf("postings: %w", err)
		}
		o := query.Options{Query: q}
		posts = cutPostingsBySession(posts, m, o)
		if len(posts) == 0 {
			continue
		}
		ss, err := scanRecords(dir, m, o, postingOffsets(posts))
		if err != nil {
			return nil, "", fmt.Errorf("records: %w", err)
		}
		if len(ss) == 0 {
			continue
		}
		if len(ss) > limit {
			ss = ss[:limit]
		}
		return ss, q, nil
	}
	return nil, "", nil
}

// SearchWithRecovery is Search plus self-healing: a corrupt bucket (crash
// mid-append) triggers one full rebuild instead of erroring until the user
// runs --rebuild by hand.
func SearchWithRecovery(dir string, o query.Options, progress io.Writer) ([]model.Session, error) {
	r, err := SearchWithRecoveryDetailed(dir, o, progress)
	return r.Sessions, err
}

// SearchWithRecoveryDetailed is SearchWithRecovery with how the answer was
// found.
func SearchWithRecoveryDetailed(dir string, o query.Options, progress io.Writer) (SearchResult, error) {
	r, err := SearchDetailed(dir, o)
	if err == nil || !IsCorrupt(err) {
		return r, err
	}
	if autoUpdatesOff() {
		return SearchResult{}, &ManualStopError{damagedOrOutdated(err) + ", and automatic indexing is off — rebuild it with: deja index --rebuild"}
	}
	if progress != nil {
		fmt.Fprintf(progress, "deja: %s (%v), rebuilding ...\n", damagedOrOutdated(err), err)
	}
	if rerr := EnsureForSearch(dir, o, true, progress); rerr != nil {
		return SearchResult{}, rerr
	}
	return SearchDetailed(dir, o)
}

func scanRecords(dir string, m Manifest, o query.Options, offsets []int64) ([]model.Session, error) {
	return scanRecordsWithVariants(dir, m, o, offsets, nil)
}

// roleMatches accepts the role names the help text documents.
//
// `--role <user|assistant|tool>` is what `deja help` promises, and the stored
// name is "tool-output", so the documented spelling matched nothing at all —
// silently, with a healthy exit. The three roles that do work today (files,
// command, edit) are the only way to reach work records and appear in no help
// text; they keep working under their own names.
func roleMatches(stored, want string) bool {
	return search.RoleMatches(stored, want)
}

// harnessMatches accepts the name deja prints as well as the one it stores.
//
// Notes are stored under the harness "deja" and narrated during indexing as
// "notes" — so `deja index` said "notes: 5 sessions" and `--harness notes`
// then answered "no sessions match" and exited 0. A filter that rejects the
// name the tool just printed is a silent miss, which is the worst kind.
func harnessMatches(stored, want string) bool {
	if stored == want {
		return true
	}
	return stored == "deja" && want == "notes"
}

// roleFiles mirrors sources.RoleFiles without importing it: this package sits
// below sources, not above it.
const roleFiles = "files"

// roleCommand mirrors sources.RoleCommand.
const roleCommand = "command"

// roleToolOutput mirrors sources.RoleToolOutput.
const roleToolOutput = "tool-output"

// roleEdit mirrors sources.RoleEdit.
const roleEdit = "edit"

// roleWrote mirrors sources.RoleWrote: hashes of the lines a session wrote,
// under the path they went into. Never served — a reader asking about a file
// has no use for a line of hex, and it is not speech, not a command and not
// something anyone said.
const roleWrote = "wrote"

// roleSummary is a harness's digest of what it compacted away. Not speech, and
// not work either — but it belongs on this side of the line for the same reason
// the others do: a per-session read bound has to spend its budget on what
// someone said before it spends it on a machine's summary of that (#3384).
const roleSummary = "summary"

// isToolRole says whether a record holds the work rather than the talk about
// it. Tool records are bulk-repetitive by nature — the same command, the same
// paths, session after session — so a query that matches one matches hundreds,
// and the per-session bound has to spend its budget on speech first.
func isToolRole(role string) bool {
	return search.IsWorkRecord(role)
}

// wholeLogScanned, when set, is told of each record a scan with no offsets
// reads. Tests count it to see which queries walk the whole record log.
var wholeLogScanned func()

func scanRecordsWithVariants(dir string, m Manifest, o query.Options, offsets []int64, variants map[string][]string) ([]model.Session, error) {
	ss, _, err := scanRecordsTextMisses(dir, m, o, offsets, variants)
	return ss, err
}

// scanRecordsTextMisses is scanRecordsWithVariants that also counts the
// records read that do not pass the text check: none, and every session the
// read dropped was dropped for its role or the caller's filters, not because
// its postings named text it does not hold.
func scanRecordsTextMisses(dir string, m Manifest, o query.Options, offsets []int64, variants map[string][]string) ([]model.Session, int, error) {
	misses := 0
	by := map[string]*model.Session{}
	// Sessions come back in the order their first match was read. Ranging over
	// the map handed the exact tier a rotated order on every call, and the
	// directory lead serves that order as it is: a package rule in the fourth
	// of seven sessions was on the page in some calls and not in others.
	var order []string
	add := func(r Record) {
		meta, ok := m.Sessions[r.Key]
		if !ok || o.ExcludeSessions[meta.ID] || o.CutRecord(meta.ID, r.Time) {
			return
		}
		if o.Harness != "" && !harnessMatches(meta.Harness, o.Harness) {
			return
		}
		if !query.ProjectAnyMatches(meta.Project, meta.From, o.ProjectWants()) {
			return
		}
		if !fromMatches(meta.From, o.From) {
			return
		}
		if o.Since > 0 && meta.Updated.Before(o.Clock().Add(-o.Since)) {
			return
		}
		if !recordServable(r.Role, o) {
			return
		}
		s := by[r.Key]
		if s == nil {
			cp := sessionFromMeta(meta)
			// Only the matching records are read here.
			cp.Held = meta.Counted
			s = &cp
			by[r.Key] = s
			order = append(order, r.Key)
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}
	// Built once: the query side of the check does not change between records,
	// and this loop runs over every record a query without postings reaches
	// (#1885).
	matcher := newRecordMatcher(o, variants)
	if len(offsets) > 0 {
		f, err := openIndexFile(filepath.Join(dir, "records.bin"))
		if err != nil {
			return nil, 0, err
		}
		defer func() { _ = f.Close() }()
		offsets = sortedUniqueOffsets(offsets)
		if err := eachRecordAt(f, offsets, tablesFromManifest(m), func(r Record) {
			if matcher.matches(r) {
				add(r)
			} else {
				misses++
			}
		}); err != nil {
			return nil, 0, err
		}
	} else {
		if err := eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
			if wholeLogScanned != nil {
				wholeLogScanned()
			}
			if matcher.matches(r) {
				add(r)
			} else {
				misses++
			}
		}); err != nil {
			return nil, 0, err
		}
	}
	out := make([]model.Session, 0, len(by))
	for _, k := range order {
		s := by[k]
		orderPromotedNote(s)
		out = append(out, *s)
	}
	// The scoring and relevance tiers build their sessions here rather than
	// through sessionsForMetas, so the ignore rule has to hold at both.
	return ignoredByPolicy(out), misses, nil
}

func cutPostingsBySession(posts []posting, m Manifest, o query.Options) []posting {
	metaByOrd := sessionMetaByOrd(m)
	// Keep the complete posting-derived candidate set. Ranking needs the
	// candidate records to calculate BM25 document frequency and length.
	out := make([]posting, 0, len(posts))
	for _, p := range posts {
		if meta, ok := metaAt(metaByOrd, p.Sid); ok && sessionMetaMatches(meta, o) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return capPostingsPerSession(out, perSessionCandidates)
}

// poolPostings keeps the postings of the sessions worth ranking in full when
// the caller reads only a window of the answer. A word in most of a store
// matched 40k sessions, and reading and scoring every one of them to show 15
// took 22 s and 2.6 GB (#5467). The pool is chosen from the postings and the
// manifest alone; the rest is described in the Unread it returns.
//
// Not for a search inside one session or one role: those score a part of each
// session the postings cannot see.
//
// holding is how many postings matched, which on the exact tier is how many
// records of the store hold the query.
//
// The scoped postings are those of the sessions o.PoolScope adds, read apart
// so they never decide this search's own tier.
func poolPostings(posts []posting, m Manifest, o query.Options, holding int) pooled {
	all := pooled{read: posts}
	if o.Window <= 0 || o.Session != "" || o.Role != "" {
		return all
	}
	n := search.PoolSize(o.Window)
	records := map[uint32]int{}
	for _, p := range posts {
		records[p.Sid]++
	}
	if len(records) <= n {
		return all
	}
	metaByOrd := sessionMetaByOrd(m)
	// What the ignore rule and the trust policy withhold is dropped here
	// rather than after the scan, so the unread count does not include it and
	// a denied session does not take a slot from one the page can show (#5488).
	pol, excluded := policy.Load(), sources.ExcludedHarnesses()
	sids := make([]uint32, 0, len(records))
	withheld := 0
	denied, allowed := map[uint32]bool{}, map[uint32]bool{}
	for sid := range records {
		meta, _ := metaAt(metaByOrd, sid)
		if excluded[strings.ToLower(meta.Harness)] || pol.Ignored(meta.Path, meta.Project) {
			continue
		}
		if o.Activation != "" && !pol.Allows(o.Activation, meta.Project) {
			withheld++
			denied[sid] = true
			continue
		}
		sids = append(sids, sid)
		allowed[sid] = true
	}
	if len(sids) <= n {
		return all
	}
	slices.Sort(sids)
	stored := 0
	for _, meta := range m.Sessions {
		stored += meta.Counted
	}
	share := 0.0
	if stored > 0 {
		share = min(float64(holding)/float64(stored), 1)
	}
	cands := make([]search.PoolCandidate, len(sids))
	for i, sid := range sids {
		meta, _ := metaAt(metaByOrd, sid)
		s := sessionFromMeta(meta)
		s.Held = meta.Counted
		cands[i] = search.PoolCandidate{Session: s, Records: records[sid]}
	}
	keep, unread := search.Pool(cands, o, n, share)
	if keep == nil {
		return all
	}
	unread.Withheld = withheld
	kept := make(map[uint32]bool, n)
	for i, sid := range sids {
		if keep[i] {
			kept[sid] = true
		}
	}
	extra, scopeWhole := scopedPool(records, sids, kept, metaByOrd, o, n)
	out := pooled{unread: &unread, whole: scopeWhole, readSessions: len(kept), all: posts, kept: kept, allowed: allowed, denied: denied}
	for _, p := range posts {
		if kept[p.Sid] {
			out.read = append(out.read, p)
		} else if extra[p.Sid] && scopeWhole {
			out.scoped = append(out.scoped, p)
		}
	}
	return out
}

// pooled is what poolPostings split a match into.
type pooled struct {
	// read are the postings of the sessions ranked in full, and
	// readSessions how many sessions they come from.
	read         []posting
	readSessions int
	unread       *query.Unread
	// scoped and whole are what Options.PoolScope added: see scopedPool.
	scoped []posting
	whole  bool
	// all are the postings the pool was drawn from. Of their sessions, kept
	// were read, allowed and not kept are what Unread.Sessions counts, and
	// denied what Unread.Withheld counts. Those two were not checked against
	// the text.
	all                   []posting
	kept, allowed, denied map[uint32]bool
}

// verifyUnreadBudget is how many postings of the unread sessions are checked
// against the text before their count is estimated instead.
var verifyUnreadBudget = 20000

// verifyUnread makes the unread counts ones the text bears out. The pool is
// drawn from the postings, which say which records hold every word, not
// whether the words stand together the way a quoted phrase asks: "alpha beta"
// over 1,900 sessions that hold both words apart read the 5 that say it and
// counted the rest as matches, 106 in all (#5495).
//
// When every session read passed the text check, the postings were exact and
// the counts stand: a plain word costs nothing here. When every record read
// passed it and only the role filter dropped a session, the word matched in a
// command or a file list, which only a session whose postings are all tool
// records can be left with: those are checked and the counts cut by the ones
// that fail, where every unread match used to be read again. Otherwise the unread
// sessions are checked the same way, and the ones that pass are returned to
// be ranked with the pool, which leaves nothing unread. Past
// verifyUnreadBudget postings the counts are instead scaled by how many of the
// sample read with the pool passed, and Unread.Estimated says so.
func verifyUnread(dir string, m Manifest, o query.Options, variants map[string][]string, ss []model.Session, pool pooled, exactText bool) ([]model.Session, error) {
	u := pool.unread
	if len(ss) >= pool.readSessions || u.Sessions+u.Withheld == 0 {
		return nil, nil
	}
	if exactText {
		// Past the budget the counts are scaled below, as for a phrase.
		if done, err := cutRoleOnlyUnread(dir, m, o, variants, pool); done || err != nil {
			return nil, err
		}
	}
	var rest, denied []posting
	over := false
	for _, p := range pool.all {
		switch {
		case pool.denied[p.Sid]:
			denied = append(denied, p)
		case pool.allowed[p.Sid] && !pool.kept[p.Sid]:
			rest = append(rest, p)
		}
		if over = len(rest)+len(denied) > verifyUnreadBudget; over {
			break
		}
	}
	if !over {
		more, err := sessionsBorneOut(dir, m, o, variants, rest)
		if err != nil {
			return nil, err
		}
		withheld, err := sessionsBorneOut(dir, m, o, variants, denied)
		if err != nil {
			return nil, err
		}
		u.Sessions, u.Length, u.Withheld = 0, 0, len(withheld)
		return more, nil
	}
	passed, sampled := 0, len(u.Sample)
	for _, s := range ss {
		if _, ok := u.Sample[s.Harness+":"+s.ID]; ok {
			passed++
		}
	}
	if sampled == 0 {
		passed, sampled = len(ss), pool.readSessions
	}
	rate := float64(passed) / float64(sampled)
	u.Sessions = int(math.Round(float64(u.Sessions) * rate))
	u.Length *= rate
	u.Withheld = int(math.Round(float64(u.Withheld) * rate))
	u.Estimated = true
	return nil, nil
}

// cutRoleOnlyUnread takes out of the unread counts the sessions whose
// postings are all tool records and none of whose records this query may be
// served: a word only a command or a file list holds. done is false when
// they are past verifyUnreadBudget postings and nothing was checked.
func cutRoleOnlyUnread(dir string, m Manifest, o query.Options, variants map[string][]string, pool pooled) (done bool, err error) {
	u := pool.unread
	spoken := map[uint32]bool{}
	for _, p := range pool.all {
		if !p.Tool {
			spoken[p.Sid] = true
		}
	}
	var rest, denied []posting
	for _, p := range pool.all {
		if spoken[p.Sid] || pool.kept[p.Sid] {
			continue
		}
		switch {
		case pool.denied[p.Sid]:
			denied = append(denied, p)
		case pool.allowed[p.Sid]:
			rest = append(rest, p)
		}
		if len(rest)+len(denied) > verifyUnreadBudget {
			return false, nil
		}
	}
	byOrd := sessionMetaByOrd(m)
	failed := func(posts []posting) (map[string]bool, error) {
		held, heldErr := sessionsBorneOut(dir, m, o, variants, posts)
		if heldErr != nil {
			return nil, heldErr
		}
		ok := map[string]bool{}
		for _, s := range held {
			ok[s.Harness+":"+s.ID] = true
		}
		out := map[string]bool{}
		for _, p := range posts {
			meta, _ := metaAt(byOrd, p.Sid)
			if k := meta.Harness + ":" + meta.ID; !ok[k] {
				out[k] = true
			}
		}
		return out, nil
	}
	gone, err := failed(rest)
	if err != nil {
		return true, err
	}
	withheld, err := failed(denied)
	if err != nil {
		return true, err
	}
	if len(gone) > 0 && u.Sessions > 0 {
		left := max(u.Sessions-len(gone), 0)
		u.Length *= float64(left) / float64(u.Sessions)
		u.Sessions = left
		for k := range gone {
			delete(u.Sample, k)
		}
	}
	u.Withheld = max(u.Withheld-len(withheld), 0)
	return true, nil
}

// unreadPostingsChecked counts the postings sessionsBorneOut reads, for the
// tests that keep a plain word from reading its unread matches again.
var unreadPostingsChecked atomic.Int64

// sessionsBorneOut is the sessions whose records at posts pass the text check
// a search makes. The reads are split by session over a few workers: one
// record read at a time spent 0.85 s in pread on 3,100 sessions of a
// 40k-session store.
func sessionsBorneOut(dir string, m Manifest, o query.Options, variants map[string][]string, posts []posting) ([]model.Session, error) {
	// No offsets would scan the whole record log.
	if len(posts) == 0 {
		return nil, nil
	}
	unreadPostingsChecked.Add(int64(len(posts)))
	workers := min(runtime.NumCPU(), 4, (len(posts)+511)/512)
	offsets := make([][]int64, workers)
	for _, p := range posts {
		w := int(p.Sid) % workers
		offsets[w] = append(offsets[w], p.Off)
	}
	found := make([][]model.Session, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		if len(offsets[w]) == 0 {
			continue
		}
		wg.Go(func() {
			found[w], errs[w] = scanRecordsWithVariants(dir, m, o, offsets[w], variants)
		})
	}
	wg.Wait()
	var out []model.Session
	for w := range workers {
		if errs[w] != nil {
			return nil, errs[w]
		}
		out = append(out, found[w]...)
	}
	return out, nil
}

// scopedPool picks what else to read for o.PoolScope: every matching session
// of theirs outside kept, when the narrower search would read them all, so
// that answer can be cut from this one instead of searched again. Recall
// ranks the caller's project first and the machine after, and the second
// search of a common word cost a third of its time on a 40k-session store
// (#5486). whole is false when the narrower search would pool too.
//
// Its rule for reading everything: under its pool size, before or after the
// ignore rule and the trust policy drop what they withhold, it reads every
// candidate, those included.
func scopedPool(records map[uint32]int, sids []uint32, kept map[uint32]bool, metaByOrd map[uint32]*SessionMeta, o query.Options, n int) (extra map[uint32]bool, whole bool) {
	if len(o.PoolScope) == 0 {
		return nil, false
	}
	in := func(sid uint32) bool {
		meta, _ := metaAt(metaByOrd, sid)
		return query.ProjectAnyMatches(meta.Project, meta.From, o.PoolScope)
	}
	var scope []uint32
	for sid := range records {
		if in(sid) {
			scope = append(scope, sid)
		}
	}
	allowed := 0
	for _, sid := range sids {
		if in(sid) {
			allowed++
		}
	}
	if len(scope) > n && allowed > n {
		return nil, false
	}
	extra = map[uint32]bool{}
	for _, sid := range scope {
		if !kept[sid] {
			extra[sid] = true
		}
	}
	return extra, true
}

// perSessionCandidates bounds how many matching messages of one session are
// read to rank it. A common word is not spread evenly: "index" resolves 3258
// records across 162 sessions with a median of one apiece, and a single long
// session holds 1355 of them. Ranking scores a session by its best matching
// message, so reading the fourteen hundredth mention costs a record read and
// decides nothing.
const perSessionCandidates = 64

// capPostingsPerSession keeps at most n postings per session, sampled evenly
// across the session rather than truncated. Postings arrive in write order, so
// taking the first n would read the beginning of a long session and never its
// conclusion — which is the half that tends to be worth ranking.
//
// No session is ever dropped: this bounds how much of a session is read, not
// which sessions are considered, so the candidate set still covers everything
// the postings matched.
func capPostingsPerSession(posts []posting, n int) []posting {
	if n <= 0 {
		return posts
	}
	bySid := make(map[uint32]int, 16)
	speechBySid := make(map[uint32]int, 16)
	over := false
	for _, p := range posts {
		bySid[p.Sid]++
		if !p.Tool {
			speechBySid[p.Sid]++
		}
		if bySid[p.Sid] > n {
			over = true
		}
	}
	if !over {
		return posts
	}
	// Speech first, tool records with whatever budget is left. A long session
	// holds thousands of matching command lines and a handful of sentences about
	// the subject; sampling the two together spends the budget on the commands
	// and the sentence that would have ranked the session is never read.
	seen := make(map[uint32]int, len(bySid))
	seenTool := make(map[uint32]int, len(bySid))
	out := make([]posting, 0, len(posts))
	for _, p := range posts {
		if bySid[p.Sid] <= n {
			out = append(out, p)
			continue
		}
		budget, total, counter := n, speechBySid[p.Sid], seen
		if p.Tool {
			budget, total, counter = n-speechBySid[p.Sid], bySid[p.Sid]-speechBySid[p.Sid], seenTool
			if budget <= 0 {
				continue
			}
		}
		if total <= budget {
			out = append(out, p)
			continue
		}
		i := counter[p.Sid]
		counter[p.Sid] = i + 1
		// Keep index i when it lands on one of budget evenly spaced slots.
		if i*budget/total != (i+1)*budget/total {
			out = append(out, p)
		}
	}
	return out
}

// sessionMetaByOrd is m's sessions by ordinal, shared: see metaViews.
func sessionMetaByOrd(m Manifest) map[uint32]*SessionMeta {
	return viewsFor(m).metaByOrd()
}

// fromMatches answers whether a session belongs to the machine asked for.
// "local" means this machine's own work, which is every session that did not
// arrive by sync — the one name a person always has, whatever their hosts are
// called.
func fromMatches(sessionFrom, want string) bool {
	if want == "" {
		return true
	}
	if strings.EqualFold(want, "local") {
		return sessionFrom == ""
	}
	return strings.EqualFold(sessionFrom, want)
}

func sessionMetaMatches(meta SessionMeta, o query.Options) bool {
	if o.ExcludeSessions[meta.ID] {
		return false
	}
	if o.Harness != "" && !harnessMatches(meta.Harness, o.Harness) {
		return false
	}
	if !query.ProjectAnyMatches(meta.Project, meta.From, o.ProjectWants()) {
		return false
	}
	if !fromMatches(meta.From, o.From) {
		return false
	}
	if o.Since > 0 && meta.Updated.Before(o.Clock().Add(-o.Since)) {
		return false
	}
	return true
}

func postingOffsets(posts []posting) []int64 {
	out := make([]int64, 0, len(posts))
	for _, p := range posts {
		out = append(out, p.Off)
	}
	return out
}

func sortedUniqueOffsets(offsets []int64) []int64 {
	out := append([]int64(nil), offsets...)
	slices.Sort(out)
	n := 0
	for _, off := range out {
		if n == 0 || out[n-1] != off {
			out[n] = off
			n++
		}
	}
	return out[:n]
}

func sortedUniquePostings(posts []posting) []posting {
	out := append([]posting(nil), posts...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Off == out[j].Off {
			return out[i].Sid < out[j].Sid
		}
		return out[i].Off < out[j].Off
	})
	n := 0
	for _, p := range out {
		if n == 0 || out[n-1].Off != p.Off {
			out[n] = p
			n++
		}
	}
	return out[:n]
}
