// Package search ranks sessions against a query and prints what it finds.
package search

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
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

// The tier names and sort orders, re-exported from internal/query.
const (
	TierExact    = query.TierExact
	TierClose    = query.TierClose
	TierSemantic = query.TierSemantic
	TierStemmed  = "stemmed"
	TierError    = query.TierError

	SortRelevance = query.SortRelevance
	SortDate      = query.SortDate
)

// ByDate puts hits newest first, keeping the ranked order between sessions
// of the same moment. On the relevance tier the sessions that hold every
// word stay ahead of the ones holding some, each part newest first: dated in
// one run, a tail of newer partial matches pushed the real ones off the page.
func ByDate(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Strict != hits[j].Strict {
			return hits[i].Strict
		}
		return hits[i].Session.Updated.After(hits[j].Session.Updated)
	})
}

// QueryParts is query.QueryParts.
func QueryParts(q string) (terms []string, phrases []string) { return query.QueryParts(q) }

// IsStopWord is query.IsStopWord.
func IsStopWord(term string) bool { return query.IsStopWord(term) }

// MatchesQuery is query.MatchesQuery.
func MatchesQuery(text, q string) bool { return query.MatchesQuery(text, q) }

// MatchesParts is query.MatchesParts.
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
	Total int `json:"total"`
	// TotalEstimated says total counts sessions the search did not read by an
	// estimate: the text check turned down sessions the postings named (a
	// quoted phrase over common words), and the unread ones were too many to
	// check.
	TotalEstimated bool `json:"total_estimated,omitempty"`
	// Strict is how many of the hits hold every word of the query. It appears
	// on the relevance tier, where a thin strict answer is merged into the
	// ranking and published under a label that means nothing matched: a
	// caller counting recall reads this rather than dropping the answer
	// (#3815). Omitted when none did, which is the ordinary relevance case.
	Strict   int                 `json:"strict,omitempty"`
	Capped   bool                `json:"capped,omitempty"`
	Withheld int                 `json:"policy_withheld,omitempty"`
	Hits     []Hit               `json:"hits"`
	Fuzzy    bool                `json:"fuzzy,omitempty"`
	Stemmed  bool                `json:"stemmed,omitempty"`
	Semantic bool                `json:"semantic,omitempty"`
	Variants map[string][]string `json:"variants,omitempty"`
}

// Hit is one session that matched a search.
type Hit struct {
	Session    model.Session `json:"session"`
	Count      int           `json:"count"`
	Snippets   []string      `json:"snippets"`
	Score      float64       `json:"score"`
	Tier       string        `json:"tier"`
	TierDetail string        `json:"tier_detail,omitempty"`
	// Strict marks a hit on the relevance tier that holds every word of the
	// query: the answer there is a thin strict head merged into the ranking,
	// and the tier label alone cannot tell the two apart (#3815). The order
	// is the merged ranking's, so these are not the first hits.
	Strict bool `json:"strict,omitempty"`
	// Superseded holds the date of a newer session in the same project whose
	// matches overlap this one — a signal that this hit is an earlier attempt.
	Superseded string `json:"superseded,omitempty"`
	// Reused counts recent agent recalls that served this session.
	Reused int `json:"reused,omitempty"`
	// Lifecycle carries what was later recorded about this session: that its
	// decision was rejected, superseded or has gone stale. A hit on a raw
	// transcript used to arrive with no trace of that, so a decision someone
	// had explicitly reverted came back reading like current truth.
	// MessagesTotal is how many messages the session holds, and MessagesCapped
	// says the list below it is the matched passages rather than all of them.
	// A hit used to carry every message of its session: on a real store one
	// relevance answer was 136 MB over 50 hits and 140,841 messages, its
	// largest hit 53 MB, because the size of an answer was the size of the
	// reader's longest transcript. `show --json` is the surface for a whole
	// session and has a documented window; a hit is about what matched (#3620).
	MessagesTotal  int  `json:"messages_total,omitempty"`
	MessagesCapped bool `json:"messages_capped,omitempty"`
	// Moved is set by the CLI when some of the files this session touched have
	// commits since it ended. Computing it means forking git, which this
	// package does not do, so it arrives filled in rather than derived here.
	Moved         string `json:"moved,omitempty"`
	Lifecycle     string `json:"lifecycle,omitempty"`
	LifecycleNote string `json:"lifecycle_note,omitempty"`
	LifecycleAt   string `json:"lifecycle_at,omitempty"`
	// matched are the indices of the messages that carried the match, in
	// message order. The scorers compute them anyway; keeping them is what
	// lets the JSON serve the passages a hit is about instead of a transcript.
	matched []int
	// snipTexts are the passages this hit would quote, chosen during scoring
	// and rendered only if the hit survives the cap. Rendering them in the
	// scoring loop made a search cost the same at --limit 1 and --limit 100:
	// 806 ms either way on a 31 MB store, with the quoting a third of the
	// 127.8 MB one query allocated (#3544). The strings are the record text
	// already in memory, so holding them costs a header apiece.
	snipTexts []string
	// quoted are the messages the rendered excerpts were cut from, kept so a
	// caller can find where in the session the hit is about.
	quoted []string
}

// QuotedMessages are the texts of the messages this hit's excerpts were cut
// from, in the order the excerpts appear.
func (h Hit) QuotedMessages() []string { return h.quoted }

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
func countIn(_, low string, qtoks, phrases []string, variants map[string][]string) int {
	if !query.MatchesLowered(low, qtoks, phrases, variants) {
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

// snipCand is one matching message, a candidate for the hit's excerpts.
type snipCand struct {
	text   string
	weight int
	// window is how tightly the query's words meet in this message; 0 means
	// they never do.
	window int
	// at is where in the session this was said: the excerpts are chosen by
	// how well they match, so the strongest passage is often not the last
	// word the session had on the subject.
	at   int
	when time.Time
	// dump is tool output that is a JSON document or a listing: it holds
	// the query's words as entries, not as anything said about them.
	dump bool
}

// scoredQuery is the query as the exact tier counts it, with the CJK-folded
// parts used when a raw match scores zero.
type scoredQuery struct {
	re                         *regexp.Regexp
	qtoks, phrases             []string
	queryCJK                   bool
	qtoksFolded, phrasesFolded []string
}

func runScored(ss []model.Session, o Options) ([]Hit, error) {
	var re *regexp.Regexp
	qtoks, phrases := QueryParts(o.Query)
	// Cross-script CJK: prepared once, used only when a raw match scores zero.
	queryCJK := cjkfold.HasCJK(o.Query)
	qtoksFolded, phrasesFolded := QueryParts(cjkfold.String(o.Query))
	if o.Regex {
		var err error
		re, err = compileQueryRegex(o.Query)
		if err != nil {
			// The (?im) prefix is deja's, not the user's. Reporting the compile
			// error verbatim echoes `(?im)(` back at someone who typed `(`, so
			// re-compile their pattern alone for a message that names what they
			// actually wrote.
			if _, uerr := regexp.Compile(o.Query); uerr != nil {
				return nil, uerr
			}
			return nil, err
		}
	}
	sq := scoredQuery{re: re, qtoks: qtoks, phrases: phrases, queryCJK: queryCJK, qtoksFolded: qtoksFolded, phrasesFolded: phrasesFolded}
	now := o.Clock()
	cut := time.Time{}
	if o.Since > 0 {
		cut = now.Add(-o.Since)
	}
	merged := mergeSessions(ss)
	documents := make([]bm25Document, 0, len(merged))
	// Reused across sessions: each one refills it, so a single allocation serves all.
	snipCands := make([]snipCand, 0, 16)
	df := make([]int, len(qtoks))
	corpusDocuments := 0
	corpusLength := 0
	// The sessions read to check the estimate retrieval counted the unread
	// ones at, measured apart.
	sampleDF := make([]int, len(qtoks))
	sampleDocuments, sampleLength, sampleEstimate := 0, 0, 0.0
	for _, s := range merged {
		if o.Harness != "" && s.Harness != o.Harness {
			continue
		}
		if !query.ProjectAnyMatches(s.Project, s.From, o.ProjectWants()) {
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
			titleLow := lower(s.Title)
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
		snipCands = sq.scanMessages(&doc, s, o, snipCands)
		pickSnippets(&doc, snipCands)
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
		var sampled bool
		if o.Unread != nil {
			var est float64
			est, sampled = o.Unread.Sample[doc.hit.Session.Harness+":"+doc.hit.Session.ID]
			if sampled {
				sampleDocuments++
				sampleLength += doc.length
				sampleEstimate += est
			}
		}
		for i, n := range doc.termCount {
			if n > 0 {
				df[i]++
				if sampled {
					sampleDF[i]++
				}
			}
		}
		if doc.hit.Count > 0 {
			documents = append(documents, doc)
		}
	}
	// The sessions retrieval did not read still belong to the corpus: their
	// count and length are what BM25's figures are made of, and leaving them
	// out would score the pool as if it were the whole match. Their length is
	// an estimate, scaled by how far the same estimate was off on the sample
	// read alongside the pool.
	if u := o.Unread; u != nil && u.Sessions > 0 && corpusDocuments > 0 {
		scale := 1.0
		share := func(i int) float64 { return float64(df[i]) / float64(corpusDocuments) }
		if sampleDocuments > 0 {
			if sampleEstimate > 0 {
				scale = float64(sampleLength) / sampleEstimate
			}
			share = func(i int) float64 { return float64(sampleDF[i]) / float64(sampleDocuments) }
		}
		corpusLength += int(u.Length * scale)
		for i := range df {
			df[i] += int(math.Round(share(i) * float64(u.Sessions)))
		}
		corpusDocuments += u.Sessions
	}
	avgLength := 0.0
	if corpusDocuments > 0 {
		avgLength = float64(corpusLength) / float64(corpusDocuments)
	}
	hits := scoreBM25At(documents, df, corpusDocuments, avgLength, len(qtoks), o.RecallWorn, now)
	// markEarlierAttempts reads the quoted text of the top 50, so those are
	// rendered here; everything below them waits for the cap.
	renderSnippets(hits, o, earlierAttemptWindow)
	markEarlierAttemptsAt(hits, now)
	return hits, nil
}

// scanMessages counts the query in each message of s into doc and collects the
// messages that matched, refilling snipCands.
func (sq scoredQuery) scanMessages(doc *bm25Document, s model.Session, o Options, snipCands []snipCand) []snipCand {
	re, qtoks, phrases := sq.re, sq.qtoks, sq.phrases
	queryCJK, qtoksFolded, phrasesFolded := sq.queryCJK, sq.qtoksFolded, sq.phrasesFolded
	snipCands = snipCands[:0]
	for mi, m := range s.Messages {
		if m.Role == "wrote" {
			continue
		}
		if o.Role != "" && !roleMatches(m.Role, o.Role) {
			continue
		}
		// A harness's summary of what it compacted away answers a question
		// only when the question asked for it. Left in the ordinary pool it
		// is a paraphrase of the conversation competing with the
		// conversation — and on one store those summaries were a fifth of
		// everything indexed in the sessions that had them (#3384).
		if o.Role == "" && m.Role == roleSummary {
			continue
		}
		// The transcript's own record of a call to deja is not something
		// anyone said, and a question matches the log of that question
		// being asked (#2067). Removed from matching only; the line stays
		// in the transcript for `deja how` and `deja fix`.
		text := withoutOwnCallLog(m.Text)
		low := lower(text)
		var c int
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
			snipCands = append(snipCands, snipCand{text: text, weight: c, window: w, at: mi, when: m.Time,
				dump: m.Role == roleToolOutput && isDataDump(text)})
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
	return snipCands
}

// pickSnippets orders the matching messages and keeps the three the hit quotes.
func pickSnippets(doc *bm25Document, snipCands []snipCand) {
	// The passage that answers a question is where its words come together, so
	// a message that says them together outranks one that repeats a single word:
	// a search for "what did my dad give me" led with six "gift"s and put the
	// line naming the dad second (#1325). The relevance tier already picks its
	// excerpt this way; the exact tier ranked on the raw count alone. Count
	// still decides between passages that are equally tight, and among equals
	// the order they were said in stands. Top three shown.
	//
	// A JSON or listing dump in tool output is quoted only when nothing
	// else matched: its words meet tightly because it holds every word,
	// and it took the excerpts of a session that was 4 MB of them (#4780).
	sort.SliceStable(snipCands, func(i, j int) bool {
		a, b := snipCands[i], snipCands[j]
		if a.dump != b.dump {
			return !a.dump
		}
		if (a.window > 0) != (b.window > 0) {
			return a.window > 0
		}
		if a.window > 0 && a.window != b.window {
			return a.window < b.window
		}
		return a.weight > b.weight
	})
	// The last passage that matched, whether or not it matched hardest. A
	// long session is answered by the fragment that scored, and twice now
	// the fragment that answered was a different one in the same session:
	// a status line saying the PR had already been opened and closed, and
	// the ClawHub submission going through. Both were the session's later
	// word on the subject, and both cost a duplicate outward action
	// (#3007, #2976). It takes the third slot, so the two strongest
	// passages are still first.
	lastIdx := -1
	for i, c := range snipCands {
		if lastIdx < 0 || c.at > snipCands[lastIdx].at {
			lastIdx = i
		}
	}
	for _, c := range snipCands {
		doc.hit.matched = append(doc.hit.matched, c.at)
	}
	shown := -1
	for i := 0; i < len(snipCands) && i < 3; i++ {
		pick := i
		if i == 2 && lastIdx >= 0 && snipCands[lastIdx].at > snipCands[0].at && snipCands[lastIdx].at > snipCands[1].at {
			pick = lastIdx
		}
		doc.hit.snipTexts = append(doc.hit.snipTexts, snipCands[pick].text)
		if snipCands[pick].at > shown {
			shown = snipCands[pick].at
		}
	}
}

// renderSnippets quotes the hits that survived the cap, and only those.
//
// Choosing the passages is part of scoring — a session is ranked partly on how
// tightly its words come together — but turning a passage into the few hundred
// runes a reader sees is not: it splits the text, scans it and allocates, and
// doing that for every session that matched a word made a search cost the same
// at --limit 1 and --limit 100 (#3544).
func renderSnippets(hits []Hit, o Options, n int) {
	var re *regexp.Regexp
	if o.Regex {
		// Already compiled once in runScored, and a hit only reaches here if
		// that compile succeeded, so a failure now is not reportable and not
		// possible: fall back to the plain highlighter.
		re, _ = compileQueryRegex(o.Query)
	}
	if n < 0 || n > len(hits) {
		n = len(hits)
	}
	for i := range n {
		if len(hits[i].snipTexts) == 0 {
			continue
		}
		for _, text := range hits[i].snipTexts {
			// A message that was only deja's credit line has no words left
			// once it is stripped, and printed as an empty bullet (#4247).
			if sn := snippet(text, o.Query, re); sn != "" {
				hits[i].Snippets = append(hits[i].Snippets, sn)
				hits[i].quoted = append(hits[i].quoted, text)
			}
		}
		hits[i].snipTexts = nil
	}
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
	// Estimated says Total counts sessions retrieval did not read by an
	// estimate (query.Unread.Estimated).
	Estimated bool
}

// RunDetailed is Run plus the numbers the cap would otherwise hide.
func RunDetailed(ss []model.Session, o Options) (Results, error) {
	return RunDetailedKeeping(ss, o, nil)
}

// RunDetailedKeeping is RunDetailed with keep applied to the ranked hits
// before the cap, so what it drops neither fills a slot nor counts in the
// total.
func RunDetailedKeeping(ss []model.Session, o Options, keep func([]Hit) []Hit) (Results, error) {
	hits, err := runScored(ss, o)
	if err != nil {
		return Results{}, err
	}
	if keep != nil {
		hits = keep(hits)
	}
	r := Results{Total: len(hits), Tier: setTier(o)}
	if o.Sort == SortDate {
		ByDate(hits)
		// The earlier-attempt marks were taken over the ranking's head; the
		// page is the date order's head now.
		renderSnippets(hits, o, earlierAttemptWindow)
		markEarlierAttempts(hits)
		// The sample read to check the unread estimate is older than every
		// session of a date pool, and the unread ones between them were not
		// read: a sampled hit on the page skips them. It still counts.
		if u := o.Unread; u != nil && (u.Sessions > 0 || u.Estimated) && len(u.Sample) > 0 {
			kept := hits[:0:0]
			for _, h := range hits {
				if _, sampled := u.Sample[h.Session.Harness+":"+h.Session.ID]; !sampled {
					kept = append(kept, h)
				}
			}
			if len(kept) < len(hits) {
				r.Capped = true
			}
			hits = kept
		}
	}
	var capped bool
	r.Hits, capped = CapHits(hits, o.Limit, o.All)
	r.Capped = r.Capped || capped
	// The unread sessions matched as well. What the trust policy withholds
	// on Options.Activation is already out of them, so the total does not
	// count a session the page could never show.
	if o.Unread != nil && o.Unread.Sessions > 0 {
		r.Total += o.Unread.Sessions
		r.Capped = true
	}
	// An estimate of none left over is still one: the sample held no match,
	// which says few, not none, and --all reads them all.
	if o.Unread != nil && o.Unread.Estimated {
		r.Estimated, r.Capped = true, true
	}
	renderSnippets(r.Hits, o, -1)
	return r, nil
}

// DefaultLimit is how many hits a search shows when no flag says.
const DefaultLimit = 15

// CapHits bounds what a search hands back, with the default a reader gets when
// they pass no flag. It lives here rather than inside RunDetailed because the
// tiers that build their own hits — relevance, error signature — never went
// through that path and printed the whole retrieval window instead: fifty
// sessions for `--limit 3`, and fifty for no flag at all (#3345).
func CapHits(hits []Hit, limit int, all bool) ([]Hit, bool) {
	if limit == 0 && !all {
		limit = DefaultLimit
	}
	if limit > 0 && len(hits) > limit {
		return hits[:limit], true
	}
	return hits, false
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

// earlierAttemptWindow bounds the comparison below: how many ranked hits are
// checked against each other, and so how many have to be quoted first.
const earlierAttemptWindow = 50

// markEarlierAttempts flags hits that look like older passes over the same
// problem: same project, heavy overlap in what matched, and a newer session
// above some margin. The old session stays in the results — history is the
// product — but agents and readers see which one the project moved on to.
func markEarlierAttempts(hits []Hit) {
	markEarlierAttemptsAt(hits, (Options{}).Clock())
}

func markEarlierAttemptsAt(hits []Hit, now time.Time) {
	n := min(len(hits), earlierAttemptWindow)
	sets := make([]map[string]bool, n)
	for i := range n {
		set := map[string]bool{}
		for _, sn := range hits[i].Snippets {
			for w := range strings.FieldsSeq(lower(sn)) {
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
	for i := range n {
		for j := range n {
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
			if b.Session.Updated.After(now) {
				continue
			}
			ov := snippetOverlap(sets[i], sets[j])
			if ov < 0.6 && !(ov > 0 && sharedTouchedTarget(a.Session, b.Session)) {
				continue
			}
			hits[i].Superseded = b.Session.Updated.Local().Format("2006-01-02")
		}
	}
}

func sharedTouchedTarget(a, b model.Session) bool {
	af := sessionTouchedFiles(a)
	if len(af) == 0 {
		return false
	}
	bf := sessionTouchedFiles(b)
	if len(bf) == 0 {
		return false
	}
	seen := make(map[string]bool, len(af))
	for _, p := range af {
		seen[p] = true
	}
	for _, p := range bf {
		if seen[p] {
			return true
		}
	}
	return false
}

func sessionTouchedFiles(s model.Session) []string {
	var out []string
	for _, p := range s.Touched {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	for _, m := range s.Messages {
		if m.Role != "edit" && m.Role != "wrote" {
			continue
		}
		p, _, _ := strings.Cut(m.Text, "\n")
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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
	return scoreBM25At(documents, df, corpusDocuments, avgLength, queryTokenCount, worn, (Options{}).Clock())
}

func scoreBM25At(documents []bm25Document, df []int, corpusDocuments int, avgLength float64, queryTokenCount int, worn map[string]int, now time.Time) []Hit {
	emptyQuery := queryTokenCount == 0
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
	liftNotesBy(hits, func(h Hit) model.Session { return h.Session })
}

// indexNotes is where each promoted note sits, by the id it names, in rank
// order. Every hit holding an id rather than one of them: two machines can
// promote the same session, and keeping only the last put the peer's copy
// above this machine's own note.
func indexNotes[T any](hits []T, of func(T) model.Session) map[string][]int {
	notes := make(map[string][]int, len(hits))
	for i, h := range hits {
		if id := noteID(of(h)); id != "" {
			notes[id] = append(notes[id], i)
		}
	}
	return notes
}

// noteID is the id a promoted note has on the machine that made it, and "" for
// a session that is not one. Mirrors cmd/deja's promotedNoteID: after a sync
// the local id is `imported-…` and the real one rides in OrigID (#975, #2833).
func noteID(s model.Session) string {
	if s.Harness != notesHarness {
		return ""
	}
	if strings.HasPrefix(s.ID, "deja-note-") {
		return s.ID
	}
	if strings.HasPrefix(s.OrigID, "deja-note-") {
		return s.OrigID
	}
	return ""
}

// sessionOrigID is the id a session had where it was recorded.
func sessionOrigID(s model.Session) string {
	if s.OrigID != "" {
		return s.OrigID
	}
	return s.ID
}

// liftNotesBy is the rule itself, over anything that names a session: blame
// ranks its own hit type and had the same omission (#2829).
func liftNotesBy[T any](hits []T, of func(T) model.Session) {
	notes := indexNotes(hits, of)
	if len(notes) == 0 {
		return
	}
	// One pass, emitting each source's notes just before it. Reordering in
	// place meant reindexing after every rotation, which is a map rebuild per
	// pair over the whole matched set — measured at 622ms and a gigabyte on a
	// store where every transcript outranked its own note (#2833).
	done := make([]bool, len(hits))
	out := make([]T, 0, len(hits))
	for i := range hits {
		src := of(hits[i])
		if src.Harness != notesHarness {
			// Both ids. The note names the session as it was on the machine
			// that made it — the local id where this machine promoted it, the
			// original where the note travelled — and after a sync a session
			// carries `imported-…` locally with the original in OrigID (#2833).
			//
			// Every copy that has not been placed yet, in rank order, so two
			// machines' notes about one session both land above it and this
			// machine's own comes first.
			for _, id := range []string{
				"deja-note-" + src.Harness + "-" + src.ID,
				"deja-note-" + src.Harness + "-" + sessionOrigID(src),
			} {
				for _, j := range notes[id] {
					if j > i && !done[j] {
						done[j] = true
						out = append(out, hits[j])
					}
				}
			}
		}
		if !done[i] {
			done[i] = true
			out = append(out, hits[i])
		}
	}
	copy(hits, out)
}

// LiftNoteSessionsAboveTheirSource is the same rule for a caller that ranks
// sessions and never builds a Hit. The per-prompt hook is one: it had no
// note-over-source rule at all, so the transcript a note was distilled from
// went into the block ahead of the note (#2803).
func LiftNoteSessionsAboveTheirSource(ss []model.Session) {
	liftNotesBy(ss, func(s model.Session) model.Session { return s })
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

// freshnessFloor bounds how much a session's age can cost it. The decay used
// to be 1/(1+age), which keeps 3% of a score after a month and 0.3% after a
// year; BM25 does not span three orders of magnitude, so the exact tier
// ordered by date and let topicality break the ties, the opposite of what it
// says it does (#1269). At a floor of 0.5 age can halve a score, not erase it:
// recency still leads between answers that say the same thing, and a session
// that answers the question outranks a newer one that only mentions it.
const freshnessFloor = 0.5

// freshnessDecay weighs a session's age between freshnessFloor and 1.
func freshnessDecay(updated, now time.Time) float64 {
	if updated.IsZero() {
		return 0
	}
	age := now.Sub(updated).Hours() / 24
	if age <= 0 {
		return 1
	}
	return freshnessFloor + (1-freshnessFloor)/(1+age)
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

// AbandonmentWorthSaying reports whether the give-up mark tells a reader
// something about this hit. Exported because the MCP recall answer words the
// same mark ("[this session abandoned one approach partway …]") and was asking
// the same question with no rule behind it: over sixteen recall calls on a real
// store it printed the mark 32 times in 16 answers, two per page.
//
// GaveUp is a property of the whole session: one line anywhere saying something
// was dropped marks all of it. On a short session that is useful — "one path
// here was abandoned, check the excerpts for which" names a real thing to look
// for. On a marathon it is certain and therefore empty: measured over twelve
// real queries, the mark sat on 23 of 60 hits, and the sessions carrying it were
// this machine's longest — 4.2 million words, 3.1 million, 2.0 million. Of
// course something was dropped somewhere in four million words.
//
// The same reasoning the rest of this file applies to a haystack: the claim has
// to be about something the reader can act on. So the mark stands on a session
// small enough to scan and goes on one where "somewhere in here" names no place.
//
// Size is the whole test on purpose. The sharper rule — does a give-up line sit
// in the excerpts this hit shows — needs the phrase list that recognises one,
// and that list lives in internal/index, which cannot be imported here.
// Re-spelling it in this package is the mistake #3473 just fixed in the line
// above: two recognisers for one idea, and the narrower one wins by accident.
// A session with no recorded length keeps the mark: an unknown is not a
// marathon.
func AbandonmentWorthSaying(h Hit) bool {
	return h.Session.Words == 0 || h.Session.Words <= abandonmentScannableWords
}

// abandonmentScannableWords is how long a session can be before "somewhere in
// here" stops being a place. Thirty thousand words is a long working session and
// still a thing a reader can search; this machine's marathons are two orders
// above it.
const abandonmentScannableWords = 30000

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

// roleSummary is a harness's digest of a conversation it compacted away. Kept
// in the index — it is the only record of the turns that went — and served only
// when a caller asks for it by role (#3384).
const roleSummary = "summary"

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
		low := lower(m.Text)
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
// measurement (internal/search reuse tests): at the old
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
				if cjkfold.Unspaced(r) {
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
					if slices.Contains(variants[term], word) {
						matched = true
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
	var order []string
	for _, s := range in {
		k := s.Harness + ":" + s.ID
		if by[k] == nil {
			cp := s
			by[k] = &cp
			order = append(order, k)
		} else {
			by[k].Messages = append(by[k].Messages, s.Messages...)
			by[k].Touch(s.Updated)
			if by[k].Project == "history" {
				by[k].Project = s.Project
			}
		}
	}
	// In first-seen order, so equal scores further on do not tie-break on map
	// iteration.
	out := make([]model.Session, 0, len(by))
	for _, k := range order {
		out = append(out, *by[k])
	}
	return out
}

// jsonHitMessages is how many messages one hit may carry. Three excerpts is
// what the text path shows; twenty leaves room for the passages around them
// and still bounds an answer a script has to hold in memory.
const jsonHitMessages = 20

// boundedHit is one hit with its session's message list reduced to the
// passages that matched.
//
// The whole list was encoded before: 136 MB for one relevance answer on a real
// store, a second of the 2.6 it took, and half a gigabyte of resident memory —
// all of it text the caller did not ask about, since a hit's own excerpts are
// in `snippets` and a whole session is what `show --json` is for (#3620).
//
// A tier that records no indices — nothing in tree does, but the field is
// unexported and a future one might not — keeps the newest messages, where a
// session says what it settled.
func boundedHit(h Hit) Hit {
	var total int
	h.Session.Messages, total = boundedSelection(h.Session, h.matched)
	if total > 0 {
		h.MessagesTotal, h.MessagesCapped = total, true
	}
	return h
}

// boundedSelection is a session's messages bounded to the matched passages,
// and the session's whole count when what is kept is a selection of it; zero
// when the whole session fits.
func boundedSelection(s model.Session, matched []int) ([]model.Message, int) {
	if len(s.Messages) == 0 {
		return s.Messages, 0
	}
	whole := max(len(s.Messages), s.Held)
	kept, _ := boundedMessages(s.Messages, matched)
	if len(kept) < whole {
		return kept, whole
	}
	return kept, 0
}

// boundedMessages keeps the messages at the given indices, in message order,
// at most jsonHitMessages of them.
//
// No indices — nothing in tree, but the field is unexported and a future tier
// might not set it — keeps the newest, where a session says what it settled.
func boundedMessages(ms []model.Message, matched []int) ([]model.Message, bool) {
	total := len(ms)
	keep := make([]int, 0, len(matched)*2)
	for _, i := range matched {
		// The match and the line after it: a question is answered by what
		// follows, which is why the MCP listing attaches exactly that (#1163).
		keep = append(keep, i, i+1)
	}
	if len(matched) == 0 {
		from := max(total-jsonHitMessages, 0)
		for i := from; i < total; i++ {
			keep = append(keep, i)
		}
	}
	// Without repeats: a message can match several terms, and the exact path
	// appends one candidate per match.
	seen := make(map[int]bool, len(keep))
	order := make([]int, 0, len(keep))
	for _, i := range keep {
		if i < 0 || i >= total || seen[i] {
			continue
		}
		seen[i] = true
		order = append(order, i)
	}
	sort.Ints(order)
	if len(order) > jsonHitMessages {
		// The newest of them: a long session's later word on the subject is
		// the one worth carrying, which is the rule the third excerpt follows.
		order = order[len(order)-jsonHitMessages:]
	}
	if len(order) == total {
		return ms, false
	}
	out := make([]model.Message, 0, len(order))
	for _, i := range order {
		out = append(out, ms[i])
	}
	return out, true
}

// SafeSession is a session with the text a transcript supplied filtered the way
// the printer filters it: newlines and tabs survive, because a JSON string
// holds them and a reader may want the shape of a message, and the characters
// that make displayed order disagree with stored order do not.
//
// The JSON surfaces need this and the printed ones do not: a control byte is
// escaped by the encoder, while U+202E and the invisible tag block are ordinary
// characters to it, so a reader rendering the text got the reordering and the
// invisible instructions the text path strips (#1090, #3616).
func SafeSession(s model.Session) model.Session {
	s.Title = SafeText(s.Title)
	s.Project = SafeText(s.Project)
	if len(s.Messages) > 0 {
		ms := make([]model.Message, len(s.Messages))
		copy(ms, s.Messages)
		for i := range ms {
			ms[i].Text = SafeText(ms[i].Text)
		}
		s.Messages = ms
	}
	return s
}

// SafeStrings is SafeSession for the free-text lists beside a session: the
// snippets a hit quotes, the titles blame carries.
func SafeStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = SafeText(s)
	}
	return out
}

func safeHit(h Hit) Hit {
	h.Session = SafeSession(h.Session)
	h.Snippets = SafeStrings(h.Snippets)
	h.LifecycleNote = SafeText(h.LifecycleNote)
	return h
}

// Print writes search hits as text, or as the JSON envelope with o.JSON.
func Print(w io.Writer, hits []Hit, o Options) {
	for i := range hits {
		if hits[i].Tier == "" {
			hits[i].Tier = TierExact
		}
		hits[i].Session.SetSource(o.SourceInstance)
	}
	if o.JSON {
		// The same filter the printer applies. A control byte is escaped by the
		// encoder, but U+202E and the invisible tag block are ordinary
		// characters to it, so a reader rendering the text got the reordering
		// and the invisible instructions that SafeText exists to stop — on the
		// one surface a dashboard reads (#3616).
		for i := range hits {
			hits[i] = safeHit(boundedHit(hits[i]))
		}
		// One shape, always. The exact path used to emit a bare array while
		// every fallback path emitted an object, so a consumer had to handle
		// two contracts and could not tell which it had until it looked.
		_ = json.NewEncoder(w).Encode(searchJSONEnvelope{ //nolint:errchkjson // no error return here; write errors are ignored like the text path
			SchemaVersion:  jsonout.Version,
			Tier:           setTier(o),
			Total:          o.Total,
			TotalEstimated: o.TotalEstimated,
			Strict:         o.Strict,
			Capped:         o.Capped,
			Withheld:       o.PolicyWithheld,
			Hits:           hits,
			Fuzzy:          o.Fuzzy,
			Stemmed:        o.Stemmed,
			Semantic:       o.Semantic,
			Variants:       o.FuzzyVariants,
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
			fmt.Fprintf(w, "%s%s %-10s %s %s %s %s %s%s%s%s%s\n", cBold, harnessTag(h.Session.Harness, true), project, cDim+"·"+cReset+cBold, d, cDim+"·"+cReset+cBold, id, cDim+"— "+cReset, cBold, MatchCount(h.Count), cReset, tierLabel(h))
		} else {
			fmt.Fprintf(w, "[%s] %-10s · %s · %s — %s%s\n", h.Session.Harness, project, d, id, MatchCount(h.Count), tierLabel(h))
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
		if h.Session.GaveUp && h.Lifecycle == "" && AbandonmentWorthSaying(h) {
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
			note := "  earlier attempt — this project has a newer session on the same ground (" + supersededDay(h.Superseded) + ")"
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

// supersededDay prints the newer session's day the way the header above it
// prints days. The marker is the reader's day, as the header's is (#5053), and
// is printed as that day; anything else is printed as it came.
func supersededDay(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	if t.Year() == time.Now().Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}

// MatchCount is a session's hit count as the result line prints it: "1 match",
// "3 matches". One spelling, so the line and the width budget fitProject
// measures it against cannot disagree (#4627).
func MatchCount(n int) string {
	if n == 1 {
		return "1 match"
	}
	return fmt.Sprintf("%d matches", n)
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
	fixed := termwidth.Columns(fmt.Sprintf("[%s]  · %s · %s — %s%s", harness, date, id, MatchCount(count), tier))
	room := max(width-fixed, 10)
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
	cut := strings.TrimRight(termwidth.Cut(s, width-1), " ")
	// And back to the end of a word, the same reason the excerpt window snaps
	// its own edges: over sixteen real searches rendered at 120 columns, 118 of
	// the 150 lines this cut shortened (79%) ended inside a word —
	// "kube-prometheus-stack-prome…". The walk is short so a line of unbroken
	// path or code keeps the columns instead.
	if at := strings.LastIndexAny(cut, " \t"); at > 0 && utf8.RuneCountInString(cut[at:]) <= lineSnapRunes {
		cut = strings.TrimRight(cut[:at], " \t")
	}
	return cut + "…"
}

// lineSnapRunes is how much of the last word may be given up to end the line on
// a whole one. A word longer than this is a path or an identifier, where the
// prefix is worth more than the tidy edge.
const lineSnapRunes = 14

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
	low := lower(text)
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

// Recent returns the sessions newest first, at most n of them when n > 0.
func Recent(ss []model.Session, n int) []model.Session {
	out := mergeSessions(ss)
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// compileQueryRegex compiles a --re pattern the way grep reads one: ignoring
// case, with ^ and $ anchored at each line. A message is often many lines of
// tool output, and anchoring at the whole text missed `^panic:` on any line
// but the first.
func compileQueryRegex(q string) (*regexp.Regexp, error) {
	return regexp.Compile("(?im)" + q)
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

// TierRelevance mirrors query.TierRelevance for callers of this package.
const TierRelevance = query.TierRelevance

// ErrorHits renders the error-signature tier. The sessions arrive already
// ranked and already narrowed to the neighbourhood of the pasted error, so
// each becomes one hit whose snippets are those messages — re-scoring them
// against the paste's words (as the relevance tier would) throws away the
// passage the tier found and can render a real match as "0 matches".
//
// paste is what the reader pasted. It does not move the excerpt's centre, only
// keeps the lines it carries from being dropped as dumps, and starts the
// excerpt at the first of them.
func ErrorHits(ss []model.Session, paste string) []Hit {
	matched := matchedLine(paste, nil)
	hits := make([]Hit, 0, len(ss))
	for rank, s := range ss {
		hit := Hit{Session: s, Tier: TierError, Count: len(s.Messages), Score: float64(len(ss) - rank)}
		for _, m := range s.Messages {
			if strings.TrimSpace(m.Text) == "" {
				continue
			}
			if sn := snippetAt(m.Text, "", nil, matched); sn != "" {
				hit.Snippets = append(hit.Snippets, sn)
				hit.quoted = append(hit.quoted, m.Text)
			}
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
	for _, m := range s.Messages {
		low := lower(m.Text)
		if strings.Contains(low, t) {
			return true
		}
		if ft := cjkfold.String(t); ft != t || cjkfold.HasCJK(t) {
			if folded := cjkfold.String(low); strings.Contains(folded, ft) {
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
	hits := make([]Hit, len(ss))
	set := newTermSet(terms)
	// One session's score is read from that session alone, and lowercasing its
	// messages is where the time goes: profiled on a real store, a recall
	// answer spent 2.4 s in it and in the ranker's own copy of the same loop.
	// Spread across cores the way the context renderer already is (#1790) —
	// the work per session is unchanged, so the answer is identical.
	parallelForSessions(len(ss), func(rank int) {
		s := ss[rank]
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
			dump     bool
		}
		best := make([]msgScore, 0, 8)
		found := make([]bool, len(terms))
		var pos []int
		for mi, m := range s.Messages {
			// The same rule the scoring loop applies: a transcript's record of
			// a call to deja is not something anyone said, and this tier is
			// where the live miss came through — the scoring loop was fixed
			// first and the hit arrived here instead (#2067).
			low := lower(withoutOwnCallLog(m.Text))
			pos, _ = set.match(low, found, pos)
			distinct := 0
			weighted := 0.0
			center := ""
			// The rarest term a message holds is what names it, so it is what
			// the excerpt gets centred on.
			heaviest := 0.0
			for ti, t := range terms {
				if !found[ti] {
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
				best = append(best, msgScore{mi, distinct, weighted, center,
					m.Role == roleToolOutput && isDataDump(m.Text)})
			}
		}
		for _, b := range best {
			hit.matched = append(hit.matched, b.idx)
		}
		// Heaviest first; a stable sort keeps message order among ties. A
		// JSON or listing dump in tool output goes last whatever it weighs:
		// it holds every word as an entry, and a session of them was quoted
		// as `},\n {\n "type": "tool"` (#4780).
		sort.SliceStable(best, func(i, j int) bool {
			if best[i].dump != best[j].dump {
				return !best[i].dump
			}
			return best[i].weighted > best[j].weighted
		})
		for i := 0; i < len(best) && i < 2; i++ {
			if sn := snippet(s.Messages[best[i].idx].Text, best[i].center, nil); sn != "" {
				hit.Snippets = append(hit.Snippets, sn)
				hit.quoted = append(hit.quoted, s.Messages[best[i].idx].Text)
			}
		}
		hit.Score = float64(len(ss) - rank)
		hits[rank] = hit
	})
	out := liftedNotes(hits)
	markEarlierAttempts(out)
	return out
}

// parallelForSessions runs fn over 0..n-1, on one goroutine per core once there
// is enough work to pay for them. The bound is the same shape renderContextTurns
// uses: a handful of items is faster on one core than it is to hand out.
func parallelForSessions(n int, fn func(i int)) {
	workers := min(relevanceWorkers(), n)
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

// relevanceWorkers is a variable so a test can force the single-core path and
// compare the two orders.
var relevanceWorkers = defaultRelevanceWorkers

func defaultRelevanceWorkers() int { return runtime.NumCPU() }

// lower is the text fold both sides of a match use: lowercase plus ё→е.
func lower(s string) string { return query.Lower(s) }
