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

	"github.com/vshulcz/deja-vu/internal/model"
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
	if !recordsIntact(dir, m) {
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
							return SearchResult{Sessions: merged, Tier: query.TierRelevance}, nil
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
				return result, nil
			}
			if result, ferr := fuzzySearch(dir, m, o); ferr != nil {
				return SearchResult{}, fmt.Errorf("fuzzy postings: %w", ferr)
			} else if result.Fuzzy {
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
			return result, nil
		}
		if result, ferr := fuzzySearch(dir, m, o); ferr != nil {
			return SearchResult{}, fmt.Errorf("fuzzy postings: %w", ferr)
		} else if result.Fuzzy {
			return result, nil
		}
		if result, ferr := cooccurSearch(dir, m, o); ferr != nil {
			return SearchResult{}, fmt.Errorf("cooccur postings: %w", ferr)
		} else if len(result.Sessions) > 0 {
			return result, nil
		}
		return relevanceSearch(dir, m, o)
	}
	return SearchResult{Sessions: ss, Tier: fallbackTier, Variants: fallbackVariants}, err
}

// RelevanceTerms extracts the rankable tokens of a natural-language query:
// lowercased, stopwords dropped. Exported so callers and the benchmark can
// mirror exactly what the relevance tier scores against.
func RelevanceTerms(q string) []string {
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		wordy := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '/' || r >= 0x400
		return !wordy
	})
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		if len(f) < 3 || search.IsStopWord(f) || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

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
	metas, _, anyMatched, termsKnown := relevantMetasCounts(dir, m, nil, terms, 50)
	if len(metas) == 0 {
		return SearchResult{}, nil
	}
	keep := make([]SessionMeta, 0, len(metas))
	var weak []SessionMeta
	for i, meta := range metas {
		if !sessionMetaMatches(meta, o) {
			continue
		}
		if anyMatched[i] >= 2 {
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
		if len(weak) > 50 {
			weak = weak[:50]
		}
		ss, err := sessionsForMetas(dir, weak)
		if err != nil {
			return SearchResult{}, err
		}
		return SearchResult{Sessions: ss, Tier: query.TierRelevance}, nil
	}
	// Single-term sessions ride BEHIND every strong candidate: they widen
	// deep recall without letting a lucky word outrank a real match.
	if len(keep)+len(weak) > 50 {
		weak = weak[:50-len(keep)]
	}
	keep = append(keep, weak...)
	ss, err := sessionsForMetas(dir, keep)
	if err != nil {
		return SearchResult{}, err
	}
	return SearchResult{Sessions: ss, Tier: query.TierRelevance}, nil
}

// ProjectRelevant ranks the current project's sessions by how well they match
// the prompt terms — without reconstructing an AND query, which poisons on
// filler words. Each session scores the IDF-weighted sum of prompt terms it
// contains (rare topical terms dominate; common filler barely moves it), from
// bucket postings only. The best sessions are materialized with transcripts.
// dejaVuIDFFloor is the informativeness bar for a term to count toward a
// déjà vu match: ln(N/df) >= 2 keeps terms present in at most ~13% of
// sessions. Conversational filler ("post", "text", "claude") is frequent in
// any large corpus and clears nothing. Terms living in one or two sessions
// are informative regardless — small corpora never reach the ratio bar.
const dejaVuIDFFloor = 2.0

// ProjectRelevant ranks the project's sessions by IDF-weighted overlap with
// the prompt terms. matched reports, per returned session, how many distinct
// INFORMATIVE terms hit (idf >= dejaVuIDFFloor) — callers gate on it so
// generic words cannot manufacture a confident "you have been here".
func ProjectRelevant(dir string, projects, terms []string, n int) ([]model.Session, []int, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return nil, nil, err
	}
	defer unlock()
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, nil, err
	}
	metas, matched := relevantMetasMatched(dir, m, projects, terms, n)
	if len(metas) == 0 {
		return nil, nil, nil
	}
	out, err := sessionsForMetas(dir, metas)
	if err != nil {
		return nil, nil, err
	}
	return out, matched, nil
}

func relevantMetasMatched(dir string, m Manifest, projects, terms []string, n int) ([]SessionMeta, []int) {
	metas, informative, _, _ := relevantMetasCounts(dir, m, projects, terms, n)
	return metas, informative
}

// relevantMetasCounts additionally reports how many terms of ANY frequency
// each session matched — the noise gate for full-index relevance search,
// where demanding two rare terms also rejects real answers that pair one
// rare word with one ordinary one.
func relevantMetasCounts(dir string, m Manifest, projects, terms []string, n int) ([]SessionMeta, []int, []int, int) {
	inProject := map[uint32]SessionMeta{}
	for _, meta := range m.Sessions {
		if len(projects) == 0 { // empty scope = whole index
			inProject[meta.Ord] = meta
			continue
		}
		lp := strings.ToLower(meta.Project)
		for _, want := range projects {
			w := strings.ToLower(want)
			if w != "" && (lp == w || strings.Contains(lp, w)) {
				inProject[meta.Ord] = meta
				break
			}
		}
	}
	if len(inProject) == 0 {
		return nil, nil, nil, 0
	}
	totalDocs := float64(len(m.Sessions)) + 1
	score := map[uint32]float64{}
	matchedTerms := map[uint32]int{}
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
		if len(orKeys) > 1 {
			// Fold stem forms in ONLY when the exact token is absent from
			// the corpus: "camped" with no postings tries "camping", but an
			// exact hit is never diluted by its variants.
			if exact, err := readBucketToken(filepath.Join(dir, "buckets", bucket(orKeys[0])+".bin"), orKeys[0]); err == nil && len(exact) > 0 {
				orKeys = orKeys[:1]
			}
		}
		if len(orKeys) > 1 {
			// OR path: union postings across the term's stem forms.
			df := map[uint32]bool{}
			hit = map[uint32]bool{}
			tf = map[uint32]int{}
			offs = map[uint32]map[int64]bool{}
			for _, key := range orKeys {
				posts, err := readBucketToken(filepath.Join(dir, "buckets", bucket(key)+".bin"), key)
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
				posts, err := readBucketToken(filepath.Join(dir, "buckets", bucket(key)+".bin"), key)
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
		if idf <= 0 {
			// In a tiny corpus every ratio collapses to zero; a term living
			// in only a couple of sessions still identifies them.
			if minDF > 2 {
				continue
			}
			idf = 0.1
		}
		informative := idf >= dejaVuIDFFloor || minDF <= 2
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
			score[ord] += idf * (1 + 0.25*math.Log2(float64(tf[ord])))
			anyTerms[ord]++
			if informative {
				matchedTerms[ord]++
			}
		}
	}
	type scored struct {
		meta    SessionMeta
		score   float64
		matched int
		any     int
	}
	ranked := make([]scored, 0, len(score))
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
		ranked = append(ranked, scored{inProject[ord], sc, matchedTerms[ord], anyTerms[ord]})
	}
	if len(ranked) == 0 {
		return nil, nil, nil, termsKnown
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
	if n > 0 && len(ranked) > n {
		ranked = ranked[:n]
	}
	metas := make([]SessionMeta, 0, len(ranked))
	matched := make([]int, 0, len(ranked))
	anyMatched := make([]int, 0, len(ranked))
	for _, r := range ranked {
		metas = append(metas, r.meta)
		matched = append(matched, r.matched)
		anyMatched = append(anyMatched, r.any)
	}
	return metas, matched, anyMatched, termsKnown
}

// loadSessionRecords materializes one session's transcript from the index.
func loadSessionRecords(dir string, m Manifest, meta SessionMeta) (model.Session, error) {
	ss, err := scanRecords(dir, m, query.Options{All: true}, nil)
	if err != nil {
		return model.Session{}, err
	}
	for _, s := range ss {
		if s.ID == meta.ID && (meta.Harness == "" || s.Harness == meta.Harness) {
			return s, nil
		}
	}
	return sessionFromMeta(meta), nil
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
	unlock, err := lockDir(dir)
	if err != nil {
		return nil, "", err
	}
	defer unlock()
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, "", fmt.Errorf("manifest: %w", err)
	}
	if !recordsIntact(dir, m) {
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
		if err != nil || len(ss) == 0 {
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

func Recent(dir string, n int) ([]model.Session, error) {
	return RecentMatching(dir, n, query.Options{})
}

func RecentMatching(dir string, n int, o query.Options) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
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
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
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
	unlock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
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
	sort.Slice(metas, func(i, j int) bool { return metas[i].Updated.After(metas[j].Updated) })
	if n > 0 && len(metas) > n {
		metas = metas[:n]
	}
	return sessionsForMetas(dir, metas)
}

// sessionsForMetas loads full sessions for the given metas in ONE pass over
// records.bin. The per-session variant re-scanned the whole log for every
// session, which turned a session-start hook into hundreds of milliseconds.
func sessionsForMetas(dir string, metas []SessionMeta) ([]model.Session, error) {
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
	err := eachRecordForKeys(filepath.Join(dir, "records.bin"), keys, func(r Record) {
		if i, ok := want[r.Key]; ok {
			out[i].Messages = append(out[i].Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RecentProjects is RecentProject for several project names at once: one
// manifest read and one records pass instead of names × sessions scans.
func RecentProjects(dir string, projects []string, perName int) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
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
		sort.Slice(mine, func(i, j int) bool { return mine[i].Updated.After(mine[j].Updated) })
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
	unlock, err := lockDir(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	defer unlock()
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
	if len(matches) == 0 {
		return model.Session{}, false, nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Updated.After(matches[j].Updated) })
	meta := matches[0]
	s := sessionFromMeta(meta)
	recs, err := recordsForKey(filepath.Join(dir, "records.bin"), meta.Harness+":"+meta.ID)
	if err != nil {
		return model.Session{}, false, err
	}
	for _, r := range recs {
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}
	return s, true, nil
}

func scanRecords(dir string, m Manifest, o query.Options, offsets []int64) ([]model.Session, error) {
	return scanRecordsWithVariants(dir, m, o, offsets, nil)
}

func scanRecordsWithVariants(dir string, m Manifest, o query.Options, offsets []int64, variants map[string][]string) ([]model.Session, error) {
	by := map[string]*model.Session{}
	add := func(r Record) {
		meta, ok := m.Sessions[r.Key]
		if !ok {
			return
		}
		if o.Harness != "" && meta.Harness != o.Harness {
			return
		}
		if o.Project != "" && !strings.Contains(strings.ToLower(meta.Project), strings.ToLower(o.Project)) {
			return
		}
		if o.Since > 0 && meta.Updated.Before(time.Now().Add(-o.Since)) {
			return
		}
		if o.Role != "" && r.Role != o.Role {
			return
		}
		s := by[r.Key]
		if s == nil {
			cp := model.Session{ID: meta.ID, Harness: meta.Harness, Project: meta.Project, Path: meta.Path, Title: meta.Title, Started: meta.Started, Updated: meta.Updated}
			s = &cp
			by[r.Key] = s
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}
	if len(offsets) > 0 {
		f, err := os.Open(filepath.Join(dir, "records.bin"))
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		offsets = sortedUniqueOffsets(offsets)
		for _, off := range offsets {
			if r, err := readRecordAt(f, off); err == nil && recordMatchesQueryVariants(r, o, variants) {
				add(r)
			}
		}
	} else {
		if err := eachRecord(filepath.Join(dir, "records.bin"), func(r Record) {
			if recordMatchesQueryVariants(r, o, variants) {
				add(r)
			}
		}); err != nil {
			return nil, err
		}
	}
	out := make([]model.Session, 0, len(by))
	for _, s := range by {
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
	return out
}

func sessionMetaByOrd(m Manifest) map[uint32]SessionMeta {
	out := make(map[uint32]SessionMeta, len(m.Sessions))
	for _, meta := range m.Sessions {
		out[meta.Ord] = meta
	}
	return out
}

func sessionMetaMatches(meta SessionMeta, o query.Options) bool {
	if o.Harness != "" && meta.Harness != o.Harness {
		return false
	}
	if o.Project != "" && !strings.Contains(strings.ToLower(meta.Project), strings.ToLower(o.Project)) {
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
			continue
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
					continue
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

func fuzzyPostings(dir string, terms, phrases []string) ([]posting, map[string][]string, error) {
	if !hasFuzzyToken(terms) {
		return nil, nil, nil
	}
	catalog, err := tokenCatalog(dir)
	if err != nil {
		return nil, nil, err
	}
	perToken := make([]map[int64]posting, len(terms))
	variants := map[string][]string{}
	for i, term := range terms {
		matches := closeTokens(term, catalog)
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
	catalog, err := tokenCatalog(dir)
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
	"ть", "л", "ла", "ло", "ли", "а", "я", "у", "ю", "ы", "и", "е", "о",
}

// cyrSuffixForms bridges Russian inflection: strip the longest known ending,
// then re-attach each — миграция matches миграции and миграцию. ASCII terms
// return nothing.
func cyrSuffixForms(term string) []string {
	runes := []rune(term)
	if len(runes) < 5 {
		return nil
	}
	cyr := false
	for _, r := range runes {
		if r >= 'а' && r <= 'я' || r == 'ё' {
			cyr = true
			break
		}
	}
	if !cyr {
		return nil
	}
	base := term
	for _, end := range cyrEndings {
		if strings.HasSuffix(term, end) && len([]rune(term))-len([]rune(end)) >= 4 {
			base = strings.TrimSuffix(term, end)
			break
		}
	}
	forms := make([]string, 0, len(cyrEndings)+1)
	forms = append(forms, base)
	for _, end := range cyrEndings {
		forms = append(forms, base+end)
	}
	return forms
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

func closeTokens(query string, catalog map[string]bool) []string {
	type match struct {
		token    string
		distance int
	}
	var matches []match
	limit := 1
	if len([]rune(query)) >= 8 {
		limit = 2
	}
	for token := range catalog {
		d := damerauDistance(query, token, limit)
		if d <= limit {
			matches = append(matches, match{token: token, distance: d})
		}
	}
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
	toks := tokens(s)
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
		out = append(out, "t"+tok)
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
	return query.MatchesParts(r.Text, terms, phrases, variants)
}

func bucket(tok string) string {
	if len(tok) >= 2 {
		return safe(tok[:2])
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(tok))
	return fmt.Sprintf("x%02x", h.Sum32()%256)
}

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
	if len([]rune(term)) < 5 {
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
