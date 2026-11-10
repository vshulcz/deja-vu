package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

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
		fmt.Fprintf(b, "%s below %s every word of the query; the rest are ranked by wording. Verify a match fits the current task before acting.\n",
			pluralSessions(strictShown), holdOrHolds(strictShown))
	case namesTheAsked:
		// The same reading recall_context got in #2831: the tier says the
		// exact match failed, not that the answer is unrelated. On a store
		// with competition an ordinary question rarely survives the exact AND,
		// so the sentence below disowned pages whose first session was the
		// right one (#2827).
		fmt.Fprintln(b, "No session matched every word, so these were ranked — the first one names what you asked about. Verify it fits the current task before acting.")
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
			fmt.Fprintln(b, "No exact match; the sessions below are ranked by relevance — verify a match fits the current task before acting.")
			// The count line follows the lead; it said "none about it"
			// under this one.
			namesTheAsked = true
		} else {
			fmt.Fprintln(b, nothingIsAboutThis+" — nearest sessions by wording below; use only if relevant.")
			saidNothing = true
		}
	}
	if absent && !saidNothing {
		if cover.aboutIt() {
			namesTheAsked = true
		} else {
			// The tier below relevance already says which word it dropped; this
			// says what dropping it means, in the words the relevance tier uses
			// for the same situation.
			fmt.Fprintln(b, nothingIsAboutThis+" — nearest sessions by wording below; use only if relevant.")
			saidNothing = true
		}
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
	if twin := twins[h.Session.Key()]; len(twin) > 0 {
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
		roles := map[string]bool{
			"user":               true,
			"assistant":          true,
			"edit":               true,
			"wrote":              true,
			index.RoleStubOthers: true,
		}
		for _, m := range h.Session.Messages {
			if m.Role != "" {
				roles[m.Role] = true
			}
		}
		if full, ok, ferr := wholeSessionForMCPInRoles(dir, whole, roles); ferr == nil && ok {
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
	if !embedOff() {
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
		if !r.Unread.Scoped[s.Key()] {
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

func hitKey(h search.Hit) string { return h.Session.Key() }

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
	key := best.Session.Key()
	for _, h := range project {
		if h.Session.Key() == key {
			return merged
		}
	}
	out := make([]search.Hit, 0, len(merged)+1)
	out = append(out, merged[0], best)
	for _, h := range merged[1:] {
		if h.Session.Key() != key {
			out = append(out, h)
		}
	}
	return out
}

// hitsNotIn is hits without the sessions already in lead.
func hitsNotIn(hits, lead []search.Hit) []search.Hit {
	seen := make(map[string]bool, len(lead))
	for _, h := range lead {
		seen[h.Session.Key()] = true
	}
	out := make([]search.Hit, 0, len(hits))
	for _, h := range hits {
		if !seen[h.Session.Key()] {
			out = append(out, h)
		}
	}
	return out
}
