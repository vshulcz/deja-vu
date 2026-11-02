package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// attachAnswers puts the decision next to the question.
//
// A recall query describes a symptom, so the user turn wins on term overlap and
// its snippet restates what the caller already knows. Search cannot fix this on
// its own: it returns only the messages that matched, so the reply that resolved
// the session is not in memory at that point. Here the index is at hand, so the
// session is read back and the reply carried along.
func attachAnswers(dir string, hits []search.Hit) {
	for i := range hits {
		h := &hits[i]
		if len(h.Snippets) == 0 || len(h.Snippets) >= 3 {
			continue
		}
		var matchedUser string
		for _, m := range h.Session.Messages {
			if m.Role == "user" {
				matchedUser = m.Text
				break
			}
		}
		if matchedUser == "" {
			continue
		}
		full, ok, err := index.FindByIdentity(dir, h.Session.Harness, h.Session.ID)
		if err != nil || !ok {
			continue
		}
		for mi, m := range full.Messages {
			if m.Role != "user" || m.Text != matchedUser {
				continue
			}
			if a := search.AnswerAfter(full.Messages, mi); a != "" {
				h.Snippets = append(h.Snippets, "→ "+a)
			}
			break
		}
	}
}

// conclusionsAboutIt keeps the conclusions that have something to do with what
// was asked.
//
// "what this session concluded" is read off the whole session, and a session
// that ran for days concluded things about everything it touched. Measured on
// sixteen recall calls against a real store, 42% of the conclusion lines served
// shared no word with the query or with the excerpts they sat under — and a long
// session served the same three lines to a question about blame rows, one about
// the MCP dispatcher and one about a connection pool, because those were simply
// the newest things it had concluded.
//
// Excerpt words count as well as query words, which is what keeps a conclusion
// worded nothing like the question — the case the list exists for (#1011): "the
// backoff counted from zero" stays under an excerpt that says backoff.
func conclusionsAboutIt(cs []string, q string, snippets []string) ([]string, bool) {
	about := contentWords(q)
	for _, sn := range snippets {
		for w := range contentWords(sn) {
			about[w] = true
		}
	}
	if len(about) == 0 {
		// Nothing to judge against: a query of common words alone, which the
		// ranking answered on its own terms.
		return cs, true
	}
	kept := make([]string, 0, len(cs))
	for _, c := range cs {
		if sharesAWord(contentWords(c), about) {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 && len(cs) > 0 {
		// One line rather than none. A session that matched on words this gate
		// cannot see — a query in one language against a transcript in another,
		// which this store is full of — still concluded something, and a
		// conclusion nobody asked for is a memory that can still turn out to be
		// the one. Three of them is wallpaper; one is an offer.
		//
		// And it is offered as one: the second return is false, so the line
		// above it does not call it a conclusion about the question. On sixteen
		// recall calls against a real store this is what four of the answers
		// show, and an agent reads the label as the claim.
		return cs[:1], false
	}
	return kept, true
}

// longSessionTurns is how many user turns make a session one that worked on
// many things. On a real store the sessions recall quoted had at most 15 or at
// least 84, so anywhere between gives the same answers.
const longSessionTurns = 20

// conclusionSource is what recall reads "what this session concluded" from: the
// whole session, or for a long one the turns its excerpts were cut from.
//
// A long session concluded something about everything it touched, and read
// whole its newest conclusions win. On 453 real recall queries one session's
// last conclusion sat under 205 answers; checked by hand on 60 of them, the
// block was about the question on 13 and about other work on 47. Read around
// the quoted turns it was about the question on 19, about other work on 15, and
// absent on 26.
//
// A passage quoted from a compaction summary is no anchor: the turn around a
// summary is whatever the session did next.
func conclusionSource(whole model.Session, quoted []string) (model.Session, bool) {
	if userTurns(whole) < longSessionTurns {
		return whole, false
	}
	return episodeAround(whole, quoted), true
}

func userTurns(s model.Session) int {
	n := 0
	for _, m := range s.Messages {
		if m.Role == "user" {
			n++
		}
	}
	return n
}

// episodeAround is the part of a session around the messages a hit quoted: for
// each, the user turn it sits in, through to the next user turn. Nothing when
// none of them is in the session.
func episodeAround(whole model.Session, quoted []string) model.Session {
	want := make(map[string]bool, len(quoted))
	for _, q := range quoted {
		if !digest.IsCompactionSummary(q) {
			want[q] = true
		}
	}
	ms := whole.Messages
	keep := make([]bool, len(ms))
	for i, m := range ms {
		if !want[m.Text] {
			continue
		}
		start := i
		for start > 0 && ms[start].Role != "user" {
			start--
		}
		end := i + 1
		for end < len(ms) && ms[end].Role != "user" {
			end++
		}
		for j := start; j < end; j++ {
			keep[j] = true
		}
	}
	ep := whole
	ep.Messages = nil
	for i, k := range keep {
		if k {
			ep.Messages = append(ep.Messages, ms[i])
		}
	}
	return ep
}

// sharesAWord is word overlap that holds for identifiers: `ManifestBuiltAt`
// lowercases to one token, so a question about the manifest shares nothing with
// it on equality alone, and the conclusion that named it was dropped for a query
// plainly about it.
func sharesAWord(words, about map[string]bool) bool {
	for w := range words {
		if about[w] {
			return true
		}
		for a := range about {
			if len(a) >= 5 && strings.Contains(w, a) {
				return true
			}
			if len(w) >= 5 && strings.Contains(a, w) {
				return true
			}
		}
	}
	return false
}

// conclusionGateSlack is how many extra conclusions to ask for, so the list can
// still show three after the gate above has dropped the ones about other work.
const conclusionGateSlack = 3

// contentWords are the words of a text that carry its subject: long enough to
// mean something and not a word every session holds.
func contentWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, tok := range query.Tokens(s) {
		if utf8.RuneCountInString(tok) < 4 || query.IsStopWord(tok) {
			continue
		}
		out[tok] = true
	}
	return out
}

// shownAnswers counts the excerpts that are answer lines rather than matched
// text, which is how many conclusions the drop below can take.
func shownAnswers(snippets []string) int {
	n := 0
	for _, sn := range snippets {
		if strings.HasPrefix(strings.TrimSpace(sn), "→ ") {
			n++
		}
	}
	return n
}

// withoutShownAnswer drops conclusions the excerpts above already carry. The
// comparison is on the text alone: the answer line is written with an arrow
// prefix and a conclusion is not, and the same sentence can be trimmed to a
// different number of sentences on the two paths, so the shorter one being a
// prefix of the longer counts as the same fact.
func withoutShownAnswer(cs []string, snippets []string) []string {
	if len(cs) == 0 || len(snippets) == 0 {
		return cs
	}
	shown := make([]string, 0, len(snippets))
	for _, sn := range snippets {
		if t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sn), "→ ")); t != sn {
			shown = append(shown, t)
		}
	}
	if len(shown) == 0 {
		return cs
	}
	out := cs[:0:0]
	for _, c := range cs {
		dup := false
		for _, sh := range shown {
			if sameFact(c, sh) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, c)
		}
	}
	return out
}

// sameFact reports whether two lines say the same thing. One being a prefix of
// the other counts, because the answer line and the conclusions list trim to a
// different number of sentences — but only when the shorter one is long enough
// to be a fact rather than an opening: "We decided to" is the start of every
// second conclusion an agent writes, and dropping the specific line behind it
// would lose the answer to keep the preamble.
func sameFact(a, b string) bool {
	if a == b {
		return true
	}
	short, long := a, b
	if len(short) > len(long) {
		short, long = long, short
	}
	if utf8.RuneCountInString(short) < sameFactFloor {
		return false
	}
	return strings.HasPrefix(long, short)
}

// sameFactFloor is how much of a sentence has to agree before two lines count
// as one fact. Forty characters is past any shared opening — "we decided to
// use" is nineteen — and inside the shortest conclusion worth printing.
//
// Characters, not bytes. Counted in bytes the floor was forty for English and
// twenty for Russian, so a thirty-character preamble cleared it and the
// specific line behind it was dropped — the exact loss the paragraph above
// says the floor exists to prevent.
const sameFactFloor = 40

// recallListingLine makes one line of the recall listing safe to hand a model.
//
// The listing is built here rather than by the search printer, so it never
// went through SafeText: measured on a planted session, `recall` returned the
// escape byte, U+202E and U+200B verbatim inside the frame, while
// `recall_context` — which does print via search — returned the same session
// clean. The worst of it is the `→ ` answer line, which attachAnswers copies
// straight out of the transcript with no filter at all.
//
// Newlines are collapsed on top of SafeText because every line here is one
// row of a numbered list: a project name or an answer spanning two lines
// forges a second result, which is the shape #1080 fixed on the sync path.
func recallListingLine(s string) string { return search.SafeLine(s) }

// commandListingLine is recallListingLine for a command, which an agent runs
// rather than reads: the spacing inside it is part of it (#2052).
func commandListingLine(s string) string { return search.SafeCommand(s) }

func recallText(dir, q, harness string, limit, budget int) (string, error) {
	text, _, _, _, err := recallTextResult(dir, q, harness, limit, 0, budget)
	return text, err
}

// recallCountLine introduces the sessions below it.
//
// It said "deja recall for <the query>" whatever the tier, so an answer whose
// first line says no session is about this went on to head the payload with
// the question itself — and an agent read three unrelated sessions as the
// answer to it. On the relevance tier the line says what they are instead
// (#2074).
func recallCountLine(q, tier string, offset, served, total, strict int, namesTheAsked bool) string {
	return recallCountLineAbout(q, tier, offset, served, total, strict, namesTheAsked, false)
}

// recallCountLineAbout is recallCountLine with the total said as an estimate
// when about is set: retrieval scaled the unread part of it from a sample.
func recallCountLineAbout(q, tier string, offset, served, total, strict int, namesTheAsked, about bool) string {
	q = clampEcho(q)
	of := strconv.Itoa(total)
	if about {
		of = "about " + of
	}
	switch {
	case tier == search.TierRelevance && strict > 0 && offset > 0:
		return fmt.Sprintf("deja recall for %q (%d-%d of %s ranked; %d %s every word)\n", q, offset+1, offset+served, of, strict, holdOrHolds(strict))
	case tier == search.TierRelevance && strict > 0:
		// "none about it" over an answer that holds the whole query is the
		// count line's half of #3815.
		return fmt.Sprintf("deja recall for %q (%d of %s ranked; %d %s every word)\n", q, served, of, strict, holdOrHolds(strict))
	case tier == search.TierRelevance && namesTheAsked && offset > 0:
		// "none about it" is the same claim the lead makes, so it follows the
		// same reading: the exact match failed, and the first session named
		// what was asked (#2827).
		return fmt.Sprintf("ranked by wording for %q (%d-%d of %s)\n", q, offset+1, offset+served, of)
	case tier == search.TierRelevance && namesTheAsked:
		return fmt.Sprintf("ranked by wording for %q (%d of %s)\n", q, served, of)
	case tier == search.TierRelevance && offset > 0:
		// The tier before the page: page two of an answer to a question
		// nothing is about is still not a page of matches, and saying so only
		// on page one was the same contradiction one line further down.
		return fmt.Sprintf("nearest by wording to %q (%d-%d of %s ranked, none about it)\n", q, offset+1, offset+served, of)
	case offset > 0:
		return fmt.Sprintf("deja recall for %q (matches %d-%d of %s)\n", q, offset+1, offset+served, of)
	case tier == search.TierRelevance && (served < total || about):
		return fmt.Sprintf("nearest by wording to %q (%d of %s ranked, none about it)\n", q, served, of)
	case tier == search.TierRelevance:
		return fmt.Sprintf("nearest by wording to %q (%d ranked, none about it)\n", q, served)
	case served < total || about:
		// How many came back is not how many matched, and the agent is the
		// reader that cannot ask a human. "(5 match(es))" reads as five exist,
		// which is a different answer from sixteen thousand matched and here
		// are five — one is worth acting on, the other is a sample that will
		// fill a context window with whatever ranked highest (#1308).
		return fmt.Sprintf("deja recall for %q (%d of %s matched)\n", q, served, of)
	default:
		return fmt.Sprintf("deja recall for %q (%s)\n", q, search.MatchCount(served))
	}
}

// wholeSessionForMCP reads one session for a caller that must not wait.
//
// `findByPrefix` is the CLI helper and opens with a blocking `index.Ensure` —
// right for a command someone typed, wrong here. Every caller below has already
// been through `EnsureForSearchStale`, which deliberately refuses to wait: it
// serves the snapshot on disk and hands the rebuild to a detached warmup,
// because rebuilding inline blows the client's tool timeout. Calling the
// blocking version afterwards undid exactly that — and if the lock happened to
// be free it would run the whole rebuild on the server's thread rather than
// merely wait for it.
//
// By identity rather than by prefix, too: `FindByPrefix` matches the id across
// every harness and answers with the newest match, so the "full story" printed
// for a hit could be a different session that shares its id — a real case, not
// a theoretical one (#719). The hit knows its own harness; use it.
func wholeSessionForMCP(dir string, s model.Session) (model.Session, bool, error) {
	return index.FindByIdentity(dir, s.Harness, s.ID)
}

// recallCountLineReserve is what the "N more, call again with offset=N" line
// below the hits costs, kept out of the budget the hits spend. It is written
// after the loop, and the final trim cut the navigation line off the end,
// which is the one line an agent needs precisely when the answer was too big
// to fit.
//
// The count line above the hits is measured rather than guessed: it carries
// the query, and a constant that fitted a short one stopped covering an error
// string pasted in whole — which is exactly what the tool description asks
// agents to send.
const recallCountLineReserve = 80

// recallHeaderReserve is that plus the count line this answer will actually
// print.
func recallHeaderReserve(q, tier string, offset, limit, total int) int {
	// The longer of the two relevance lines: this reserves room before the
	// answer is built, and reserving for the shorter one would let the
	// other overrun what it was promised.
	short := len(recallCountLine(q, tier, offset, limit, total, 0, true))
	long := max(short, len(recallCountLine(q, tier, offset, limit, total, 0, false)))
	// The strict line names a count of its own and can be the longest of the
	// three, so reserve for it too rather than let it overrun (#3815).
	if strict := len(recallCountLine(q, search.TierRelevance, offset, limit, total, limit, false)); strict > long {
		long = strict
	}
	return recallCountLineReserve + long
}

// twinSessionsFor pairs each session with the same session as it exists on
// another machine, so a page holding both copies can say so. One manifest read
// per page; empty when the index cannot be read, which costs a marker rather
// than an answer.
func twinSessionsFor(dir string) map[string][]string {
	metas, err := index.AllMetaShared(dir)
	if err != nil {
		return nil
	}
	return index.TwinSessions(metas)
}

// recallTextResult keeps the four-value shape its callers and tests read.
func recallTextResult(dir, q, harness string, limit, offset, budget int) (string, int, int64, []string, error) { //nolint:unparam // same shape as recallTextResultFrom; fix_fallback.go and many tests call it
	text, sessions, raw, ids, _, err := recallTextResultFrom(dir, q, harness, limit, offset, budget)
	return text, sessions, raw, ids, err
}

// recallTextResultFrom is recallTextResult plus the projects the answer was
// built from, which the digest log records so a stored digest can be checked
// against a rule tightened later (#2324).
func recallTextResultFrom(dir, q, harness string, limit, offset, budget int) (string, int, int64, []string, []string, error) {
	return recallTextResultIn(dir, q, harness, "", limit, offset, budget)
}

// recallTextResultIn is recall scoped to a project: project, when the caller
// names one, keeps only that project's sessions; otherwise the project of the
// directory the server runs in comes first, unless DEJA_RECALL_GLOBAL=1.
func recallTextResultIn(dir, q, harness, project string, limit, offset, budget int) (string, int, int64, []string, []string, error) {
	if limit <= 0 {
		limit = 5
	}
	// An id resolves as an id here too. `recall` is the tool an agent reaches
	// for first and the block it reads prints a session id beside every
	// session: asked for the 120 most recent ids on a real store, recall
	// answered about *other* sessions 77 times, named the right one 37 and
	// said nothing 6 (#3717).
	if looksLikeSessionID(q) {
		if text, id, ok := contextByID(dir, q); ok {
			note := ""
			if id.note != "" {
				note = id.note + "\n"
			}
			// A deep read, not a page: it gets recall_context's room and cut
			// marker on top of what the caller reserved. It used to return
			// before any budget applied, up to 3653 bytes over (#4023).
			text = fitContextDigest(note+text, q, id.session, budget+contextMCPBudget-recallMCPBudget)
			return text, 1, id.size, []string{id.session}, nil, nil
		}
	}
	r, err := rankRecall(dir, q, harness, project, offset+limit)
	if err != nil {
		return "", 0, 0, nil, nil, err
	}
	result, o, hits, policyHidden, cov := r.result, r.o, r.hits, r.policyHidden, r.cov
	unread, about := r.unread, r.about
	if len(hits) == 0 {
		return recallMissAnswer(dir, q, budget, cov, policyHidden)
	}
	// Before the page is cut, not after: demoting inside the page is a no-op
	// when every hit on it was rejected, and then the approaches the reader did
	// not reject sit below the cut and never reach the agent at all. On 30
	// matches with the 25 newest rejected, `limit 15` served 15 rejected
	// attempts and none of the 5 clean ones.
	total := len(hits) + unread
	attachLifecycles(dir, hits)
	twins := twinSessionsFor(dir)
	demoted := demoteRejected(hits)
	if offset > 0 {
		// Past the read hits too: the total counts sessions ranking never
		// read, and filters can leave fewer hits than the pool held.
		if offset >= total || offset >= len(hits) {
			what := "matches"
			if result.Tier == search.TierRelevance {
				what = "sessions ranked by wording"
			}
			return fmt.Sprintf("No more %s for %q: %s total, offset %d.", what, clampEcho(q), aboutCount(total, about), offset), 0, 0, nil, nil, nil
		}
		hits = hits[offset:]
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return recallPage(dir, q, budget, offset, limit, total, demoted, about, result, o, hits, twins, cov, r.added)
}

// rankedRecall is a recall's ranking before it is cut into a page: what the
// search found, scoped to the project the agent works in first.
type rankedRecall struct {
	result       index.SearchResult
	o            search.Options
	hits         []search.Hit
	policyHidden int
	unread       int
	about        bool
	cov          index.Coverage
	// added is the machine's best answer withMachineBest put on a
	// project-first page, by harness:id; empty when it put none there.
	added string
}

// rankRecall runs the search recall serves from. context_search reads the
// same ranking, so the two surfaces cannot disagree about what is relevant.
func rankRecall(dir, q, harness, project string, window int) (rankedRecall, error) {
	live, cut := liveLineageCut(dir)
	o := search.Options{Query: nfcfold.Compose(q), Harness: harness, All: true, RecallWorn: usage.WornSessions(dir), ExcludeSessions: live, ExcludeUpTo: cut, Window: window, Activation: policy.ActivationMCP, LoneWord: true}
	cov, err := index.EnsureForSearchCoverage(dir, o, mcpProgress())
	if err != nil {
		return rankedRecall{}, err
	}
	if cov.Stale && !warmupRunning(dir) {
		// A store rewrote itself (cline, cursor); rebuilding inline would
		// blow the client's tool timeout, so refresh detached and serve the
		// current index with an honest note. Not while a build runs: the
		// sentinel dedups for ten minutes, a version rebuild can outlast that,
		// and a second build over one index is the thing to avoid.
		requestWarmup(dir)
	}
	// An agent names the project by the directory it works in, often a git
	// worktree no recorded session carries in its path. A directory on this
	// machine scopes the way the cwd does: every worktree of the repo, matched
	// by the rule the hooks use.
	var pathScope []string
	if p := strings.TrimSpace(project); filepath.IsAbs(p) {
		if st, statErr := os.Stat(p); statErr == nil && st.IsDir() {
			pathScope = howScope(p, "", false)
		}
	}
	if len(pathScope) > 0 {
		o.Projects = pathScope
	} else if strings.TrimSpace(project) != "" {
		// Named by the caller: a filter, the way the tool describes it. It was
		// accepted and then ignored.
		o.Projects = []string{project}
	}
	// The project the agent is working in answers first. The prompt hook
	// ranks inside it and recall ranked the whole machine, so the same
	// question found the decision through the hook and buried it here: on a
	// stand with ~1800 real sessions behind one project, the session that
	// answered sat at 20-39 for the queries agents sent, or outside the
	// candidates altogether. The rest of the machine follows.
	var scope []string
	added := ""
	if project == "" && os.Getenv("DEJA_RECALL_GLOBAL") != "1" {
		scope = howScope(howCwd(), "", false)
		o.PoolScope = scope
	}
	result, o2, hits, policyHidden, err := recallRank(dir, q, o)
	if err != nil {
		return rankedRecall{}, err
	}
	// Matching sessions ranking never read count toward the total all the
	// same (#5467).
	unread, about := unreadSessions(result), unreadEstimated(result)
	if len(pathScope) > 0 {
		hits = inProjectHits(hits, pathScope)
	}
	if len(scope) > 0 {
		po := o
		po.Projects, po.PoolScope = scope, nil
		// The project falls back to one word of two only where the machine
		// did too: otherwise its guess would replace the machine's answer.
		po.LoneWord = o.LoneWord && (len(hits) == 0 || dropsAWord(result.Variants))
		// Every rung above the ranked tier came back empty for the whole
		// machine, so it does for the project: start where that ended.
		po.FellThrough = result.Fell
		// Cut from the machine's search when that one read everything the
		// project's would: a second search of a word most sessions hold
		// cost as much again (#5486). And not run at all where no session
		// is the project's, which used to walk every rung of the ladder to
		// find nothing.
		var pr index.SearchResult
		var po2 search.Options
		var ph []search.Hit
		var phidden int
		var perr error
		if cut, ok := index.ScopedFrom(result, scope); ok {
			pr, po2, ph, phidden, perr = recallRankFrom(dir, q, po, cut)
		} else if index.AnyInScope(dir, scope) {
			pr, po2, ph, phidden, perr = recallRank(dir, q, po)
		}
		// The index filter matches names as substrings; kept are the
		// sessions the hooks call this project's, by the same rule, so a
		// directory that merely shares the name does not lead (#2333).
		ph = inProjectHits(ph, scope)
		// Even when another project matches the words better: there the
		// words are someone else's PR or pool, here they are the decision
		// this repository lives by, which is why the hook ranks inside the
		// project only. The rest of the machine follows only from the same
		// tier, so the lines above the page describe all of it.
		if perr == nil && len(ph) > 0 {
			rest := hitsNotIn(hits, ph)
			if result.Tier != pr.Tier {
				rest = nil
			}
			// What the two runs left unread: the machine's figure covers
			// the project's sessions too, so the larger of the two.
			if rest == nil {
				unread, about = unreadSessions(pr), unreadEstimated(pr)
			} else {
				unread = max(unreadSessions(pr), unread+len(hits)-len(ph)-len(rest))
				about = about || unreadEstimated(pr)
			}
			result, o2, policyHidden = pr, po2, phidden
			// The machine's best answer keeps a place, from the same tier only,
			// for the same reason.
			machine := hits
			hits = slices.Concat(ph, rest)
			if rest != nil {
				before := hits
				hits = withMachineBest(hits, machine, ph)
				// It only ever changes the second place.
				if len(hits) > 1 && (len(before) < 2 || hitKey(hits[1]) != hitKey(before[1])) {
					added = hitKey(hits[1])
				}
			}
		}
	}
	o = o2
	// A fork quoting the turns its source holds said everything twice, the
	// same as on the search screen (#5320, #5335).
	hits = dropCopiedHits(dir, hits)
	hits = diversifyUncoveredTerms(hits, q, result.TermIDF, added)
	return rankedRecall{result: result, o: o, hits: hits, policyHidden: policyHidden, unread: unread, about: about, cov: cov, added: added}, nil
}

// recallPage writes one page of ranked hits, already cut to the page.
func recallPage(dir, q string, budget, offset, limit, total, demoted int, about bool, result index.SearchResult, o search.Options, hits []search.Hit, twins map[string][]string, cov index.Coverage, added string) (string, int, int64, []string, []string, error) {
	// Counted on the page rather than taken from the result: the strict head
	// is small but the trust policy can withhold part of it and paging can
	// leave the rest behind, and a line counting sessions the reader cannot
	// see is worse than no line (#3815).
	strictShown := 0
	for _, h := range hits {
		if h.Strict {
			strictShown++
		}
	}
	var b strings.Builder
	served, pageFull := 0, false
	// One answer for both lines: the lead and the count used to be able to
	// disagree, and a page that says "the first one names what you asked"
	// above "none about it" is worse than either alone (#2827).
	namesTheAsked := result.Tier == search.TierRelevance && len(hits) > 0 &&
		sessionNamesTheAsked(hits[0].Session, q, result.TermIDF)
	if cov.Stale {
		fmt.Fprintln(&b, "("+coverageLine(cov)+")")
	}
	// Whether the question named something the store does not hold. The word
	// forms line already says which word was dropped; what it did not say is
	// that dropping the subject leaves an answer about the rest of the
	// sentence, which is the shape #657 measured on twenty invented subjects.
	absent := namedSomethingAbsent(result.Variants)
	// The strict head is already gone when a word was dropped — nothing holds
	// every word of a query the search could not match in full. The reading of
	// the first hit goes the same way: the word that identified the question
	// is the one that was dropped, so this page does not name what was asked.
	if absent {
		namesTheAsked = false
	}
	lone := result.LoneWord && absent
	if lone && len(hits) > loneWordPageHits {
		hits = hits[:loneWordPageHits]
	}
	// Coverage settles what the two readings above cannot: a session holding
	// most of a long query is an answer in part even when a word was dropped
	// or the query's rarest word is one nothing holds.
	lead, skipped := leadHits(hits, added)
	cover := bestCoverage(lead, q, result.TermIDF)
	if skipped >= 0 && cover.at >= skipped {
		cover.at++
	}
	covered := result.Tier == search.TierRelevance && strictShown == 0 && result.Directory == "" &&
		!namesTheAsked && cover.aboutIt()
	var lb strings.Builder
	namesTheAsked, nothing := writeRecallLead(&lb, result, lead, q, strictShown, absent, namesTheAsked, cover, covered)
	b.WriteString(lb.String())
	if !nothing {
		decorateRecallHits(dir, hits)
	} else if len(hits) > 0 {
		// The first lead keeps one excerpt, in its author's words.
		markRecallHits(dir, hits[:1])
	}
	if note := demotedNote(hits, demoted); note != "" {
		fmt.Fprintln(&b, note+" — read the order as the user's judgement, not as recency.")
	}
	// The hits are built first so the line above them can count what actually
	// went out. It used to be written ahead of the loop, which also stops on
	// the token budget: the header promised fifteen while nine arrived, and the
	// follow-up line was trimmed off the end (#1308).
	var hb strings.Builder
	headerRoom := b.Len() + recallHeaderReserve(q, result.Tier, offset, limit, total)
	if about {
		headerRoom += len("about ")
	}
	for i, h := range hits {
		switch {
		case nothing:
			writeLeadLine(&hb, h, i, cover.terms)
		case lone:
			writeLoneWordHit(&hb, h, i)
		default:
			writeRecallHitHead(&hb, h, i, twins)
		}
		// Under the best hit only, and only when there is budget left: the
		// excerpts say where the query words appear, which is not the same as
		// what the session concluded. An agent had to open the session to learn
		// the outcome; these are the decision-carrying lines `share` surfaces,
		// so the common case — "did we solve this before?" — is answered in the
		// recall itself. Later hits stay excerpt-only; they are candidates to
		// choose between, not answers.
		if i == 0 && !lone && !nothing {
			writeRecallConclusions(&hb, dir, q, h, o, budget, headerRoom, len(hits) > 1)
		}
		served++
		if headerRoom+hb.Len() >= budget {
			pageFull = true
			break
		}
	}
	// From what was served, not from the limit: the loop also stops on the
	// token budget, and then this said "2 more" while five were left — the
	// agent asks for offset=served and the arithmetic has to hold.
	b.WriteString(recallCountLineAbout(q, result.Tier, offset, served, total, strictShown, namesTheAsked, about))
	b.WriteString(hb.String())
	// From what was served, not from the limit: the loop also stops on the
	// token budget, and then this said "2 more" while five were left — the
	// agent asks for offset=served and the arithmetic has to hold.
	out := recallPageFooter(&b, hits, result, offset, served, total, budget, pageFull, about, nothing)
	raw, ids, projects := recallServedFacts(hits, served)
	return out, served, raw, ids, projects, nil
}

// decorateRecallHits puts every line in its author's words, and the person's
// own check and build rules for the project the agent works in under the mark
// the frame says to follow. The scope is the working directory's even when the
// caller filtered on another project: a rule is only ever one of this
// project's.
func decorateRecallHits(dir string, hits []search.Hit) {
	attachAnswers(dir, hits)
	markRecallHits(dir, hits)
}

// markRecallHits is decorateRecallHits without the answer lookup.
func markRecallHits(dir string, hits []search.Hit) {
	scope := howScope(howCwd(), "", false)
	for i := range hits {
		index.MarkUserRules(dir, &hits[i].Session, scope)
		hits[i].Snippets = search.EvidenceRewrite(hits[i].Session, dir, hits[i].Snippets)
	}
}

// writeLeadLine is a hit on a page no session is about: where, when, which of
// the query's words it holds, and its id. Excerpts went with every one before,
// and on a replay of real recall calls agents followed one of these leads 6
// times in 212 while they were most of the bytes of the page. The first keeps
// one, so a lead that does answer still shows what it says.
func writeLeadLine(hb *strings.Builder, h search.Hit, i int, terms []string) {
	fmt.Fprintf(hb, "%d. [%s] %s", i+1, recallListingLine(h.Session.Harness), recallListingLine(h.Session.Project))
	if !h.Session.Updated.IsZero() {
		fmt.Fprintf(hb, " · %s", h.Session.Updated.Local().Format("2006-01-02"))
	}
	if held := sessionHolds(h.Session, terms); len(held) > 0 {
		fmt.Fprintf(hb, " · has %s", recallListingLine(strings.Join(held, ", ")))
	}
	fmt.Fprintf(hb, " · %s\n", recallListingLine(h.Session.ID))
	if i > 0 {
		return
	}
	for _, sn := range h.Snippets {
		if !strings.HasPrefix(sn, search.UserRuleLabel) {
			fmt.Fprintf(hb, "- %s\n", recallListingLine(sn))
			return
		}
	}
}

// recallMissAnswer answers a recall whose words matched nothing.
func recallMissAnswer(dir, q string, budget int, cov index.Coverage, policyHidden int) (string, int, int64, []string, []string, error) {
	// The words found nothing, so try the string as an id, as
	// recall_context does. The page sends a session too deep to quote back
	// here by its id (#5579), and an id looksLikeSessionID turns away —
	// a Devin subagent's carries a colon — would otherwise answer that
	// nothing matched.
	if !looksLikeSessionID(q) {
		if text, id, ok := contextByID(dir, q); ok {
			note := ""
			if id.note != "" {
				note = id.note + "\n"
			}
			text = fitContextDigest(note+text, q, id.session, budget+contextMCPBudget-recallMCPBudget)
			return text, 1, id.size, []string{id.session}, nil, nil
		}
	}
	// A miss on a snapshot is a miss on part of the history, and the
	// part matters: on a first build's early slice it is everything older
	// than the newest few hundred sessions (#5390).
	if cov.Stale && cov.Sessions > 0 && policyHidden == 0 {
		return fmt.Sprintf("No prior deja sessions matched %q — %s.", clampEcho(q), coverageLine(cov)), 0, 0, nil, nil, nil
	}
	return emptyRecallAnswerPolicy(dir, q, policyHidden), 0, 0, nil, nil, nil
}

// recallPageFooter is the page with its paging and depth lines, trimmed to
// the budget with room left for them.
func recallPageFooter(b *strings.Builder, hits []search.Hit, result index.SearchResult, offset, served, total, budget int, pageFull, about, leadsOnly bool) string {
	more := ""
	if left := total - offset - served; left > 0 {
		what := "matches"
		if left == 1 {
			what = "match"
		}
		if result.Tier == search.TierRelevance {
			// Nothing matched, so there are no more matches to offer — only
			// more of the nearest wording.
			what = "ranked by wording"
		}
		// A page cut by its size, not by limit: say so, or a caller that
		// asked for 20 and got 3 cannot tell a bigger limit will not help
		// (#5511).
		page := ""
		if pageFull {
			page = " (page full)"
		}
		more = fmt.Sprintf("\n%s more %s%s — call recall again with offset=%d.\n", aboutCount(left, about), what, page, offset+served)
	}
	// The move the paging line cannot offer. A hit quotes at most three of its
	// matches, so a session that matched hundreds of times arrives as a
	// fragment, and the only follow-up this page named was more sessions.
	// Measured on a live harness over six questions whose answer sat inside
	// such a session: three to seven tool calls each, all but one of them a
	// re-worded recall, and the figure asked for was in the served excerpts
	// twice of six. With this line the same six cost 36% less context handed
	// and 51% less billed. It names recall with the id, which resolves to the
	// same digest as recall_context: the OpenClaw and Hermes plugins and the
	// shell have recall and no recall_context, and were sent to a tool they
	// could not call (#5579).
	deep := ""
	// Not under leads: it sends the reader into a session nothing says is
	// about the question.
	if deepest := deepestServed(hits, served); deepest != nil && !leadsOnly {
		deep = fmt.Sprintf("\nSession %s matched %d times and only three of them fit here — call recall again with that id as the query for a digest of it.\n",
			deepest.Session.ID, deepest.Count)
	}
	// The paging line is the instruction, not the evidence: appending it before
	// the trim made a full page drop the one thing that says how to reach the
	// rest, exactly where offset is meant to be used (#1726). Trim the excerpts
	// to leave room for it instead.
	out := b.String()
	if len(out)+len(more)+len(deep) > budget {
		// A budget smaller than the instruction itself: keep the page and drop
		// the line rather than trim to a negative length. The depth line goes
		// first — paging is the move an agent can always fall back to.
		if len(more)+len(deep) >= budget {
			deep = ""
		}
		if len(more) >= budget {
			more = ""
		}
		// Every excerpt shortened on its own ends with the marker; the one the
		// page budget cut ended mid-word saying nothing, so the last line an
		// agent reads was the one line it could not tell was a fragment
		// (#1799). Reserved before the trim, like the paging line above.
		room := budget - len(more) - len(deep)
		if room <= len(cutMarker) {
			// No room to say it was cut without eating what was cut from:
			// keep the bytes, drop the marker.
			out = trimUTF8(out, room)
		} else {
			out = markCut(trimUTF8(out, room-len(cutMarker)))
		}
	}
	// After the paging line, not before it: the cut marker on the last excerpt
	// is read as the line above the paging line (#1799), and a second
	// instruction wedged between them takes that place.
	out += more + deep
	return out
}

// writeRecallLead writes the line that says how the page was matched, and
// returns namesTheAsked as that line left it and whether it said no session
// is about the query.
func writeRecallLead(b *strings.Builder, result index.SearchResult, hits []search.Hit, q string, strictShown int, absent, namesTheAsked bool, cover recallCoverage, covered bool) (bool, bool) {
	// Whether the line below the tier already said nothing is about this, so
	// the absent-word case can say it from wherever the chain stopped without
	// saying it twice.
	saidNothing := false
	if covered {
		// In place of the word forms line too: it called the path the
		// session below holds "ignored", because no session holds it with
		// every other word.
		fmt.Fprintln(b, cover.line())
		return true, false
	}
	switch {
	case result.Stemmed, result.Fuzzy:
		writeFormsLine(b, result)
	case result.Tier == search.TierError:
		fmt.Fprintln(b, "No exact match; these sessions hit the same error (matched by signature).")
	case result.Directory != "" && strictShown == 0:
		// The file was asked about, nothing names it, and these sessions are
		// about the directory it sits in. "No session is about this" over a
		// rule set for that directory read as "nothing applies", and the
		// agent changed the file without the check the rule asked for (#4762).
		fmt.Fprintf(b, "Nothing in history names that file; sessions about its directory %s come first. A rule set for the directory may apply to this change, so check whether it still holds before acting on it.\n", recallListingLine(result.Directory))
		namesTheAsked = true
	case strictShown > 0:
		// Before every heuristic below it, because this one is a fact about
		// the answer rather than a reading of its wording: these sessions
		// were matched, not ranked, and the relevance label they arrive
		// under is what a thin strict answer gets once the ranking is hung
		// underneath it (#3815).
		fmt.Fprintf(b, "%s below %s every word of the query; the rest are ranked by wording. Check that one describes what is happening now before acting on it.\n",
			pluralSessions(strictShown), holdOrHolds(strictShown))
	case namesTheAsked:
		// The same reading recall_context got in #2831: the tier says the
		// exact match failed, not that the answer is unrelated. On a store
		// with competition an ordinary question rarely survives the exact AND,
		// so the sentence below disowned pages whose first session was the
		// right one (#2827).
		fmt.Fprintln(b, "No session matched every word, so these were ranked — the first one names what you asked about. Check that it describes what is happening now before acting on it.")
	case result.Tier == search.TierRelevance:
		// Not "ranked by relevance", which reads as "here is what I found
		// about it". Nothing matched: these are the nearest sessions by
		// wording, and an agent handed them under the old line answered
		// questions about subjects this machine has never held — eight of
		// eight came back with sessions rather than nothing, and the tool
		// description promises an empty result means no record (#2074).
		//
		// But only where it is true. The tier is reached whenever the exact
		// AND misses, which a real question does all the time: measured on
		// this store, four of five questions about work done that same day
		// were headed "No session is about this" while the session below was
		// exactly about it (#657). A session that speaks one of the question's
		// identifying words is about it as far as deja can tell, and saying
		// otherwise teaches the reader to ignore the line that matters.
		// The query's own words, not the stem forms the ranking also tries:
		// the idf map is keyed by what the reader typed.
		asked, _ := query.QueryParts(q)
		if !absent && relevanceHitsAreAboutIt(hits, asked, result.TermIDF) {
			fmt.Fprintln(b, "No exact match; the sessions below are ranked by relevance — check that one describes what is happening now before acting on it.")
			// The count line follows the lead; it said "none about it"
			// under this one.
			namesTheAsked = true
		} else {
			fmt.Fprintln(b, nothingIsAboutThis+" so the sessions below are the nearest by wording — treat them as leads to check, not as a record, and say plainly if none of them answers.")
			saidNothing = true
		}
	}
	if absent && !saidNothing {
		// The tier below relevance already says which word it dropped; this
		// says what dropping it means, in the words the relevance tier uses
		// for the same situation.
		fmt.Fprintln(b, nothingIsAboutThis+" so the sessions below are the nearest by wording — treat them as leads to check, not as a record, and say plainly if none of them answers.")
		saidNothing = true
	}
	return namesTheAsked, saidNothing
}

// writeFormsLine names the word forms or close spellings the ladder used.
func writeFormsLine(b *strings.Builder, result index.SearchResult) {
	how := "word forms"
	if !result.Stemmed {
		how = "close spellings"
	}
	fmt.Fprintf(b, "No exact match; using %s: %s\n", how, strings.Join(fuzzySummary(result.Variants), ", "))
}

// writeRecallHitHead writes one hit's header, its marks and its excerpts.
func writeRecallHitHead(hb *strings.Builder, h search.Hit, i int, twins map[string][]string) {
	writeRecallHitLine(hb, h, i)
	if line := lifecycleLine(h); line != "" {
		fmt.Fprintf(hb, "%s\n", line)
	}
	writeRecallHitMarks(hb, h, twins)
}

// loneWordPageHits is how many sessions a page answered on one word of two
// shows (#5711). They are leads the header already calls nobody's answer, so
// the page names a few and quotes one line of each.
const loneWordPageHits = 3

// writeLoneWordHit is a hit on such a page: its line and one excerpt.
func writeLoneWordHit(hb *strings.Builder, h search.Hit, i int) {
	writeRecallHitLine(hb, h, i)
	for _, sn := range h.Snippets {
		if !strings.HasPrefix(sn, search.UserRuleLabel) {
			fmt.Fprintf(hb, "- %s\n", recallListingLine(sn))
			return
		}
	}
}

// writeRecallHitLine is a hit's numbered line: where, which, how many, when.
func writeRecallHitLine(hb *strings.Builder, h search.Hit, i int) {
	fmt.Fprintf(hb, "\n%d. [%s] %s · %s · %s", i+1,
		recallListingLine(h.Session.Harness), recallListingLine(h.Session.Project), recallListingLine(h.Session.ID), search.MatchCount(h.Count))
	// A session with no user turn is the agent's own words, and the lines
	// below carry no role — so an assertion a model made arrived as a fact
	// from the store (#1107, the shape #1100 fixed for the listing).
	if h.Session.AgentTitle {
		fmt.Fprint(hb, " · agent-opened, no human turn")
	}
	if !h.Session.Updated.IsZero() {
		fmt.Fprintf(hb, " · updated %s (%s)", h.Session.Updated.Local().Format("2006-01-02"), search.RelativeDate(h.Session.Updated))
	}
	if h.Reused > 1 {
		fmt.Fprintf(hb, " · reused %d×", h.Reused)
	}
	fmt.Fprintln(hb)
}

// writeRecallHitMarks writes a hit's bracketed notes, the person's rules and
// its excerpts.
func writeRecallHitMarks(hb *strings.Builder, h search.Hit, twins map[string][]string) {
	// A session that says it backed an approach out carries a signal the
	// lifecycle states would carry if anyone set them by hand — on a real
	// 1160-session store not one did. The wording tells the agent an
	// approach inside was abandoned, not that the whole session is a dead
	// end: the tried-then-fixed session is the most useful one, and the
	// excerpts show which path was dropped.
	// And only where "somewhere in this session" is a place the agent can
	// look: the same rule the search screen got in #3474, from the same
	// function so the two cannot drift.
	if h.Session.GaveUp && h.Lifecycle == "" && search.AbandonmentWorthSaying(h) {
		fmt.Fprintln(hb, "[this session abandoned one approach partway — check the excerpts for which, the rest may still hold]")
	}
	// Sync keeps both copies when a session id is on two machines, and
	// nothing connected them: one session's two histories read as two
	// unrelated sessions, sometimes disagreeing with each other (#1775).
	if twin := twins[h.Session.Harness+":"+h.Session.ID]; len(twin) > 0 {
		// OrigID is the fact; an "imported-" id is only the convention
		// sync mints, and a harness could name a local session that way.
		if h.Session.OrigID != "" {
			fmt.Fprintf(hb, "[another machine's copy of %s, which this machine has too — the two may not say the same thing]\n", joinCapped(twin, 3))
		} else {
			fmt.Fprintf(hb, "[this machine's copy; the same session arrived from elsewhere as %s — they may not say the same thing]\n", joinCapped(twin, 3))
		}
	}
	if h.Superseded != "" {
		fmt.Fprintf(hb, "[earlier attempt — a newer session in this project covers the same ground, updated %s]\n", h.Superseded)
	}
	// `deja brief` counts these — a wrong clock, a transcript copied from a
	// machine set wrong, a harness writing local time as UTC — so deja knew
	// and the page that an agent reads did not. The date stays as the
	// transcript wrote it; what is added is that it cannot be trusted for
	// "newest", which is the one thing the top of a recall page implies
	// (#1753).
	if index.StampedAhead(h.Session.Updated, time.Now()) {
		fmt.Fprintln(hb, "[stamped later than this machine's clock — its date cannot place it against the others]")
	}
	if h.Strict {
		// The tier label belongs to the set; this hit holds the whole
		// query, and printing "relevance" over it is the per-session half
		// of the same false claim (#3815).
		fmt.Fprintln(hb, "[holds every word of the query]")
	} else if h.Tier != search.TierExact {
		fmt.Fprintf(hb, "[%s]\n", h.Tier)
	}
	// The person's rules for this project lead, whether or not an excerpt
	// reached them, and are not repeated below.
	for _, r := range h.Session.UserRules {
		fmt.Fprintf(hb, "- %s%s\n", search.UserRuleLabel, recallListingLine(r))
	}
	for _, sn := range h.Snippets {
		if strings.HasPrefix(sn, search.UserRuleLabel) {
			continue
		}
		fmt.Fprintf(hb, "- %s\n", recallListingLine(sn))
	}
}

// writeRecallConclusions writes what the best hit concluded, the files it
// touched and what it ran, when the budget leaves room for them. With more
// hits waiting, the block keeps recallConclusionsReserve free for them: on a
// replay of real recall calls a first hit with thirty thousand matches spent
// the rest of the page here, and the hit ranked second, the one that
// answered, arrived as half a header line.
func writeRecallConclusions(hb *strings.Builder, dir, q string, h search.Hit, o search.Options, budget, headerRoom int, more bool) {
	block := recallConclusionLines(dir, q, h, o, budget-headerRoom+hb.Len()-recallConclusionsReserve)
	if more {
		block = fitLines(block, budget-headerRoom-hb.Len()-recallConclusionsReserve)
	}
	for _, l := range block {
		hb.WriteString(l)
	}
}

// fitLines drops lines from the end of a conclusions block until it fits in
// room: what it ran first, then the files, then the later conclusions. The
// first conclusion stays, the outcome a small page is still meant to carry.
func fitLines(lines []string, room int) []string {
	keep := 0
	if len(lines) > 1 && strings.HasPrefix(lines[0], "  what this session concluded") {
		keep = 2
	}
	n := 0
	for _, l := range lines {
		n += len(l)
	}
	for len(lines) > keep && n > room {
		n -= len(lines[len(lines)-1])
		lines = lines[:len(lines)-1]
	}
	return lines
}

// recallConclusionLines is the block writeRecallConclusions writes, one line
// per string, given the room the conclusions may take.
func recallConclusionLines(dir, q string, h search.Hit, o search.Options, left int) []string {
	var out []string
	// The plus is deliberate as it stands, and measured: making it a
	// minus — which is what the words above suggest — changes nothing
	// on a full page (identical payloads at 4096, 3000 and 2400 bytes,
	// with and without long conclusions) and only withholds the block
	// on a nearly empty one, where it fits with room to spare. The
	// outer trim is what actually bounds the payload. Left as it is
	// rather than "corrected" blind; whoever changes it should measure
	// the same three budgets first (#1319).
	if left > recallConclusionsMin {
		// The hit carries only the matching messages, so the decision —
		// usually worded nothing like the query — is not in it. Read the
		// whole session for the best hit alone, the same upgrade
		// recall_context makes for the same reason (#1011).
		whole := h.Session
		if full, ok, ferr := wholeSessionForMCP(dir, whole); ferr == nil && ok {
			whole = cutSession(full, o)
		}
		// Not the line already shown as this hit's answer. The answer
		// under the excerpt and the newest conclusion are usually the
		// same sentence, and printing it twice charged the agent twice
		// for one fact — 16% of a small payload — while costing the
		// conclusions list one of its three slots (#1319).
		// Ask for as many extra as there are answer lines above: the
		// drop below can take any of them, and asking for a fixed one
		// more still showed two where three were available.
		// Three more than the list can show: the gate below drops the
		// ones about something else, and asking for exactly three
		// showed one where three were available.
		want := 3 + shownAnswers(h.Snippets) + conclusionGateSlack
		// A long session is read around the passages it was quoted
		// for, not as a whole: its newest conclusions are about
		// whatever it did last.
		src, long := conclusionSource(whole, h.QuotedMessages())
		cs := withoutShownAnswer(digest.SubstantialConclusions(src, left, want), h.Snippets)
		cs, aboutIt := conclusionsAboutIt(cs, q, h.Snippets)
		if long && !aboutIt {
			// The one-line offer below is the newest thing the
			// passages' turns settled, which in a long session is
			// rarely the thing asked about.
			cs = nil
		}
		if len(cs) > 0 {
			if len(cs) > 3 {
				cs = cs[:3]
			}
			label := "  what this session concluded:"
			if !aboutIt {
				// Nothing it concluded is about the question, and this
				// is the newest thing it settled instead. Said plainly,
				// because the label is what an agent reads as the claim.
				label = "  what this session concluded, about its own work:"
			}
			// In their authors' words, and once: a receipt can be a line the
			// excerpts above already show, or a rule the head already marks.
			cs = search.EvidenceRewrite(h.Session, dir, cs)
			cs = slices.DeleteFunc(cs, func(c string) bool {
				return strings.HasPrefix(c, search.UserRuleLabel) ||
					slices.ContainsFunc(h.Snippets, func(sn string) bool { return strings.TrimSpace(sn) == strings.TrimSpace(c) })
			})
			if len(cs) > 0 {
				out = append(out, label+"\n")
			}
			for _, c := range cs {
				out = append(out, "  → "+recallListingLine(c)+"\n")
			}
		}
		// The files that work touched, for a few dozen bytes. Without
		// them an agent that has just learned "we solved this before"
		// still has to search the tree for where — and that search
		// costs far more context than naming the paths here does.
		if paths := recallTouchedLine(dir, h.Session, query.Tokens(q)); paths != "" {
			out = append(out, "  files it touched: "+paths+"\n")
		}
		if diffLines := recallCodeChangedLines(whole, q); len(diffLines) > 0 {
			out = append(out, diffLines...)
		}
		// And what it ran, with the status the harness recorded. The
		// line above says where the work was; this one is the only
		// part of a recall answer that is not a claim — an agent
		// verifies "the suite reads SVC_FIXTURES" by running it, and
		// measured on a 325-file repository it spent ten to sixteen
		// reads doing that after the answer had already arrived
		// (#3951). "exit 0 here" is the evidence that search was for.
		if ran := recallRanLine(dir, h.Session); ran != "" {
			out = append(out, "  "+ran+"\n")
		}
	}
	return out
}

// recallServedFacts is the size, ids and projects of the hits a page served.
func recallServedFacts(hits []search.Hit, served int) (int64, []string, []string) {
	var raw int64
	var ids []string
	// The projects behind the served hits, deduped in the order they appear —
	// what the digest log records so a later rule can be applied to the digest
	// without the sessions still being in the index (#2324).
	var projects []string
	seenProject := map[string]bool{}
	for i, h := range hits {
		if i >= served {
			break
		}
		ids = append(ids, h.Session.ID)
		if p := h.Session.Project; p != "" && !seenProject[p] {
			seenProject[p] = true
			projects = append(projects, p)
		}
		for _, m := range h.Session.Messages {
			raw += int64(len(m.Text))
		}
	}
	return raw, ids, projects
}

// cutMarker ends a line the page budget cut, matching what an excerpt
// shortened on its own already carries.
const cutMarker = " …\n"

// markCut ends a trimmed page with the marker, on its own line. A trim landing
// exactly on a line break needs no marker inside the line, only the newline it
// already has.
func markCut(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	// A trim landing between an excerpt's own ellipsis and its newline already
	// says the line was shortened; a second one reads as "… …".
	if strings.HasSuffix(strings.TrimRight(s, " "), "…") {
		return s + "\n"
	}
	return s + cutMarker
}

func trimUTF8(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len(s) <= budget {
		return s
	}
	for budget > 0 && !utf8.RuneStart(s[budget]) {
		budget--
	}
	return s[:budget]
}

// recallRank runs one recall search and turns its sessions into ranked hits.
func recallRank(dir, q string, o search.Options) (index.SearchResult, search.Options, []search.Hit, int, error) {
	result, err := recallSearch(dir, o, mcpProgress())
	if err != nil {
		return index.SearchResult{}, o, nil, 0, err
	}
	return recallRankFrom(dir, q, o, result)
}

// recallSearch is the index search recall ranks; a test counts the calls.
var recallSearch = index.SearchWithRecoveryDetailed

// recallRankFrom is recallRank on a search already run.
func recallRankFrom(dir, q string, o search.Options, result index.SearchResult) (index.SearchResult, search.Options, []search.Hit, int, error) {
	var err error
	ss := withoutLiveSessions(dir, withoutScoped(result))
	o.Tier = result.Tier
	o.Unread = result.Unread
	if result.Stemmed {
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	} else if result.Fuzzy {
		o.FuzzyVariants = result.Variants
	}
	if o.Tier == search.TierClose && o.FuzzyVariants == nil {
		o.FuzzyVariants = result.Variants
	}
	var hits []search.Hit
	if result.Tier == search.TierError {
		hits = search.ErrorHits(ss, o.Query)
	} else if result.Tier == search.TierRelevance {
		terms := index.RelevanceMatchTerms(q)
		if result.Directory != "" {
			// The directory is what these sessions matched on; without it the
			// snippet chooser looks for the file's words and shows nothing.
			terms = append(terms, result.Directory)
		}
		hits = markStrictHits(search.RelevanceHitsWeighted(ss, terms, result.TermIDF), result)
	} else if hits, err = search.Run(ss, o); err != nil {
		return index.SearchResult{}, o, nil, 0, err
	}
	hits, policyHidden := policyFilterHitsCounted(policy.ActivationMCP, hits)
	policyHidden += unreadWithheld(result)
	o.Activation = policy.ActivationMCP
	if os.Getenv("DEJA_EMBED") != "off" {
		hits = maybeRerank(dir, hits, o, os.Stderr)
	}
	var semantic bool
	hits, semantic = maybeSemantic(dir, hits, o, os.Stderr)
	if semantic {
		// The semantic tier reaches the whole sidecar, past the policy scoping
		// the lexical hits already had; scope its hits too or an imported peer's
		// content the policy withholds reaches the agent through recall.
		hits, _ = policyFilterHitsCounted(policy.ActivationMCP, hits)
	}
	o.Semantic = semantic
	return result, o, hits, policyHidden, nil
}

// withoutScoped is what a search read for its own ranking: the sessions
// read only so a project's answer could be cut from it are left out.
func withoutScoped(r index.SearchResult) []model.Session {
	if r.Unread == nil || len(r.Unread.Scoped) == 0 {
		return r.Sessions
	}
	out := make([]model.Session, 0, len(r.Sessions))
	for _, s := range r.Sessions {
		if !r.Unread.Scoped[s.Harness+":"+s.ID] {
			out = append(out, s)
		}
	}
	return out
}

// unreadWithheld is how many matching sessions the trust policy kept out of
// the pool, which the filter after ranking never sees.
func unreadWithheld(r index.SearchResult) int {
	if r.Unread == nil {
		return 0
	}
	return r.Unread.Withheld
}

// unreadEstimated says unreadSessions is an estimate: retrieval scaled it
// from a sample rather than checking each unread session against the text.
func unreadEstimated(r index.SearchResult) bool {
	return r.Unread != nil && r.Unread.Estimated
}

// aboutCount is n, said as an estimate when about is set.
func aboutCount(n int, about bool) string {
	if about {
		return fmt.Sprintf("about %d", n)
	}
	return strconv.Itoa(n)
}

// unreadSessions is how many matching sessions retrieval left unread.
func unreadSessions(r index.SearchResult) int {
	if r.Unread == nil {
		return 0
	}
	return r.Unread.Sessions
}

// inProjectHits keeps the hits whose session belongs to one of names.
func inProjectHits(hits []search.Hit, names []string) []search.Hit {
	out := hits[:0:0]
	for _, h := range hits {
		for _, n := range names {
			if index.ProjectInScope(h.Session.Project, n) {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

// leadHits is the page the lead line reads: without the machine's best answer
// withMachineBest added. It is there for the reader of the page, and the lines
// above described it instead of the page: on a replay of real recall calls,
// 11 of 59 questions with nothing to find lost "No session is about this" to a
// coverage line about the added session. skipped is its position, or -1.
func leadHits(hits []search.Hit, added string) ([]search.Hit, int) {
	if added == "" {
		return hits, -1
	}
	for i, h := range hits {
		if hitKey(h) == added {
			return slices.Concat(hits[:i], hits[i+1:]), i
		}
	}
	return hits, -1
}

func hitKey(h search.Hit) string { return h.Session.Harness + ":" + h.Session.ID }

// withMachineBest puts the machine-wide ranking's first session second on a
// project-first page, when it is not one of the project's own. Project-first
// otherwise pushes it below every project session that shares a word with the
// question: on a replay of real recall calls the answer sat first machine-wide
// and tenth or lower on the page in 3 of 22 calls, one of them a session
// holding every word of the query.
func withMachineBest(merged, machine, project []search.Hit) []search.Hit {
	if len(machine) == 0 || len(merged) == 0 {
		return merged
	}
	best := machine[0]
	key := best.Session.Harness + ":" + best.Session.ID
	for _, h := range project {
		if h.Session.Harness+":"+h.Session.ID == key {
			return merged
		}
	}
	out := make([]search.Hit, 0, len(merged)+1)
	out = append(out, merged[0], best)
	for _, h := range merged[1:] {
		if h.Session.Harness+":"+h.Session.ID != key {
			out = append(out, h)
		}
	}
	return out
}

// hitsNotIn is hits without the sessions already in lead.
func hitsNotIn(hits, lead []search.Hit) []search.Hit {
	seen := make(map[string]bool, len(lead))
	for _, h := range lead {
		seen[h.Session.Harness+":"+h.Session.ID] = true
	}
	out := make([]search.Hit, 0, len(hits))
	for _, h := range hits {
		if !seen[h.Session.Harness+":"+h.Session.ID] {
			out = append(out, h)
		}
	}
	return out
}
