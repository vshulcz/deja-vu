package search

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// PoolCandidate is a session that matched, as retrieval knows it before
// reading any of its records: the manifest's row, with Held set to how many
// messages the session holds, and how many of its records retrieval would read.
type PoolCandidate struct {
	Session model.Session
	Records int
}

const (
	// poolMin is the fewest sessions a search with a window ranks in full,
	// and poolPerHit how many it ranks per hit the caller reads. The pool is
	// chosen from what the manifest and the postings say, which leaves out
	// what only the text can tell — whether a session decided something,
	// whether its match is a pasted log, where the words meet — and those
	// move a score by up to four times. Measured on a 40k-session store over
	// seven common words, a page drawn from a pool this deep held 14 or 15
	// of the full ranking's first 15, and what it swapped in scored within
	// 1% of the 15th (#5467).
	poolMin    = 1000
	poolPerHit = 20
	// poolSample is how many sessions outside the pool are read to check
	// the length estimate the rest is counted at.
	poolSample = 200
)

// PoolSize is how many matching sessions a search whose caller reads window
// hits ranks in full.
func PoolSize(window int) int { return max(poolMin, poolPerHit*window) }

// Pool picks the n of cands worth reading: the newest under SortDate,
// otherwise the ones with the best score the manifest and the postings alone
// can give, built from the same parts as the
// full ranking — term frequency against length, the title, reuse, notes and
// age. keep is nil when every candidate fits. unread describes the rest, with
// lengths estimated the way runScored measures them.
//
// share is the part of the store's records that hold the query, which is what
// says how often a word recurs inside one record: a record that holds a common
// word holds it several times, and the postings count the record once.
func Pool(cands []PoolCandidate, o Options, n int, share float64) (keep []bool, unread query.Unread) {
	if n <= 0 || len(cands) <= n {
		return nil, query.Unread{}
	}
	score, est := poolScores(cands, o, share)
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := order[a], order[b]
		// A date-ordered answer pages through the newest matches, so those
		// are the ones read; ranked by score, the pool's newest were the
		// newest of its best thousand (#5577).
		if o.Sort == SortDate && !cands[x].Session.Updated.Equal(cands[y].Session.Updated) {
			return cands[x].Session.Updated.After(cands[y].Session.Updated)
		}
		if score[x] != score[y] {
			return score[x] > score[y]
		}
		return cands[x].Session.Updated.After(cands[y].Session.Updated)
	})
	keep = make([]bool, len(cands))
	for _, i := range order[:n] {
		keep[i] = true
	}
	// The sample is spread evenly over the rest by estimated length. Lengths
	// are heavy-tailed and the estimate is off by a different amount for a
	// marathon than for a short session, so a sample drawn without regard to
	// length swung with the few marathons it happened to catch.
	rest := order[n:]
	sort.SliceStable(rest, func(a, b int) bool { return est[rest[a]] < est[rest[b]] })
	step := float64(len(rest)) / float64(min(poolSample, len(rest)))
	next := step / 2
	unread.Sample = map[string]float64{}
	for r, i := range rest {
		if float64(r) >= next {
			next += step
			keep[i] = true
			c := cands[i].Session
			unread.Sample[c.Harness+":"+c.ID] = est[i]
			continue
		}
		unread.Sessions++
		unread.Length += est[i]
	}
	return keep, unread
}

// poolScores is the score each candidate gets from what the manifest and the
// postings say, and its estimated length.
func poolScores(cands []PoolCandidate, o Options, share float64) (score, est []float64) {
	// Words per message where a session does not say: the store's own mean.
	words, held := 0, 0
	for _, c := range cands {
		if c.Session.Words > 0 && c.Session.Held > 0 {
			words += c.Session.Words
			held += c.Session.Held
		}
	}
	perRecord := 1.0
	if held > 0 {
		perRecord = float64(words) / float64(held)
	}
	// Occurrences as a Poisson rate per word, from the share of records
	// holding at least one: a record of w words that holds the word holds it
	// rate·w / (1 - e^(-rate·w)) times on average.
	rate := 0.0
	if share > 0 && perRecord > 0 {
		rate = -math.Log(1-min(share, 0.999)) / perRecord
	}
	est = make([]float64, len(cands))
	total := 0.0
	for i, c := range cands {
		est[i] = poolLength(c, perRecord, rate)
		total += est[i]
	}
	avg := total / float64(len(cands))
	qtoks, _ := QueryParts(o.Query)
	now := time.Now()
	score = make([]float64, len(cands))
	for i, c := range cands {
		tf := float64(c.Records)
		if x := rate * heldWords(c, perRecord, rate); x > 0 {
			tf *= x / -math.Expm1(-x)
		}
		norm := 1 - bm25B
		if avg > 0 {
			norm += bm25B * est[i] / avg
		}
		s := tf * (bm25K1 + 1) / (tf + bm25K1*norm)
		titleHits := 0
		if c.Session.Title != "" {
			low := lower(c.Session.Title)
			for _, tok := range qtoks {
				if strings.Contains(low, tok) {
					titleHits++
				}
			}
		}
		// A title is the person's opening prompt unless the agent opened the
		// session, so a word in it is a word the person said: the role boost
		// counts it once.
		if titleHits > 0 && !c.Session.AgentTitle {
			s += (bm25K1 + 1) / (tf + bm25K1*norm) * (userRoleBoost - 1)
		}
		s *= titleBoost(titleHits, len(qtoks))
		if w := o.RecallWorn[model.LoggedID(c.Session.ID)]; w > 0 {
			s *= wornBoost(w)
		}
		if c.Session.Harness == notesHarness {
			s *= promotedNoteBoost
		}
		// Not the give-up penalty: a session that also decided something is
		// exempt from it, and only the text says whether it did.
		score[i] = s * freshnessDecay(c.Session.Updated, now)
	}
	return score, est
}

// poolLength estimates the BM25 length runScored will measure for a session
// it has not read: the matched records at the session's words per message,
// and the geometric mean with the session's own length where the manifest
// records one.
func poolLength(c PoolCandidate, perRecord, rate float64) float64 {
	matched := float64(c.Records) * heldWords(c, perRecord, rate)
	if c.Session.Words > 0 {
		return math.Sqrt(matched * float64(c.Session.Words))
	}
	return matched
}

// heldWords is how long a record holding the query is expected to be. A long
// record is likelier to hold a word than a short one, so the records that
// match run longer than the session's average: for lengths spread
// exponentially around a mean m, a word at rate r is held by records of mean
// length m(2+rm)/(1+rm) — twice the mean for a rare word, the mean itself for
// one in every record.
func heldWords(c PoolCandidate, perRecord, rate float64) float64 {
	m := recordWords(c, perRecord)
	x := rate * m
	return m * (2 + x) / (1 + x)
}

// recordWords is a session's words per message, or the store's where the
// manifest does not say.
func recordWords(c PoolCandidate, perRecord float64) float64 {
	if c.Session.Words > 0 && c.Session.Held > 0 {
		return float64(c.Session.Words) / float64(c.Session.Held)
	}
	return perRecord
}
