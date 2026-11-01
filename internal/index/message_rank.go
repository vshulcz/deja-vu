package index

import (
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/query"
)

// doubtRerankGap is how crowded the top of the ranking has to be before the
// per-message reading below is paid for on a store too small for it otherwise.
//
// The ranking's own score says when its first place is in question. Measured on
// LoCoMo's relevance tier, 1,317 questions: where the answer ended at rank 1 the
// median relative distance between the first two scores is 0.523, where it
// ended at rank 2-5 it is 0.114, and a distance under 0.05 covers 165 questions
// of which only 27% were answered right, against 94% for 0.60 or more.
//
// Swept on both public benchmarks (hit@1, and the same runs the records in
// docs/benchmarks hold):
//
//	gap   LongMemEval   LoCoMo
//	off   85.3%         69.7%
//	0.10  85.3%         70.1%
//	0.20  85.7%         70.0%
//	0.30  86.6%         69.9%
//	0.40  86.8%         69.7%
//	0.60  86.6%         69.5%
//	1.00  86.4%         69.0%
//
// Both have an interior optimum, which is what says the gate is selecting and
// not standing in for "fuse everywhere": at 1.00 — the fusion applied to every
// question — LongMemEval is 0.4 points below its best and LoCoMo 1.1 below.
// 0.30 is the middle of the plateau where neither is under its baseline, rather
// than the peak of either.
const doubtRerankGap = 0.30

// rankingIsDoubtful reports whether the ranking's top place is crowded enough
// to be worth a second reading.
func rankingIsDoubtful(rank relevanceRanking) bool {
	if len(rank.scores) < 2 {
		return false
	}
	first, second := rank.scores[0], rank.scores[1]
	if first <= 0 {
		return false
	}
	return (first-second)/first < doubtRerankGap
}

// bestMessageStore is the store size from which the relevance pool is fused
// with a per-message reading. On a haystack of a few hundred sessions the
// session-level overlap already ranks well and the fusion costs it points
// (LoCoMo 70.3 -> 69.7 hit@1 when applied everywhere); on a pile of nineteen
// thousand the answer is one turn in one session among fifty that spread the
// same words thin, and the fusion is worth hit@1 15 -> 18, hit@5 32 -> 35
// (day0bench, -corpus 1000; #3016).
const bestMessageStore = 2000

// bestMessageTurns bounds how many turns of one session are read for the
// fusion: the pool is fifty sessions, a marathon can hold thousands of turns,
// and the fact this is looking for sits in one of them, not in the tail.
const bestMessageTurns = 4000

// rerankByBestMessage fuses the relevance pool's own order with the single
// message in each session that covers the most query weight, on the reading
// that a fact lives in one turn — usually the user's — while a lookalike
// session spreads the same words over many. Reciprocal rank fusion keeps both
// votes; stable on ties, so the pool's order survives where messages cannot
// separate sessions.

// parallelForRanked runs fn over 0..n-1 on one goroutine per core, once there is
// enough work to pay for handing it out.
func parallelForRanked(n int, fn func(i int)) {
	workers := min(rerankWorkers(), n)
	if workers < 2 || n < 4 {
		for i := range n {
			fn(i)
		}
		return
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
}

// dayInsideRange says whether the resolved day falls inside the span the pool
// itself covers. Outside it the hint separates nothing — every candidate is
// equally far away, so the order becomes "newest first", which is not what the
// question asked.
func dayInsideRange(ss []model.Session, day time.Time) bool {
	var oldest, newest time.Time
	for _, s := range ss {
		t := s.Started
		if t.IsZero() {
			t = s.Updated
		}
		if t.IsZero() {
			continue
		}
		if oldest.IsZero() || t.Before(oldest) {
			oldest = t
		}
		if newest.IsZero() || t.After(newest) {
			newest = t
		}
	}
	if oldest.IsZero() {
		return false
	}
	return !day.Before(oldest.AddDate(0, 0, -1)) && !day.After(newest.AddDate(0, 0, 1))
}

// rerankByDayProximity fuses the pool's order with how close each session sits
// to the day the question named. Fusion rather than a sort: the nearest session
// is a hint, and the words are still the evidence.
func rerankByDayProximity(ss []model.Session, day time.Time) []model.Session {
	if len(ss) < 2 || !dayInsideRange(ss, day) {
		return ss
	}
	near := make([]int, len(ss))
	for i := range near {
		near[i] = i
	}
	dist := func(s model.Session) time.Duration {
		t := s.Started
		if t.IsZero() {
			t = s.Updated
		}
		if t.IsZero() {
			return time.Duration(1 << 62)
		}
		d := t.Sub(day)
		if d < 0 {
			d = -d
		}
		return d
	}
	sort.SliceStable(near, func(a, b int) bool { return dist(ss[near[a]]) < dist(ss[near[b]]) })
	nearRank := make([]int, len(ss))
	for r, i := range near {
		nearRank[i] = r
	}
	const k = 5.0
	idx := make([]int, len(ss))
	for i := range idx {
		idx[i] = i
	}
	fused := make([]float64, len(ss))
	for i := range ss {
		fused[i] = 1/(k+float64(i)) + 1/(k+float64(nearRank[i]))
	}
	sort.SliceStable(idx, func(a, b int) bool { return fused[idx[a]] > fused[idx[b]] })
	out := make([]model.Session, len(ss))
	for i, j := range idx {
		out[i] = ss[j]
	}
	return out
}

// rerankWorkers is a variable so a test can force the single-core path and
// compare the two orders.
var rerankWorkers = defaultRerankWorkers

func defaultRerankWorkers() int { return runtime.NumCPU() }

// messageTerms is each term with the forms a message may hold it in, every
// one of those forms, and the term each form stands for.
type messageTerms struct {
	terms    []string
	forms    [][]string
	wanted   map[string]bool
	of       map[string]int
	idf      map[string]float64
	unspaced bool
}

// newMessageTerms takes each term in its word forms, or with exact as asked.
func newMessageTerms(terms []string, idf map[string]float64, exact bool) messageTerms {
	mt := messageTerms{terms: terms, forms: make([][]string, len(terms)), wanted: map[string]bool{}, of: map[string]int{}, idf: idf}
	for i, t := range terms {
		t = query.Lower(t)
		for _, r := range t {
			if cjkfold.Unspaced(r) {
				mt.unspaced = true
				break
			}
		}
		mt.forms[i] = []string{t}
		if ft := cjkfold.String(t); ft != t {
			mt.forms[i] = append(mt.forms[i], ft)
		}
		if !exact {
			mt.forms[i] = append(mt.forms[i], stemMatchForms(t)...)
		}
		for _, f := range mt.forms[i] {
			mt.wanted[f] = true
			if _, ok := mt.of[f]; !ok {
				mt.of[f] = i
			}
		}
	}
	return mt
}

// unspacedTokenHits adds any wanted CJK/Thai bigrams (raw or Traditional-folded)
// present in s to hit.
func unspacedTokenHits(s string, want map[string]bool, hit map[string]bool) map[string]bool {
	if len(want) == 0 {
		return hit
	}
	ascii := true
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return hit
	}
	var pair [2 * utf8.UTFMax]byte
	check := func(r1, r2 rune) {
		n := 0
		if r1 != 0 {
			n += utf8.EncodeRune(pair[n:], r1)
		}
		n += utf8.EncodeRune(pair[n:], r2)
		if want[string(pair[:n])] {
			if hit == nil {
				hit = make(map[string]bool, len(want))
			}
			hit[string(pair[:n])] = true
		}
	}
	var prevRaw, prevFold rune
	runLen := 0
	flush := func() {
		if runLen == 1 {
			check(0, prevRaw)
			if prevFold != prevRaw {
				check(0, prevFold)
			}
		}
		runLen = 0
	}
	for _, r := range s {
		if !cjkfold.Unspaced(r) {
			flush()
			continue
		}
		f := cjkfold.Rune(r)
		if runLen == 0 {
			prevRaw, prevFold = r, f
			runLen = 1
			continue
		}
		check(prevRaw, r)
		if prevFold != prevRaw || f != r {
			check(prevFold, f)
		}
		prevRaw, prevFold = r, f
		runLen = 2
	}
	flush()
	return hit
}

// proximityWeight is what a message gains for holding every term close
// together: up to this much more when they sit side by side, less as the
// words between them spread them apart. On a 3,000-session store where every
// word is common it took a query's target from 10th to 5th, and from the second
// page to the first for one worded as the target said it (#5369).
const proximityWeight = 1.0

// best is the score of s's best message: the idf of each term it holds, half
// again in a user turn, more when the terms sit together, and a tool record
// at toolMatchWeight.
func (mt messageTerms) best(s model.Session) float64 {
	msgs := s.Messages
	if len(msgs) > bestMessageTurns {
		msgs = msgs[:bestMessageTurns]
	}
	best := 0.0
	for _, msg := range msgs {
		toks := tokenHits(msg.Text, mt.wanted)
		if mt.unspaced {
			toks = unspacedTokenHits(msg.Text, mt.wanted, toks)
		}
		score := 0.0
		held := 0
		for ti, t := range mt.terms {
			hit := false
			for _, f := range mt.forms[ti] {
				if toks[f] {
					hit = true
					break
				}
			}
			if hit {
				held++
				w := mt.idf[query.Lower(t)]
				if w <= 0 {
					w = 1
				}
				score += w
				if msg.Role == "user" {
					score += 0.5 * w
				}
			}
		}
		if held > 1 && held == len(mt.terms) {
			if w := mt.window(msg.Text); w > 0 {
				score *= 1 + proximityWeight*float64(held)/float64(w)
			}
		}
		if isToolRole(msg.Role) {
			score *= toolMatchWeight
		}
		if score > best {
			best = score
		}
	}
	return best
}

// window is the fewest words of text that hold a form of every term, or 0
// when one is missing. Words are cut as tokenHits cuts them.
func (mt messageTerms) window(text string) int {
	var at []int // the term each word is a form of, -1 for any other word
	var buf [tokenHitCap]byte
	var pair [2 * utf8.UTFMax]byte
	n := 0
	flush := func() {
		if n >= 2 {
			i, ok := mt.of[string(buf[:n])]
			if !ok {
				i = -1
			}
			at = append(at, i)
		}
		n = 0
	}
	lookupPair := func(r1, r2 rune) int {
		m := 0
		if r1 != 0 {
			m += utf8.EncodeRune(pair[m:], r1)
		}
		m += utf8.EncodeRune(pair[m:], r2)
		if i, ok := mt.of[string(pair[:m])]; ok {
			return i
		}
		f1, f2 := r1, cjkfold.Rune(r2)
		if r1 != 0 {
			f1 = cjkfold.Rune(r1)
		}
		if f1 != r1 || f2 != r2 {
			m = 0
			if f1 != 0 {
				m += utf8.EncodeRune(pair[m:], f1)
			}
			m += utf8.EncodeRune(pair[m:], f2)
			if i, ok := mt.of[string(pair[:m])]; ok {
				return i
			}
		}
		return -1
	}
	var prevUnspaced rune
	unspacedRun := 0
	flushUnspaced := func() {
		if unspacedRun == 1 {
			at = append(at, lookupPair(0, prevUnspaced))
		}
		unspacedRun = 0
	}
	for _, r := range query.Lower(nfcfold.Compose(text)) {
		if mt.unspaced && cjkfold.Unspaced(r) {
			flush()
			if unspacedRun == 0 {
				prevUnspaced = r
				unspacedRun = 1
			} else {
				at = append(at, lookupPair(prevUnspaced, r))
				prevUnspaced = r
				unspacedRun = 2
			}
			continue
		}
		flushUnspaced()
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || (n > 0 && isMark(r)) {
			n += utf8.EncodeRune(buf[n:], r)
			if n > 64 {
				flush()
			}
		} else {
			flush()
		}
	}
	flushUnspaced()
	flush()
	count := make([]int, len(mt.forms))
	have, best, lo := 0, 0, 0
	for hi, t := range at {
		if t < 0 {
			continue
		}
		if count[t] == 0 {
			have++
		}
		count[t]++
		for have == len(mt.forms) {
			if w := hi - lo + 1; best == 0 || w < best {
				best = w
			}
			if l := at[lo]; l >= 0 {
				count[l]--
				if count[l] == 0 {
					have--
				}
			}
			lo++
		}
	}
	return best
}

func rerankByBestMessage(ss []model.Session, terms []string, idf map[string]float64) []model.Session {
	mt := newMessageTerms(terms, idf, false)
	best := make([]float64, len(ss))
	// Per session and independent, and lowercasing every message of every
	// ranked session is where a recall answer spends its time: 1.6 s of 2.4 s
	// on a real store, profiled. Spread across cores; each session's score is
	// computed exactly as before, so the ranking is unchanged.
	parallelForRanked(len(ss), func(i int) { best[i] = mt.best(ss[i]) })
	// Fuse rather than replace: the pool's own order is session-level IDF
	// overlap, which wins on a small haystack; the best-message score wins on
	// a large pile. Reciprocal rank fusion keeps both votes.
	byMsg := make([]int, len(ss))
	for i := range byMsg {
		byMsg[i] = i
	}
	sort.SliceStable(byMsg, func(a, b int) bool { return best[byMsg[a]] > best[byMsg[b]] })
	msgRank := make([]int, len(ss))
	for r, i := range byMsg {
		msgRank[i] = r
	}
	const k = 5.0
	fused := make([]float64, len(ss))
	for i := range ss {
		fused[i] = 1/(k+float64(i)) + 1/(k+float64(msgRank[i]))
	}
	idx := make([]int, len(ss))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return fused[idx[a]] > fused[idx[b]] })
	out := make([]model.Session, len(ss))
	for i, j := range idx {
		out[i] = ss[j]
	}
	return out
}
