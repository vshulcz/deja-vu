package search

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

const (
	cReset  = "\x1b[0m"
	cDim    = "\x1b[2m"
	cBold   = "\x1b[1m"
	cOrange = "\x1b[38;5;208m"
	cGreen  = "\x1b[32m"
	cBlue   = "\x1b[34m"
	cMatch  = "\x1b[48;5;236;38;5;230m"
)

// Options and the tier names live in internal/query so packages below
// search (index) can use them without importing the ranking engine.
type Options = query.Options

const (
	TierExact    = query.TierExact
	TierClose    = query.TierClose
	TierSemantic = query.TierSemantic
	TierStemmed  = "stemmed"
	TierError    = query.TierError
)

func QueryParts(q string) (terms []string, phrases []string) { return query.QueryParts(q) }

func IsStopWord(term string) bool { return query.IsStopWord(term) }

func MatchesQuery(text, q string) bool { return query.MatchesQuery(text, q) }

func MatchesParts(text string, terms, phrases []string, variants map[string][]string) bool {
	return query.MatchesParts(text, terms, phrases, variants)
}

type searchJSONEnvelope struct {
	SchemaVersion int `json:"schema_version"`
	// Match is the set-level tier. "relevance" means nothing matched and these
	// are the nearest sessions — a different kind of answer, and the one a
	// caller counting recall has to exclude. It used to be readable only as a
	// sentence on stderr.
	Tier string `json:"tier"`
	// Total is how many sessions matched before the cap; Capped says whether
	// the cap hid any. Counting the returned hits alone measures the cap.
	Total    int                 `json:"total"`
	Capped   bool                `json:"capped,omitempty"`
	Withheld int                 `json:"policy_withheld,omitempty"`
	Hits     []Hit               `json:"hits"`
	Fuzzy    bool                `json:"fuzzy,omitempty"`
	Stemmed  bool                `json:"stemmed,omitempty"`
	Semantic bool                `json:"semantic,omitempty"`
	Variants map[string][]string `json:"variants,omitempty"`
}

type Hit struct {
	Session    model.Session `json:"session"`
	Count      int           `json:"count"`
	Snippets   []string      `json:"snippets"`
	Score      float64       `json:"score"`
	Tier       string        `json:"tier"`
	TierDetail string        `json:"tier_detail,omitempty"`
	// Superseded holds the date of a newer session in the same project whose
	// matches overlap this one — a signal that this hit is an earlier attempt.
	Superseded string `json:"superseded,omitempty"`
	// Reused counts recent agent recalls that served this session.
	Reused int `json:"reused,omitempty"`
	// Lifecycle carries what was later recorded about this session: that its
	// decision was rejected, superseded or has gone stale. A hit on a raw
	// transcript used to arrive with no trace of that, so a decision someone
	// had explicitly reverted came back reading like current truth.
	// Moved is set by the CLI when some of the files this session touched have
	// commits since it ended. Computing it means forking git, which this
	// package does not do, so it arrives filled in rather than derived here.
	Moved         string `json:"moved,omitempty"`
	Lifecycle     string `json:"lifecycle,omitempty"`
	LifecycleNote string `json:"lifecycle_note,omitempty"`
	LifecycleAt   string `json:"lifecycle_at,omitempty"`
}

const (
	bm25K1        = 1.2
	bm25B         = 0.75
	userRoleBoost = 1.3
)

type bm25Document struct {
	hit       Hit
	termCount []int
	userCount []int
	length    int
	// minWindow is the tightest character span containing every query token
	// inside a single message; 0 means never seen together.
	minWindow int
	// titleHits counts query tokens found in the session title.
	titleHits int
}

// countIn scores one message against the query the way the exact tier does:
// a parts match first, then either a whole-query substring count for a single
// bare token or a per-token count, plus any quoted phrases.
func countIn(text, low string, qtoks, phrases []string, variants map[string][]string) int {
	if !MatchesParts(text, qtoks, phrases, variants) {
		return 0
	}
	if len(qtoks) == 1 && len(phrases) == 0 && variants == nil {
		// The token, not the raw query: the query string still carries whatever
		// punctuation the reader typed, and counting that scored zero for
		// "retry?" — the shape of a pasted question — on a session
		// MatchesParts had already accepted (#1603).
		needle := qtoks[0]
		if strings.Contains(low, needle) {
			return strings.Count(low, needle)
		}
		return 0
	}
	var c int
	if variants != nil {
		c = countAllVariants(low, qtoks, variants)
	} else {
		c = countAllTokens(low, qtoks)
	}
	for _, phrase := range phrases {
		c += strings.Count(low, phrase)
	}
	return c
}

func runScored(ss []model.Session, o Options) ([]Hit, error) {
	var re *regexp.Regexp
	qtoks, phrases := QueryParts(o.Query)
	// Cross-script CJK: prepared once, used only when a raw match scores zero.
	queryCJK := cjkfold.HasCJK(o.Query)
	qtoksFolded, phrasesFolded := QueryParts(cjkfold.String(o.Query))
	if o.Regex {
		var err error
		re, err = regexp.Compile("(?i)" + o.Query)
		if err != nil {
			// The (?i) prefix is deja's, not the user's. Reporting the compile
			// error verbatim echoes `(?i)(` back at someone who typed `(`, so
			// re-compile their pattern alone for a message that names what they
			// actually wrote.
			if _, uerr := regexp.Compile(o.Query); uerr != nil {
				return nil, uerr
			}
			return nil, err
		}
	}
	cut := time.Time{}
	if o.Since > 0 {
		cut = time.Now().Add(-o.Since)
	}
	merged := mergeSessions(ss)
	documents := make([]bm25Document, 0, len(merged))
	// Reused across sessions: each one refills it, so a single allocation serves all.
	type snipCand struct {
		text   string
		weight int
		// window is how tightly the query's words meet in this message; 0 means
		// they never do.
		window int
	}
	snipCands := make([]snipCand, 0, 16)
	df := make([]int, len(qtoks))
	corpusDocuments := 0
	corpusLength := 0
	for _, s := range merged {
		if o.Harness != "" && s.Harness != o.Harness {
			continue
		}
		if !query.ProjectMatches(s.Project, s.From, o.Project) {
			continue
		}
		// By the id prefix a hit prints, and by the id a session was synced
		// under, so "find the session, then search inside it" can use the id
		// from the first step whichever machine it came from (#1321, #1316).
		if o.Session != "" && !strings.HasPrefix(s.ID, o.Session) && !strings.HasPrefix(s.OrigID, o.Session) {
			continue
		}
		if !cut.IsZero() && s.Updated.Before(cut) {
			continue
		}
		// The same name the envelope carries: docs/json-output.md calls them the
		// same idea at two scopes, and an agent reading a hit gets the tier from
		// here (#1616).
		tier := setTier(o)
		doc := bm25Document{hit: Hit{Session: s, Tier: tier}, termCount: make([]int, len(qtoks)), userCount: make([]int, len(qtoks))}
		if s.Title != "" && len(qtoks) > 0 {
			titleLow := strings.ToLower(s.Title)
			for _, tok := range qtoks {
				if strings.Contains(titleLow, tok) {
					doc.titleHits++
				}
			}
		}
		if len(qtoks) == 0 {
			doc.termCount = []int{0}
			doc.userCount = []int{0}
		}
		snipCands = snipCands[:0]
		for _, m := range s.Messages {
			if o.Role != "" && !roleMatches(m.Role, o.Role) {
				continue
			}
			// The transcript's own record of a call to deja is not something
			// anyone said, and a question matches the log of that question
			// being asked (#2067). Removed from matching only; the line stays
			// in the transcript for `deja how` and `deja fix`.
			text := withoutOwnCallLog(m.Text)
			low := strings.ToLower(text)
			c := 0
			// windowText and windowToks are the pair proximity is measured on: the
			// surface text and the query's own words, except where the match came from
			// folding — measuring the unfolded pair there finds nothing and reports a
			// genuine match as words that never meet.
			windowText, windowToks := low, qtoks
			// The pair the scorer counts on, folded below when that is where
			// the match came from.
			scoreLow, scoreToks := low, qtoks
			if re != nil {
				c = countRegex(re, text)
			} else {
				c = countIn(text, low, qtoks, phrases, o.FuzzyVariants)
				// Postings are keyed on Traditional-folded CJK, so a query in
				// one script legitimately reaches a record in the other. This
				// counting pass works on surface text and would score that
				// record zero, dropping it from the results the postings
				// already found — retry with both sides folded.
				if c == 0 && queryCJK {
					foldedLow := cjkfold.String(low)
					c = countIn(cjkfold.String(text), foldedLow, qtoksFolded,
						phrasesFolded, o.FuzzyVariants)
					if c > 0 {
						windowText, windowToks = foldedLow, qtoksFolded
						// And the pair the scorer counts on. Leaving it
						// unfolded gave BM25 a term frequency of zero for a
						// match the postings had already found, so a
						// cross-script hit counted twice and scored nothing —
						// ranked below a record that matched once (#1605).
						// Only when the two token lists line up: termCount is
						// indexed by position, and a fold that merged or split
						// a token would count against the wrong term.
						if len(qtoksFolded) == len(qtoks) {
							scoreLow, scoreToks = foldedLow, qtoksFolded
						}
					}
				}
			}
			if c > 0 {
				doc.hit.Count += c
				if (doc.hit.Tier == TierClose || doc.hit.Tier == TierStemmed) && doc.hit.TierDetail == "" {
					doc.hit.TierDetail = variantDetail(m.Text, qtoks, o.FuzzyVariants)
				}
				// Collect every matching message with its match count; the
				// strongest few become the excerpts after the scan. Taking the
				// first three showed wherever a word happened to appear early
				// rather than the passage that carries the answer.
				w := tokenWindow(windowText, windowToks)
				snipCands = append(snipCands, snipCand{text: text, weight: c, window: w})
				if w > 0 && (doc.minWindow == 0 || w < doc.minWindow) {
					doc.minWindow = w
				}
			}
			// Counted over the pair the match was found on: a cross-script hit
			// is counted above through the fold and, scored against the surface
			// text where the query's words appear nowhere, would take a term
			// frequency of zero and rank below a record that matched once.
			doc.length += countDocumentWords(scoreLow, scoreToks, o.FuzzyVariants, doc.termCount, doc.userCount, m.Role == "user")
			// The token, not the raw query — the same rule as countIn. This
			// path is what scores `retry` inside `retry-backoff`, which
			// countDocumentWords cannot match because it counts whole words
			// and treats the hyphen as one. Comparing the raw query here left
			// a punctuated query with a session that matched three times and
			// scored zero, ranked below one that matched once (#1603).
			if len(scoreToks) == 1 && doc.termCount[0] == 0 && strings.Contains(scoreLow, scoreToks[0]) {
				n := strings.Count(scoreLow, scoreToks[0])
				doc.termCount[0] += n
				if m.Role == "user" {
					doc.userCount[0] += n
				}
			}
		}
		// The passage that answers a question is where its words come together, so
		// a message that says them together outranks one that repeats a single word:
		// a search for "what did my dad give me" led with six "gift"s and put the
		// line naming the dad second (#1325). The relevance tier already picks its
		// excerpt this way; the exact tier ranked on the raw count alone. Count
		// still decides between passages that are equally tight, and among equals
		// the order they were said in stands. Top three shown.
		sort.SliceStable(snipCands, func(i, j int) bool {
			a, b := snipCands[i], snipCands[j]
			if (a.window > 0) != (b.window > 0) {
				return a.window > 0
			}
			if a.window > 0 && a.window != b.window {
				return a.window < b.window
			}
			return a.weight > b.weight
		})
		for i := 0; i < len(snipCands) && i < 3; i++ {
			doc.hit.Snippets = append(doc.hit.Snippets, snippet(snipCands[i].text, o.Query, re))
		}
		// The index hands ranking the records that matched, not the session, so
		// doc.length measures the size of the match. Normalising by that told
		// BM25 that a marathon mentioning a word in three short lines is a
		// three-line document — measured on a real store, one active session
		// took first place on five of ten unrelated queries. When the index
		// counted the session, normalise by the session.
		// Not the session length outright: normalising by it alone punishes a
		// long session that really is about the query, and cost one answer on
		// LongMemEval (84.2% → 84.0% hit@1). The geometric mean of the two
		// lengths keeps that question and still demotes the marathon —
		// LongMemEval unchanged at 84.2% / 0.887 MRR, and a session that
		// mentions the query twice inside a day's work no longer outranks the
		// session about it.
		if doc.hit.Session.Words > 0 {
			doc.length = int(math.Sqrt(float64(doc.length) * float64(doc.hit.Session.Words)))
		}
		corpusDocuments++
		corpusLength += doc.length
		for i, n := range doc.termCount {
			if n > 0 {
				df[i]++
			}
		}
		if doc.hit.Count > 0 {
			documents = append(documents, doc)
		}
	}
	avgLength := 0.0
	if corpusDocuments > 0 {
		avgLength = float64(corpusLength) / float64(corpusDocuments)
	}
	hits := scoreBM25(documents, df, corpusDocuments, avgLength, len(qtoks), o.RecallWorn)
	markEarlierAttempts(hits)
	return hits, nil
}

// Run returns the capped result the CLI and the MCP tools have always
// returned. RunDetailed is the same search with the numbers the cap hides.
func Run(ss []model.Session, o Options) ([]Hit, error) {
	r, err := RunDetailed(ss, o)
	return r.Hits, err
}

// Results carries what a caller needs to interpret a search rather than only
// read it: how many sessions matched before the cap, whether the cap hid any,
// and which tier answered. Counting hits alone measures the cap — a figure that
// moves when the window's membership changes, whether or not recall improved.
type Results struct {
	Hits   []Hit
	Total  int
	Capped bool
	// Tier is the set-level answer: exact, close, stemmed, semantic or
	// relevance. relevance means nothing matched and these are the nearest
	// sessions, which is not the same kind of answer.
	Tier string
}

// RunDetailed is Run plus the numbers the cap would otherwise hide.
func RunDetailed(ss []model.Session, o Options) (Results, error) {
	hits, err := runScored(ss, o)
	if err != nil {
		return Results{}, err
	}
	r := Results{Hits: hits, Total: len(hits), Tier: setTier(o)}
	limit := o.Limit
	if limit == 0 && !o.All {
		limit = 15
	}
	if limit > 0 && len(hits) > limit {
		r.Hits = hits[:limit]
		r.Capped = true
	}
	return r, nil
}

func setTier(o Options) string {
	switch {
	case o.Semantic:
		return TierSemantic
	// Retrieval reports the coarse "close" for everything below exact and marks
	// stemming with its own flag. Taking its word overwrote the finer name, so
	// a word form and a misspelling — two rungs deja narrates differently —
	// arrived at a consumer as the same tier (#1616).
	case o.Stemmed && (o.Tier == "" || o.Tier == TierClose):
		return TierStemmed
	case o.Tier != "":
		return o.Tier
	case o.Stemmed:
		return TierStemmed
	case o.Fuzzy:
		return TierClose
	default:
		return TierExact
	}
}

// notesHarness is the pseudo-harness deja files its own notes under.
const notesHarness = "deja"

// markEarlierAttempts flags hits that look like older passes over the same
// problem: same project, heavy overlap in what matched, and a newer session
// above some margin. The old session stays in the results — history is the
// product — but agents and readers see which one the project moved on to.
func markEarlierAttempts(hits []Hit) {
	n := len(hits)
	if n > 50 {
		n = 50
	}
	sets := make([]map[string]bool, n)
	for i := 0; i < n; i++ {
		set := map[string]bool{}
		for _, sn := range hits[i].Snippets {
			for _, w := range strings.Fields(strings.ToLower(sn)) {
				if len(w) > 3 {
					set[w] = true
				}
			}
			// Overlap is what marks one session as an earlier pass over another's
			// problem. Chinese, Japanese and Korean write no separator between
			// words, so a whole excerpt was one token and two sessions about the
			// same thing overlapped in nothing (#1348) — the index reads those
			// scripts as bigrams and so does this.
			for _, b := range cjkfold.Bigrams(sn) {
				set[b] = true
			}
		}
		sets[i] = set
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j || hits[i].Superseded != "" {
				continue
			}
			a, b := hits[i], hits[j]
			// Notes are not attempts. A day of what someone wrote down is
			// grouped into one session, so two days of notes about the same
			// decision overlap by construction — on a store with a thousand
			// notes this labelled 17 of 20 note hits as work the project had
			// moved past, including decisions nobody took back. deja already
			// has a way to say a decision was replaced, and it is the person
			// saying it: `promote --state superseded` (#863).
			if a.Session.Harness == notesHarness || b.Session.Harness == notesHarness {
				continue
			}
			if a.Session.Project == "" || a.Session.Project != b.Session.Project {
				continue
			}
			// b must be meaningfully newer than a
			if !b.Session.Updated.After(a.Session.Updated.Add(24 * time.Hour)) {
				continue
			}
			// …and it must have happened. A transcript stamped ahead of the
			// clock is newer than everything, so one bad timestamp labelled
			// every real session in the project as an earlier attempt (#880).
			if b.Session.Updated.After(time.Now()) {
				continue
			}
			if snippetOverlap(sets[i], sets[j]) < 0.6 {
				continue
			}
			hits[i].Superseded = b.Session.Updated.Format("2006-01-02")
		}
	}
}

func snippetOverlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	shared := 0
	for w := range small {
		if large[w] {
			shared++
		}
	}
	return float64(shared) / float64(len(small))
}

// RelativeDate is the human "3w ago" form used in listings and digests.
func RelativeDate(t time.Time) string { return relativeDate(t) }

// noteBucketNoon anchors a bucket day at midday in the reader's zone, so the
// relative wording ("today", "3d ago") is computed from the day the id names
// and no hour of the clock can push it onto a neighbouring date.
func noteBucketNoon(day string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t.Add(12 * time.Hour)
}

// NoteBucketDay returns the day a note bucket's id encodes, when the session is
// one. The id is deja-YYYY-MM-DD-<project>: the bucket itself rather than a
// moment inside it, so it reads the same for every reader (#883).
func NoteBucketDay(s model.Session) (string, bool) {
	if s.Harness != notesHarness || !strings.HasPrefix(s.ID, "deja-") {
		return "", false
	}
	rest := strings.TrimPrefix(s.ID, "deja-")
	if len(rest) < 10 {
		return "", false
	}
	day := rest[:10]
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return "", false
	}
	return day, true
}

func scoreBM25(documents []bm25Document, df []int, corpusDocuments int, avgLength float64, queryTokenCount int, worn map[string]int) []Hit {
	emptyQuery := queryTokenCount == 0
	now := time.Now()
	hits := make([]Hit, 0, len(documents))
	for _, doc := range documents {
		score := 0.0
		for i, tf := range doc.termCount {
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (float64(corpusDocuments-df[i])+.5)/(float64(df[i])+.5))
			norm := 1 - bm25B
			if avgLength > 0 {
				norm += bm25B * float64(doc.length) / avgLength
			}
			term := idf * (float64(tf) * (bm25K1 + 1)) / (float64(tf) + bm25K1*norm)
			if doc.userCount[i] > 0 {
				term += idf * (float64(doc.userCount[i]) * (bm25K1 + 1)) / (float64(tf) + bm25K1*norm) * (userRoleBoost - 1)
			}
			score += term
		}
		if emptyQuery {
			score = float64(doc.hit.Count)
		}
		score *= proximityBoost(doc.minWindow, queryTokenCount)
		score *= titleBoost(doc.titleHits, queryTokenCount)
		// By the id as a log holds it: the counts come from .usage.jsonl, and
		// a byte that is not valid UTF-8 is U+FFFD there and itself here, so
		// the two never met (#2199).
		if n := worn[model.LoggedID(doc.hit.Session.ID)]; n > 0 {
			doc.hit.Reused = n
			score *= wornBoost(n)
		}
		// Curated notes are distilled truth with provenance; they outrank the
		// raw transcripts they were promoted from.
		if doc.hit.Session.Harness == "deja" {
			score *= promotedNoteBoost
		}
		// A session that reached a conclusion outranks one that only discussed
		// the topic. Term frequency alone ranks these backwards: the session
		// where someone kept asking repeats the words most, and the one that
		// answered says them once and then explains. Measured on a corpus built
		// for exactly that shape, the deciding session was ranked last of four
		// in every case before this.
		//
		// A session can both back out one attempt and land another ("reverted
		// the pool cap; the fix was switching to pgx"). That still reached a
		// conclusion, so it earns the boost — the give-up penalty below is what
		// stays clear of it.
		decided := decidesSomething(doc.hit.Session)
		if decided {
			score *= decisionBoost
		}
		// A pasted log outranks a human answer on term frequency alone: it
		// repeats the words fourteen times and says nothing. Measured before
		// this existed, the paste won every question of that shape.
		if looksPasted(doc.hit.Session) {
			score *= pastePenalty
		}
		// A session whose own text says the approach was backed out and reached
		// no other conclusion is the dead end, not the answer: handed to an
		// agent first, it invites repeating what already failed — and the
		// louder it flailed, the higher term frequency ranks it. Demote it
		// below a session that held, without hiding it (the marker still
		// explains it was reverted). Only when it decided nothing else: a
		// session that reverted one thing and settled another keeps the boost
		// above. Skipped only when a person accepted the session afterwards —
		// that is a fresher judgement that overrides the transcript. The other
		// lifecycle states do not rescue it: rejected and stale agree it was a
		// dead end, and superseded means a better record exists, so the give-up
		// penalty still applies. Read the state off the session, not the hit: a
		// hit's Lifecycle is attached by the CLI after ranking, so it is empty
		// here, while a promoted session that arrived by sync carries its state
		// on the session itself.
		if doc.hit.Session.GaveUp && !decided && doc.hit.Session.Lifecycle != "accepted" {
			score *= gaveUpPenalty
		}
		score *= freshnessDecay(doc.hit.Session.Updated, now)
		doc.hit.Score = score
		hits = append(hits, doc.hit)
	}
	sortHits(hits)
	liftNotesAboveTheirSource(hits)
	return hits
}

// liftedNotes applies the note-over-source rule to a tier that ranks by
// position rather than by a score the sort respects, and re-stamps the scores
// so a caller that re-sorts reads the same order back.
//
// The rule lived in the sort, so it reached the tiers that go through it and
// missed the two that build their hits already ranked: a pasted error and the
// "ranked by relevance" screen put a note behind the transcript it was
// distilled from, which is the ordering `promote` prints a promise about
// (#2803).
func liftedNotes(hits []Hit) []Hit {
	liftNotesAboveTheirSource(hits)
	for i := range hits {
		hits[i].Score = float64(len(hits) - i)
	}
	return hits
}

// LiftNotesAboveTheirSource applies the note-over-source rule to a hit list a
// caller outside this package built. The semantic path is such a caller: it
// ranks by cosine alone and never goes through the sort the rule lived in
// (#2803).
func LiftNotesAboveTheirSource(hits []Hit) {
	liftNotesAboveTheirSource(hits)
}

// liftNotesAboveTheirSource keeps a promoted note in front of the transcript it
// was distilled from. The two say the same thing, so nothing is buried by the
// swap — but the note says it in one line with a state attached, and that is
// what `promote` promises. Score alone cannot deliver it: the transcript
// carries the query words in its title and the note does not, and once a note
// is dated by its evidence rather than by the day it was filed (V4) the two are
// equally fresh, so the transcript won its own distillation.
func liftNotesAboveTheirSource(hits []Hit) {
	order := liftedNoteOrder(len(hits),
		func(i int) string { return hits[i].Session.Harness },
		func(i int) string { return hits[i].Session.ID },
		func(i int) string { return hits[i].Session.OrigID })
	if order == nil {
		return
	}
	out := make([]Hit, len(hits))
	for at, from := range order {
		out[at] = hits[from]
	}
	copy(hits, out)
}

// LiftNoteSessionsAboveTheirSource is the same rule for a caller that ranks
// sessions and never builds a Hit. The per-prompt hook is one: it had no
// note-over-source rule at all, so the transcript a note was distilled from
// went into the block ahead of the note (#2803).
func LiftNoteSessionsAboveTheirSource(ss []model.Session) {
	order := liftedNoteOrder(len(ss),
		func(i int) string { return ss[i].Harness },
		func(i int) string { return ss[i].ID },
		func(i int) string { return ss[i].OrigID })
	if order == nil {
		return
	}
	out := make([]model.Session, len(ss))
	for at, from := range order {
		out[at] = ss[from]
	}
	copy(ss, out)
}

// liftedNoteOrder answers where each element goes so that a promoted note sits
// in front of the transcript it was distilled from, and nothing else moves. It
// returns nil when there is nothing to lift, so a caller can skip the copy.
func liftedNoteOrder(n int, harnessAt, idAt, origAt func(int) string) []int {
	// A session that arrived by sync has its local id rewritten to imported-…
	// and keeps the real one in OrigID. Both sides of the pair need reading
	// that way — the note's own id, and the source's key a note is built from
	// — or an imported note is never recognised and the transcript it distils
	// outranks it everywhere this rule reaches (#2833). lifecycle.go and
	// hook_tool.go already consult OrigID for the same reason (#975).
	noteID := func(i int) string {
		if id := idAt(i); strings.HasPrefix(id, "deja-note-") {
			return id
		}
		if orig := origAt(i); strings.HasPrefix(orig, "deja-note-") {
			return orig
		}
		return ""
	}
	notes := make(map[string]int, n)
	for i := 0; i < n; i++ {
		if harnessAt(i) == notesHarness && noteID(i) != "" {
			notes[noteID(i)] = i
		}
	}
	if len(notes) == 0 {
		return nil
	}
	order := make([]int, 0, n)
	taken := make(map[int]bool, len(notes))
	moved := false
	for i := 0; i < n; i++ {
		if taken[i] {
			continue
		}
		if harnessAt(i) != notesHarness {
			// Mirrors sources.PromotedNoteID: building the id rather than
			// parsing one keeps a harness name with a dash in it from
			// splitting wrong. Built from the source's own id and from the one
			// it had before it was imported, since either may be what the note
			// was made against.
			for _, key := range []string{idAt(i), origAt(i)} {
				if key == "" {
					continue
				}
				j, ok := notes["deja-note-"+harnessAt(i)+"-"+key]
				if !ok || j <= i || taken[j] {
					continue
				}
				order = append(order, j)
				taken[j] = true
				moved = true
				break
			}
		}
		order = append(order, i)
	}
	if !moved {
		return nil
	}
	return order
}

// sortHits orders a ranked result set.
func sortHits(hits []Hit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			if hits[i].Session.Updated.Equal(hits[j].Session.Updated) {
				// Same evidence, same moment: prefer this machine's own work.
				// The tie used to fall to the id, and "imported-…" sorts ahead
				// of most session ids — so a peer's copy outranked the local
				// session for no reason anyone chose (#711).
				if li, lj := isLocalProject(hits[i].Session.Project), isLocalProject(hits[j].Session.Project); li != lj {
					return li
				}
				return hits[i].Session.ID < hits[j].Session.ID
			}
			return hits[i].Session.Updated.After(hits[j].Session.Updated)
		}
		return hits[i].Score > hits[j].Score
	})
}

// isLocalProject reports whether a session came from this machine. Imported
// sessions carry the peer prefix the sync path gives them.
func isLocalProject(project string) bool {
	return !strings.HasPrefix(project, "imported:")
}

func freshnessDecay(updated, now time.Time) float64 {
	if updated.IsZero() {
		return 0
	}
	age := now.Sub(updated).Hours() / 24
	if age <= 0 {
		return 1
	}
	return 1 / (1 + age)
}

// windowScanLimit bounds how many places of one token are enumerated when
// measuring how close the query's words come together. Without it, a long
// transcript message full of a common word makes this quadratic.
//
// Every place inside that bound is weighed. Sampling a subset was the earlier
// shape and it lost the tight cluster whenever all the query's words were
// common: the sample is chosen without knowing where the meeting is (#1319).
const (
	windowScanLimit = 4096
	// windowSegments is how many equal slices of a message get one sampled
	// place each, for a word that runs past the scan limit. A word that common
	// has a place near everything, so which ones are looked at stops mattering
	// — what matters is that the sample covers the whole text rather than its
	// first few kilobytes.
	windowSegments = 64
)

// tokenWindow is the tightest character span in this text that contains every
// query token.
//
// It used to measure between the FIRST place each token appears, which is not a
// window at all: a message that mentions "connection" in its opening line and
// then discusses "connection pool exhausted" as one phrase four paragraphs down
// was scored on the four paragraphs. Proximity is the signal that the words
// belong to one thought, and first-occurrence spacing is nearly the opposite of
// measuring it.
func tokenWindow(low string, qtoks []string) int {
	span, _ := tokenSpan(low, qtoks)
	return span
}

// tokenSpan is tokenWindow plus where that span starts, which is what an
// excerpt has to be centred on to show the words meeting rather than the first
// one to appear.
func tokenSpan(low string, qtoks []string) (span, start int) {
	if len(qtoks) < 2 {
		return 0, 0
	}
	// The span between first occurrences is an upper bound on the real window —
	// it is one way of picking one place per token, and the real answer is the
	// best of all of them. When that bound already reads as one thought, the
	// exact figure cannot change the verdict, only sharpen a boost that is
	// nearly at its ceiling. Enumerating occurrences costs about a fifth of
	// search latency, so it is spent only where the cheap answer says the words
	// are scattered, which is the case it gets wrong.
	first, last := -1, -1
	for _, tok := range qtoks {
		i := strings.Index(low, tok)
		if i < 0 {
			return 0, 0
		}
		if first < 0 || i < first {
			first = i
		}
		if end := i + len(tok); end > last {
			last = end
		}
	}
	if cheap := last - first; cheap <= proximityNear {
		return cheap, first
	}

	type occurrence struct{ at, tok int }
	var occs []occurrence

	// Every place of every token, not a sample of one token's places.
	//
	// The sampled version anchored on the rarest token and asked, for each of 32
	// of its places, where the nearest other tokens were (#1318). That is exact
	// when one query word is rare and blind when they are all common: in a 33 KB
	// message where three query words each appear about eighty times and meet
	// once, in one sentence, it measured 229 against a true 19 — across the
	// boundary where the proximity boost and the excerpt centring live, so the
	// passage that actually answered the question lost both (#1319).
	//
	// Collecting them all is the same walk the sampling already did to find the
	// rarest token, and the sliding window below is linear in what it is handed.
	for ti, tok := range qtoks {
		n := 0
		for i := 0; i < len(low) && n < windowScanLimit; {
			j := strings.Index(low[i:], tok)
			if j < 0 {
				break
			}
			occs = append(occs, occurrence{i + j, ti})
			n++
			i += j + 1
		}
		if n == 0 {
			return 0, 0
		}
		if n < windowScanLimit {
			continue
		}
		// The word runs past the scan limit, so the walk above stopped part way
		// through the message and everything after it is unseen. Replace those
		// places with a sample spread across the whole text plus the last one:
		// the phrase that answers the question sits at the end of a long output
		// as often as anywhere (#1318).
		occs = occs[:len(occs)-n]
		for i, seg := 0, 0; i < len(low) && seg < windowSegments; {
			j := strings.Index(low[i:], tok)
			if j < 0 {
				break
			}
			at := i + j
			occs = append(occs, occurrence{at, ti})
			seg = at*windowSegments/len(low) + 1
			if next := len(low) * seg / windowSegments; next > at {
				i = next
			} else {
				i = at + 1
			}
		}
		if last := strings.LastIndex(low, tok); last >= 0 {
			occs = append(occs, occurrence{last, ti})
		}
	}

	sort.Slice(occs, func(a, b int) bool { return occs[a].at < occs[b].at })

	// Slide a window along the occurrences, shrinking it from the left for as
	// long as it still holds every token.
	seen := make([]int, len(qtoks))
	have, best, left := 0, -1, 0
	bestAt := 0
	for right := range occs {
		if seen[occs[right].tok] == 0 {
			have++
		}
		seen[occs[right].tok]++
		for have == len(qtoks) {
			span := occs[right].at + len(qtoks[occs[right].tok]) - occs[left].at
			if best < 0 || span < best {
				best, bestAt = span, occs[left].at
			}
			seen[occs[left].tok]--
			if seen[occs[left].tok] == 0 {
				have--
			}
			left++
		}
	}
	if best < 0 {
		return 0, 0
	}
	return best, bestAt
}

// proximityNear is the width at which a window stops reading as one thought.
const proximityNear = 200

// proximityBoost rewards documents where the query terms sit together: a
// window under proximityNear chars reads as one thought, a spread across
// kilobytes is coincidence. Bounded at +35%.
func proximityBoost(window, queryTokenCount int) float64 {
	if window <= 0 || queryTokenCount < 2 {
		return 1
	}
	span := float64(window)
	boost := 1 + 0.35*(proximityNear/(proximityNear+span))
	return boost
}

// lifecycleSummary words a hit's recorded state for a person. It says what
// happened rather than naming the state: "superseded" is our vocabulary, not
// the reader's.
func lifecycleSummary(h Hit) string {
	var head string
	switch h.Lifecycle {
	case "rejected":
		head = "tried and rejected"
	case "superseded":
		head = "replaced by a later decision"
	case "stale":
		head = "marked stale — may no longer hold"
	default:
		head = SafeLine(h.Lifecycle)
	}
	// LifecycleAt and LifecycleNote are free text a peer may have synced; this
	// line prints to a terminal, so strip them like blame's lifecycle line.
	if h.LifecycleAt != "" {
		head += " (" + SafeLine(h.LifecycleAt) + ")"
	}
	if h.LifecycleNote != "" {
		head += ": " + SafeNote(h.LifecycleNote)
	}
	return head
}

// decisionBoost is sized to the effect it has to overcome, not tuned until a
// test passed: a session that repeats the query terms about three times more
// than another scores roughly twice as high once BM25 saturation is accounted
// for, and that is the ordinary shape of "someone kept asking" against "someone
// answered". It stays a tie-breaker rather than an override — a conclusion
// about a different topic must not outrank a session that squarely matches the
// question, and there is a test for exactly that.
const decisionBoost = 2.0

// roleToolOutput mirrors sources.RoleToolOutput; this package sits below it.
const roleToolOutput = "tool-output"

// decidesSomething reports whether a session contains an answer rather than a
// conversation about one. It looks only at non-user turns: a user writing "we
// should just pin it" is a proposal, and the same words from the side that did
// the work are a record of what happened.
func decidesSomething(s model.Session) bool {
	for _, m := range s.Messages {
		// Tool output is not the assistant concluding something. Before #559 it
		// arrived labelled `user` and was skipped here by accident; now it is
		// labelled honestly and has to be skipped on purpose.
		if m.Role == "user" || m.Role == "" || m.Role == roleToolOutput {
			continue
		}
		low := strings.ToLower(m.Text)
		for _, phrase := range decisionPhrases {
			if strings.Contains(low, phrase) {
				return true
			}
		}
	}
	return false
}

// pastePenalty damps a session whose matched text is a dump rather than a
// conversation. It is deliberately mild: some pastes are the answer — a stack
// trace someone diagnosed, a config that turned out to be wrong — so this
// lowers them behind real discussion rather than hiding them.
const pastePenalty = 0.5

// gaveUpPenalty damps a session whose own text reports the approach was backed
// out. Same magnitude as pastePenalty and for the same reason: it lowers the
// dead end behind a conclusion that held, but does not hide it — "we tried this
// and reverted" is worth reading, second, so an agent learns what not to redo.
const gaveUpPenalty = 0.5

// pasteMinLines is the length below which repetition means nothing. Three
// identical short lines are a list; fourteen are a log.
const pasteMinLines = 8

// pasteDistinctRatio is the share of distinct lines below which a message reads
// as machine output. A conversation almost never repeats two thirds of itself.
const pasteDistinctRatio = 0.35

// looksPasted reports whether a session's text is dominated by repeated lines.
// Line repetition rather than vocabulary: a log repeats its shape, and a person
// explaining something at length does not.
func looksPasted(s model.Session) bool {
	dump, total := 0, 0
	for _, m := range s.Messages {
		lines := strings.Split(strings.TrimSpace(m.Text), "\n")
		if len(lines) < pasteMinLines {
			continue
		}
		total++
		distinct := make(map[string]struct{}, len(lines))
		for _, l := range lines {
			distinct[strings.TrimSpace(l)] = struct{}{}
		}
		if float64(len(distinct))/float64(len(lines)) < pasteDistinctRatio {
			dump++
		}
	}
	return total > 0 && dump*2 >= total
}

// promotedNoteBoost lifts curated deja notes over raw transcripts on equal
// relevance. Kept modest: a note about X must not bury a transcript about Y.
const promotedNoteBoost = 1.25

// wornBoost rewards sessions agents keep recalling. The shape is set by
// measurement (scripts/reusebench, internal/search reuse tests): at the old
// 0.05 slope / 1.2 cap the boost rescued a reused answer only when a louder
// session tied it exactly — a single extra term of noise buried it, so the
// signal was nearly inert. A 0.10 slope and 1.5 cap let reuse surface an answer
// out-matched by a couple of terms, while a heavily-reused near-miss still loses
// to a strong match: relevance stays dominant, reuse breaks more than dead ties.
func wornBoost(n int) float64 {
	boost := 1 + 0.10*math.Log2(float64(1+n))
	if boost > 1.5 {
		return 1.5
	}
	return boost
}

// titleBoost rewards query tokens appearing in the session title — the
// strongest single relevance signal a session carries. Bounded at +40%.
func titleBoost(titleHits, queryTokenCount int) float64 {
	if titleHits == 0 || queryTokenCount == 0 {
		return 1
	}
	return 1 + 0.4*float64(titleHits)/float64(queryTokenCount)
}

func countDocumentWords(s string, terms []string, variants map[string][]string, counts, userCounts []int, user bool) int {
	length := 0
	start := -1
	// cjk counts the characters of a run written in a script without spaces, and
	// other counts the rest of that run, so a mixed run like "scheduler調度器"
	// contributes both. Counted here rather than in a second pass over the word:
	// this runs over every message of every candidate on every search.
	cjk, other := 0, 0
	for i := 0; i <= len(s); {
		isWord := false
		size := 0
		if i < len(s) {
			r, n := utf8.DecodeRuneInString(s[i:])
			size = n
			isWord = unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
			if isWord {
				if cjkfold.IsCJK(r) {
					cjk++
				} else {
					other++
				}
			}
		}
		if isWord && start < 0 {
			start = i
		} else if !isWord && start >= 0 {
			word := s[start:i]
			// Length feeds BM25's normalisation, the part that stops a long
			// document mentioning a term once from outranking a short one about
			// it. Chinese, Japanese and Korean put no spaces between words, so a
			// four-thousand-character aside was one word long and normalisation
			// had nothing to work with: measured on the same content, a passing
			// mention was separated from the answer by 6.2x in English and 1.3x
			// in Chinese (#1344). Their characters are the words.
			if cjk > 0 {
				length += cjk
				if other > 0 {
					length++
				}
			} else {
				length++
			}
			cjk, other = 0, 0
			for j, term := range terms {
				matched := word == term
				if !matched {
					// A word matching a fuzzy/stem variant of this term counts
					// toward its term frequency so BM25 ranks the hit instead of
					// falling back to recency-only order.
					for _, v := range variants[term] {
						if word == v {
							matched = true
							break
						}
					}
				}
				if matched {
					counts[j]++
					if user {
						userCounts[j]++
					}
				}
			}
			start = -1
		}
		if i == len(s) {
			break
		}
		i += size
	}
	return length
}

func mergeSessions(in []model.Session) []model.Session {
	by := map[string]*model.Session{}
	for _, s := range in {
		k := s.Harness + ":" + s.ID
		if by[k] == nil {
			cp := s
			by[k] = &cp
		} else {
			by[k].Messages = append(by[k].Messages, s.Messages...)
			by[k].Touch(s.Updated)
			if by[k].Project == "history" {
				by[k].Project = s.Project
			}
		}
	}
	out := make([]model.Session, 0, len(by))
	for _, s := range by {
		out = append(out, *s)
	}
	return out
}

func Print(w io.Writer, hits []Hit, o Options) {
	for i := range hits {
		if hits[i].Tier == "" {
			hits[i].Tier = TierExact
		}
		hits[i].Session.SetSource(o.SourceInstance)
	}
	if o.JSON {
		// One shape, always. The exact path used to emit a bare array while
		// every fallback path emitted an object, so a consumer had to handle
		// two contracts and could not tell which it had until it looked.
		_ = json.NewEncoder(w).Encode(searchJSONEnvelope{
			SchemaVersion: jsonout.Version,
			Tier:          setTier(o),
			Total:         o.Total,
			Capped:        o.Capped,
			Withheld:      o.PolicyWithheld,
			Hits:          hits,
			Fuzzy:         o.Fuzzy,
			Stemmed:       o.Stemmed,
			Semantic:      o.Semantic,
			Variants:      o.FuzzyVariants,
		})
		return
	}
	color := colorOK(w)
	// One form for the whole column, decided before the first row is printed.
	when := make([]time.Time, 0, len(hits))
	for _, h := range hits {
		t := h.Session.Updated
		if day, ok := NoteBucketDay(h.Session); ok {
			t = noteBucketNoon(day)
		}
		when = append(when, t)
	}
	dated := dateColumn(when)
	for i, h := range hits {
		d := "-"
		if !when[i].IsZero() {
			// A day of notes is one session whose id *is* a date, minted in
			// UTC. The reader's zone put a different day on the line than the
			// id beside it, and only for deja's own buckets (#883) — which is
			// why the time was chosen above rather than here.
			d = dated(when[i])
		}
		// project and id are transcript/peer free text printed to a terminal;
		// an escape or bidi run in an imported project name would repaint the
		// line the way it would in blame. Snippets below are already SafeText'd.
		project := SafeLine(h.Session.Project)
		id := SafeLine(short(h.Session.ID))
		// The project column gives way first. Cutting the line at its end takes
		// the match count and half the word "matches" with it, and those are
		// what a reader scans; the project is the one field that is long
		// because a directory was named at length (#604).
		project = fitProject(project, o.Width, h.Session.Harness, d, id, h.Count, tierLabel(h))
		if color {
			fmt.Fprintf(w, "%s%s %-10s %s %s %s %s %s%s%d matches%s%s\n", cBold, harnessTag(h.Session.Harness, true), project, cDim+"·"+cReset+cBold, d, cDim+"·"+cReset+cBold, id, cDim+"— "+cReset, cBold, h.Count, cReset, tierLabel(h))
		} else {
			fmt.Fprintf(w, "[%s] %-10s · %s · %s — %d matches%s\n", h.Session.Harness, project, d, id, h.Count, tierLabel(h))
		}
		if h.Reused > 1 {
			note := fmt.Sprintf("  reused %d× by agents recently", h.Reused)
			if color {
				note = cDim + note + cReset
			}
			fmt.Fprintln(w, note)
		}
		// What was later recorded about this decision comes before anything
		// else about the hit: a reader who stops after one line must not stop
		// on a conclusion that was reverted.
		if h.Lifecycle != "" {
			note := "  " + lifecycleSummary(h)
			if color {
				note = cOrange + note + cReset
			}
			fmt.Fprintln(w, note)
		}
		// Nobody sets the rejected state by hand, so the sessions that ended in
		// a dead end look exactly like the ones that ended in an answer. When
		// the transcript itself says something was backed out, say so — as
		// evidence from the session, not as a state someone recorded. The
		// wording is deliberately mild: a session that tried one thing, dropped
		// it and found another is the most useful kind, so this flags an
		// abandoned approach inside it, not the whole session as a dead end.
		if h.Session.GaveUp && h.Lifecycle == "" {
			note := "  mentions backing an approach out — one path here was abandoned"
			if color {
				note = cDim + note + cReset
			}
			fmt.Fprintln(w, note)
		}
		if h.Moved != "" {
			note := SafeLine(h.Moved)
			if color {
				note = cDim + note + cReset
			}
			fmt.Fprintln(w, note)
		}
		if h.Superseded != "" {
			note := "  earlier attempt — this project has a newer session on the same ground (" + h.Superseded + ")"
			if color {
				note = cDim + note + cReset
			}
			fmt.Fprintln(w, note)
		}
		for _, sn := range h.Snippets {
			// Cut before the highlight, not after: the colour codes are not
			// characters the terminal counts, so trimming the rendered string
			// would cut a different number of visible runes on every line.
			fmt.Fprintf(w, "  %s\n", highlight(SafeText(fitLine(sn, o.Width-2)), o.Query, o.Regex, color))
		}
	}
}

// fitProject bounds the one variable-width field on a hit header so the rest of
// the line survives a narrow terminal. Six runes is the floor: below that the
// name says nothing and the reader is better served by the ellipsis alone.
//
// Narrower than the rest of the line needs, the line is left to wrap. What is
// left is the harness, the date, the session id and the count, and the id is
// the field a reader copies into `deja show` — cutting it further would hand
// them a prefix that matches nothing, which is the trap #859 already worded a
// message for.
func fitProject(project string, width int, harness, date, id string, count int, tier string) string {
	if width <= 0 {
		return project
	}
	// The column is padded to ten, so a name shorter than that costs ten either
	// way and the budget has to say so.
	fixed := termwidth.Columns(fmt.Sprintf("[%s]  · %s · %s — %d matches%s", harness, date, id, count, tier))
	room := width - fixed
	if room < 10 {
		room = 10
	}
	if termwidth.Columns(project) <= room {
		return project
	}
	if room < 6 {
		room = 6
	}
	return termwidth.Cut(project, room-1) + "…"
}

// fitLine cuts a line to the width it is being printed into, counting runes
// rather than bytes. Width zero — piped output, or a terminal that would not
// say — leaves the line alone: a script reading deja's output wants the whole
// thing, and truncating there would lose data rather than layout.
func fitLine(s string, width int) string {
	if width <= 0 {
		return s
	}
	if width < 8 {
		width = 8
	}
	if termwidth.Columns(s) <= width {
		return s
	}
	// One column short of the width, for the ellipsis.
	return strings.TrimRight(termwidth.Cut(s, width-1), " ") + "…"
}

func tierLabel(h Hit) string {
	if h.Tier == "" || h.Tier == TierExact {
		return ""
	}
	if h.TierDetail == "" {
		return " · " + h.Tier
	}
	return " · " + h.Tier + " (" + h.TierDetail + ")"
}

func variantDetail(text string, terms []string, variants map[string][]string) string {
	low := strings.ToLower(text)
	for _, term := range terms {
		for _, variant := range variants[term] {
			// A term that matched itself is an exact hit, not a variant —
			// don't render "connection->connection" as the tier detail.
			if variant == term || variant == "" {
				continue
			}
			if strings.Contains(low, variant) {
				return term + "->" + variant
			}
		}
	}
	return ""
}

// FindByPrefix is the fallback the CLI takes when there is no index to read, so
// it has to answer an empty prefix the same way index.FindByPrefix does: with
// no match. Otherwise the guard depends on whether a rebuild happens to be
// running, which is the worst kind of intermittent.
func FindByPrefix(ss []model.Session, p string) (model.Session, bool) {
	if p == "" {
		return model.Session{}, false
	}
	for _, s := range mergeSessions(ss) {
		if strings.HasPrefix(s.ID, p) {
			return s, true
		}
	}
	return model.Session{}, false
}

// repeatedStamps names the minutes this session renders more than once from
// different instants — what the fall-back hour does twice a year. Nothing else
// is affected: an ordinary transcript keeps its narrower column.
func repeatedStamps(ms []model.Message) map[string]bool {
	seen := map[string]time.Time{}
	repeated := map[string]bool{}
	for _, m := range ms {
		if m.Time.IsZero() {
			continue
		}
		stamp := m.Time.Format("2006-01-02 15:04")
		if first, ok := seen[stamp]; ok {
			if !first.Equal(m.Time) {
				repeated[stamp] = true
			}
			continue
		}
		seen[stamp] = m.Time
	}
	return repeated
}

func PrintSession(w io.Writer, s model.Session) {
	// Project and id are transcript text a harness wrote, and this is one
	// line: an escape byte in either recolours the transcript that follows, a
	// carriage return rewinds the header, and a newline splits it into two
	// lines of what reads as deja's own output. PrintContext below has said
	// this since #1090; this header was missed by it.
	fmt.Fprintf(w, "# %s · %s · %s\n", s.Harness, SafeLine(s.Project), SafeLine(s.ID))
	repeated := repeatedStamps(s.Messages)
	for _, m := range s.Messages {
		txt := redact.SafeForDisplay(collapseTool(m.Text))
		if strings.TrimSpace(txt) == "" {
			continue
		}
		t := ""
		if !m.Time.IsZero() {
			stamp := m.Time.Format("2006-01-02 15:04")
			if repeated[stamp] {
				// The clocks went back and this minute happened twice. Both
				// stamps are right, which is why an hour of conversation reads
				// as a duplicated message without the offset (#1788).
				stamp = m.Time.Format("2006-01-02 15:04 -07:00")
			}
			t = stamp + " "
		}
		fmt.Fprintf(w, "\n%s%s:\n%s\n", t, m.Role, SafeText(txt))
	}
}

// roleMatches accepts the role names the help text documents. `--role tool`
// is what `deja help` promises and "tool-output" is what is stored, so the
// documented spelling matched nothing — silently, with a healthy exit. Mirrors
// index.roleMatches, which cannot be imported here.
func roleMatches(stored, want string) bool {
	if stored == want {
		return true
	}
	return want == "tool" && stored == roleToolOutput
}

// isWorkRecord reports whether a message records what an agent did rather than
// what was said. Mirrors index.isToolRole, which cannot be imported here.
func isWorkRecord(role string) bool {
	switch role {
	case roleToolOutput, "files", "command", "edit":
		return true
	}
	return false
}

// ContextBudget is how much of a session PrintContext prints. The whole
// transcript would be tens of times an answer's size, and it is one
// recall_context away when an agent genuinely needs it.
const ContextBudget = 8000

func PrintContext(w io.Writer, s model.Session, query string) {
	// Project and id are transcript text a harness wrote, and this is one line:
	// an escape byte in either recolours the header and a carriage return
	// rewinds it. The body below is already SafeText'd; the header was not, so
	// `deja show`, `deja ctx` and the MCP context tools printed it raw (#1090).
	fmt.Fprintf(w, "# deja context: %s · %s · %s", s.Harness, SafeLine(s.Project), SafeLine(s.ID))
	if !s.Updated.IsZero() {
		// The reader's zone, as `deja last`, resources/list and the MCP recall
		// listing already do. This header formatted the timestamp as it came —
		// UTC for every source but aider — so a reader far enough from it saw
		// ctx name one day and those surfaces name the next for one session.
		// ctx is the briefing written to be handed to another agent, which
		// makes its header a date that gets repeated back (#856).
		//
		// Not everything is local yet: `deja show` prints per-message stamps in
		// the timestamp's own zone, and the superseded marker is minted in UTC
		// because lifecycle.go compares against it. Those want their own change.
		fmt.Fprintf(w, " · updated %s", s.Updated.Local().Format("2006-01-02"))
	}
	fmt.Fprintln(w)
	qlow := strings.ToLower(query)
	terms, phrases := QueryParts(query)
	budget := ContextBudget
	// The reply to an included turn comes with it. Every user turn was kept
	// and every assistant turn had to match, but the decision lives in the
	// answer and is worded nothing like the question: `ctx "http client"`
	// handed an agent "the http client hammered the server on failure" and
	// dropped "we decided to cap retries at 3" from the turn below it. That
	// is the problem statement without its resolution (#R8).
	parts := contextQueryParts(terms, phrases, qlow)
	prevKept := false
	written := printContextChunks(w, s, budget, func(m model.Message) (bool, []int) {
		// How much of each part of the query the turn carries, counted
		// separately. countIn scores a turn only when it carries the whole
		// query, so on a two-word question the turns holding the identifying
		// word alone weighed nothing, no turn qualified, and the window fell
		// back to the session's opening — 8 KB about something else (#2726).
		hits := contextPartHits(m.Text, parts)
		carries := false
		for _, h := range hits {
			if h > 0 {
				carries = true
				break
			}
		}
		keep := carries || m.Role == "user" || (m.Role == "assistant" && prevKept)
		prevKept = keep
		return keep, hits
	})
	if written > 0 {
		return
	}
	// The session can match with query terms spread across messages, so no
	// single message qualifies above; show an overview instead of a bare header.
	if qlow != "" {
		fmt.Fprintf(w, "\nNo single message contains the full query; showing the session's opening exchange.\n")
	}
	printContextChunks(w, s, budget, func(m model.Message) (bool, []int) { return true, nil })
}

// contextQueryParts is what the digest weighs a turn against: the query's terms
// and phrases, or the raw query when tokenising left nothing — a reader who
// typed punctuation still gets scored against what they typed.
func contextQueryParts(terms, phrases []string, qlow string) []string {
	parts := make([]string, 0, len(terms)+len(phrases)+1)
	parts = append(parts, terms...)
	parts = append(parts, phrases...)
	if len(parts) == 0 && qlow != "" {
		parts = append(parts, qlow)
	}
	return parts
}

// contextPartHits counts each part of the query in one turn.
func contextPartHits(text string, parts []string) []int {
	if len(parts) == 0 {
		return nil
	}
	low := strings.ToLower(text)
	hits := make([]int, len(parts))
	for i, p := range parts {
		hits[i] = strings.Count(low, p)
	}
	return hits
}

// contextTurn is a turn the digest keeps, before it is rendered.
type contextTurn struct {
	role string
	raw  string
	// hits is how often the turn says each part of the query. Kept per part
	// rather than summed, because how much a part is worth is not known until
	// every kept turn has been seen: see contextTurnWeights.
	hits []int
}

// carriesQuery reports whether the turn says any part of the query at all.
func (t contextTurn) carriesQuery() bool {
	for _, h := range t.hits {
		if h > 0 {
			return true
		}
	}
	return false
}

// contextTurnWeights scores each kept turn by the rarity of the query parts it
// carries, so the budget window lands where the identifying word is.
//
// A word the session repeats on every turn says nothing about which passage was
// wanted; the word that picked this session out of the index says everything.
// Rarity is measured across the kept turns themselves — no index lookup, and it
// is the same reasoning the ranking uses for identifying words (#2480).
// Repetition inside one turn is damped: a turn returning to the subject should
// outrank one that mentions it once, but not forty turns of scaffolding.
func contextTurnWeights(turns []contextTurn, parts int) []float64 {
	weights := make([]float64, len(turns))
	if parts == 0 {
		return weights
	}
	df := make([]int, parts)
	for _, t := range turns {
		for i, h := range t.hits {
			if i < parts && h > 0 {
				df[i]++
			}
		}
	}
	idf := make([]float64, parts)
	for i, n := range df {
		if n > 0 {
			idf[i] = math.Log(1 + float64(len(turns))/float64(n))
		}
	}
	// How much of the query the turn covers multiplies what it is worth. The
	// turn that says every word of the question is the one the reader meant,
	// and demanding all of them was the whole strength of the old rule; keeping
	// it as a multiplier holds that strength while still letting a turn with
	// one identifying word anchor the window when no turn carries them all.
	for j, t := range turns {
		var sum float64
		covered := 0
		for i, h := range t.hits {
			if i < parts && h > 0 {
				sum += idf[i] * math.Log1p(float64(h))
				covered++
			}
		}
		weights[j] = sum * float64(covered)
	}
	return weights
}

// renderContextTurnsWorkers is how many turns render at once. One worker per
// core; a session small enough that the goroutines cost more than the work
// renders in place.
var renderContextTurnsWorkers = runtime.NumCPU

// renderContextTurns renders each kept turn, in parallel when there are enough
// of them to pay for it. Results are written by index, so the digest is the
// same bytes in the same order however many cores run it.
func renderContextTurns(turns []contextTurn) []string {
	out := make([]string, len(turns))
	workers := renderContextTurnsWorkers()
	if workers > len(turns) {
		workers = len(turns)
	}
	if workers < 2 || len(turns) < 32 {
		for i, t := range turns {
			out[i] = SafeText(contextText(t.raw, t.carriesQuery()))
		}
		return out
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(turns) {
					return
				}
				out[i] = SafeText(contextText(turns[i].raw, turns[i].carriesQuery()))
			}
		}()
	}
	wg.Wait()
	return out
}

func printContextChunks(w io.Writer, s model.Session, budget int, include func(m model.Message) (ok bool, hits []int)) int {
	// A digest is what someone pipes into a prompt, so it carries the
	// conversation. The work records — tool output, the files a turn touched, the
	// commands it ran, the spans it replaced — are indexed and searchable by role
	// and are not what anyone means by context; before they were labelled
	// honestly (#560) they arrived as `user` and filled this with `## tool-output`
	// blocks. Collect the kept turns in one ordered pass so the include callback's
	// prevKept bookkeeping still runs left to right.
	type chunk struct {
		text string
		// weight is how much of the query this turn carries, by rarity, so the
		// window can sit where the identifying words are rather than where the
		// common ones repeat.
		weight float64
	}
	// The include callback carries state from turn to turn (prevKept), so which
	// turns are kept is decided in one ordered pass. Rendering them is not:
	// redaction and prose collapsing are the bulk of the command's time — 3.5 s
	// of a 4.5 s run on a 30001-message session — and every turn is rendered
	// before the 8 KB window can say which ones get printed (#1790). Rendering
	// only the window was tried and reverted (#1742): collapsed sizes differ
	// from predicted ones and the window moves. Spreading the same renders
	// across cores keeps the output identical by construction.
	var kept []contextTurn
	parts := 0
	for _, m := range s.Messages {
		if isWorkRecord(m.Role) {
			continue
		}
		ok, hits := include(m)
		if !ok {
			continue
		}
		if len(hits) > parts {
			parts = len(hits)
		}
		kept = append(kept, contextTurn{role: m.Role, raw: m.Text, hits: hits})
	}
	weights := contextTurnWeights(kept, parts)
	rendered := renderContextTurns(kept)
	var chunks []chunk
	for i, k := range kept {
		text := rendered[i]
		if strings.TrimSpace(text) == "" {
			continue
		}
		chunks = append(chunks, chunk{fmt.Sprintf("\n## %s\n\n%s\n", k.role, text), weights[i]})
	}
	if len(chunks) == 0 {
		return 0
	}
	// Anchor the budget window where the matched turns are densest, with one turn
	// of lead-in (usually the question when the answer is what matched). Walking
	// from the top let earlier scaffolding crowd the match out of budget: a query
	// whose answer sat deep in a long session got 8KB of unrelated opening and
	// none of the turns it found (#R8-budget). Anchoring on the first match alone
	// left the other half of that: a word mentioned once in passing at the top
	// pinned the window there, and the exchange that settled the question — every
	// later mention of it — fell outside the budget again (#1322). No match means
	// the fallback overview, which wants the session's start, so it stays at 0.
	start, span := 0, 0
	best := 0.0
	sum, bytes, left := 0.0, 0, 0
	for right, c := range chunks {
		sum += c.weight
		bytes += len(c.text)
		for left < right && bytes > budget {
			sum -= chunks[left].weight
			bytes -= len(chunks[left].text)
			left++
		}
		if sum > best {
			best, start, span = sum, left, bytes
		}
	}
	// The lead-in is only worth having if it does not push a matched turn back
	// out of the budget it was just chosen for.
	if best > 0 && start > 0 && span+len(chunks[start-1].text) <= budget {
		start--
	}
	written := 0
	for _, c := range chunks[start:] {
		if written >= budget {
			break
		}
		text := c.text
		if written+len(text) > budget {
			cut := max(0, budget-written)
			for cut > 0 && !utf8.RuneStart(text[cut]) {
				cut--
			}
			text = text[:cut]
		}
		fmt.Fprint(w, text)
		written += len(text)
	}
	return written
}

func Recent(ss []model.Session, n int) []model.Session {
	out := mergeSessions(ss)
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// A pattern that can match the empty string — `x*`, `foo?`, `(a|)` — matches
// between every pair of characters. Counting those reported 88 matches in a
// session that held none of the pattern's characters at all (#1606); a match
// with no width is not a match.
func countRegex(re *regexp.Regexp, s string) int {
	n := 0
	for _, loc := range re.FindAllStringIndex(s, -1) {
		if loc[1] > loc[0] {
			n++
		}
	}
	return n
}

func snippet(s, q string, re *regexp.Regexp) string {
	s = proseForSnippet(s)
	r := []rune(s)
	idx := 0
	if re != nil {
		// Anchor on the first match that has width; a zero-width one points at
		// no passage in particular (#1606).
		for _, loc := range re.FindAllStringIndex(s, -1) {
			if loc[1] > loc[0] {
				idx = utf8.RuneCountInString(s[:loc[0]])
				break
			}
		}
	} else {
		low := strings.ToLower(s)
		b := densestMention(low, strings.ToLower(q))
		if b < 0 {
			toks := query.Tokens(q)
			// Centre on where the query's words meet, not on the first one to
			// appear. A message that says a word once at the top and answers the
			// question four paragraphs down was excerpted at the top, so the
			// excerpt held none of the words the reader searched for together
			// (#1327). Only when they meet inside what an excerpt can show —
			// past that there is no meeting to point at, and the first mention is
			// as good a place to start as any.
			if span, at := tokenSpan(low, toks); span > 0 && span <= proximityNear {
				b = at
			}
			if b < 0 {
				for _, tok := range toks {
					if p := strings.Index(low, tok); p >= 0 && (b < 0 || p < b) {
						b = p
					}
				}
			}
		}
		if b > 0 {
			// Count runes in the lowercased string, which is where b came from.
			// Lowercasing maps rune to rune, so the count carries over to s —
			// the byte offsets do not: "İ" is two bytes and lowercases to one,
			// so a Turkish message put the excerpt a rune earlier for every
			// capital İ before the match, and enough of them moved the window
			// off the match entirely (#1331).
			idx = utf8.RuneCountInString(low[:b])
		}
	}
	start := idx - 100
	if start < 0 {
		start = 0
	}
	end := start + 300
	if end > len(r) {
		// Spend the whole window: a match near the end of a message left the
		// tail of the budget unused and showed 120 runes where a match in the
		// middle of the same message showed 302 — and the end is where the
		// answer sits in a long output as often as anywhere (#1319).
		end = len(r)
		if start = end - 300; start < 0 {
			start = 0
		}
	}
	out := strings.TrimSpace(string(r[start:end]))
	out = strings.Trim(out, " ,.;:-\n\t")
	if start > 0 {
		out = "… " + out
	}
	if end < len(r) {
		out += " …"
	}
	return out
}

// densestMention is where in low the query is discussed rather than mentioned:
// the occurrence with the most others inside the part of an excerpt that
// follows it.
//
// #1329 fixed the same shape one level up — blame quoted the first message to
// name a file rather than the one that said the most about it. Inside the
// message the excerpt still started at the first occurrence, so a session that
// noticed a file in passing at the top and took it apart four paragraphs down
// was excerpted on the passing line, with the discussion out of view. Returns
// -1 when the query does not appear, which is the caller's signal to fall back
// to its own token handling.
func densestMention(low, q string) int {
	if q == "" {
		return -1
	}
	first := strings.Index(low, q)
	if first < 0 {
		return -1
	}
	// Positions in bytes, bounded: a term repeated thousands of times in one
	// message is a log dump, and the first few hundred say as much about where
	// the discussion is as all of them.
	const scanCap = 256
	at := make([]int, 0, 8)
	for p := first; p >= 0 && len(at) < scanCap; {
		at = append(at, p)
		next := strings.Index(low[p+len(q):], q)
		if next < 0 {
			break
		}
		p += len(q) + next
	}
	if len(at) == 1 {
		return first
	}
	// The window is what the excerpt shows after its centre — 200 of its 300
	// runes — measured in runes, because a byte window is four times too wide
	// on CJK and then every occurrence looks like it sits with every other.
	best, bestCount := first, 0
	for i, p := range at {
		n := 0
		for _, other := range at[i:] {
			if utf8.RuneCountInString(low[p:other]) > snippetForwardRunes {
				break
			}
			n++
		}
		if n > bestCount {
			best, bestCount = p, n
		}
	}
	return best
}

// snippetForwardRunes is how much of an excerpt follows the point it centres
// on: start is idx-100 and the excerpt is 300 runes long.
const snippetForwardRunes = 200

// Snippet formats a message for search results, including semantic matches.
func Snippet(s, q string) string { return snippet(s, q, nil) }

func countAllTokens(low string, toks []string) int {
	total := 0
	for _, tok := range toks {
		c := strings.Count(low, tok)
		if c == 0 {
			return 0
		}
		total += c
	}
	return total
}

func countAllVariants(low string, toks []string, variants map[string][]string) int {
	total := 0
	for _, tok := range toks {
		optional := false
		count := strings.Count(low, tok)
		for _, variant := range variants[tok] {
			if variant == "" {
				optional = true
				continue
			}
			count += strings.Count(low, variant)
		}
		if count == 0 {
			if optional {
				continue
			}
			return 0
		}
		total += count
	}
	return total
}

// short keeps a result line narrow without destroying the one field that
// identifies the session.
//
// A flat 12-character cut printed `deja-note-cl` for every promoted note and
// `00000000-000` for UUID sessions — twelve identical rows in one answer, and
// the cut string is what a reader copies into `deja show` (#707). Ids are
// meaningful at both ends, so the middle goes.
func short(s string) string {
	const width = 20
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	head := width/2 - 1
	tail := width - head - 1
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}
func highlight(s, q string, isRe bool, color bool) string {
	if !color {
		return s
	}
	if isRe {
		re, err := regexp.Compile("(?i)" + q)
		if err == nil {
			return re.ReplaceAllStringFunc(s, func(x string) string {
				// Zero-width match: colouring it wraps an escape pair around
				// nothing, once per character (#1606).
				if x == "" {
					return x
				}
				return cMatch + x + cReset
			})
		}
	}
	if strings.Contains(strings.ToLower(s), strings.ToLower(q)) {
		return regexp.MustCompile(`(?i)`+regexp.QuoteMeta(q)).ReplaceAllStringFunc(s, func(x string) string { return cMatch + x + cReset })
	}
	toks := query.Tokens(q)
	if len(toks) == 0 {
		return s
	}
	parts := make([]string, 0, len(toks))
	for _, t := range toks {
		parts = append(parts, regexp.QuoteMeta(t))
	}
	return regexp.MustCompile(`(?i)(`+strings.Join(parts, "|")+`)`).ReplaceAllStringFunc(s, func(x string) string { return cMatch + x + cReset })
}

func colorOK(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

func harnessTag(h string, color bool) string {
	tag := "[" + h + "]"
	if !color {
		return tag
	}
	switch h {
	case "claude":
		return cOrange + tag + cReset + cBold
	case "codex":
		return cGreen + tag + cReset + cBold
	case "opencode":
		return cBlue + tag + cReset + cBold
	case "pi":
		return cGreen + tag + cReset + cBold
	}
	return tag
}

// dateColumn picks one form for a whole column and returns a formatter that
// holds to it.
//
// Both forms are correct, and mixing them is what makes the column unreadable:
// two sessions a day apart printed "6d ago" and "Jul 26" one row under the
// other, because the relative form stops at a week. A reader scanning down
// cannot see they are consecutive without doing the arithmetic. So the rule is
// per rendering rather than per row — if any row has aged out of the relative
// form, every row is dated.
func dateColumn(times []time.Time) func(time.Time) string {
	for _, t := range times {
		if t.IsZero() {
			continue
		}
		if !isRelativeAge(t) {
			return absoluteDate
		}
	}
	return relativeDate
}

// isRelativeAge reports whether relativeDate would answer in the relative form.
func isRelativeAge(t time.Time) bool {
	return daysAgo(t) < 7
}

func daysAgo(t time.Time) int {
	now := time.Now()
	// In the reader's zone, not the timestamp's — see relativeDate.
	t = t.In(now.Location())
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	today := time.Date(y1, m1, d1, 0, 0, 0, 0, now.Location())
	day := time.Date(y2, m2, d2, 0, 0, 0, 0, now.Location())
	return int(today.Sub(day).Hours() / 24)
}

func absoluteDate(t time.Time) string {
	now := time.Now()
	t = t.In(now.Location())
	if t.Year() == now.Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}

func relativeDate(t time.Time) string {
	now := time.Now()
	// In the reader's zone, not the timestamp's: work done at 00:30 local time
	// is stored as 21:30 UTC the day before, and taking its calendar date in
	// UTC made this morning read as "1d ago" — while the counter above it, which
	// compares instants, said "today" (#767).
	t = t.In(now.Location())
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	today := time.Date(y1, m1, d1, 0, 0, 0, 0, now.Location())
	day := time.Date(y2, m2, d2, 0, 0, 0, 0, now.Location())
	days := int(today.Sub(day).Hours() / 24)
	if days == 0 {
		return "today"
	}
	if days > 0 && days < 7 {
		return fmt.Sprintf("%dd ago", days)
	}
	if y1 == y2 {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}
func collapseTool(s string) string {
	if strings.Contains(s, "tool_use") || strings.Contains(s, "tool_result") || strings.Contains(s, "<local-command") {
		if utf8.RuneCountInString(s) > 400 {
			return "[tool/local output collapsed]"
		}
	}
	return s
}

var (
	lineNumberRE = regexp.MustCompile(`^\s*\d{1,5}[:|]\s+`)
	toolDumpRE   = regexp.MustCompile(`(?i)(tool_use|tool_result|<local-command|netcat|npm ERR!|panic:|goroutine \d+)`)
	// The literal each alternative of toolDumpRE begins with, in the same
	// order. A line holding none of them cannot match, and looksToolDump uses
	// that to skip the engine entirely.
	toolDumpLiterals = []string{"tool_use", "tool_result", "<local-command", "netcat", "npm err!", "panic:", "goroutine "}
)

// containsFold is strings.Contains for a lowercase ASCII needle, without
// allocating a lowercased copy of the line.
func containsFold(hay, lowerNeedle string) bool {
	if len(lowerNeedle) == 0 || len(hay) < len(lowerNeedle) {
		return len(lowerNeedle) == 0
	}
	last := len(hay) - len(lowerNeedle)
	for i := 0; i <= last; i++ {
		k := 0
		for k < len(lowerNeedle) {
			c := hay[i+k]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != lowerNeedle[k] {
				break
			}
			k++
		}
		if k == len(lowerNeedle) {
			return true
		}
	}
	return false
}

// toolDumpEngineCalls counts the lines that reach the regexp engine. The point
// of the prefilter is that ordinary prose does not, and a counter says so in a
// test without timing anything (#1742).
var toolDumpEngineCalls atomic.Int64

// looksToolDump is toolDumpRE with the scan the regexp engine spends most of
// its time on done by hand: every alternative starts with a literal, so a line
// holding none of them cannot match. Rendering a digest ran this per line over
// the whole session — 16.5 s of the 17 s a 240 MB render took (#1742).
func looksToolDump(line string) bool {
	for i := 0; i < len(line); i++ {
		if line[i] >= 0x80 {
			// Case folding is Unicode-wide in the engine (ſ folds to s), and
			// this prefilter is ASCII. A line with any byte outside ASCII goes
			// to the engine rather than being decided here.
			toolDumpEngineCalls.Add(1)
			return toolDumpRE.MatchString(line)
		}
	}
	for _, lit := range toolDumpLiterals {
		if containsFold(line, lit) {
			toolDumpEngineCalls.Add(1)
			return toolDumpRE.MatchString(line)
		}
	}
	return false
}

// looksNumbered is lineNumberRE — `^\s*\d{1,5}[:|]\s+` — read directly. It
// anchors at the start, so the whole line never needs scanning.
func looksNumbered(line string) bool {
	i := 0
	for i < len(line) && isRegexSpace(line[i]) {
		i++
	}
	digits := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' && digits < 6 {
		i++
		digits++
	}
	if digits == 0 || digits > 5 || i >= len(line) {
		return false
	}
	if line[i] != ':' && line[i] != '|' {
		return false
	}
	i++
	return i < len(line) && isRegexSpace(line[i])
}

// isRegexSpace is \s as the regexp engine reads it: [\t\n\f\r ]. Leaving out
// the ones a caller usually trims is how a fast path drifts from the pattern it
// stands in for.
func isRegexSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\f', '\r':
		return true
	}
	return false
}

func proseForSnippet(s string) string {
	s = redact.SafeForDisplay(s)
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || looksNumbered(line) || looksToolDump(line) || digest.IsListingDump(line) {
			continue
		}
		keep = append(keep, line)
	}
	out := strings.Join(keep, " ")
	out = strings.Join(strings.Fields(out), " ")
	if out == "" {
		out = strings.Join(strings.Fields(redact.SafeForDisplay(s)), " ")
	}
	return out
}

func contextText(s string, matched bool) string {
	s = redact.SafeForDisplay(s)
	if strings.Contains(s, "```") {
		return strings.TrimSpace(s)
	}
	if matched {
		return proseForSnippet(s)
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 8 {
		lines = lines[:8]
	}
	return proseForSnippet(strings.Join(lines, "\n"))
}

// TierRelevance mirrors query.TierRelevance for callers of this package.
const TierRelevance = query.TierRelevance

// ErrorHits renders the error-signature tier. The sessions arrive already
// ranked and already narrowed to the neighbourhood of the pasted error, so
// each becomes one hit whose snippets are those messages — re-scoring them
// against the paste's words (as the relevance tier would) throws away the
// passage the tier found and can render a real match as "0 matches".
func ErrorHits(ss []model.Session) []Hit {
	hits := make([]Hit, 0, len(ss))
	for rank, s := range ss {
		hit := Hit{Session: s, Tier: TierError, Count: len(s.Messages), Score: float64(len(ss) - rank)}
		for _, m := range s.Messages {
			if strings.TrimSpace(m.Text) == "" {
				continue
			}
			hit.Snippets = append(hit.Snippets, snippet(m.Text, "", nil))
			if len(hit.Snippets) == 2 {
				break
			}
		}
		hits = append(hits, hit)
	}
	return liftedNotes(hits)
}

// sessionHolds says whether a term appears anywhere in a session, stopping at
// the first message that has it. Joining a session's text into one string to
// ask this is quadratic in the number of messages, and these are transcripts.
func sessionHolds(s model.Session, t string) bool {
	folded := ""
	for _, m := range s.Messages {
		low := strings.ToLower(m.Text)
		if strings.Contains(low, t) {
			return true
		}
		if ft := cjkfold.String(t); ft != t || cjkfold.HasCJK(t) {
			if folded = cjkfold.String(low); strings.Contains(folded, ft) {
				return true
			}
		}
	}
	return false
}

// termWeights says what each query term is worth for picking an excerpt, from
// how much of the answer set holds it.
//
// The index knows the real idf and this layer does not, but it does not need
// to: the question here is only which of two messages in one session to show,
// and a term carried by every session that came back cannot be what separates
// them. A word in one result of fifty is what names that result; a word in all
// fifty is the shape of the question.
//
// Bounded below so a term the whole set shares still counts for something — it
// is weak evidence, not none — and so a message holding two common words can
// still beat one holding a single common word.
func termWeights(ss []model.Session, terms []string) map[string]float64 {
	if len(ss) == 0 {
		return nil
	}
	df := make(map[string]int, len(terms))
	for _, s := range ss {
		for _, t := range terms {
			if sessionHolds(s, t) {
				df[t]++
			}
		}
	}
	out := make(map[string]float64, len(terms))
	for _, t := range terms {
		out[t] = 0.2 + math.Log(float64(len(ss)+1)/float64(df[t]+1))
	}
	return out
}

// RelevanceHits wraps relevance-ranked sessions as hits WITHOUT re-scoring:
// the index already ordered them by IDF overlap, and exact-match BM25 (which
// just failed) must not reshuffle the ranking. Count and snippets come from
// term occurrences so output still shows why each session surfaced.
func RelevanceHits(ss []model.Session, terms []string) []Hit {
	return RelevanceHitsWeighted(ss, terms, nil)
}

// RelevanceHitsWeighted is RelevanceHits told what the ranking judged each term
// to be worth.
//
// The ranking picks a session by the idf mass of its best single message and
// then discards which message that was, so the excerpt has to work out the same
// answer again. Given the ranking's own weights it works out the same one;
// given nothing it estimates them from how much of the answer set holds each
// term, which is close but is an estimate.
func RelevanceHitsWeighted(ss []model.Session, terms []string, idf map[string]float64) []Hit {
	weight := idf
	if weight == nil {
		weight = termWeights(ss, terms)
	}
	hits := make([]Hit, 0, len(ss))
	for rank, s := range ss {
		hit := Hit{Session: s, Tier: TierRelevance}
		// Snippet the messages where the most query terms MEET, not the first
		// message that contains any one of them. The passage that answers a
		// question is where its words come together — a session about a gift
		// from a sister used to be shown for "what did my dad give me" because
		// "gift" matched first.
		//
		// Terms are worth what they distinguish, not one each. The ranker picks
		// a session by the idf mass of its best message and then discards which
		// message that was; counting terms equally here picked a different one
		// whenever a single rare word carried the session. "How many bikes do I
		// own?" ranked a session on "three bikes" and displayed "many people own
		// one", which is the passage an agent reads before deciding whether the
		// result is worth anything.
		type msgScore struct {
			idx      int
			distinct int
			weighted float64
			center   string
		}
		best := make([]msgScore, 0, 8)
		for mi, m := range s.Messages {
			// The same rule the scoring loop applies: a transcript's record of
			// a call to deja is not something anyone said, and this tier is
			// where the live miss came through — the scoring loop was fixed
			// first and the hit arrived here instead (#2067).
			low := strings.ToLower(withoutOwnCallLog(m.Text))
			var foldedLow string
			distinct := 0
			weighted := 0.0
			center := ""
			// The rarest term a message holds is what names it, so it is what
			// the excerpt gets centred on.
			heaviest := 0.0
			for _, t := range terms {
				found := strings.Contains(low, t)
				if !found {
					if ft := cjkfold.String(t); ft != t || cjkfold.HasCJK(t) {
						if foldedLow == "" {
							foldedLow = cjkfold.String(low)
						}
						found = strings.Contains(foldedLow, ft)
					}
				}
				if !found {
					continue
				}
				distinct++
				weighted += weight[t]
				if center == "" || weight[t] > heaviest {
					center, heaviest = t, weight[t]
				}
			}
			if distinct > 0 {
				hit.Count++
				best = append(best, msgScore{mi, distinct, weighted, center})
			}
		}
		// Heaviest first; a stable sort keeps message order among ties.
		sort.SliceStable(best, func(i, j int) bool { return best[i].weighted > best[j].weighted })
		for i := 0; i < len(best) && i < 2; i++ {
			hit.Snippets = append(hit.Snippets, snippet(s.Messages[best[i].idx].Text, best[i].center, nil))
		}
		hit.Score = float64(len(ss) - rank)
		hits = append(hits, hit)
	}
	return liftedNotes(hits)
}

// SafeText neutralises what a terminal acts on rather than prints. Transcript
// text arrives verbatim from a harness, and after `deja sync import` from
// another machine — the boundary the trust policy exists for. An escape byte
// recolours the rest of the screen, erases the line above or sets the window
// title; a bell rings on every redraw. The status bar (#634) and the brief
// titles have stripped these since they were written; the reading surfaces
// printed them raw (#1090).
//
// Newlines and tabs stay: unlike a one-line bar, these renderers are the
// session's own layout. Most format characters (Cf) are left alone here too:
// a zero-width joiner holds an emoji sequence together. The ones that reorder
// the line, and the ones that render as nothing at all, do not.
func SafeText(s string) string {
	if !strings.ContainsFunc(s, unsafeForTerminal) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unsafeForTerminal(r) {
			return ' '
		}
		return r
	}, s)
}

// SafeNote is SafeLine bounded to the length of an answer, for the one string
// on these screens that a person wrote at whatever length they liked. Printed
// whole, a 4000-character promote note came out as a 4051-column line on a
// screen budgeted to 80, and as 4074 of the 4347 bytes an agent got back
// (#1645). The full note stays readable where a whole note belongs: `deja show`
// on the promoted note prints it as a message.
func SafeNote(s string) string {
	return clip(SafeLine(s))
}

// noteStateCap bounds the bracketed tail SafeNoteTitle will carry past the
// clip. The longest state deja writes is " [superseded]" at 13 bytes; the room
// above that is for one deja does not know yet, from a store written by hand.
const noteStateCap = 24

// SafeNoteTitle is SafeNote for a promoted note's title, which ends in the state
// the note is in — "… [rejected]" — and that suffix is the part every one-line
// surface reads it for. Clipping from the left would drop exactly it (#R11), so
// the middle gives way instead.
func SafeNoteTitle(s string) string {
	line := SafeLine(s)
	state := ""
	// Only a short tail: the suffix is a state word, and exempting whatever
	// follows the last " [" would let a long bracketed tail carry the whole
	// title past the bound the clip is here to apply (#1645).
	if i := strings.LastIndex(line, " ["); i > 0 && strings.HasSuffix(line, "]") && len(line)-i <= noteStateCap {
		state, line = line[i:], line[:i]
	}
	return clip(line) + state
}

// SafeLine is SafeText confined to a single line, for the places that print
// an untrusted string as one row of something structured — a listing entry, a
// digest row, a "saved <path>" confirmation. A newline there ends deja's own
// line and starts a line of the caller's, which reads as deja's own output.
func SafeLine(s string) string {
	return strings.Join(strings.Fields(SafeText(s)), " ")
}

// SafePath is SafeLine for a path, which is an identifier rather than prose: it
// strips what a terminal would obey and keeps the result on one line, but the
// spaces inside a name are part of the name. Collapsing them made blame print
// "/tmp/app/two spaces.go" for a file with two, and restoring that printed path
// found nothing (#2044).
func SafePath(s string) string { return safeVerbatim(s, clipPath) }

// SafeCommand is the same treatment for a command, which is the other thing on
// these screens that a person copies and runs rather than reads. `deja how`
// printed `-run "Pool Size"` for a command that ran `-run "Pool  Size"`, which
// is a different test filter (#2052).
//
// Clipped from the other end than a path: a path is recognised by its tail, a
// command by the program it runs.
func SafeCommand(s string) string { return safeVerbatim(s, clipCommand) }

// safeVerbatim keeps what was recorded, minus what a terminal would obey and
// minus the line breaks that would let one row become two.
func safeVerbatim(s string, clip func(string) string) string {
	if s == "" {
		return ""
	}
	// SafeText leaves only newline and tab standing, and both have to go: this
	// is one row of something structured. One space each rather than a run
	// collapsed, which is the whole point.
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, SafeText(s))
	return clip(strings.Trim(cleaned, " "))
}

// pathCap bounds a printed path. Long enough for any real one, short enough
// that a transcript-supplied string cannot fill a terminal or an MCP payload.
const pathCap = 300

// clipCommand bounds a command from the right: what identifies a command is the
// program it runs, so the head is the part a reader cannot give up.
func clipCommand(s string) string {
	r := []rune(s)
	if len(r) <= pathCap {
		return s
	}
	return string(r[:pathCap-1]) + "…"
}

// clipPath bounds a path from the left: a path is recognised by its tail, and
// the head is what a reader gives up first.
func clipPath(s string) string {
	r := []rune(s)
	if len(r) <= pathCap {
		return s
	}
	return "…" + string(r[len(r)-pathCap+1:])
}

func unsafeForTerminal(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	if unicode.IsControl(r) {
		return true
	}
	// The bidi overrides and isolates are the same attack without an escape
	// byte: U+202E reverses the rendering of everything after it, so a line
	// can read as the opposite of the bytes stored. Nothing recalled from a
	// transcript needs to reorder the reader's screen. The other format
	// characters stay: a zero-width joiner holds an emoji sequence together.
	switch r {
	case '\u200e', '\u200f', '\u061c', // left/right/arabic marks
		'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
		'\u2066', '\u2067', '\u2068', '\u2069':
		return true
	}
	// Characters that render as nothing at all are the quiet version: text a
	// reader never sees and a model still reads. The tag block is a whole
	// invisible ASCII alphabet: "SYSTEM: ignore prior instructions" fits in
	// it and arrives in the agent's context looking like an empty string
	// (#1090). Only regional-flag sequences use tags for anything, and a plain
	// black flag is cheaper than an invisible instruction.
	switch {
	case r == '\u00ad', r == '\u200b', r == '\u2060', r == '\ufeff':
		return true
	case r >= '\U000e0000' && r <= '\U000e007f':
		return true
	}
	return false
}
