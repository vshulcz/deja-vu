package index

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

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
	if canLoosenEmbeddedQuotes(o.Query) {
		o2 := o
		o2.Query = strings.ReplaceAll(o.Query, "\"", " ")
		r2, err2 := searchDetailedOnce(dir, o2)
		if err2 == nil && len(r2.Sessions) > 0 {
			// The retry can land on the exact tier — the words are all
			// there, only the phrase was not — and the label about to be
			// put on it means nothing matched. Say how many did, the same
			// way a thin strict answer does (#3815).
			if r2.Strict == 0 && r2.Tier == query.TierExact && !droppedAWord(r2.Variants) {
				r2.Strict, r2.StrictIDs = len(r2.Sessions), sessionKeySet(r2.Sessions)
			}
			r2.Tier = query.TierRelevance
			// Found for another query than the caller's.
			r2.Fell = 0
			return r2, nil
		}
	}
	return withDirectorySessions(dir, o, r), nil
}

var quotedSpanRE = regexp.MustCompile(`"[^"]*"`)

// canLoosenEmbeddedQuotes reports whether a failed or thin strict search can
// fall back to bag-of-words relevance with quotes stripped. Single-line queries
// need >=2 non-quoted terms so bare phrases stay exact; pasted multi-line
// issues (>=3 lines, >=8 terms) can span unmatched quotes across lines.
func canLoosenEmbeddedQuotes(q string) bool {
	if !strings.Contains(q, "\"") {
		return false
	}
	outside := quotedSpanRE.ReplaceAllString(q, " ")
	if len(RelevanceTerms(outside)) >= 2 {
		return true
	}
	return strings.Count(q, "\n") >= 2 && len(RelevanceTerms(strings.ReplaceAll(q, "\"", " "))) >= 8
}

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
		out[s.Key()] = true
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
	oTail := o
	if canLoosenEmbeddedQuotes(o.Query) {
		oTail.Query = strings.ReplaceAll(o.Query, "\"", " ")
	}
	rel, err := once.get(dir, m, oTail)
	if err != nil || len(rel.Sessions) == 0 {
		// A tail is an improvement, not a requirement: if ranking the whole
		// store fails, the strict answer is still a correct answer.
		return res
	}
	seen := make(map[string]bool, len(ss))
	for _, s := range ss {
		seen[s.Key()] = true
	}
	// The head arrives in posting order, not in any ranking: the tier that
	// produced it expected the caller to rank it, and the caller cannot once
	// this is labelled relevance. Order it by where the same sessions sit in
	// the relevance pool, which is a real ranking over the same query, and
	// leave anything the pool never scored at the back of the head.
	relRank := make(map[string]int, len(rel.Sessions))
	for i, r := range rel.Sessions {
		relRank[r.Key()] = i
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
	tailTerms := RelevanceTermsWithTime(oTail.Query, oTail.Now)
	if !isLongQuery(tailTerms) && !droppedAWord(res.Variants) {
		for _, s := range ss {
			if _, ok := relRank[s.Key()]; !ok {
				outside = append(outside, s)
			}
		}
	}
	if len(outside) > 0 {
		mt := newMessageTerms(tailTerms, rel.TermIDF, true)
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
			byMsg[s.Key()] = r
		}
	}
	// Doubled, so a session placed by its message sits just behind the pool
	// session it ties with rather than ahead of it.
	place := func(s model.Session, strict bool) int {
		key := s.Key()
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
		if seen[r.Key()] {
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
		key := s.Key()
		at[key] = place(s, seen[key] && !dropped)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return at[merged[i].Key()] < at[merged[j].Key()]
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
	out := relevanceResult(merged, max(rel.Total, len(merged)), rel.TermIDF, rel.SessionIDF)
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
	done      bool
	lastQuery string
	res       SearchResult
	err       error
}

func (r *relevanceOnce) get(dir string, m Manifest, o query.Options) (SearchResult, error) {
	if !r.done || r.lastQuery != o.Query {
		r.res, r.err = relevanceSearch(dir, m, o)
		r.lastQuery = o.Query
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
	qClean := cleanIssueQuery(q)
	terms := RelevanceTerms(qClean)
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
	for _, p := range queryCompoundParts(qClean, terms) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
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
	qClean := cleanIssueQuery(o.Query)
	terms := RelevanceTermsWithTime(qClean, o.Now)
	if len(terms) < 2 {
		return SearchResult{}, nil
	}
	rank, rerr := relevantMetasCountsForQuery(dir, m, nil, qClean, terms, relevanceWindow, true, func(meta SessionMeta) bool {
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
			rank, rerr = relevantMetasCountsForQuery(dir, m, nil, qClean, append(append([]string{}, terms...), bridged...), relevanceWindow, true, func(meta SessionMeta) bool {
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
		return relevanceResult(ss, matched, rank.idf, rank.sessionIDF), nil
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
	return relevanceResult(ss, matched, rank.idf, rank.sessionIDF), nil
}

// relevanceResult labels a relevance answer with the pool it was drawn from:
// matched is every session the ranking scored, counted before the window
// trimmed it, so capped can say plainly whether the caller is holding all of
// them. Reporting len(ss) as the total was the bug in #497 — the window is
// exactly the thing the number was supposed to see past.
func relevanceResult(ss []model.Session, matched int, idf, sessionIDF map[string]float64) SearchResult {
	return SearchResult{
		Sessions:   ss,
		Tier:       query.TierRelevance,
		Total:      matched,
		Capped:     matched > len(ss),
		TermIDF:    idf,
		SessionIDF: sessionIDF,
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
