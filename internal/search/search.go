// Package search ranks sessions against a query and prints what it finds.
package search

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

const (
	cReset  = "\x1b[0m"
	cDim    = "\x1b[2m"
	cBold   = "\x1b[1m"
	cOrange = "\x1b[38;5;208m"
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
			est, sampled = o.Unread.Sample[doc.hit.Session.Key()]
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
		if o.Role == "" && (m.Role == roleSummary || (m.Role == "user" && digest.IsCompactionSummary(strings.TrimSpace(m.Text)))) {
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
				if _, sampled := u.Sample[h.Session.Key()]; !sampled {
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
