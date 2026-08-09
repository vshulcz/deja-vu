package search

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/redact"
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
func countIn(text, low string, qtoks, phrases []string, qlow string, variants map[string][]string) int {
	if !MatchesParts(text, qtoks, phrases, variants) {
		return 0
	}
	if len(qtoks) <= 1 && len(phrases) == 0 && variants == nil {
		if strings.Contains(low, qlow) {
			return strings.Count(low, qlow)
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
	qlow := strings.ToLower(o.Query)
	qtoks, phrases := QueryParts(o.Query)
	// Cross-script CJK: prepared once, used only when a raw match scores zero.
	queryCJK := cjkfold.HasCJK(o.Query)
	qlowFolded := cjkfold.String(qlow)
	qtoksFolded, phrasesFolded := QueryParts(cjkfold.String(o.Query))
	if o.Regex {
		var err error
		re, err = regexp.Compile("(?i)" + o.Query)
		if err != nil {
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
	}
	snipCands := make([]snipCand, 0, 16)
	df := make([]int, len(qtoks))
	corpusDocuments := 0
	corpusLength := 0
	for _, s := range merged {
		if o.Harness != "" && s.Harness != o.Harness {
			continue
		}
		if o.Project != "" && !strings.Contains(strings.ToLower(s.Project), strings.ToLower(o.Project)) {
			continue
		}
		if !cut.IsZero() && s.Updated.Before(cut) {
			continue
		}
		tier := o.Tier
		if tier == "" {
			tier = TierExact
		}
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
			low := strings.ToLower(m.Text)
			c := 0
			if re != nil {
				c = len(re.FindAllStringIndex(m.Text, -1))
			} else {
				c = countIn(m.Text, low, qtoks, phrases, qlow, o.FuzzyVariants)
				// Postings are keyed on Traditional-folded CJK, so a query in
				// one script legitimately reaches a record in the other. This
				// counting pass works on surface text and would score that
				// record zero, dropping it from the results the postings
				// already found — retry with both sides folded.
				if c == 0 && queryCJK {
					foldedLow := cjkfold.String(low)
					c = countIn(cjkfold.String(m.Text), foldedLow, qtoksFolded,
						phrasesFolded, qlowFolded, o.FuzzyVariants)
				}
			}
			if c > 0 {
				doc.hit.Count += c
				if doc.hit.Tier == TierClose && doc.hit.TierDetail == "" {
					doc.hit.TierDetail = variantDetail(m.Text, qtoks, o.FuzzyVariants)
				}
				// Collect every matching message with its match count; the
				// strongest few become the excerpts after the scan. Taking the
				// first three showed wherever a word happened to appear early
				// rather than the passage that carries the answer.
				snipCands = append(snipCands, snipCand{text: m.Text, weight: c})
				if w := tokenWindow(low, qtoks); w > 0 && (doc.minWindow == 0 || w < doc.minWindow) {
					doc.minWindow = w
				}
			}
			doc.length += countDocumentWords(low, qtoks, o.FuzzyVariants, doc.termCount, doc.userCount, m.Role == "user")
			if len(qtoks) == 1 && doc.termCount[0] == 0 && strings.Contains(low, qlow) {
				n := strings.Count(low, qlow)
				doc.termCount[0] += n
				if m.Role == "user" {
					doc.userCount[0] += n
				}
			}
		}
		// Strongest matches first, original order among equals, top three shown.
		sort.SliceStable(snipCands, func(i, j int) bool { return snipCands[i].weight > snipCands[j].weight })
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
		if n := worn[doc.hit.Session.ID]; n > 0 {
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
		if decidesSomething(doc.hit.Session) {
			score *= decisionBoost
		}
		// A pasted log outranks a human answer on term frequency alone: it
		// repeats the words fourteen times and says nothing. Measured before
		// this existed, the paste won every question of that shape.
		if looksPasted(doc.hit.Session) {
			score *= pastePenalty
		}
		score *= freshnessDecay(doc.hit.Session.Updated, now)
		doc.hit.Score = score
		hits = append(hits, doc.hit)
	}
	sortHits(hits)
	liftNotesAboveTheirSource(hits)
	return hits
}

// liftNotesAboveTheirSource keeps a promoted note in front of the transcript it
// was distilled from. The two say the same thing, so nothing is buried by the
// swap — but the note says it in one line with a state attached, and that is
// what `promote` promises. Score alone cannot deliver it: the transcript
// carries the query words in its title and the note does not, and once a note
// is dated by its evidence rather than by the day it was filed (V4) the two are
// equally fresh, so the transcript won its own distillation.
func liftNotesAboveTheirSource(hits []Hit) {
	notes := make(map[string]int, len(hits))
	for i, h := range hits {
		if h.Session.Harness == notesHarness && strings.HasPrefix(h.Session.ID, "deja-note-") {
			notes[h.Session.ID] = i
		}
	}
	if len(notes) == 0 {
		return
	}
	for i := 0; i < len(hits); i++ {
		h := hits[i]
		if h.Session.Harness == notesHarness {
			continue
		}
		// Mirrors sources.PromotedNoteID: building the id rather than parsing
		// one keeps a harness name with a dash in it from splitting wrong.
		id := "deja-note-" + h.Session.Harness + "-" + h.Session.ID
		j, ok := notes[id]
		if !ok || j < i {
			continue
		}
		note := hits[j]
		copy(hits[i+1:j+1], hits[i:j])
		hits[i] = note
		for k := i; k <= j; k++ {
			if hits[k].Session.Harness == notesHarness && strings.HasPrefix(hits[k].Session.ID, "deja-note-") {
				notes[hits[k].Session.ID] = k
			}
		}
	}
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

// tokenWindow returns the tightest character span of a message that contains
// every query token at least once (first occurrences — an approximation that
// costs nothing extra at scan time). 0 when some token is missing.
func tokenWindow(low string, qtoks []string) int {
	if len(qtoks) < 2 {
		return 0
	}
	first, last := -1, -1
	for _, tok := range qtoks {
		i := strings.Index(low, tok)
		if i < 0 {
			return 0
		}
		if first < 0 || i < first {
			first = i
		}
		if end := i + len(tok); end > last {
			last = end
		}
	}
	return last - first
}

// proximityBoost rewards documents where the query terms sit together: a
// window under ~200 chars reads as one thought, a spread across kilobytes is
// coincidence. Bounded at +35%.
func proximityBoost(window, queryTokenCount int) float64 {
	if window <= 0 || queryTokenCount < 2 {
		return 1
	}
	span := float64(window)
	boost := 1 + 0.35*(200/(200+span))
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
		head += ": " + SafeLine(h.LifecycleNote)
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

// wornBoost rewards sessions agents keep recalling — capped hard at +20% so
// popularity can never outrank relevance, only break near-ties.
func wornBoost(n int) float64 {
	boost := 1 + 0.05*math.Log2(float64(1+n))
	if boost > 1.2 {
		return 1.2
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
	for i := 0; i <= len(s); {
		isWord := false
		size := 0
		if i < len(s) {
			r, n := utf8.DecodeRuneInString(s[i:])
			size = n
			isWord = unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
		}
		if isWord && start < 0 {
			start = i
		} else if !isWord && start >= 0 {
			word := s[start:i]
			length++
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
	for _, h := range hits {
		d := "-"
		if !h.Session.Updated.IsZero() {
			d = relativeDate(h.Session.Updated)
		}
		// A day of notes is one session whose id *is* a date, minted in UTC.
		// The reader's zone put a different day on the line than the id beside
		// it, and only for deja's own buckets (#883).
		if day, ok := NoteBucketDay(h.Session); ok {
			d = relativeDate(noteBucketNoon(day))
		}
		// project and id are transcript/peer free text printed to a terminal;
		// an escape or bidi run in an imported project name would repaint the
		// line the way it would in blame. Snippets below are already SafeText'd.
		project := SafeLine(h.Session.Project)
		id := SafeLine(short(h.Session.ID))
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
			fmt.Fprintf(w, "  %s\n", highlight(SafeText(sn), o.Query, o.Regex, color))
		}
	}
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

func FindByPrefix(ss []model.Session, p string) (model.Session, bool) {
	for _, s := range mergeSessions(ss) {
		if strings.HasPrefix(s.ID, p) {
			return s, true
		}
	}
	return model.Session{}, false
}

func PrintSession(w io.Writer, s model.Session) {
	fmt.Fprintf(w, "# %s · %s · %s\n", s.Harness, s.Project, s.ID)
	for _, m := range s.Messages {
		txt := redact.SafeForDisplay(collapseTool(m.Text))
		if strings.TrimSpace(txt) == "" {
			continue
		}
		t := ""
		if !m.Time.IsZero() {
			t = m.Time.Format("2006-01-02 15:04") + " "
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

func PrintContext(w io.Writer, s model.Session, query string) {
	fmt.Fprintf(w, "# deja context: %s · %s · %s", s.Harness, s.Project, s.ID)
	if !s.Updated.IsZero() {
		fmt.Fprintf(w, " · updated %s", s.Updated.Format("2006-01-02"))
	}
	fmt.Fprintln(w)
	qlow := strings.ToLower(query)
	terms, phrases := QueryParts(query)
	budget := 8000
	// The reply to an included turn comes with it. Every user turn was kept
	// and every assistant turn had to match, but the decision lives in the
	// answer and is worded nothing like the question: `ctx "http client"`
	// handed an agent "the http client hammered the server on failure" and
	// dropped "we decided to cap retries at 3" from the turn below it. That
	// is the problem statement without its resolution (#R8).
	prevKept := false
	written := printContextChunks(w, s, budget, func(m model.Message) (bool, bool) {
		matched := qlow != "" && (strings.Contains(strings.ToLower(m.Text), qlow) || MatchesParts(m.Text, terms, phrases, nil))
		keep := matched || m.Role == "user" || (m.Role == "assistant" && prevKept)
		prevKept = keep
		return keep, matched
	})
	if written > 0 {
		return
	}
	// The session can match with query terms spread across messages, so no
	// single message qualifies above; show an overview instead of a bare header.
	if qlow != "" {
		fmt.Fprintf(w, "\nNo single message contains the full query; showing the session's opening exchange.\n")
	}
	printContextChunks(w, s, budget, func(m model.Message) (bool, bool) { return true, false })
}

func printContextChunks(w io.Writer, s model.Session, budget int, include func(m model.Message) (ok, matched bool)) int {
	// A digest is what someone pipes into a prompt, so it carries the
	// conversation. The work records — tool output, the files a turn touched, the
	// commands it ran, the spans it replaced — are indexed and searchable by role
	// and are not what anyone means by context; before they were labelled
	// honestly (#560) they arrived as `user` and filled this with `## tool-output`
	// blocks. Collect the kept turns in one ordered pass so the include callback's
	// prevKept bookkeeping still runs left to right.
	type chunk struct {
		text    string
		matched bool
	}
	var chunks []chunk
	for _, m := range s.Messages {
		if isWorkRecord(m.Role) {
			continue
		}
		ok, matched := include(m)
		if !ok {
			continue
		}
		text := SafeText(contextText(m.Text, matched))
		if strings.TrimSpace(text) == "" {
			continue
		}
		chunks = append(chunks, chunk{fmt.Sprintf("\n## %s\n\n%s\n", m.Role, text), matched})
	}
	if len(chunks) == 0 {
		return 0
	}
	// Anchor the budget window on the first matched turn (with one turn of
	// lead-in, usually the question when the answer is what matched). Walking from
	// the top instead let earlier scaffolding crowd the match out of budget: a
	// query whose answer sat deep in a long session got 8KB of unrelated opening
	// and none of the turns it found (#R8-budget). No match means the fallback
	// overview, which wants the session's start, so it stays anchored at 0.
	start := 0
	for i, c := range chunks {
		if c.matched {
			start = i
			if start > 0 {
				start--
			}
			break
		}
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

func snippet(s, q string, re *regexp.Regexp) string {
	s = proseForSnippet(s)
	r := []rune(s)
	idx := 0
	if re != nil {
		loc := re.FindStringIndex(s)
		if loc != nil {
			idx = utf8.RuneCountInString(s[:loc[0]])
		}
	} else {
		low := strings.ToLower(s)
		b := strings.Index(low, strings.ToLower(q))
		if b < 0 {
			for _, tok := range query.Tokens(q) {
				if p := strings.Index(low, tok); p >= 0 && (b < 0 || p < b) {
					b = p
				}
			}
		}
		if b > 0 {
			idx = utf8.RuneCountInString(s[:b])
		}
	}
	start := idx - 100
	if start < 0 {
		start = 0
	}
	end := start + 300
	if end > len(r) {
		end = len(r)
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
			return re.ReplaceAllStringFunc(s, func(x string) string { return cMatch + x + cReset })
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
)

func proseForSnippet(s string) string {
	s = redact.SafeForDisplay(s)
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || lineNumberRE.MatchString(line) || toolDumpRE.MatchString(line) {
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
	return hits
}

// RelevanceHits wraps relevance-ranked sessions as hits WITHOUT re-scoring:
// the index already ordered them by IDF overlap, and exact-match BM25 (which
// just failed) must not reshuffle the ranking. Count and snippets come from
// term occurrences so output still shows why each session surfaced.
func RelevanceHits(ss []model.Session, terms []string) []Hit {
	hits := make([]Hit, 0, len(ss))
	for rank, s := range ss {
		hit := Hit{Session: s, Tier: TierRelevance}
		// Snippet the messages where the most query terms MEET, not the first
		// message that contains any one of them. The passage that answers a
		// question is where its words come together — a session about a gift
		// from a sister used to be shown for "what did my dad give me" because
		// "gift" matched first. Score each message by distinct terms hit; the
		// best two become the excerpts an agent reads to decide.
		type msgScore struct {
			idx, distinct int
			center        string
		}
		best := make([]msgScore, 0, 8)
		for mi, m := range s.Messages {
			low := strings.ToLower(m.Text)
			var foldedLow string
			distinct := 0
			center := ""
			for _, t := range terms {
				if strings.Contains(low, t) {
					distinct++
					if center == "" {
						center = t
					}
					continue
				}
				if ft := cjkfold.String(t); ft != t || cjkfold.HasCJK(t) {
					if foldedLow == "" {
						foldedLow = cjkfold.String(low)
					}
					if strings.Contains(foldedLow, ft) {
						distinct++
						if center == "" {
							center = t
						}
					}
				}
			}
			if distinct > 0 {
				hit.Count++
				best = append(best, msgScore{mi, distinct, center})
			}
		}
		// Most distinct terms first; a stable sort keeps message order among ties.
		sort.SliceStable(best, func(i, j int) bool { return best[i].distinct > best[j].distinct })
		for i := 0; i < len(best) && i < 2; i++ {
			hit.Snippets = append(hit.Snippets, snippet(s.Messages[best[i].idx].Text, best[i].center, nil))
		}
		hit.Score = float64(len(ss) - rank)
		hits = append(hits, hit)
	}
	return hits
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

// SafeLine is SafeText confined to a single line, for the places that print
// an untrusted string as one row of something structured — a listing entry, a
// digest row, a "saved <path>" confirmation. A newline there ends deja's own
// line and starts a line of the caller's, which reads as deja's own output.
func SafeLine(s string) string {
	return strings.Join(strings.Fields(SafeText(s)), " ")
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
