package index

import (
	"math"

	"github.com/vshulcz/deja-vu/internal/search"
)

// relevanceAcc is what relevantMetasCounts collects term by term before it
// ranks the sessions.
//
// Sessions are counted by their place in metas and records in one table, not
// in maps keyed by ordinal and offset: a common word holds postings in most of
// a 19,000-session store, and the maps of maps they went into were the larger
// part of what a recall spent ranking.
type relevanceAcc struct {
	m  Manifest
	br *bucketReader
	// metas are the sessions being ranked, and place is each ordinal's index
	// there, -1 for a session that is not.
	metas []SessionMeta
	place []int32
	// A real bucket read error (a corrupt or unreadable postings file) must not
	// pass as "the term is absent": that silently drops the term from the
	// ranking. Remember the first one and hand it back so the caller triggers
	// the same self-heal the exact tier already does.
	readErr   error
	totalDocs float64
	idfOf     map[string]float64
	// sess is what each session collected, by place, and credited the places
	// that collected anything, in the order they first did.
	sess     []sessionCredit
	credited []int32
	// Coverage counted over the terms that identify something on their own,
	// kept alongside so the choice between the two can be made once the whole
	// query has been read rather than term by term.
	identifyingTerms int
	// The rarest term the question holds; see sessionCredit.bestMatched.
	queryBest       float64
	queryBestUnseen int
	queryBestSeen   int
	// msgs is every record of the sessions being ranked that a term matched or
	// that is a tool record, and msgAt finds one by its offset.
	msgs  []messageCredit
	msgAt map[int64]int32
	// Counted over the whole store, not the sessions being ranked: the guard
	// that reads it is telling a typo from a real word, and a word another
	// project uses is a real word. Counted over the project, a question with
	// one word this project shares and two it never said read as one known
	// word among typos, and the project-scoped recall answered nothing (#4789).
	termsKnown int

	// Scratch for one term at a time, left zeroed between terms.
	dfCount   []int32 // by ordinal: the term's capped message count there
	dfTouched []uint32
	termTF    []int32 // by place: the term's postings there
	keyTF     []int32 // by place: one sub-token's postings there
	inHit     []bool  // by place
	spoken    []bool  // by place
	hits      []int32
	keyHits   []int32
	termOffs  []placeOff
	keyOffs   []placeOff
}

// sessionCredit is what one session collected over the query's terms.
type sessionCredit struct {
	score float64
	// focus is the same scoring restricted to the words that distinguish. A
	// question asked in plain speech carries filler that is a content word to
	// the index — "how many bikes do I own" ranks on many, bikes and own — and
	// the session that answers it often holds only the rare one. Scored on the
	// whole query it loses to sessions carrying more of the filler; scored on
	// the rare part alone it wins. Neither view is right on its own, so both are
	// kept and the better place of the two is used, at a price.
	focus float64
	// The rarest term this session matched, against the rarest the question
	// holds. A short question skips the store check entirely — "a short
	// question is all subject" — so `decide saltmarsh` asked where saltmarsh
	// has never been said fired on `decide`, and nothing downstream could tell
	// the subject from the verb beside it (#3351).
	bestMatched float64
	// The terms it matched: informative ones, identifying ones, any, strong.
	matched, identifying, any, strong int32
	// messages is how many of its records matched a term. bestTerms and
	// bestIDF are, over those records, the most distinct terms one of them
	// holds and the most idf mass one of them collects (scoreSession).
	messages  int32
	bestTerms float64
	bestIDF   float64
	credited  bool
}

// messageCredit is one matched record. Co-occurrence inside one message is a
// far stronger topical signal than terms scattered across a long session, so
// each record counts the distinct terms that hit it and the idf mass they
// carry: a session can then be ranked by its best single message rather than
// by the total it collects across thousands of them.
type messageCredit struct {
	off   int64
	place int32
	terms int32
	idf   float64
	// tool marks a tool record rather than speech, so its credit can be
	// weighed at toolMatchWeight below.
	tool bool
	// matched says a term credited the record; a tool record can be listed
	// before any does.
	matched bool
}

// placeOff is one record of a session being ranked.
type placeOff struct {
	place int32
	off   int64
}

func newRelevanceAcc(m Manifest, metas []SessionMeta, place []int32, br *bucketReader) *relevanceAcc {
	n := len(metas)
	return &relevanceAcc{
		m:         m,
		br:        br,
		metas:     metas,
		place:     place,
		totalDocs: float64(countedDocs(m)) + 1,
		idfOf:     map[string]float64{},
		sess:      make([]sessionCredit, n),
		msgAt:     map[int64]int32{},
		dfCount:   make([]int32, len(place)),
		termTF:    make([]int32, n),
		keyTF:     make([]int32, n),
		inHit:     make([]bool, n),
		spoken:    make([]bool, n),
	}
}

// placeOf is where the session with ordinal sid is among those being ranked,
// -1 when it is not one of them.
func (a *relevanceAcc) placeOf(sid uint32) int32 {
	if int(sid) < len(a.place) {
		return a.place[sid]
	}
	return -1
}

// message is the index in msgs of the record at off, listed on first use.
func (a *relevanceAcc) message(off int64, place int32) int32 {
	if i, ok := a.msgAt[off]; ok {
		return i
	}
	i := int32(len(a.msgs)) //nolint:gosec // a store's records are far below 2^31
	a.msgs = append(a.msgs, messageCredit{off: off, place: place})
	a.msgAt[off] = i
	return i
}

// termHits is where one query term matched among the sessions being ranked:
// the places it hit, each once, with its postings there in termTF.
type termHits struct {
	hits []int32
	// offs is the records that give the term's message credit, in the
	// sessions being ranked. Places outside hits are skipped.
	offs []placeOff
	// minDF is in capped messages, minSess in whole sessions. Neither
	// unit is right alone: sessions call a subject word common because
	// the marathons everything lives in all mention it, and capped
	// messages call a word that saturates one long session filler —
	// which is exactly what the name of the thing you have been working
	// on all month looks like. The rarer of the two verdicts wins, so a
	// term has to read as ordinary under BOTH before it is treated as
	// filler.
	minDF   int
	minSess int
}

// noteQuerySubject raises that mark for a word that names something. df is
// counted over the whole store, before the project scope drops the term, so
// a subject that lives somewhere else still counts as the subject — which
// is the case this exists for.
func (a *relevanceAcc) noteQuerySubject(term string, sessions int, inProject bool) {
	// A word this store has never seen says nothing about what the
	// question is about here: counting it set a mark no session could
	// reach, and the recall went silent whenever a question carried one
	// unseen word — which is most questions.
	if sessions <= 0 || !search.HasIdentifierTerm([]string{term}) {
		return
	}
	r := rankIDF(len(a.m.Sessions), sessions)
	if r >= dejaVuStrongIDFFloor {
		a.idfOf[term] = math.Max(a.idfOf[term], r)
	}
	if r > a.queryBest {
		a.queryBest = r
		if inProject {
			a.queryBestSeen, a.queryBestUnseen = 1, 0
		} else {
			a.queryBestSeen, a.queryBestUnseen = 0, 1
		}
	} else if r == a.queryBest {
		if inProject {
			a.queryBestSeen++
		} else {
			a.queryBestUnseen++
		}
	}
}

// addTerm matches one query term and adds its credit to the sessions holding it.
func (a *relevanceAcc) addTerm(term string) {
	keys := queryKeys(term)
	if len(keys) == 0 {
		return
	}
	orKeys := a.stemKeys(term, keys)
	// A dotted or slashed term ("203.0.113.51", "pkg/index") tokenizes
	// into several index keys. Matching only the first key made an IP
	// degrade to its first octet — a bare small number that lives in
	// half the corpus — so déjà vu fired on unrelated sessions. The
	// term counts only where every sub-token is present; idf comes from
	// the rarest sub-token, which is the one that actually identifies.
	var th termHits
	var ok bool
	if len(orKeys) > 1 {
		th, ok = a.unionForms(term, orKeys)
		// fallthrough to idf/scoring below
	} else {
		th, ok = a.everyKey(term, keys)
	}
	if ok {
		a.scoreTerm(term, th)
	}
	for _, p := range th.hits {
		a.termTF[p] = 0
	}
	a.hits = th.hits[:0]
}

// stemKeys is a single-token term's key with its stem forms as OR-variants,
// cut back to the exact key when the sessions being ranked hold it.
func (a *relevanceAcc) stemKeys(term string, keys []string) []string {
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
		if exact, err := a.br.postings(orKeys[0]); err == nil && len(exact) > 0 {
			for _, pp := range exact {
				if a.placeOf(pp.Sid) >= 0 {
					orKeys = orKeys[:1]
					break
				}
			}
		}
	}
	return orKeys
}

// postings reads one key's postings and keeps the first read error.
func (a *relevanceAcc) postings(key string) ([]posting, error) {
	posts, err := a.br.postings(key)
	if err != nil && a.readErr == nil {
		a.readErr = err
	}
	return posts, err
}

// countDF is the document frequency of posts over the whole store, in the unit
// the corpus size uses (messages, at most dfMessageCap a session) and in
// sessions. Document frequency in sessions, not postings: one
// marathon session repeating a term 300 times must not make it common. posts
// hold each record once.
func (a *relevanceAcc) countDF(posts []posting) (df, sessions int) {
	for _, p := range posts {
		if int(p.Sid) >= len(a.dfCount) {
			a.dfCount = append(a.dfCount, make([]int32, int(p.Sid)+1-len(a.dfCount))...)
		}
		c := a.dfCount[p.Sid]
		if c == 0 {
			sessions++
			a.dfTouched = append(a.dfTouched, p.Sid)
		}
		if c < dfMessageCap {
			a.dfCount[p.Sid] = c + 1
			df++
		}
	}
	for _, sid := range a.dfTouched {
		a.dfCount[sid] = 0
	}
	a.dfTouched = a.dfTouched[:0]
	return df, sessions
}

// noteTool marks a tool posting of a session being ranked.
func (a *relevanceAcc) noteTool(p posting, place int32) {
	if p.Tool {
		a.msgs[a.message(p.Off, place)].tool = true
	}
}

// unionForms matches a term on any of its stem forms.
func (a *relevanceAcc) unionForms(term string, orKeys []string) (termHits, bool) {
	// OR path: union postings across the term's stem forms.
	th := termHits{hits: a.hits[:0], minDF: -1, minSess: -1}
	var lists [][]posting
	for _, key := range orKeys {
		posts, err := a.postings(key)
		if err != nil || len(posts) == 0 {
			continue
		}
		lists = append(lists, posts)
		for _, pp := range posts {
			place := a.placeOf(pp.Sid)
			if place < 0 {
				continue
			}
			a.noteTool(pp, place)
			if a.termTF[place] == 0 {
				th.hits = append(th.hits, place)
			}
			a.termTF[place]++
		}
	}
	// Two forms can share a record; it counts once.
	union := unionPostings(lists)
	df, sessions := a.countDF(union)
	offs := a.termOffs[:0]
	for _, pp := range union {
		if place := a.placeOf(pp.Sid); place >= 0 {
			offs = append(offs, placeOff{place, pp.Off})
		}
	}
	a.termOffs, th.offs = offs, offs
	if sessions > 0 {
		a.termsKnown++
	}
	if len(th.hits) == 0 {
		a.noteQuerySubject(term, sessions, false)
		return th, false
	}
	th.minDF, th.minSess = df, sessions
	return th, true
}

// unionPostings is the postings of every list, each sorted by offset, in one
// list sorted by offset with each record once.
func unionPostings(lists [][]posting) []posting {
	switch len(lists) {
	case 0:
		return nil
	case 1:
		return lists[0]
	}
	out := lists[0]
	for _, l := range lists[1:] {
		merged := make([]posting, 0, len(out)+len(l))
		i, j := 0, 0
		for i < len(out) && j < len(l) {
			switch {
			case out[i].Off < l[j].Off:
				merged = append(merged, out[i])
				i++
			case out[i].Off > l[j].Off:
				merged = append(merged, l[j])
				j++
			default:
				merged = append(merged, out[i])
				i++
				j++
			}
		}
		merged = append(merged, out[i:]...)
		merged = append(merged, l[j:]...)
		out = merged
	}
	return out
}

// everyKey matches a term only where every one of its sub-tokens is present.
func (a *relevanceAcc) everyKey(term string, keys []string) (termHits, bool) {
	th := termHits{hits: a.hits[:0], minDF: -1, minSess: -1}
	missed := false
	// Every sub-token of the term is somewhere in the store.
	inStore := true
	first := true
	for _, key := range keys {
		posts, err := a.postings(key)
		if err != nil || len(posts) == 0 {
			missed, inStore = true, false
			break
		}
		df, sessions := a.countDF(posts)
		keyHits, keyOffs := a.keyHits[:0], a.keyOffs[:0]
		for _, pp := range posts {
			place := a.placeOf(pp.Sid)
			if place < 0 {
				continue
			}
			a.noteTool(pp, place)
			if a.keyTF[place] == 0 {
				keyHits = append(keyHits, place)
			}
			a.keyTF[place]++
			keyOffs = append(keyOffs, placeOff{place, pp.Off})
		}
		if first {
			first = false
			for _, p := range keyHits {
				a.termTF[p] = a.keyTF[p]
			}
			th.hits = append(th.hits, keyHits...)
		} else {
			kept := th.hits[:0]
			for _, p := range th.hits {
				switch {
				case a.keyTF[p] == 0:
					a.termTF[p] = 0
					continue
				case a.keyTF[p] < a.termTF[p]:
					a.termTF[p] = a.keyTF[p]
				}
				kept = append(kept, p)
			}
			th.hits = kept
		}
		for _, p := range keyHits {
			a.keyTF[p] = 0
		}
		a.keyHits = keyHits[:0]
		// Message credit follows the rarest sub-token — the one whose df
		// sets the term's idf. A union would let a message containing
		// only a common sub-token ("index" of "pkg/index") collect the
		// full term mass, and best-message ranking amplifies that.
		if th.minDF == -1 || df < th.minDF {
			th.minDF, th.minSess = df, sessions
			a.termOffs, a.keyOffs = keyOffs, a.termOffs[:0]
			th.offs = a.termOffs
		} else {
			a.keyOffs = keyOffs[:0]
		}
		if len(th.hits) == 0 {
			a.noteQuerySubject(term, sessions, false)
			missed = true
			break
		}
	}
	if inStore {
		a.termsKnown++
	}
	if missed || len(th.hits) == 0 {
		return th, false
	}
	return th, true
}

// scoreTerm weighs a matched term and credits it to every session it hit.
func (a *relevanceAcc) scoreTerm(term string, th termHits) {
	minDF, minSess := th.minDF, th.minSess
	// Two verdicts on how rare the term is, and they answer two different
	// questions. Whether the term is worth speaking up about is the rarer
	// of the two: sessions alone call a subject word common because the
	// marathons that hold most of a store all mention it, and capped
	// messages alone call the topic of one long session filler.
	//
	// What a term is WORTH once it matched is a different question, and
	// the capped-message view is the wrong answer to it: it lifts every
	// term that a few long sessions happen to concentrate, which reorders
	// the top of the ranking. Measured on LongMemEval-S, weighting by the
	// rarer verdict cost 1.7 points of hit@1 — preference questions fell
	// from 36.7% to 26.7% — while the gate it was introduced for kept its
	// gain. So the score keeps counting documents in sessions.
	rank := rankIDF(len(a.m.Sessions), minSess)
	idf := gateIDF(a.totalDocs, minDF, rank)
	a.idfOf[term] = idf
	if idf <= 0 {
		// In a tiny corpus every ratio collapses to zero; a term living
		// in only a couple of sessions still identifies them.
		if minSess > 2 {
			return
		}
		idf = 0.1
	}
	if rank <= 0 {
		rank = 0.1
	}
	informative := idf >= dejaVuIDFFloor || minSess <= 2
	// Identifying is the same bar read against the number the score itself
	// uses: rare counted in whole sessions. gateIDF deliberately takes the
	// more generous of its two verdicts so that a subject word is never
	// called filler, which is what a term has to clear to be spoken about
	// at all. Coverage is a different question — it multiplies a session's
	// score by how much of the query it covers — and answering it
	// generously pays a session for the ordinary words a question is
	// phrased with. "Can you suggest a hotel for my trip" is four such
	// words and one that matters.
	identifying := rank >= dejaVuIDFFloor || minSess <= 2
	if identifying {
		a.identifyingTerms++
	}
	// Rare enough to identify something on its own: either well past the
	// ordinary bar, or living in a single session of the whole corpus.
	strong := idf >= dejaVuStrongIDFFloor || minSess <= 1
	// The question's own high-water mark: its rarest word is what it is
	// about, whether or not any session here holds it (#3351).
	a.noteQuerySubject(term, minSess, true)
	for _, p := range th.hits {
		a.inHit[p] = true
	}
	for _, po := range th.offs {
		if !a.inHit[po.place] {
			continue
		}
		msg := &a.msgs[a.message(po.off, po.place)]
		if !msg.matched {
			msg.matched = true
			a.sess[po.place].messages++
		}
		msg.terms++
		msg.idf += rank
		// A term the session only ever held in tool records was not said
		// by anyone there; it sat in a file it read or a command's output.
		if !msg.tool {
			a.spoken[po.place] = true
		}
	}
	for _, p := range th.hits {
		s := &a.sess[p]
		// Saturated term frequency: repeated mentions add confidence
		// with quickly diminishing returns, so a marathon session cannot
		// bury a focused one through sheer repetition.
		weighted := rank * (1 + 0.25*math.Log2(float64(a.termTF[p])))
		if !a.spoken[p] {
			weighted *= toolMatchWeight
		}
		a.inHit[p], a.spoken[p] = false, false
		if !s.credited {
			s.credited = true
			a.credited = append(a.credited, p)
		}
		s.score += weighted
		if informative {
			s.focus += weighted
			s.matched++
		}
		s.any++
		if identifying {
			s.identifying++
		}
		if rank > s.bestMatched {
			s.bestMatched = rank
		}
		if strong {
			s.strong++
		}
	}
}

// bestMessages sets each session's best message, once every term is in.
//
// A tool record co-occurs at toolMatchWeight: a 50 KB file read or JSON dump
// holds most words of any question, so one such record made a session that
// only ever read files look like it answered (#4780). Speech is weighed
// exactly as before.
func (a *relevanceAcc) bestMessages() {
	for i := range a.msgs {
		msg := &a.msgs[i]
		if !msg.matched {
			continue
		}
		s := &a.sess[msg.place]
		kk, v := float64(msg.terms), msg.idf
		if msg.tool {
			kk = 1 + (kk-1)*toolMatchWeight
			v *= toolMatchWeight
		}
		if kk > s.bestTerms {
			s.bestTerms = kk
		}
		if v > s.bestIDF {
			s.bestIDF = v
		}
	}
}

// scoreSession turns one session's collected credit into its final score.
func (a *relevanceAcc) scoreSession(p int32, long bool, sumWords, nWords float64) relevanceScored {
	s := &a.sess[p]
	meta := a.metas[p]
	sc := s.score
	// Same-message co-occurrence bonus: the best single message covering
	// k distinct query terms scales the session's score. A session where
	// one message answers the whole question outranks one that merely
	// mentions every word somewhere.
	best := max(1.0, s.bestTerms)
	// A focused message beats diffuse mentions: the session's score is
	// its best message's idf mass (scaled by same-message co-occurrence),
	// with the session-wide total only as a dampened tail. Without this,
	// one marathon session that brushes every query word somewhere
	// outranks the short session that actually answers.
	bestMsg := s.bestIDF
	coocc := 1 + 0.2*(best-1)
	if !long {
		sc = bestMsg*coocc + 0.25*(sc-bestMsg)
	}
	// Coverage: distinct informative terms beat repetition.
	covered := coverageCount(s.matched, s.identifying, a.identifyingTerms)
	if covered > 1 {
		sc *= 1 + 0.15*float64(covered-1)
	}
	// How much of the session the match is. A marathon that brushes the
	// question across hundreds of turns and a short session where the
	// matching turns are most of what it holds scored alike, and on ten
	// questions phrased from real PR titles the marathon took the top
	// place twice (#3214). Counted in messages, which is the unit the
	// match is already measured in.
	if counted := meta.Counted; counted > 0 {
		share := float64(s.messages) / float64(counted)
		if share > 1 {
			share = 1
		}
		sc *= 1 + marathonShare*share
	}
	// Did this session match what the question is about, rather than the
	// working word beside it. The subject is the question's rarest naming
	// word, read over the whole store so one that lives in another project
	// still counts; a session that matched only commoner words matched
	// around it. Half the mark, not all of it: a rephrasing reaches the
	// subject through a neighbouring word, and demanding the exact one cost
	// the reworded arm nine of twenty (#3351).
	named := 0
	if a.queryBest <= 0 || s.bestMatched >= a.queryBest*subjectShare {
		named = 1
	}
	if a.queryBest >= dejaVuIDFFloor && a.queryBestUnseen > 0 && a.queryBestSeen == 0 && s.matched < 3 {
		named = 0
	}
	if long && sumWords > 0 {
		// A session indexed before lengths were counted is taken as
		// average: neither paid nor charged for a length nobody knows.
		w := float64(meta.Words)
		if w == 0 {
			w = sumWords / nWords
		}
		norm := 1 - longQueryLengthB + longQueryLengthB*w/(sumWords/nWords)
		sc /= norm
		s.focus /= norm
	}
	return relevanceScored{meta, sc, int(covered), int(s.any), int(s.strong), named, s.focus}
}
