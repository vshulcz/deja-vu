package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
	type pending struct {
		hitIdx      int
		matchedUser string
	}
	var want []index.Identity
	var pend []pending
	seen := map[string]bool{}
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
		id := index.Identity{Harness: h.Session.Harness, ID: h.Session.ID}
		if !seen[id.Key()] {
			seen[id.Key()] = true
			want = append(want, id)
		}
		pend = append(pend, pending{hitIdx: i, matchedUser: matchedUser})
	}
	if len(want) == 0 {
		return
	}
	sessions, err := index.FindManyByIdentityInRoles(dir, want, map[string]bool{
		"user":               true,
		"assistant":          true,
		index.RoleStubOthers: true,
	})
	if err != nil {
		return
	}
	byKey := make(map[string]model.Session, len(sessions))
	for _, s := range sessions {
		byKey[s.Key()] = s
	}
	for _, p := range pend {
		h := &hits[p.hitIdx]
		full, ok := byKey[h.Session.Key()]
		if !ok {
			continue
		}
		for mi, m := range full.Messages {
			if m.Role != "user" || m.Text != p.matchedUser {
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

func wholeSessionForMCPInRoles(dir string, s model.Session, roles map[string]bool) (model.Session, bool, error) {
	return index.FindByIdentityInRoles(dir, s.Harness, s.ID, roles)
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
	pol := policy.Load()
	visible := make([]index.SessionMeta, 0, len(metas))
	for _, m := range metas {
		if metaVisible(pol, policy.ActivationMCP, m) {
			visible = append(visible, m)
		}
	}
	return index.TwinSessions(visible)
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
		if text, id, ok := contextByID(dir, q, harness); ok {
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
	var fixHead string
	var fixCount int
	if offset == 0 {
		fixHead, fixCount = recallErrorFixesBlock(dir, q)
	}
	result, o, hits, policyHidden, cov := r.result, r.o, r.hits, r.policyHidden, r.cov
	unread, about := r.unread, r.about
	if len(hits) == 0 {
		if fixHead != "" {
			return strings.TrimSpace(fixHead), fixCount, 0, nil, nil, nil
		}
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
	pageBudget := budget - len(fixHead)
	if pageBudget < 200 {
		pageBudget = budget
		fixHead = ""
	}
	out, served, raw, ids, projects, err := recallPage(dir, q, pageBudget, offset, limit, total, demoted, about, result, o, hits, twins, cov, r.added)
	if err == nil && fixHead != "" {
		out = fixHead + out
	}
	return out, served, raw, ids, projects, err
}

// recallErrorFixesBlock returns a compact remedy header when a default recall
// query is itself a recognised error line (`index.FrictionLine`) with a
// confirmed remedy on this machine, so agents that call `recall` instead of
// `fix` on an error still receive the command or edit that resolved it.
func recallErrorFixesBlock(dir, q string) (string, int) {
	if _, ok := index.FrictionLine(q); !ok {
		return "", 0
	}
	pol := policy.Load()
	pairs := index.FixesFor(dir, q, 2, func(project string) bool {
		return pol.Allows(policy.ActivationMCP, project)
	})
	if len(pairs) == 0 {
		return "", 0
	}
	var b strings.Builder
	b.WriteString("Remedy from prior sessions on this machine:\n")
	shown := 0
	for _, p := range pairs {
		if p.Candidate {
			continue
		}
		if p.Edit != "" {
			fmt.Fprintf(&b, "  changed next: %s\n", search.SafePath(p.Edit))
			shown++
			continue
		}
		if p.Command != "" {
			fmt.Fprintf(&b, "  ran next: %s\n", commandListingLine(p.Command))
			shown++
		}
	}
	if shown == 0 {
		return "", 0
	}
	b.WriteByte('\n')
	return b.String(), shown
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
