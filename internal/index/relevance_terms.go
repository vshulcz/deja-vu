package index

import (
	"math"

	"github.com/vshulcz/deja-vu/internal/search"
)

// relevanceAcc is what relevantMetasCounts collects term by term before it
// ranks the sessions.
type relevanceAcc struct {
	m         Manifest
	inProject map[uint32]SessionMeta
	br        *bucketReader
	// A real bucket read error (a corrupt or unreadable postings file) must not
	// pass as "the term is absent": that silently drops the term from the
	// ranking. Remember the first one and hand it back so the caller triggers
	// the same self-heal the exact tier already does.
	readErr   error
	totalDocs float64
	idfOf     map[string]float64
	score     map[uint32]float64
	// focus is the same scoring restricted to the words that distinguish. A
	// question asked in plain speech carries filler that is a content word to
	// the index — "how many bikes do I own" ranks on many, bikes and own — and
	// the session that answers it often holds only the rare one. Scored on the
	// whole query it loses to sessions carrying more of the filler; scored on
	// the rare part alone it wins. Neither view is right on its own, so both are
	// kept and the better place of the two is used, at a price.
	focus        map[uint32]float64
	matchedTerms map[uint32]int
	// Coverage counted over the terms that identify something on their own,
	// kept alongside so the choice between the two can be made once the whole
	// query has been read rather than term by term.
	identifyingTerms   int
	matchedIdentifying map[uint32]int
	// The rarest term this session matched, against the rarest the question
	// holds. A short question skips the store check entirely — "a short
	// question is all subject" — so `decide saltmarsh` asked where saltmarsh
	// has never been said fired on `decide`, and nothing downstream could tell
	// the subject from the verb beside it (#3351).
	bestMatched map[uint32]float64
	queryBest   float64
	strongTerms map[uint32]int
	anyTerms    map[uint32]int
	// perMessage tracks how many distinct terms hit each message (record
	// offset) of a session: co-occurrence inside one message is a far
	// stronger topical signal than terms scattered across a long session.
	perMessage map[uint32]map[int64]int
	// msgIDF accumulates the idf mass of distinct terms per message, so a
	// session can be ranked by its best single message rather than by the
	// total it collects across thousands of them.
	msgIDF map[uint32]map[int64]float64
	// toolOff marks the matched records that are tool records rather than
	// speech, so their credit can be weighed at toolMatchWeight below.
	toolOff map[int64]bool
	// Counted over the whole store, not the sessions being ranked: the guard
	// that reads it is telling a typo from a real word, and a word another
	// project uses is a real word. Counted over the project, a question with
	// one word this project shares and two it never said read as one known
	// word among typos, and the project-scoped recall answered nothing (#4789).
	termsKnown int
}

func newRelevanceAcc(m Manifest, inProject map[uint32]SessionMeta, br *bucketReader) *relevanceAcc {
	return &relevanceAcc{
		m:                  m,
		inProject:          inProject,
		br:                 br,
		totalDocs:          float64(countedDocs(m)) + 1,
		idfOf:              map[string]float64{},
		score:              map[uint32]float64{},
		focus:              map[uint32]float64{},
		matchedTerms:       map[uint32]int{},
		matchedIdentifying: map[uint32]int{},
		bestMatched:        map[uint32]float64{},
		strongTerms:        map[uint32]int{},
		anyTerms:           map[uint32]int{},
		perMessage:         map[uint32]map[int64]int{},
		msgIDF:             map[uint32]map[int64]float64{},
		toolOff:            map[int64]bool{},
	}
}

// termHits is where one query term matched among the sessions being ranked.
type termHits struct {
	hit map[uint32]bool
	tf  map[uint32]int
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
	offs    map[uint32]map[int64]bool
}

// noteQuerySubject raises that mark for a word that names something. df is
// counted over the whole store, before the project scope drops the term, so
// a subject that lives somewhere else still counts as the subject — which
// is the case this exists for.
func (a *relevanceAcc) noteQuerySubject(term string, sessions int) {
	// A word this store has never seen says nothing about what the
	// question is about here: counting it set a mark no session could
	// reach, and the recall went silent whenever a question carried one
	// unseen word — which is most questions.
	if sessions <= 0 || !search.HasIdentifierTerm([]string{term}) {
		return
	}
	if r := rankIDF(len(a.m.Sessions), sessions); r > a.queryBest {
		a.queryBest = r
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
				if _, ok := a.inProject[pp.Sid]; ok {
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

// notePostings counts posts into df over the whole store, and into hit, tf
// and offs for the sessions being ranked.
func (a *relevanceAcc) notePostings(posts []posting, df map[uint32]map[int64]bool, hit map[uint32]bool, tf map[uint32]int, offs map[uint32]map[int64]bool) {
	for _, pp := range posts {
		noteDF(df, pp.Sid, pp.Off)
		if _, ok := a.inProject[pp.Sid]; ok {
			if pp.Tool {
				a.toolOff[pp.Off] = true
			}
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

// unionForms matches a term on any of its stem forms.
func (a *relevanceAcc) unionForms(term string, orKeys []string) (termHits, bool) {
	// OR path: union postings across the term's stem forms.
	df := map[uint32]map[int64]bool{}
	th := termHits{
		hit:     map[uint32]bool{},
		tf:      map[uint32]int{},
		offs:    map[uint32]map[int64]bool{},
		minDF:   -1,
		minSess: -1,
	}
	for _, key := range orKeys {
		posts, err := a.postings(key)
		if err != nil || len(posts) == 0 {
			continue
		}
		a.notePostings(posts, df, th.hit, th.tf, th.offs)
	}
	if len(df) > 0 {
		a.termsKnown++
	}
	if len(th.hit) == 0 {
		a.noteQuerySubject(term, len(df))
		return th, false
	}
	th.minDF, th.minSess = countDF(df), len(df)
	return th, true
}

// everyKey matches a term only where every one of its sub-tokens is present.
func (a *relevanceAcc) everyKey(term string, keys []string) (termHits, bool) {
	th := termHits{minDF: -1, minSess: -1}
	missed := false
	// Every sub-token of the term is somewhere in the store.
	inStore := true
	for _, key := range keys {
		posts, err := a.postings(key)
		if err != nil || len(posts) == 0 {
			missed, inStore = true, false
			break
		}
		// Document frequency in sessions, not postings: one marathon
		// session repeating a term 300 times must not make it common.
		df := map[uint32]map[int64]bool{}
		keyHit := map[uint32]bool{}
		keyTF := map[uint32]int{}
		keyOffs := map[uint32]map[int64]bool{}
		a.notePostings(posts, df, keyHit, keyTF, keyOffs)
		if th.hit == nil {
			th.hit, th.tf = keyHit, keyTF
		} else {
			for ord := range th.hit {
				if !keyHit[ord] {
					delete(th.hit, ord)
					delete(th.tf, ord)
				} else if keyTF[ord] < th.tf[ord] {
					th.tf[ord] = keyTF[ord]
				}
			}
		}
		// Message credit follows the rarest sub-token — the one whose df
		// sets the term's idf. A union would let a message containing
		// only a common sub-token ("index" of "pkg/index") collect the
		// full term mass, and best-message ranking amplifies that.
		if th.minDF == -1 || countDF(df) < th.minDF {
			th.minDF, th.minSess = countDF(df), len(df)
			th.offs = keyOffs
		}
		if len(th.hit) == 0 {
			a.noteQuerySubject(term, len(df))
			missed = true
			break
		}
	}
	if inStore {
		a.termsKnown++
	}
	if missed || len(th.hit) == 0 {
		return th, false
	}
	return th, true
}

// scoreTerm weighs a matched term and credits it to every session it hit.
func (a *relevanceAcc) scoreTerm(term string, th termHits) {
	hit, tf, offs, minDF, minSess := th.hit, th.tf, th.offs, th.minDF, th.minSess
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
	a.noteQuerySubject(term, minSess)
	for ord := range hit {
		mm := a.perMessage[ord]
		if mm == nil {
			mm = map[int64]int{}
			a.perMessage[ord] = mm
		}
		mi := a.msgIDF[ord]
		if mi == nil {
			mi = map[int64]float64{}
			a.msgIDF[ord] = mi
		}
		for off := range offs[ord] {
			mm[off]++
			mi[off] += rank
		}
	}
	for ord := range hit {
		// Saturated term frequency: repeated mentions add confidence
		// with quickly diminishing returns, so a marathon session cannot
		// bury a focused one through sheer repetition.
		weighted := rank * (1 + 0.25*math.Log2(float64(tf[ord])))
		// A term the session only ever held in tool records was not said
		// by anyone there; it sat in a file it read or a command's output.
		spoken := false
		for off := range offs[ord] {
			if !a.toolOff[off] {
				spoken = true
				break
			}
		}
		if !spoken {
			weighted *= toolMatchWeight
		}
		a.score[ord] += weighted
		if informative {
			a.focus[ord] += weighted
		}
		a.anyTerms[ord]++
		if informative {
			a.matchedTerms[ord]++
		}
		if identifying {
			a.matchedIdentifying[ord]++
		}
		if rank > a.bestMatched[ord] {
			a.bestMatched[ord] = rank
		}
		if strong {
			a.strongTerms[ord]++
		}
	}
}

// scoreSession turns one session's collected credit into its final score.
func (a *relevanceAcc) scoreSession(ord uint32, sc float64, long bool, sumWords, nWords float64) relevanceScored {
	// Same-message co-occurrence bonus: the best single message covering
	// k distinct query terms scales the session's score. A session where
	// one message answers the whole question outranks one that merely
	// mentions every word somewhere.
	//
	// A tool record co-occurs at toolMatchWeight: a 50 KB file read or
	// JSON dump holds most words of any question, so one such record
	// made a session that only ever read files look like it answered
	// (#4780). Speech is weighed exactly as before.
	best := 1.0
	for off, k := range a.perMessage[ord] {
		kk := float64(k)
		if a.toolOff[off] {
			kk = 1 + (kk-1)*toolMatchWeight
		}
		if kk > best {
			best = kk
		}
	}
	// A focused message beats diffuse mentions: the session's score is
	// its best message's idf mass (scaled by same-message co-occurrence),
	// with the session-wide total only as a dampened tail. Without this,
	// one marathon session that brushes every query word somewhere
	// outranks the short session that actually answers.
	var bestMsg float64
	for off, v := range a.msgIDF[ord] {
		if a.toolOff[off] {
			v *= toolMatchWeight
		}
		if v > bestMsg {
			bestMsg = v
		}
	}
	coocc := 1 + 0.2*(best-1)
	if !long {
		sc = bestMsg*coocc + 0.25*(sc-bestMsg)
	}
	// Coverage: distinct informative terms beat repetition.
	if a.matchedTerms[ord] > 1 {
		sc *= 1 + 0.15*float64(a.matchedTerms[ord]-1)
	}
	// How much of the session the match is. A marathon that brushes the
	// question across hundreds of turns and a short session where the
	// matching turns are most of what it holds scored alike, and on ten
	// questions phrased from real PR titles the marathon took the top
	// place twice (#3214). Counted in messages, which is the unit the
	// match is already measured in.
	if counted := a.inProject[ord].Counted; counted > 0 {
		share := float64(len(a.perMessage[ord])) / float64(counted)
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
	if a.queryBest <= 0 || a.bestMatched[ord] >= a.queryBest*subjectShare {
		named = 1
	}
	if long && sumWords > 0 {
		// A session indexed before lengths were counted is taken as
		// average: neither paid nor charged for a length nobody knows.
		w := float64(a.inProject[ord].Words)
		if w == 0 {
			w = sumWords / nWords
		}
		norm := 1 - longQueryLengthB + longQueryLengthB*w/(sumWords/nWords)
		sc /= norm
		a.focus[ord] /= norm
	}
	return relevanceScored{a.inProject[ord], sc, a.matchedTerms[ord], a.anyTerms[ord], a.strongTerms[ord], named, a.focus[ord]}
}
