package index

import (
	"cmp"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

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
// earlier in the same agent session, and In real-world stores, that is 15 of
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
	var r projectRelevantRead
	err := withLockedProjectRelevantDir(dir, func(d string) error {
		var rerr error
		r, rerr = projectRelevantIn(d, projects, terms, n, skip, cut)
		return rerr
	})
	return r.out, r.matched, r.strong, r.naming, r.idf, err
}

// ProjectRelevantMetas ranks project sessions for terms from the manifest and
// posting buckets without hydrating session transcripts from records.bin.
func ProjectRelevantMetas(dir string, projects, terms []string, n int, skip map[string]bool) ([]SessionMeta, []int, []int, error) {
	var r projectRelevantMetasRead
	err := withLockedProjectRelevantDir(dir, func(d string) error {
		var rerr error
		r, rerr = projectRelevantMetasIn(d, projects, terms, n, skip)
		return rerr
	})
	return r.metas, r.matched, r.strong, err
}

// withLockedProjectRelevantDir runs fn under the non-blocking read lock and
// a single generation pinned to dir.
func withLockedProjectRelevantDir(dir string, fn func(string) error) error {
	if dir == "" {
		dir = DefaultDir()
	}
	// Non-blocking, for the same reason searchDetailedOnce is: this runs on
	// the session-start hook, and waiting here stalls the agent for the whole
	// rebuild — which every user hits on an index-format upgrade.
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return err
	}
	if ok {
		defer unlock()
	}
	// From one generation, as a search is. A pass that swapped in between a
	// bucket and its overlays left the ranking the old bucket, where the
	// session's postings were the dead ones, and no overlay with its live
	// ones: the session the question named fell out (#5459).
	_, err = inOneGeneration(dir, func() (struct{}, error) {
		return struct{}{}, fn(dir)
	})
	return err
}

// projectRelevantRead is what ProjectRelevantNamingCut hands back.
type projectRelevantRead struct {
	out                     []model.Session
	matched, strong, naming []int
	idf                     map[string]float64
}

// projectRelevantMetasRead is what ProjectRelevantMetas hands back.
type projectRelevantMetasRead struct {
	metas           []SessionMeta
	matched, strong []int
}

// rankProjectRelevant ranks project sessions from manifest and posting buckets.
func rankProjectRelevant(dir string, projects, terms []string, n int, skip map[string]bool) (
	metas []SessionMeta, matched, strong, naming []int, idf map[string]float64, offsets map[uint32][]int64, err error,
) {
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
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
		return nil, nil, nil, nil, nil, nil, rerr
	}
	metas, matched, strong, naming, idf, offsets = rank.metas, rank.informative, rank.strong, rank.naming, rank.idf, rank.offsets
	if bm, bmatched, bstrong, _, bidf, boffsets, ok := bridgedRetry(dir, m, projects, terms, n, skip, strong, idf); ok {
		metas, matched, strong, naming, idf, offsets = bm, bmatched, bstrong, nil, bidf, boffsets
	}
	return metas, matched, strong, naming, idf, offsets, nil
}

// projectRelevantMetasIn is the read ProjectRelevantMetas makes, without the
// lock.
func projectRelevantMetasIn(dir string, projects, terms []string, n int, skip map[string]bool) (projectRelevantMetasRead, error) {
	metas, matched, strong, _, _, _, err := rankProjectRelevant(dir, projects, terms, n, skip)
	if err != nil || len(metas) == 0 {
		return projectRelevantMetasRead{}, err
	}
	pol, excluded := metaIgnoredOrExcludedLoad()
	keptMetas := make([]SessionMeta, 0, len(metas))
	keptMatched := make([]int, 0, len(matched))
	keptStrong := make([]int, 0, len(strong))
	for i, meta := range metas {
		if metaIgnoredOrExcluded(pol, excluded, meta) {
			continue
		}
		keptMetas = append(keptMetas, meta)
		if i < len(matched) {
			keptMatched = append(keptMatched, matched[i])
		}
		if i < len(strong) {
			keptStrong = append(keptStrong, strong[i])
		}
	}
	return projectRelevantMetasRead{keptMetas, keptMatched, keptStrong}, nil
}

// projectRelevantIn is the read ProjectRelevantNamingCut makes, without the
// lock.
func projectRelevantIn(dir string, projects, terms []string, n int, skip map[string]bool, cut map[string]time.Time) (projectRelevantRead, error) {
	metas, matched, strong, naming, idf, offsets, err := rankProjectRelevant(dir, projects, terms, n, skip)
	if err != nil || len(metas) == 0 {
		return projectRelevantRead{}, err
	}
	out, err := sessionsServableWindowed(dir, metas, offsets, query.Options{ExcludeUpTo: cut})
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
		at[meta.Key()] = i
	}
	m := make([]int, len(out))
	st := make([]int, len(out))
	for j, s := range out {
		i, ok := at[s.Key()]
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
	idf        map[string]float64
	sessionIDF map[string]float64
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
// node-autoscaler, webhook-event-forwarder. Recall in one project then ranks
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
	return query.LowerProjectInScope(p, w)
}

func endsWithSegment(p, w string) bool {
	return query.EndsWithSegment(p, w)
}

// ProjectEndsWithSegment reports whether project's path ends with the given
// segment (case-insensitive), e.g. "goprojects/netgateway" ends with "netgateway".
func ProjectEndsWithSegment(project, segment string) bool {
	if project == "" || segment == "" {
		return false
	}
	return endsWithSegment(strings.ToLower(project), strings.ToLower(segment))
}

// ProjectsMatchingTerms returns the distinct recorded project names in dir
// whose path ends with any term in terms (length >= 4). Used when an unscoped
// prompt names a repository by its basename ("edgeproxy", "netgateway").
func ProjectsMatchingTerms(dir string, terms []string) []string {
	if len(terms) == 0 {
		return nil
	}
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	var lwants []string
	for _, t := range terms {
		if len(t) >= 4 {
			lwants = append(lwants, strings.ToLower(t))
		}
	}
	if len(lwants) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, meta := range viewsFor(m).allMeta() {
		if meta.Project == "" || seen[meta.Project] {
			continue
		}
		lp := strings.ToLower(meta.Project)
		for _, w := range lwants {
			if endsWithSegment(lp, w) {
				seen[meta.Project] = true
				out = append(out, meta.Project)
				break
			}
		}
	}
	return out
}

// ProjectInScopeStrict is ProjectInScope without the allowance synced work
// gets. The loose rule is right for recall — a peer's parent path is not this
// machine's, so `imported:goprojects/svc` has to answer to a local `svc` — and
// wrong for a command that packages one session's content for another agent:
// `deja handoff`, run from a directory named api, picked a teammate's
// `clients/acme/api` because it was the newest thing ending in /api (#2347).
func ProjectInScopeStrict(project, want string) bool {
	return query.ProjectInScopeStrict(project, want)
}

// ProjectInScope reports whether a session's project is the one a caller is
// standing in. Exported so the automatic surfaces share one rule: the same
// question answered three different ways is how a client's project reached a
// session start (#2333), a handoff (#2336) and a tool-time line (#2339).
func ProjectInScope(project, want string) bool { return query.ProjectInScope(project, want) }

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
	return relevantMetasCountsForQuery(dir, m, projects, "", terms, n, false, keep)
}

func relevantMetasCountsForQuery(dir string, m Manifest, projects []string, rawQuery string, terms []string, n int, allowRescue bool, keep func(SessionMeta) bool) (relevanceRanking, error) {
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
	long := isLongQuery(terms)
	if rawQuery != "" && (long || strings.Count(rawQuery, "\n") >= 2) {
		a.docWeights, a.titleTerms = docQueryTermWeights(rawQuery, terms)
	}
	for _, term := range terms {
		a.addTerm(term)
	}
	rescue := allowRescue && (len(terms) <= 14 || len(a.credited) == 0)
	a.addCompoundParts(queryCompoundParts(rawQuery, terms), rescue)
	a.addAssociativeTerms(dir, terms)
	a.bestMessages()
	// A query this long is a pasted document — an issue, a log, a spec — and
	// ranking it is finding the documents most like it, so a session's score
	// pays for its length the way BM25 does and is summed over the session
	// rather than read off its best message.
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
		if xm.Harness != ym.Harness {
			return strings.Compare(xm.Harness, ym.Harness)
		}
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
		sessionIDF:  a.sessionIDF,
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
		rank[meta.Key()] = i
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
		return rank[out[i].Key()] < rank[out[j].Key()]
	})
	return ignoredByPolicy(out), nil
}
