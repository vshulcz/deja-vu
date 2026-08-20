package index

import (
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

func Search(dir string, o query.Options) ([]model.Session, error) {
	r, err := SearchDetailed(dir, o)
	return r.Sessions, err
}

func SearchDetailed(dir string, o query.Options) (SearchResult, error) {
	r, err := searchDetailedOnce(dir, o)
	if err != nil || len(r.Sessions) > 0 || o.Regex {
		return r, err
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
				r2.Tier = query.TierRelevance
				return r2, nil
			}
		}
	}
	return r, err
}

var quotedSpanRE = regexp.MustCompile(`"[^"]*"`)

func searchDetailedOnce(dir string, o query.Options) (SearchResult, error) {
	if dir == "" {
		dir = DefaultDir()
	}
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
	var posts []posting
	var fallbackVariants map[string][]string
	fallbackTier := query.TierExact
	usedPostings := false
	if !o.Regex {
		if keys := queryKeys(o.Query); len(keys) > 0 {
			usedPostings = true
			posts, err = intersectPostings(dir, retrievalKeys(keys))
			if err != nil {
				return SearchResult{}, fmt.Errorf("postings: %w", err)
			}
			if len(posts) == 0 {
				// grep expectation: "code" should find "opencode". Expand each query
				// token to all indexed tokens containing it (bucket directories only,
				// no record scan), then intersect.
				var variants map[string][]string
				posts, variants, err = intersectSubstringPostingsDetailed(dir, tokens(o.Query))
				if err != nil {
					return SearchResult{}, fmt.Errorf("substr postings: %w", err)
				}
				if len(posts) > 0 {
					fallbackVariants = variants
					fallbackTier = query.TierClose
					// A natural-language query that degraded to substring
					// intersection often lands on one incidental session and
					// the ladder used to stop there. When the query carries
					// enough informative words to rank by relevance, prefer
					// that ranking and keep the substring hits as the tail.
					if rel, rerr := relevanceSearch(dir, m, o); rerr == nil && len(rel.Sessions) > 0 {
						closeSS, serr := scanRecords(dir, m, o, postingOffsets(cutPostingsBySession(posts, m, o)))
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
						if serr == nil && len(closeSS) > 0 && !agree {
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
							return relevanceResult(merged, rel.Total+tail, rel.TermIDF), nil
						}
					}
				}
			}
		}
	}
	if len(posts) == 0 {
		if usedPostings {
			if result, ferr := stemSearch(dir, m, o); ferr != nil {
				return SearchResult{}, fmt.Errorf("stem postings: %w", ferr)
			} else if result.Stemmed {
				return withRelevanceTail(dir, m, o, result)
			}
			if result, ferr := fuzzySearch(dir, m, o); ferr != nil {
				return SearchResult{}, fmt.Errorf("fuzzy postings: %w", ferr)
			} else if result.Fuzzy {
				return withRelevanceTail(dir, m, o, result)
			}
			// A pasted error that matched no postings is the case the word
			// ladder cannot win: its identifiers are line numbers and hashes.
			// The friction hashes the store already keeps answer it exactly.
			if result, ferr := errorSigSearch(dir, m, o); ferr != nil {
				return SearchResult{}, fmt.Errorf("error signature: %w", ferr)
			} else if len(result.Sessions) > 0 {
				return result, nil
			}
			return relevanceSearch(dir, m, o)
		}
		ss, err := scanRecords(dir, m, o, nil)
		return SearchResult{Sessions: ss, Tier: fallbackTier, Variants: fallbackVariants}, err
	}
	posts = cutPostingsBySession(posts, m, o)
	if len(posts) == 0 {
		return SearchResult{}, nil
	}
	ss, err := scanRecords(dir, m, o, postingOffsets(posts))
	if err == nil && len(ss) == 0 {
		if result, ferr := stemSearch(dir, m, o); ferr != nil {
			return SearchResult{}, fmt.Errorf("stem postings: %w", ferr)
		} else if result.Stemmed {
			return withRelevanceTail(dir, m, o, result)
		}
		if result, ferr := fuzzySearch(dir, m, o); ferr != nil {
			return SearchResult{}, fmt.Errorf("fuzzy postings: %w", ferr)
		} else if result.Fuzzy {
			return withRelevanceTail(dir, m, o, result)
		}
		if result, ferr := cooccurSearch(dir, m, o); ferr != nil {
			return SearchResult{}, fmt.Errorf("cooccur postings: %w", ferr)
		} else if len(result.Sessions) > 0 {
			return withRelevanceTail(dir, m, o, result)
		}
		// A pasted error that matched no postings is the case the word
		// ladder cannot win: its identifiers are line numbers and hashes.
		// The friction hashes the store already keeps answer it exactly.
		if result, ferr := errorSigSearch(dir, m, o); ferr != nil {
			return SearchResult{}, fmt.Errorf("error signature: %w", ferr)
		} else if len(result.Sessions) > 0 {
			return result, nil
		}
		return relevanceSearch(dir, m, o)
	}
	if err != nil {
		return SearchResult{}, err
	}
	return withRelevanceTail(dir, m, o, SearchResult{Sessions: ss, Tier: fallbackTier, Variants: fallbackVariants})
}

// thinAND is how few sessions an AND has to return before its own strictness
// becomes the suspect. A question asked in plain words requires every content
// word to be present, so on a large history it lands on a handful of sessions
// or on nothing — measured over a 1910-session corpus, the queries whose answer
// never came back returned between one and eight. Above that the intersection
// is describing a real cluster and needs no help.
const thinAND = 10

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
// this by exact-match scoring would undo the point.
func withRelevanceTail(dir string, m Manifest, o query.Options, res SearchResult) (SearchResult, error) {
	ss := res.Sessions
	if len(ss) == 0 || len(ss) >= thinAND {
		return res, nil
	}
	// A store smaller than the window the tail is drawn from has nothing to
	// rank: relevance would hand back most of it, which is a dump rather than
	// a ranking, and a precise query would come back with the rest of the
	// store attached. Few results out of few sessions is an answer, not a
	// symptom; the case this exists for is a handful out of thousands.
	if len(m.Sessions) <= relevanceWindow {
		return res, nil
	}
	// relevanceSearch declines quoted phrases, regex, and queries with fewer
	// than two informative words, which is exactly the set that wants its
	// strict answer left alone.
	rel, err := relevanceSearch(dir, m, o)
	if err != nil || len(rel.Sessions) == 0 {
		// A tail is an improvement, not a requirement: if ranking the whole
		// store fails, the strict answer is still a correct answer.
		return res, nil
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
	place := func(s model.Session, strict bool) int {
		r, ok := relRank[s.Harness+":"+s.ID]
		if !ok {
			r = unranked
		}
		return fusedPlace(r, strict)
	}
	merged := append([]model.Session(nil), ss...)
	added := 0
	for _, r := range rel.Sessions {
		if seen[r.Harness+":"+r.ID] {
			continue
		}
		merged = append(merged, r)
		added++
	}
	at := make(map[string]int, len(merged))
	for _, s := range merged {
		key := s.Harness + ":" + s.ID
		at[key] = place(s, seen[key])
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return at[merged[i].Harness+":"+merged[i].ID] < at[merged[j].Harness+":"+merged[j].ID]
	})
	if added == 0 {
		return res, nil
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
	return out, nil
}

// RelevanceTerms extracts the rankable tokens of a natural-language query:
// lowercased, stopwords dropped. Exported so callers and the benchmark can
// mirror exactly what the relevance tier scores against.
func RelevanceTerms(q string) []string {
	// Letters and digits are wordy in every script; everything else splits.
	// The old "anything above U+0400 is wordy" rule swallowed CJK and
	// fullwidth punctuation ("？", "，"), so a real Chinese question became
	// one giant term that matched nothing and never reached bigram
	// expansion.
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		if r < 128 {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9') &&
				r != '-' && r != '_' && r != '.' && r != '/'
		}
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	fields = expandCJKTokens(fields)
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		if len([]rune(f)) < 2 || (len(f) < 3 && !cjkfold.IsCJK([]rune(f)[0])) || search.IsStopWord(f) || seen[f] {
			continue
		}
		if cjkFunctionBigram(f) {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

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

// relevanceSearch is the ladder's last resort: no AND survived, so rank every
// session by IDF-weighted overlap with the query's informative words. Order
// carries the ranking; callers must not re-sort by exact-match BM25 (the whole
// point is that exact matching already failed). A session must match at least
// two informative terms — one lucky word is noise, the same bar the déjà vu
// hook applies.
func relevanceSearch(dir string, m Manifest, o query.Options) (SearchResult, error) {
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
		return SearchResult{}, nil
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
		ss, err := sessionsServable(dir, weak, o)
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
	ss, err := sessionsServable(dir, keep, o)
	if err != nil {
		return SearchResult{}, err
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

// dejaVuIDFFloor is the informativeness bar for a term to count toward a
// déjà vu match: ln(N/df) >= 2 keeps terms present in at most ~13% of
// sessions. Conversational filler ("post", "text", "claude") is frequent in
// any large corpus and clears nothing. Terms living in one or two sessions
// are informative regardless — small corpora never reach the ratio bar.
const dejaVuIDFFloor = 2.0

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
func ProjectRelevant(dir string, projects, terms []string, n int) ([]model.Session, []int, []int, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	// Non-blocking, for the same reason searchDetailedOnce is: this runs on
	// the session-start hook, and waiting here stalls the agent for the whole
	// rebuild — which every user hits on an index-format upgrade.
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	metas, matched, strong, rerr := relevantMetasMatched(dir, m, projects, terms, n)
	if rerr != nil {
		// A corrupt or unreadable bucket. The hook never rebuilds, so surface
		// it rather than inject a silently short-ranked déjà vu; the caller
		// stays quiet on an error.
		return nil, nil, nil, rerr
	}
	if len(metas) == 0 {
		return nil, nil, nil, nil
	}
	out, err := sessionsServable(dir, metas, query.Options{})
	if err != nil {
		return nil, nil, nil, err
	}
	return out, matched, strong, nil
}

func relevantMetasMatched(dir string, m Manifest, projects, terms []string, n int) ([]SessionMeta, []int, []int, error) {
	rank, err := relevantMetasCounts(dir, m, projects, terms, n, nil)
	return rank.metas, rank.informative, rank.strong, err
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
	// termsKnown is how many of the query's terms the corpus contains at all,
	// and total how many sessions the ranking scored before n truncated it.
	termsKnown, total int
	// idf is what each query term was worth, so a caller choosing which message
	// to show can weigh it the same way the ranking weighed the session rather
	// than approximating it.
	idf map[string]float64
}

// relevanceScored is one session's standing after the ranking pass: its score
// over the whole query, and its score over only the words that distinguish.
type relevanceScored struct {
	meta    SessionMeta
	score   float64
	matched int
	any     int
	strong  int
	focus   float64
}

// focusPrice is what a session pays for being convincing only on the rare part
// of the query rather than on all of it. Counted in places, like the strict
// promotion above: a session the focused view puts first and the whole-query
// view puts thirtieth ends up fourth, not first.
const focusPrice = 3

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
func fuseFocus(ranked []relevanceScored) []relevanceScored {
	if len(ranked) < 2 {
		return ranked
	}
	order := make([]int, len(ranked))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return ranked[order[a]].focus > ranked[order[b]].focus })
	byFocus := make([]int, len(ranked))
	for place, idx := range order {
		byFocus[idx] = place
	}
	// The place travels with the row. Sorting the rows while indexing a
	// parallel slice by the comparator's arguments reads the place of whatever
	// has been swapped into that position, not of the row being compared.
	type placed struct {
		row   relevanceScored
		place int
	}
	rows := make([]placed, len(ranked))
	for i, r := range ranked {
		rows[i] = placed{r, min(i, byFocus[i]+focusPrice)}
	}
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].place < rows[b].place })
	out := make([]relevanceScored, len(rows))
	for i, r := range rows {
		out[i] = r.row
	}
	return out
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
	p, w := strings.ToLower(project), strings.ToLower(want)
	if rest, ok := strings.CutPrefix(p, "imported:"); ok {
		// A synced session carries the peer's prefix and is otherwise the same
		// project under the same name.
		p = rest
	}
	if p == w {
		return true
	}
	// Both separators, because a project name is built from a path and windows
	// builds it with backslashes. Matching only "/" made every scoped ranking on
	// windows return nothing — the substring rule this replaced happened not to
	// care which separator it was, and taking the loose rule out took the
	// platform's separator with it.
	return strings.HasSuffix(p, "/"+w) || strings.HasSuffix(p, `\`+w)
}

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
	// A real bucket read error (a corrupt or unreadable postings file) must not
	// pass as "the term is absent": that silently drops the term from the
	// ranking. Remember the first one and hand it back so the caller triggers
	// the same self-heal the exact tier already does.
	var readErr error
	inProject := map[uint32]SessionMeta{}
	for _, meta := range m.Sessions {
		if keep != nil && !keep(meta) {
			continue
		}
		if len(projects) == 0 { // empty scope = whole index
			inProject[meta.Ord] = meta
			continue
		}
		for _, want := range projects {
			if projectInScope(meta.Project, want) {
				inProject[meta.Ord] = meta
				break
			}
		}
	}
	if len(inProject) == 0 {
		return relevanceRanking{}, readErr
	}
	br := newBucketReader(dir)
	defer br.close()
	totalDocs := float64(len(m.Sessions)) + 1
	idfOf := map[string]float64{}
	score := map[uint32]float64{}
	// focus is the same scoring restricted to the words that distinguish. A
	// question asked in plain speech carries filler that is a content word to
	// the index — "how many bikes do I own" ranks on many, bikes and own — and
	// the session that answers it often holds only the rare one. Scored on the
	// whole query it loses to sessions carrying more of the filler; scored on
	// the rare part alone it wins. Neither view is right on its own, so both are
	// kept and the better place of the two is used, at a price.
	focus := map[uint32]float64{}
	matchedTerms := map[uint32]int{}
	strongTerms := map[uint32]int{}
	anyTerms := map[uint32]int{}
	// perMessage tracks how many distinct terms hit each message (record
	// offset) of a session: co-occurrence inside one message is a far
	// stronger topical signal than terms scattered across a long session.
	perMessage := map[uint32]map[int64]int{}
	// msgIDF accumulates the idf mass of distinct terms per message, so a
	// session can be ranked by its best single message rather than by the
	// total it collects across thousands of them.
	msgIDF := map[uint32]map[int64]float64{}
	termsKnown := 0
	for _, term := range terms {
		keys := queryKeys(term)
		if len(keys) == 0 {
			continue
		}
		// A single-token term folds its stem forms in as OR-variants:
		// "camped" must score sessions that say "camping". Multi-token
		// terms keep strict AND semantics below.
		var orKeys []string
		if len(keys) == 1 {
			seenForm := map[string]bool{keys[0]: true}
			orKeys = []string{keys[0]}
			for _, form := range stemMatchForms(term) {
				k := "t" + form
				if !seenForm[k] {
					seenForm[k] = true
					orKeys = append(orKeys, k)
				}
			}
		}
		// A dotted or slashed term ("203.0.113.51", "pkg/index") tokenizes
		// into several index keys. Matching only the first key made an IP
		// degrade to its first octet — a bare small number that lives in
		// half the corpus — so déjà vu fired on unrelated sessions. The
		// term counts only where every sub-token is present; idf comes from
		// the rarest sub-token, which is the one that actually identifies.
		var (
			hit    map[uint32]bool
			tf     map[uint32]int
			minDF  = -1
			offs   map[uint32]map[int64]bool
			missed bool
		)
		if len(orKeys) > 1 && !isCyrToken(term) {
			// Fold stem forms in ONLY when the exact token is absent from the
			// sessions being ranked: "camped" with no postings tries "camping",
			// but an exact hit is never diluted by its variants. Russian
			// inflects too heavily for that gate — a Cyrillic term keeps its
			// whole form union, matching сеть against сетью and сети alike.
			//
			// Absent from *these* sessions, not from the whole store. Asking
			// the store meant that unrelated work decided it: with "write"
			// somewhere in the index the fold switched off, and "how often do
			// we write parquet?" stopped matching the session that says
			// "parquet writes are batched per hour". Measured on the benchmark,
			// that lost the haystack arm a case — a question missing the one
			// session that answers it because of sessions in other projects.
			if exact, err := br.postings(orKeys[0]); err == nil && len(exact) > 0 {
				for _, pp := range exact {
					if _, ok := inProject[pp.Sid]; ok {
						orKeys = orKeys[:1]
						break
					}
				}
			}
		}
		if len(orKeys) > 1 {
			// OR path: union postings across the term's stem forms.
			df := map[uint32]bool{}
			hit = map[uint32]bool{}
			tf = map[uint32]int{}
			offs = map[uint32]map[int64]bool{}
			for _, key := range orKeys {
				posts, err := br.postings(key)
				if err != nil && readErr == nil {
					readErr = err
				}
				if err != nil || len(posts) == 0 {
					continue
				}
				for _, pp := range posts {
					df[pp.Sid] = true
					if _, ok := inProject[pp.Sid]; ok {
						hit[pp.Sid] = true
						tf[pp.Sid]++
						oo := offs[pp.Sid]
						if oo == nil {
							oo = map[int64]bool{}
							offs[pp.Sid] = oo
						}
						oo[pp.Off] = true
					}
				}
			}
			if len(hit) == 0 {
				continue
			}
			minDF = len(df)
			termsKnown++
			// fallthrough to idf/scoring below
		} else {
			for _, key := range keys {
				posts, err := br.postings(key)
				if err != nil && readErr == nil {
					readErr = err
				}
				if err != nil || len(posts) == 0 {
					missed = true
					break
				}
				// Document frequency in sessions, not postings: one marathon
				// session repeating a term 300 times must not make it common.
				df := map[uint32]bool{}
				keyHit := map[uint32]bool{}
				keyTF := map[uint32]int{}
				keyOffs := map[uint32]map[int64]bool{}
				for _, pp := range posts {
					df[pp.Sid] = true
					if _, ok := inProject[pp.Sid]; ok {
						keyHit[pp.Sid] = true
						keyTF[pp.Sid]++
						oo := keyOffs[pp.Sid]
						if oo == nil {
							oo = map[int64]bool{}
							keyOffs[pp.Sid] = oo
						}
						oo[pp.Off] = true
					}
				}
				if hit == nil {
					hit, tf = keyHit, keyTF
				} else {
					for ord := range hit {
						if !keyHit[ord] {
							delete(hit, ord)
							delete(tf, ord)
						} else if keyTF[ord] < tf[ord] {
							tf[ord] = keyTF[ord]
						}
					}
				}
				// Message credit follows the rarest sub-token — the one whose df
				// sets the term's idf. A union would let a message containing
				// only a common sub-token ("index" of "pkg/index") collect the
				// full term mass, and best-message ranking amplifies that.
				if minDF == -1 || len(df) < minDF {
					minDF = len(df)
					offs = keyOffs
				}
				if len(hit) == 0 {
					missed = true
					break
				}
			}
			if missed || len(hit) == 0 {
				continue
			}
			termsKnown++
		}
		idf := math.Log(totalDocs / float64(minDF+1))
		idfOf[term] = idf
		if idf <= 0 {
			// In a tiny corpus every ratio collapses to zero; a term living
			// in only a couple of sessions still identifies them.
			if minDF > 2 {
				continue
			}
			idf = 0.1
		}
		informative := idf >= dejaVuIDFFloor || minDF <= 2
		// Rare enough to identify something on its own: either well past the
		// ordinary bar, or living in a single session of the whole corpus.
		strong := idf >= dejaVuStrongIDFFloor || minDF <= 1
		for ord := range hit {
			mm := perMessage[ord]
			if mm == nil {
				mm = map[int64]int{}
				perMessage[ord] = mm
			}
			mi := msgIDF[ord]
			if mi == nil {
				mi = map[int64]float64{}
				msgIDF[ord] = mi
			}
			for off := range offs[ord] {
				mm[off]++
				mi[off] += idf
			}
		}
		for ord := range hit {
			// Saturated term frequency: repeated mentions add confidence
			// with quickly diminishing returns, so a marathon session cannot
			// bury a focused one through sheer repetition.
			weighted := idf * (1 + 0.25*math.Log2(float64(tf[ord])))
			score[ord] += weighted
			if informative {
				focus[ord] += weighted
			}
			anyTerms[ord]++
			if informative {
				matchedTerms[ord]++
			}
			if strong {
				strongTerms[ord]++
			}
		}
	}
	ranked := make([]relevanceScored, 0, len(score))
	for ord, sc := range score {
		if sc <= 0 {
			continue
		}
		// Same-message co-occurrence bonus: the best single message covering
		// k distinct query terms scales the session's score. A session where
		// one message answers the whole question outranks one that merely
		// mentions every word somewhere.
		best := 1
		for _, k := range perMessage[ord] {
			if k > best {
				best = k
			}
		}
		// A focused message beats diffuse mentions: the session's score is
		// its best message's idf mass (scaled by same-message co-occurrence),
		// with the session-wide total only as a dampened tail. Without this,
		// one marathon session that brushes every query word somewhere
		// outranks the short session that actually answers.
		var bestMsg float64
		for _, v := range msgIDF[ord] {
			if v > bestMsg {
				bestMsg = v
			}
		}
		coocc := 1 + 0.2*float64(best-1)
		sc = bestMsg*coocc + 0.25*(sc-bestMsg)
		// Coverage: distinct informative terms beat repetition.
		if matchedTerms[ord] > 1 {
			sc *= 1 + 0.15*float64(matchedTerms[ord]-1)
		}
		ranked = append(ranked, relevanceScored{inProject[ord], sc, matchedTerms[ord], anyTerms[ord], strongTerms[ord], focus[ord]})
	}
	if len(ranked) == 0 {
		return relevanceRanking{termsKnown: termsKnown}, readErr
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if !ranked[i].meta.Updated.Equal(ranked[j].meta.Updated) {
			return ranked[i].meta.Updated.After(ranked[j].meta.Updated)
		}
		// Total order even on full ties: map iteration must never decide
		// what the user sees first.
		return ranked[i].meta.ID < ranked[j].meta.ID
	})
	ranked = fuseFocus(ranked)
	// Counted here, one line above the truncation, because this is the last
	// moment the pool exists.
	matchedTotal := len(ranked)
	if n > 0 && len(ranked) > n {
		ranked = ranked[:n]
	}
	metas := make([]SessionMeta, 0, len(ranked))
	matched := make([]int, 0, len(ranked))
	anyMatched := make([]int, 0, len(ranked))
	strong := make([]int, 0, len(ranked))
	for _, r := range ranked {
		metas = append(metas, r.meta)
		matched = append(matched, r.matched)
		anyMatched = append(anyMatched, r.any)
		strong = append(strong, r.strong)
	}
	return relevanceRanking{
		metas:       metas,
		informative: matched,
		any:         anyMatched,
		strong:      strong,
		termsKnown:  termsKnown,
		total:       matchedTotal,
		idf:         idfOf,
	}, readErr
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

func SearchWithRecoveryDetailed(dir string, o query.Options, progress io.Writer) (SearchResult, error) {
	r, err := SearchDetailed(dir, o)
	if err == nil || !IsCorrupt(err) {
		return r, err
	}
	if progress != nil {
		fmt.Fprintf(progress, "deja: index damaged (%v), rebuilding ...\n", err)
	}
	if rerr := EnsureForSearch(dir, o, true, progress); rerr != nil {
		return SearchResult{}, rerr
	}
	return SearchDetailed(dir, o)
}

// newestFirstMeta orders sessions for every "most recent" answer deja gives.
//
// A timestamp alone is not an order. Sessions come out of a map, so a store
// where several share a stamp — a day's work imported in one go, a harness
// that stamps per-day, transcripts restored from a backup — reordered itself
// on every run: six calls to `deja last`, six different answers (#713).
// Identity breaks the tie because it is the one thing that does not change.
func newestFirstMeta(a, b SessionMeta) bool {
	if !a.Updated.Equal(b.Updated) {
		return a.Updated.After(b.Updated)
	}
	if a.Harness != b.Harness {
		return a.Harness < b.Harness
	}
	return a.ID < b.ID
}

// newestFirstSession is newestFirstMeta for loaded sessions.
func newestFirstSession(a, b model.Session) bool {
	if !a.Updated.Equal(b.Updated) {
		return a.Updated.After(b.Updated)
	}
	if a.Harness != b.Harness {
		return a.Harness < b.Harness
	}
	return a.ID < b.ID
}

func Recent(dir string, n int) ([]model.Session, error) {
	return RecentMatching(dir, n, query.Options{})
}

func RecentMatching(dir string, n int, o query.Options) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	out := make([]model.Session, 0, len(m.Sessions))
	for _, meta := range m.Sessions {
		if !sessionMetaMatches(meta, o) {
			continue
		}
		out = append(out, sessionFromMeta(meta))
	}
	sort.Slice(out, func(i, j int) bool { return newestFirstSession(out[i], out[j]) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// displayPath contracts the home directory to ~ in user-facing messages.
func displayPath(p string) string {
	if h, err := os.UserHomeDir(); err == nil && h != "" && strings.HasPrefix(p, h) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

func RecentProject(dir, project string, n int) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	project = strings.ToLower(project)
	var metas []SessionMeta
	for _, meta := range m.Sessions {
		p := strings.ToLower(meta.Project)
		if p == project || (project != "" && strings.Contains(p, project)) {
			metas = append(metas, meta)
		}
	}
	sort.Slice(metas, func(i, j int) bool { return newestFirstMeta(metas[i], metas[j]) })
	if n > 0 && len(metas) > n {
		metas = metas[:n]
	}
	return sessionsForMetas(dir, metas)
}

// recordServable says whether a record may be served for a query.
//
// Both retrieval paths have to ask this and only one of them did. Exact search
// asked it per record while scanning; the relevance tier never scans, so it
// served every record of every session it ranked — a --role=user recall could
// come back holding assistant text, and an ordinary one could be carried by a
// file list or a command that exact search hides on purpose.
//
// Work records are why the question is not simply o.Role. A file list is a
// record of what a turn touched, not something said; an invocation is an
// action, not an answer; a replaced span is the file's old contents. All three
// are indexed and searchable, and served only when asked for by name.
func recordServable(role string, o query.Options) bool {
	if o.Role != "" && !roleMatches(role, o.Role) {
		return false
	}
	for _, work := range []string{roleFiles, roleCommand, roleEdit} {
		if role == work && o.Role != work {
			return false
		}
	}
	return true
}

// sessionsServable loads the sessions and keeps only the records this query is
// allowed to be served, dropping any session left holding nothing.
//
// A session that emptied here was ranked entirely on records the caller is not
// allowed to see — a file list, a command, or, under --role, the other side of
// the conversation. Returning it with no messages would be worse than dropping
// it: the count would say a match exists and the result would show nothing.
func sessionsServable(dir string, metas []SessionMeta, o query.Options) ([]model.Session, error) {
	all, err := sessionsForMetas(dir, metas)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, s := range all {
		kept := s.Messages[:0]
		for _, m := range s.Messages {
			if recordServable(m.Role, o) {
				kept = append(kept, m)
			}
		}
		if len(kept) == 0 {
			continue
		}
		s.Messages = kept
		out = append(out, s)
	}
	return out, nil
}

// sessionsForMetas loads full sessions for the given metas in ONE pass over
// records.bin. The per-session variant re-scanned the whole log for every
// session, which turned a session-start hook into hundreds of milliseconds.
func sessionsForMetas(dir string, metas []SessionMeta) ([]model.Session, error) {
	tbl, terr := loadRecordTables(dir)
	if terr != nil {
		return nil, terr
	}
	want := make(map[string]int, len(metas))
	out := make([]model.Session, len(metas))
	for i, meta := range metas {
		want[meta.Harness+":"+meta.ID] = i
		out[i] = sessionFromMeta(meta)
	}
	keys := make(map[string]bool, len(want))
	for k := range want {
		keys[k] = true
	}
	err := eachRecordForKeys(filepath.Join(dir, "records.bin"), tbl, keys, func(r Record) {
		if i, ok := want[r.Key]; ok {
			out[i].Messages = append(out[i].Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
		}
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		orderPromotedNote(&out[i])
	}
	return out, nil
}

// RecentProjects is RecentProject for several project names at once: one
// manifest read and one records pass instead of names × sessions scans.
func RecentProjects(dir string, projects []string, perName int) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var metas []SessionMeta
	for _, project := range projects {
		project = strings.ToLower(project)
		var mine []SessionMeta
		for _, meta := range m.Sessions {
			p := strings.ToLower(meta.Project)
			if p == project || (project != "" && strings.Contains(p, project)) {
				mine = append(mine, meta)
			}
		}
		sort.Slice(mine, func(i, j int) bool { return newestFirstMeta(mine[i], mine[j]) })
		if perName > 0 && len(mine) > perName {
			mine = mine[:perName]
		}
		for _, meta := range mine {
			k := meta.Harness + ":" + meta.ID
			if !seen[k] {
				seen[k] = true
				metas = append(metas, meta)
			}
		}
	}
	return sessionsForMetas(dir, metas)
}

func FindByPrefix(dir, p string) (model.Session, bool, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	// Non-blocking: the session-start hook reaches this through the handoff
	// tip, and a blocking lock made the agent wait out the entire rebuild —
	// twelve seconds on a real corpus, on the very upgrade that triggers one.
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	var matches []SessionMeta
	for _, meta := range m.Sessions {
		if strings.HasPrefix(meta.ID, p) {
			matches = append(matches, meta)
		}
	}
	// The id the session came with, before the loose pass: import rewrites the
	// id and OrigID keeps the old one (#1049), and a reader who typed that id in
	// full should not be handed a local session that merely contains it as a
	// substring. Nothing consulted OrigID at all, so `show`/`resume`/`ctx` said
	// no session matches about a session deja holds and prints that id for.
	if len(matches) == 0 {
		for _, meta := range m.Sessions {
			if meta.OrigID != "" && strings.HasPrefix(meta.OrigID, p) {
				matches = append(matches, meta)
			}
		}
	}
	if len(matches) == 0 {
		for _, meta := range m.Sessions {
			if idLooselyMatches(meta.ID, p) {
				matches = append(matches, meta)
			}
		}
	}
	// The id the session had on the machine it came from. Import rewrites the
	// id, and OrigID is kept precisely so that fact is not lost (#1049) — but
	// nothing looked at it, so someone who read an id on one machine and typed
	// it on another was told no session matches, about a session deja holds and
	// prints that very id for under `--json`. After the local forms, so a local
	// id always wins over an imported one that happens to collide.
	// Last: the `harness:id` shape deja prints in `forget --list` and in
	// promote's receipts, which every reading command refused (#921).
	if len(matches) == 0 {
		if harness, id := splitSelector(p); harness != "" {
			for _, meta := range m.Sessions {
				if strings.EqualFold(meta.Harness, harness) && (strings.HasPrefix(meta.ID, id) || idLooselyMatches(meta.ID, id)) {
					matches = append(matches, meta)
				}
			}
		}
	}
	if len(matches) == 0 {
		return model.Session{}, false, nil
	}
	sort.Slice(matches, func(i, j int) bool { return newestFirstMeta(matches[i], matches[j]) })
	return loadSessionMeta(dir, m, matches[0])
}

// PrefixMatches counts the sessions a prefix resolves to. FindByPrefix picks
// the newest of them, which is the right default and a silent one: a
// single-character prefix matched eleven sessions on a real store and the
// reader had no way to know they were looking at a choice rather than at the
// only answer.
func PrefixMatches(dir, p string) int {
	if dir == "" {
		dir = DefaultDir()
	}
	if p == "" {
		return 0
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return 0
	}
	// Counted the same way FindByPrefix resolves, including the id a session was
	// imported under: #853 requires the count and the resolver to agree, and a
	// selector that opens a session while the count says zero is that failure
	// with the sign flipped.
	n := 0
	for _, meta := range m.Sessions {
		if meta.OrigID != "" && strings.HasPrefix(meta.OrigID, p) {
			n++
			continue
		}
		if strings.HasPrefix(meta.ID, p) {
			n++
		}
	}
	if n == 0 {
		// The count and the resolver have to agree, or a reader is told an id
		// matches nothing and then watches it open (#853).
		for _, meta := range m.Sessions {
			if idLooselyMatches(meta.ID, p) {
				n++
			}
		}
	}
	return n
}

// idLooselyMatches accepts what a reader can actually copy off the screen.
// Result lines elide the middle of a long id, so the copied text is a head and
// a tail rather than a prefix (#707) — and it carries the "…" itself, which
// appears in no id, so a prefix or substring test can never hit it (#853).
func idLooselyMatches(id, p string) bool {
	if idElided(p) {
		return idMatchesElided(id, p)
	}
	return strings.Contains(id, p)
}

// idElided reports whether p is an id as a result line printed it.
func idElided(p string) bool { return strings.Contains(p, "…") }

// idMatchesElided reads the printed form back: the head and tail it stands for.
// A destructive selector takes only this half of idLooselyMatches — a substring
// selector would make `forget --session s1` also match `deja-note-claude-s1`
// and destroy the decision #690 and #841 keep (#855).
func idMatchesElided(id, p string) bool {
	head, tail, ok := strings.Cut(p, "…")
	return ok && strings.HasPrefix(id, head) && strings.HasSuffix(id, tail)
}

// PrefixHarnesses names the harnesses holding a session whose id is exactly the
// given string.
//
// Two harnesses can carry the same id — that is what #698 is about, and it
// happens naturally when a transcript is copied between tools. There the advice
// "use a longer prefix" cannot be followed, because the ids are the same
// string; --harness is the only thing that separates them (#719).
func PrefixHarnesses(dir, id string) []string {
	if dir == "" {
		dir = DefaultDir()
	}
	if id == "" {
		return nil
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, meta := range m.Sessions {
		if meta.OrigID == id {
			out = append(out, meta.Harness)
			continue
		}
		if meta.ID == id {
			out = append(out, meta.Harness)
		}
	}
	sort.Strings(out)
	return out
}

// FindByIdentity resolves the exact composite identity emitted by machine
// search and recent output. Unlike the human prefix command, it never guesses
// between harnesses or accepts a shortened native ID.
func FindByIdentity(dir, harness, id string) (model.Session, bool, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	meta, ok := m.Sessions[harness+":"+id]
	if !ok {
		return model.Session{}, false, nil
	}
	return loadSessionMeta(dir, m, meta)
}

// Identity names one session the way machine output does.
type Identity struct {
	Harness string
	ID      string
}

// FindManyByIdentity is FindByIdentity for a list. Records live in one
// append-only log with no per-session offsets, so resolving a single identity
// streams the whole of records.bin; calling it in a loop streams it once per
// session. `deja files` did that 250 times over a 59 MB log and spent 1.6 s of
// its 1.9 s there (#1069). One pass, all keys.
//
// Sessions come back in the order asked for, with identities the manifest does
// not know dropped.
func FindManyByIdentity(dir string, ids []Identity) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if len(ids) == 0 {
		return nil, nil
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return nil, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	metas := make([]SessionMeta, 0, len(ids))
	for _, want := range ids {
		if meta, found := m.Sessions[want.Harness+":"+want.ID]; found {
			metas = append(metas, meta)
		}
	}
	if len(metas) == 0 {
		return nil, nil
	}
	return sessionsForMetas(dir, metas)
}

// FindByID looks a session up when only its id is known. Hook payloads carry
// one without naming the harness, and the id is unique in practice: the
// harnesses that generate them use uuids or their own prefixed ids.
func FindByID(dir, id string) (model.Session, bool, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if id == "" {
		return model.Session{}, false, nil
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	if ok {
		defer unlock()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	for _, meta := range m.Sessions {
		if meta.ID == id {
			return loadSessionMeta(dir, m, meta)
		}
	}
	return model.Session{}, false, nil
}

func loadSessionMeta(dir string, m Manifest, meta SessionMeta) (model.Session, bool, error) {
	s := sessionFromMeta(meta)
	recs, err := recordsForKey(filepath.Join(dir, "records.bin"), tablesFromManifest(m), meta.Harness+":"+meta.ID)
	if err != nil {
		return model.Session{}, false, err
	}
	for _, r := range recs {
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}
	orderPromotedNote(&s)
	return s, true, nil
}

// IsPromotedNote reports whether a session is a promoted note, on this machine
// or on the one it came from. Import renames sessions to imported-<hash>, so
// the id alone stops answering the question after a sync (#975).
func IsPromotedNote(harness, id, origID string) bool {
	if harness != "deja" {
		return false
	}
	return strings.HasPrefix(id, "deja-note-") || strings.HasPrefix(origID, "deja-note-")
}

// orderPromotedNote keeps a promoted note's corrections newest-first. The
// parser writes them that way (#812), but an incremental build appends the new
// lines to what the log already holds, so the record order stopped saying which
// answer holds: the snippet an agent reads led with the answer that had been
// overturned (#944). Ordering on read fixes indexes already on disk too.
func orderPromotedNote(s *model.Session) {
	if !IsPromotedNote(s.Harness, s.ID, s.OrigID) || len(s.Messages) < 2 {
		return
	}
	// Newest first by time; when a batch carries no timestamps — a hand-made
	// one, or an older export — the file order is the record: the parser
	// reverses it for the same reason (#812, #975).
	sort.SliceStable(s.Messages, func(i, j int) bool {
		a, b := s.Messages[i], s.Messages[j]
		if a.Time.Equal(b.Time) {
			return i > j
		}
		return a.Time.After(b.Time)
	})
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
	if stored == want {
		return true
	}
	return want == "tool" && stored == roleToolOutput
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

// isToolRole says whether a record holds the work rather than the talk about
// it. Tool records are bulk-repetitive by nature — the same command, the same
// paths, session after session — so a query that matches one matches hundreds,
// and the per-session bound has to spend its budget on speech first.
func isToolRole(role string) bool {
	return role == roleFiles || role == roleCommand || role == roleToolOutput || role == roleEdit
}

func scanRecordsWithVariants(dir string, m Manifest, o query.Options, offsets []int64, variants map[string][]string) ([]model.Session, error) {
	by := map[string]*model.Session{}
	add := func(r Record) {
		meta, ok := m.Sessions[r.Key]
		if !ok {
			return
		}
		if o.Harness != "" && !harnessMatches(meta.Harness, o.Harness) {
			return
		}
		if o.Project != "" && !strings.Contains(strings.ToLower(meta.Project), strings.ToLower(o.Project)) {
			return
		}
		if !fromMatches(meta.From, o.From) {
			return
		}
		if o.Since > 0 && meta.Updated.Before(time.Now().Add(-o.Since)) {
			return
		}
		if !recordServable(r.Role, o) {
			return
		}
		s := by[r.Key]
		if s == nil {
			cp := sessionFromMeta(meta)
			s = &cp
			by[r.Key] = s
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}
	if len(offsets) > 0 {
		f, err := openIndexFile(filepath.Join(dir, "records.bin"))
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		offsets = sortedUniqueOffsets(offsets)
		if err := eachRecordAt(f, offsets, tablesFromManifest(m), func(r Record) {
			if recordMatchesQueryVariants(r, o, variants) {
				add(r)
			}
		}); err != nil {
			return nil, err
		}
	} else {
		if err := eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
			if recordMatchesQueryVariants(r, o, variants) {
				add(r)
			}
		}); err != nil {
			return nil, err
		}
	}
	out := make([]model.Session, 0, len(by))
	for _, s := range by {
		orderPromotedNote(s)
		out = append(out, *s)
	}
	return out, nil
}

func cutPostingsBySession(posts []posting, m Manifest, o query.Options) []posting {
	metaByOrd := sessionMetaByOrd(m)
	// Keep the complete posting-derived candidate set. Ranking needs the
	// candidate records to calculate BM25 document frequency and length.
	out := make([]posting, 0, len(posts))
	for _, p := range posts {
		if meta, ok := metaByOrd[p.Sid]; ok && sessionMetaMatches(meta, o) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return capPostingsPerSession(out, perSessionCandidates)
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

func sessionMetaByOrd(m Manifest) map[uint32]SessionMeta {
	out := make(map[uint32]SessionMeta, len(m.Sessions))
	for _, meta := range m.Sessions {
		out[meta.Ord] = meta
	}
	return out
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
	if o.Harness != "" && !harnessMatches(meta.Harness, o.Harness) {
		return false
	}
	if o.Project != "" && !strings.Contains(strings.ToLower(meta.Project), strings.ToLower(o.Project)) {
		return false
	}
	if !fromMatches(meta.From, o.From) {
		return false
	}
	if o.Since > 0 && meta.Updated.Before(time.Now().Add(-o.Since)) {
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
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
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

func postingsFor(dir, tok string) ([]posting, error) {
	return readBucketToken(filepath.Join(dir, "buckets", bucket(tok)+".bin"), tok)
}

// OtherWordForms lists, per query term, the other forms of that word the
// corpus actually holds — "retries" for "retry", "rotated" for "rotate".
//
// The stem tier already knows these, but it only ever runs after the exact
// tier came up empty. When the exact tier does answer, `retry` returns the
// sessions that wrote "retry" and stops: the sessions that wrote "retries"
// are neither returned nor mentioned, and the ladder is invisible from the
// outside. This is the lookup that lets the caller say so.
func OtherWordForms(dir string, terms []string) map[string][]string {
	if dir == "" {
		dir = DefaultDir()
	}
	catalog, err := tokenCatalogCached(dir)
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, term := range terms {
		var others []string
		for _, form := range stemMatches(term, catalog) {
			if form != term {
				others = append(others, form)
			}
		}
		if len(others) > 0 {
			out[term] = others
		}
	}
	return out
}

// TermSessionCounts says how many sessions each term appears in on its own.
// When an AND query finds no intersection, "try fewer words" is right in
// direction and empty in content — the reader has to guess which of their words
// to drop, while deja already read these counts to decide there was none (#826).
func TermSessionCounts(dir string, terms []string) map[string]int {
	if dir == "" {
		dir = DefaultDir()
	}
	out := make(map[string]int, len(terms))
	for _, t := range terms {
		key := "t" + t
		posts, err := postingsFor(dir, key)
		if err != nil {
			// Best effort by design: this only decorates the "try fewer words"
			// hint after a search that already returned. A term whose bucket
			// will not read drops out of the advice rather than failing the
			// command — a real corruption already surfaced on the search path
			// that ran first.
			continue
		}
		seen := map[uint32]bool{}
		for _, p := range posts {
			seen[p.Sid] = true
		}
		out[t] = len(seen)
	}
	return out
}

func intersectPostings(dir string, keys []string) ([]posting, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	lists := make([][]posting, 0, len(keys))
	for _, key := range keys {
		list, err := postingsFor(dir, key)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, nil
		}
		lists = append(lists, list)
	}
	sort.Slice(lists, func(i, j int) bool { return len(lists[i]) < len(lists[j]) })
	set := make(map[int64]posting, len(lists[0]))
	for _, p := range lists[0] {
		set[p.Off] = p
	}
	for _, list := range lists[1:] {
		next := make(map[int64]posting, min(len(set), len(list)))
		for _, p := range list {
			if _, ok := set[p.Off]; ok {
				next[p.Off] = p
			}
		}
		set = next
		if len(set) == 0 {
			return nil, nil
		}
	}
	out := make([]posting, 0, len(set))
	for _, p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Off < out[j].Off })
	return out, nil
}

func intersectSubstringPostings(dir string, bare []string) ([]posting, error) {
	posts, _, err := intersectSubstringPostingsDetailed(dir, bare)
	return posts, err
}

func intersectSubstringPostingsDetailed(dir string, bare []string) ([]posting, map[string][]string, error) {
	if len(bare) == 0 {
		return nil, nil, nil
	}
	if len(bare) > 3 {
		bare = bare[:3] // longest-first; keep the expansion bounded
	}
	buckets, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	perTok := make([]map[int64]posting, len(bare))
	variants := make(map[string][]string, len(bare))
	for i := range perTok {
		perTok[i] = map[int64]posting{}
	}
	for _, de := range buckets {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".bin") {
			continue
		}
		path := filepath.Join(dir, "buckets", de.Name())
		entries, f, err := openBucketDir(path)
		if err != nil {
			// The directory listing named this bucket, so a miss now means a
			// concurrent rebuild swapped it out — tolerate that. Anything else
			// (a corrupt header, a denied read) is real and is surfaced so the
			// fuzzy tier does not silently under-match a damaged store.
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, err
		}
		for _, e := range entries {
			tok := strings.TrimPrefix(e.tok, "t")
			for i, b := range bare {
				if !strings.Contains(tok, b) {
					continue
				}
				variants[b] = append(variants[b], tok)
				buf := make([]byte, e.n)
				if _, err := f.ReadAt(buf, int64(e.off)); err != nil {
					// The bucket directory validated but the block it points to
					// will not read: corruption, not a missing token. Surface it.
					f.Close()
					return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
				}
				for _, p := range decodePostings(buf) {
					perTok[i][p.Off] = p
				}
			}
		}
		f.Close()
	}
	set := perTok[0]
	for _, m := range perTok[1:] {
		next := map[int64]posting{}
		for off, p := range m {
			if _, ok := set[off]; ok {
				next[off] = p
			}
		}
		set = next
		if len(set) == 0 {
			return nil, nil, nil
		}
	}
	out := make([]posting, 0, len(set))
	for _, p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Off < out[j].Off })
	for token := range variants {
		sort.Strings(variants[token])
	}
	return out, variants, nil
}

// commonTokenPostings is where a word stops being worth widening. Below it a
// near-neighbour is usually the same thing spelled differently and is worth
// the candidate walk; at or above it the word is ordinary vocabulary the
// corpus is full of, and its neighbours only cost time.
const commonTokenPostings = 200

func fuzzyPostings(dir string, terms, phrases []string) ([]posting, map[string][]string, error) {
	if !hasFuzzyToken(terms) {
		return nil, nil, nil
	}
	idx, err := tokenIndexCached(dir)
	if err != nil {
		return nil, nil, err
	}
	perToken := make([]map[int64]posting, len(terms))
	variants := map[string][]string{}
	for i, term := range terms {
		// A term the corpus spells exactly is not a typo, and this tier exists
		// for typos. One bucket read settles it; the candidate walk behind
		// closeTokens compares the term against every token of a similar
		// length and was running for words like "rice" and "favorite" that
		// the index already holds verbatim.
		// A word the corpus already uses everywhere gains nothing from its
		// neighbours: it is not a typo, and widening it only adds postings
		// that discriminate nothing. A rare word is still worth widening even
		// when it is spelled correctly, because a near-neighbour of a rare
		// word is usually the same thing said differently.
		var matches []string
		if exact, eerr := postingsFor(dir, "t"+term); eerr == nil && len(exact) >= commonTokenPostings {
			matches = []string{term}
		} else {
			matches = closeTokens(term, idx)
		}
		if len(matches) == 0 {
			return nil, nil, nil
		}
		variants[term] = matches
		perToken[i] = map[int64]posting{}
		for _, variant := range matches {
			posts, err := postingsFor(dir, "t"+variant)
			if err != nil {
				return nil, nil, err
			}
			for _, p := range posts {
				perToken[i][p.Off] = p
			}
		}
	}
	// Phrase text is verified from records; phrase tokens participate in the
	// same fuzzy candidate intersection above, so phrases need no extra work.
	_ = phrases
	return intersectPostingMaps(perToken), variants, nil
}

func fuzzySearch(dir string, m Manifest, o query.Options) (SearchResult, error) {
	terms, phrases := query.QueryParts(o.Query)
	posts, variants, err := fuzzyPostings(dir, terms, phrases)
	if err != nil || len(posts) == 0 {
		return SearchResult{}, err
	}
	posts = cutPostingsBySession(posts, m, o)
	if len(posts) == 0 {
		return SearchResult{}, nil
	}
	ss, err := scanRecordsWithVariants(dir, m, o, postingOffsets(posts), variants)
	if err != nil || len(ss) == 0 {
		return SearchResult{}, err
	}
	return SearchResult{Sessions: ss, Fuzzy: true, Variants: variants, Tier: query.TierClose}, nil
}

func stemSearch(dir string, m Manifest, o query.Options) (SearchResult, error) {
	terms, phrases := query.QueryParts(o.Query)
	posts, variants, err := stemPostings(dir, terms, phrases)
	if err != nil || len(posts) == 0 {
		return SearchResult{}, err
	}
	posts = cutPostingsBySession(posts, m, o)
	if len(posts) == 0 {
		return SearchResult{}, nil
	}
	ss, err := scanRecordsWithVariants(dir, m, o, postingOffsets(posts), variants)
	if err != nil || len(ss) == 0 {
		return SearchResult{}, err
	}
	return SearchResult{Sessions: ss, Stemmed: true, Variants: variants, Tier: query.TierClose}, nil
}

func stemPostings(dir string, terms, phrases []string) ([]posting, map[string][]string, error) {
	if !hasStemToken(terms) {
		return nil, nil, nil
	}
	catalog, err := tokenCatalogCached(dir)
	if err != nil {
		return nil, nil, err
	}
	matchesPer := make([][]string, len(terms))
	anchored := 0
	for i, term := range terms {
		matchesPer[i] = stemMatches(term, catalog)
		if len(matchesPer[i]) > 0 {
			anchored++
		}
	}
	if anchored == 0 {
		return nil, nil, nil
	}
	// A token with no occurrences anywhere in the corpus cannot anchor the
	// AND — natural-language queries are full of them. Drop such tokens when
	// at least two anchored terms remain; the empty-string variant marks the
	// token optional for the scan-time matcher.
	variants := map[string][]string{}
	type anchor struct {
		term string
		set  map[int64]posting
	}
	anchors := make([]anchor, 0, len(terms))
	for i, term := range terms {
		if len(matchesPer[i]) == 0 {
			if anchored < 2 {
				return nil, nil, nil
			}
			variants[term] = []string{""}
			continue
		}
		variants[term] = matchesPer[i]
		set := map[int64]posting{}
		for _, variant := range matchesPer[i] {
			posts, err := postingsFor(dir, "t"+variant)
			if err != nil {
				return nil, nil, err
			}
			for _, p := range posts {
				set[p.Off] = p
			}
		}
		anchors = append(anchors, anchor{term: term, set: set})
	}
	_ = phrases
	sets := func(skip map[int]bool) []map[int64]posting {
		out := make([]map[int64]posting, 0, len(anchors))
		for i, a := range anchors {
			if !skip[i] {
				out = append(out, a.set)
			}
		}
		return out
	}
	if posts := intersectPostingMaps(sets(nil)); len(posts) > 0 {
		return posts, variants, nil
	}
	// Best-effort AND: no single session holds every anchored token. Natural
	// queries carry filler ("why", "let") — try dropping up to two tokens,
	// shortest first, and keep the first combination that matches anything.
	if len(anchors) < 3 {
		return nil, variants, nil
	}
	order := make([]int, len(anchors))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return len(anchors[order[a]].term) < len(anchors[order[b]].term)
	})
	for _, i := range order {
		if posts := intersectPostingMaps(sets(map[int]bool{i: true})); len(posts) > 0 {
			variants[anchors[i].term] = []string{""}
			return posts, variants, nil
		}
	}
	if len(anchors) >= 4 {
		for x := 0; x < len(order); x++ {
			for y := x + 1; y < len(order); y++ {
				i, j := order[x], order[y]
				if posts := intersectPostingMaps(sets(map[int]bool{i: true, j: true})); len(posts) > 0 {
					variants[anchors[i].term] = []string{""}
					variants[anchors[j].term] = []string{""}
					return posts, variants, nil
				}
			}
		}
	}
	return nil, variants, nil
}

func hasStemToken(terms []string) bool {
	for _, term := range terms {
		if len([]rune(term)) >= 5 {
			return true
		}
	}
	return false
}

func stemMatches(term string, catalog map[string]bool) []string {
	var forms []string
	if len([]rune(term)) < 5 {
		for _, form := range []string{term, term + "s", term + "es", strings.TrimSuffix(term, "s")} {
			if len(form) >= 3 {
				forms = append(forms, form)
			}
		}
	} else {
		forms = suffixForms(term)
	}
	forms = append(forms, devSynonyms[term]...)
	forms = append(forms, cyrSuffixForms(term)...)
	matches := make([]string, 0, 8)
	seen := map[string]bool{}
	for _, form := range forms {
		if !seen[form] && catalog[form] {
			seen[form] = true
			matches = append(matches, form)
		}
	}
	if len(matches) > 8 {
		matches = matches[:8]
	}
	return matches
}

// devSynonyms is a small reviewed fold table for the abbreviations developers
// actually type. Deterministic and shipped in the repo — no embeddings, no
// guessing. Applied only in the stem tier (exact matches never consult it)
// and narrated like any other variant.
var devSynonyms = func() map[string][]string {
	pairs := [][2]string{
		{"auth", "authentication"}, {"auth", "authorization"},
		{"db", "database"}, {"k8s", "kubernetes"},
		{"env", "environment"}, {"config", "configuration"},
		{"cfg", "config"}, {"repo", "repository"},
		{"perm", "permission"}, {"cert", "certificate"},
		{"dir", "directory"}, {"msg", "message"},
		{"deps", "dependencies"}, {"prod", "production"},
		{"param", "parameter"}, {"arg", "argument"},
		{"docs", "documentation"}, {"err", "error"},
		{"regex", "regexp"}, {"spec", "specification"},
	}
	m := map[string][]string{}
	for _, p := range pairs {
		m[p[0]] = append(m[p[0]], p[1])
		m[p[1]] = append(m[p[1]], p[0])
	}
	return m
}()

// cyrEndings are common Russian inflection endings, longest first so the
// stem strips greedily. A bounded fold, not a morphology engine.
var cyrEndings = []string{
	"иями", "ями", "ами", "ией", "иях", "ях", "ах", "ов", "ев", "ей",
	"ой", "ий", "ия", "ию", "ии", "ие", "ый", "ая", "ое", "ые",
	"ью", "ям", "ем", "ом",
	"ть", "л", "ла", "ло", "ли", "а", "я", "у", "ю", "ы", "и", "е", "о", "ь",
}

// endsInfinitive reports whether a ть-final term is shaped like a verb: the
// rune before "ть" is a vowel. Consonant-stem infinitives (лезть, сесть,
// класть) are misread as nouns and lose the verb branch — 11 reachable pairs
// across a 20k frequency list, against closing часть -> час and весть -> вес,
// which are common words landing on a different lemma entirely.
func endsInfinitive(term string) bool {
	if !strings.HasSuffix(term, "ть") {
		return false
	}
	runes := []rune(term)
	if len(runes) < 3 {
		return false
	}
	switch runes[len(runes)-3] {
	case 'а', 'е', 'ё', 'и', 'о', 'у', 'ы', 'э', 'ю', 'я':
		return true
	}
	return false
}

// cyrSoftEndings is the third-declension feminine paradigm, and the only set
// that may attach to a stem exposed by stripping a soft sign. Attaching the
// hard adjective endings there is what folded цель onto целая and верь onto
// вера: сеть->сети needs "и", цель->целая needs "ая", so keeping the two
// tables apart separates shapes that look identical by length alone.
var cyrSoftEndings = []string{"и", "ью", "ям", "ях", "ями", "ей", "е", "я", "ю", "ем"}

// cyrVerbEndings are the infinitive and past-tense endings.
var cyrVerbEndings = map[string]bool{"ть": true, "л": true, "ла": true, "ло": true, "ли": true}

// cyrVerbEndingList is what a ь-final infinitive's stem takes back: the past
// tense plus the present-tense vowels, so знать reaches both знал and знаю.
var cyrVerbEndingList = []string{"ть", "л", "ла", "ло", "ли", "ю", "у", "а", "я", "е", "и", "ем"}

// cyrNominalEndings are unambiguously noun-case: a stem exposed by stripping
// one of these came from a noun, so verb endings must not attach to it —
// весом strips to вес, and вес+ть is весть. Endings a verb can also carry
// (ю, у, а, и ...) stay out of this set, so знаю -> зна -> знать survives.
var cyrNominalEndings = map[string]bool{
	"иями": true, "ями": true, "ами": true, "ией": true, "иях": true,
	"ях": true, "ах": true, "ов": true, "ев": true, "ей": true, "ой": true,
	"ий": true, "ия": true, "ию": true, "ии": true, "ие": true,
	"ый": true, "ая": true, "ое": true, "ые": true, "ом": true,
}

// cyrMatchForms folds a Russian term onto its inflection family for the
// relevance tier: strip a known ending, then re-attach the endings that
// belong to the same paradigm. Four runes is the floor — below it the
// three-letter function words (что, как, его) would fan out for nothing.
//
// The tier has no catalog gate, so whatever this invents is looked up for
// real and can surface a session. That is why stripping and re-attaching use
// different tables: one shared table folded цель onto целая.
func cyrMatchForms(term string) []string {
	runes := []rune(term)
	if len(runes) < 4 {
		return nil
	}
	seen := map[string]bool{term: true}
	forms := make([]string, 0, len(cyrEndings)+1)
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			forms = append(forms, f)
		}
	}
	if strings.HasSuffix(term, "ь") {
		// Soft stem — the third-declension nouns: сеть, жизнь, ночь, очередь.
		if base := strings.TrimSuffix(term, "ь"); len([]rune(base)) >= 3 {
			// The bare stem is deliberately not emitted: a third-declension
			// noun's stem is not a word on its own (сеть -> сет), while for a
			// ь-final imperative it is — борись would recall Борис.
			for _, end := range cyrSoftEndings {
				add(base + end)
			}
		}
		// A ь-final infinitive is a verb, not a noun: знать -> знал. But a
		// noun can end in "ть" too, and часть taking the verb branch reaches
		// час — a different lemma entirely. Infinitives are vowel + ть
		// (де-лать, зна-ть, ви-деть); ть-final nouns are consonant + ть
		// (час-ть, вес-ть, лес-ть, кос-ть). That also closes весть -> вес,
		// which was the surviving inverse of весом -> весть.
		if strings.HasSuffix(term, "ть") && endsInfinitive(term) {
			if base := strings.TrimSuffix(term, "ть"); len([]rune(base)) >= 3 {
				add(base)
				for _, end := range cyrVerbEndingList {
					add(base + end)
				}
			}
		}
		return forms
	}
	base, stripped := term, ""
	for _, end := range cyrEndings {
		if strings.HasSuffix(term, end) && len(runes)-len([]rune(end)) >= 3 {
			base, stripped = strings.TrimSuffix(term, end), end
			break
		}
	}
	if base != term {
		add(base)
	}
	nominal := cyrNominalEndings[stripped]
	for _, end := range cyrEndings {
		if nominal && cyrVerbEndings[end] {
			continue
		}
		add(base + end)
	}
	return forms
}

// cyrSuffixForms bridges Russian inflection: strip the longest known ending,
// then re-attach each — миграция matches миграции and миграцию. ASCII terms
// return nothing.
func cyrSuffixForms(term string) []string {
	if !isCyrToken(term) {
		return nil
	}
	// Both tiers fold Russian the same way. This one used to strip and
	// re-attach from one shared table, which is the цель->целая shape the
	// relevance tier was fixed for — it survived here one tier over, letting
	// пусть recall a session that only says пустой. The catalog gate and the
	// 8-match cap made it milder, not correct.
	return cyrMatchForms(term)
}

func suffixForms(word string) []string {
	seen := map[string]bool{word: true}
	type candidate struct {
		word  string
		depth int
	}
	queue := []candidate{{word: word}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.depth == 2 {
			continue
		}
		for _, form := range oneSuffixStep(current.word) {
			if form != "" && !seen[form] {
				seen[form] = true
				queue = append(queue, candidate{word: form, depth: current.depth + 1})
			}
		}
	}
	out := make([]string, 0, len(seen))
	for form := range seen {
		out = append(out, form)
	}
	sort.Strings(out)
	return out
}

func oneSuffixStep(word string) []string {
	var out []string
	add := func(form string) {
		if len(form) >= 3 && form != word {
			out = append(out, form)
		}
	}
	switch {
	case strings.HasSuffix(word, "tion"):
		add(strings.TrimSuffix(word, "tion") + "te")
	case strings.HasSuffix(word, "ing"):
		base := strings.TrimSuffix(word, "ing")
		add(base)
		add(base + "e")
	case strings.HasSuffix(word, "ies"):
		// retries -> retry. Ahead of the "es" and "s" cases, which reduce it
		// to "retri" and stop there: neither a consonant+y word nor its
		// plural could reach the other, so "retry" found nothing in a store
		// full of "retries" (#1079).
		add(strings.TrimSuffix(word, "ies") + "y")
		add(strings.TrimSuffix(word, "es"))
	case strings.HasSuffix(word, "ied"):
		add(strings.TrimSuffix(word, "ied") + "y")
	case strings.HasSuffix(word, "ed"):
		base := strings.TrimSuffix(word, "ed")
		add(base)
		add(base + "e")
	case strings.HasSuffix(word, "ment"):
		base := strings.TrimSuffix(word, "ment")
		add(base)
		add(base + "e")
	case strings.HasSuffix(word, "es"):
		add(strings.TrimSuffix(word, "es"))
	case strings.HasSuffix(word, "s"):
		add(strings.TrimSuffix(word, "s"))
	}
	if strings.HasSuffix(word, "e") {
		base := strings.TrimSuffix(word, "e")
		add(base + "ing")
		add(base + "ed")
		add(strings.TrimSuffix(word, "te") + "tion")
	}
	// expansions: fail->fails, fail->failing/failed. The catalog filter keeps
	// nonsense forms from ever reaching a lookup.
	if base, ok := consonantY(word); ok {
		// retry -> retries, retried. The plain +s below gives "retrys", which
		// no transcript contains (#1079).
		add(base + "ies")
		add(base + "ied")
	}
	if !strings.HasSuffix(word, "s") {
		add(word + "s")
	}
	if !strings.HasSuffix(word, "e") && !strings.HasSuffix(word, "ing") && !strings.HasSuffix(word, "ed") {
		add(word + "ing")
		add(word + "ed")
	}
	return out
}

func hasFuzzyToken(terms []string) bool {
	for _, term := range terms {
		// The 4-rune floor also keeps CJK bigrams (always 2 runes) out of
		// fuzzy variant generation entirely — no variant-space blowup (#338).
		if len([]rune(term)) >= 4 {
			return true
		}
	}
	return false
}

func tokenCatalog(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	catalog := map[string]bool{}
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".bin") {
			continue
		}
		header, f, err := openBucketDir(filepath.Join(dir, "buckets", de.Name()))
		if err != nil {
			return nil, err
		}
		for _, entry := range header {
			catalog[strings.TrimPrefix(entry.tok, "t")] = true
		}
		_ = f.Close()
	}
	return catalog, nil
}

func closeTokens(query string, idx *tokenIndex) []string {
	type match struct {
		token    string
		distance int
	}
	var matches []match
	qr := len([]rune(query))
	limit := 1
	if qr >= 8 {
		limit = 2
	}
	// An edit changes the length by at most one and a transposition not at
	// all, so only tokens within the limit of the query's length can match.
	// Walking those buckets replaces a Damerau-Levenshtein run against every
	// token in the corpus — ~200k of them — with a few short slices.
	idx.candidates(qr, limit, func(token string) {
		if d := damerauDistance(query, token, limit); d <= limit {
			matches = append(matches, match{token: token, distance: d})
		}
	})
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].distance == matches[j].distance {
			return matches[i].token < matches[j].token
		}
		return matches[i].distance < matches[j].distance
	})
	if len(matches) > 8 {
		matches = matches[:8]
	}
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.token
	}
	return out
}

// isASCIIString reports whether every byte is one rune, so the byte length is
// the rune count.
func isASCIIString(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func damerauDistance(a, b string, max int) int {
	if len(a) <= 64 && len(b) <= 64 && utf8.ValidString(a) && utf8.ValidString(b) && utf8.RuneCountInString(a) == len(a) && utf8.RuneCountInString(b) == len(b) {
		var prev, prevPrev, cur [65]int
		for j := 0; j <= len(b); j++ {
			prev[j] = j
		}
		for i := 1; i <= len(a); i++ {
			cur[0] = i
			for j := 1; j <= len(b); j++ {
				cost := 0
				if a[i-1] != b[j-1] {
					cost = 1
				}
				cur[j] = min(cur[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
				if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
					cur[j] = min(cur[j], prevPrev[j-2]+1)
				}
			}
			prevPrev, prev, cur = prev, cur, prevPrev
		}
		return prev[len(b)]
	}
	return damerauDistanceRunes(a, b, max)
}

func damerauDistanceRunes(a, b string, max int) int {
	ar, br := []rune(a), []rune(b)
	if abs(len(ar)-len(br)) > max {
		return max + 1
	}
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	prevPrev := append([]int(nil), prev...)
	for i := 1; i <= len(ar); i++ {
		cur := make([]int, len(br)+1)
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			cur[j] = min(cur[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				cur[j] = min(cur[j], prevPrev[j-2]+1)
			}
		}
		prevPrev, prev = prev, cur
	}
	return prev[len(br)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func intersectPostingMaps(sets []map[int64]posting) []posting {
	if len(sets) == 0 {
		return nil
	}
	set := sets[0]
	for _, next := range sets[1:] {
		for off := range set {
			if _, ok := next[off]; !ok {
				delete(set, off)
			}
		}
	}
	out := make([]posting, 0, len(set))
	for _, p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Off < out[j].Off })
	return out
}

func tokens(s string) []string {
	// Fold NFD to NFC so an accented word keys the same whether it was typed or
	// stored decomposed. A combining mark is category Mn, not a letter, so the
	// tokenizer below would otherwise split "café" (NFD) into "cafe" and drop
	// the accent, keying it apart from the precomposed "café" (#1098). Both the
	// stored text (indexKeys) and the query (queryKeys) pass through here, so
	// the fold is symmetric and the two sides always meet on one key.
	s = nfcfold.Compose(s)
	seen := map[string]bool{}
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() >= 2 {
			t := b.String()
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
		b.Reset()
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b.WriteRune(r)
			if b.Len() > 64 {
				flush()
			}
		} else {
			flush()
		}
	}
	flush()
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func indexKeys(s string) []string {
	var out []string
	for _, tok := range tokens(s) {
		out = append(out, "t"+tok)
	}
	for _, part := range identifierParts(s) {
		out = append(out, "t"+part)
	}
	// Bigram keys fold Traditional to Simplified so the same word written
	// either way lands on one key and a query in one script reaches content in
	// the other. Folding only the key keeps the stored text untouched. The
	// emitter folds runs in place and dedupes folded pairs — cheaper than
	// folding each emitted bigram, and every caller collapses repeated keys
	// anyway (#492).
	cjkIndexKeys(s, func(tok string) {
		out = append(out, tok)
	})
	return out
}

// identifierParts emits the lowered inner words of compound identifiers so
// `deja "user profile"` finds getUserProfile and refresh_token_rotation.
// It walks the original-cased text: case humps are gone after lowering.
// Only words of 6+ runes with a real boundary produce parts, and only parts
// of 3+ runes are kept — short fragments ride the substring tier instead.
func identifierParts(s string) []string {
	var out []string
	var word []rune
	flushWord := func() {
		if len(word) >= 6 {
			splitCompound(word, &out)
		}
		word = word[:0]
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			word = append(word, r)
			if len(word) > 64 {
				flushWord()
			}
			continue
		}
		flushWord()
	}
	flushWord()
	return out
}

func splitCompound(word []rune, out *[]string) {
	start := 0
	boundaries := 0
	emit := func(end int) {
		if end-start >= 3 {
			*out = append(*out, strings.ToLower(string(word[start:end])))
		}
		start = end
	}
	for i := 1; i < len(word); i++ {
		c, p := word[i], word[i-1]
		switch {
		case c == '_' || c == '-':
			emit(i)
			start = i + 1
			boundaries++
		case unicode.IsUpper(c) && (unicode.IsLower(p) || unicode.IsDigit(p)):
			// getUser | getUserById: hump boundary
			emit(i)
			boundaries++
		case unicode.IsLower(c) && unicode.IsUpper(p) && i-1 > start:
			// JSONData -> JSON | Data: break before the last upper
			emit(i - 1)
			boundaries++
		}
	}
	if boundaries > 0 {
		emit(len(word))
	}
}

func retrievalKeys(keys []string) []string {
	// Fetch postings for up to 8 tokens: a bucket read is sub-millisecond and
	// intersectPostings sorts the fetched lists rarest-first with an early
	// exit, so more keys means a more selective AND, not a slower one. The old
	// cap of 3 longest tokens guessed at rarity by length and guessed wrong on
	// long-but-common words.
	if len(keys) <= 8 {
		return keys
	}
	return keys[:8]
}

func queryKeys(s string) []string {
	toks := expandCJKTokens(tokens(s))
	if len(toks) == 0 {
		return nil
	}
	// Drop stop words so retrievalKeys picks selective content tokens; a
	// long stop word like "before" must not over-constrain the AND. If the
	// query is all stop words, keep them (odd results beat none).
	content := make([]string, 0, len(toks))
	for _, tok := range toks {
		if !query.IsStopWord(tok) {
			content = append(content, tok)
		}
	}
	if len(content) == 0 {
		content = toks
	}
	out := make([]string, 0, len(content))
	for _, tok := range content {
		// Mirror of indexKeys: the posting side is folded, so the query side
		// must be too.
		out = append(out, "t"+cjkfold.String(tok))
	}
	return out
}

func recordMatchesQuery(r Record, o query.Options) bool {
	return recordMatchesQueryVariants(r, o, nil)
}

func recordMatchesQueryVariants(r Record, o query.Options, variants map[string][]string) bool {
	if o.Regex {
		return true
	}
	terms, phrases := query.QueryParts(o.Query)
	if len(terms) == 0 && len(phrases) == 0 {
		return true
	}
	if query.MatchesParts(r.Text, terms, phrases, variants) {
		return true
	}
	// The postings that pointed here folded NFD to NFC (tokens); this surface
	// check runs on the raw bytes, where the same accented word in the other
	// normalization would not substring-match. Retry both composed, mirroring
	// the CJK retry below (#1098).
	if nfcfold.Compose(r.Text) != r.Text || nfcfold.Compose(o.Query) != o.Query {
		nterms, nphrases := query.QueryParts(nfcfold.Compose(o.Query))
		if query.MatchesParts(nfcfold.Compose(r.Text), nterms, nphrases, variants) {
			return true
		}
	}
	// Postings are keyed on Traditional-folded bigrams, so a Simplified query
	// can legitimately reach a Traditional record (and the reverse). This
	// verification step compares surface text, which would then reject the
	// very record the postings pointed at — retry it on both sides folded.
	folded := cjkfold.String(r.Text)
	if folded == r.Text {
		if q := cjkfold.String(o.Query); q == o.Query {
			return false
		}
	}
	fterms, fphrases := query.QueryParts(cjkfold.String(o.Query))
	return query.MatchesParts(folded, fterms, fphrases, variants)
}

// bucket shards a token by its opening characters. It used to slice two
// *bytes*, which for any non-ASCII token is a prefix plus half a UTF-8
// sequence: safe() mapped the broken half to "_", so every Russian, Chinese
// and Greek token in the corpus landed in the single bucket "t_". Each lookup
// then had to scan the whole non-ASCII vocabulary. Sharding by runes keeps
// ASCII bucket names exactly as they were and spreads the rest over 256.
// isShardASCII reports whether a rune is one byte wide, so slicing it cannot
// split a multi-byte sequence.
func isShardASCII(r rune) bool { return r < 128 }

func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
}

// stemMatchForms generates the same candidate surface forms stemMatches
// derives, without requiring the token catalog: absent forms simply read
// empty buckets.
func stemMatchForms(term string) []string {
	runes := []rune(term)
	// Cyrillic terms took the ASCII path, which appends English suffixes to
	// Russian words — "миграции" became "миграцииed" and read nothing but
	// empty buckets, so the relevance-tier fold was a no-op in both
	// directions. Route them through the Russian ending table instead.
	if isCyrToken(term) {
		return cyrMatchForms(term)
	}
	if len(runes) < 5 {
		var out []string
		for _, form := range []string{term + "s", term + "es", strings.TrimSuffix(term, "s")} {
			if len(form) >= 3 && form != term {
				out = append(out, form)
			}
		}
		return out
	}
	var out []string
	for _, form := range suffixForms(term) {
		if form != term {
			out = append(out, form)
		}
	}
	return out
}

// isCyrToken reports whether the token is written in Cyrillic.
func isCyrToken(term string) bool {
	for _, r := range term {
		if r >= 0x400 && r <= 0x4FF {
			return true
		}
		if r < 128 {
			return false
		}
	}
	return false
}

// askedMinSpan is how far apart two askings must be to mean anything.
const askedMinSpan = 48 * time.Hour

// AskedTwice is one question this store has been asked in more than one
// session, with the sessions that asked it, newest first. Empty when nothing
// repeats — which is the honest answer for a store a few days old.
type AskedTwice struct {
	Text     string
	Sessions []SessionMeta
}

// FindAskedTwice picks the question worth showing: the one asked in the most
// sessions, and among equals the one spanning the longest stretch of time. A
// thing asked twice in one afternoon is a person working; the same thing asked
// in March and again in July is the thing this tool exists for.
//
// The search runs entirely over the manifest. Only the matching sessions are
// read back, and only to recover the text the hashes stand for.
// allow is the trust gate: it reports whether a session's project may reach the
// caller's activation. Imported sessions now carry asked hashes, so an
// asked-twice repeat can be all imported — without this gate the brief would
// print it on a machine whose auto rule withholds imported memory. A nil allow
// counts every session (the manifest-level view the tests read).
func FindAskedTwice(dir string, allow func(project string) bool) (AskedTwice, bool) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return AskedTwice{}, false
	}
	byHash := map[uint64][]SessionMeta{}
	for _, meta := range m.Sessions {
		if allow != nil && !allow(meta.Project) {
			continue
		}
		for _, h := range meta.Asked {
			byHash[h] = append(byHash[h], meta)
		}
	}
	var best []SessionMeta
	var bestSpan time.Duration
	var bestHash uint64
	for h, metas := range byHash {
		if len(metas) < 2 {
			continue
		}
		sort.Slice(metas, func(i, j int) bool { return newestFirstMeta(metas[i], metas[j]) })
		span := metas[0].Updated.Sub(metas[len(metas)-1].Updated)
		// Time between the askings, not the number of them. The same question
		// sixteen times in one afternoon is somebody retrying; the same question
		// in March and again in July is the thing worth showing. Below a couple
		// of days it is the former.
		if span < askedMinSpan {
			continue
		}
		if span > bestSpan || (span == bestSpan && len(metas) > len(best)) {
			best, bestSpan, bestHash = metas, span, h
		}
	}
	if len(best) < 2 {
		return AskedTwice{}, false
	}
	text := askedTextFor(dir, m, best, bestHash)
	if text == "" {
		return AskedTwice{}, false
	}
	return AskedTwice{Text: text, Sessions: best}, true
}

// askedTextFor recovers what a hash stood for by reading back the sessions that
// carry it, stopping at the first one that yields the question.
func askedTextFor(dir string, m Manifest, metas []SessionMeta, want uint64) string {
	for _, meta := range metas {
		s, ok, err := loadSessionMeta(dir, m, meta)
		if err != nil || !ok {
			continue
		}
		for _, msg := range s.Messages {
			if msg.Role != "user" {
				continue
			}
			stem := questionStem(msg.Text)
			if stem == "" {
				continue
			}
			h := fnv.New64a()
			_, _ = h.Write([]byte(stem))
			if h.Sum64() == want {
				return strings.TrimSpace(msg.Text)
			}
		}
	}
	return ""
}

// consonantY splits a word ending in consonant+y, the form whose plural is
// -ies rather than -s. "key" and "day" end in vowel+y and keep their -s.
func consonantY(word string) (string, bool) {
	if !strings.HasSuffix(word, "y") || len(word) < 3 {
		return "", false
	}
	switch word[len(word)-2] {
	case 'a', 'e', 'i', 'o', 'u':
		return "", false
	}
	return strings.TrimSuffix(word, "y"), true
}
