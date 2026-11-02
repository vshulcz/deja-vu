package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

func recallContext(dir, q string) (string, error) {
	text, _, _, _, err := recallContextResult(dir, q, "")
	return text, err
}

// idContext is what contextByID reports alongside the text: the session it
// opened and what that session weighs, both of which the caller records.
type idContext struct {
	session string
	size    int64
	// note is deja's own word about the session — today, that it was
	// forgotten. It travels beside the text rather than inside it, because
	// the frame the text goes into tells the reader to treat what it holds as
	// untrusted, and this is not recalled content (#1624).
	note string
}

// looksLikeSessionID reports whether a query is a session id rather than
// something somebody would search for.
//
// Every store's id carries a digit, a dash or an underscore — a uuid,
// `ses_01cbcf…`, cline's `1784846110172_0h0xw`, the `arev-cline-json2-…` deja
// writes for a subagent run — and a word carries none of the three, which is
// what keeps this from swallowing an ordinary query: "timeout" and "wiring"
// stay searches even if a session id happens to begin with those letters. Long
// enough to be a prefix worth resolving, and a selector has no spaces in it.
func looksLikeSessionID(q string) bool {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < mcpIDPrefixMin || strings.ContainsAny(q, " \t\n") {
		return false
	}
	marked := false
	for _, r := range q {
		switch {
		case r >= '0' && r <= '9', r == '-', r == '_':
			marked = true
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		default:
			// Anything else — punctuation, a slash, a non-Latin letter — is
			// not in any store's id.
			return false
		}
	}
	return marked
}

// mcpIDPrefixMin is how much of an id has to be given before it is read as
// one, matching the CLI's own bar.
const mcpIDPrefixMin = 6

// contextByID answers from the session an id-prefix names, for the tool an
// agent calls with whatever deja printed at it (#1622). Empty when the string
// is not an id, when it names nothing, or when this machine's policy withholds
// what it names.
func contextByID(dir, q string) (string, idContext, bool) {
	if strings.ContainsAny(q, " \t\n") {
		return "", idContext{}, false
	}
	// index.FindByPrefix directly, never the CLI's findByPrefix: that one calls
	// index.Ensure first and blocks on the index lock, and this path serves a
	// client that cannot wait (mcp_no_block_test.go guards it). The resource
	// reader resolves an id the same way.
	s, ok, err := index.FindByPrefix(dir, q)
	if err != nil || !ok {
		return "", idContext{}, false
	}
	kept, _ := policyFilterSessionsCounted(policy.ActivationMCP, []model.Session{s})
	if len(kept) == 0 {
		return "", idContext{}, false
	}
	whole := kept[0]
	if full, ok, ferr := wholeSessionForMCP(dir, whole); ferr == nil && ok {
		whole = full
	}
	index.MarkUserRules(dir, &whole, howScope(howCwd(), "", false))
	search.ScrubMessages(&whole)
	var b bytes.Buffer
	search.PrintContext(&b, whole, "")
	printed := withChanges(b.String(), whole)
	// The fact before the content, the way the CLI prints it: an agent handed
	// the note promoted from a session it named has to be told the session was
	// forgotten (#1624). It travels out of band — see idContext.note — because
	// deja's own words do not belong inside a frame that tells the reader to
	// treat what it holds as untrusted.
	return printed, idContext{session: whole.ID, size: rawSize([]model.Session{whole}),
		// FindByPrefix resolved this session from the selector, so a prefix
		// here is honest by construction.
		note: forgottenSourceNote(whole, q, true)}, true
}

// recallContextResult keeps the four-value shape its callers and tests read.
func recallContextResult(dir, q, harness string) (string, int, int64, []string, error) { //nolint:unparam // same shape as recallTextResult; changes_block_test.go calls it
	text, sessions, raw, ids, _, _, err := recallContextResultFrom(dir, q, harness)
	return text, sessions, raw, ids, err
}

// recallContextResultFrom is recallContextResult plus the project behind the
// session it served, for the reason recallTextResultFrom exists (#2324).
func recallContextResultFrom(dir, q, harness string) (string, int, int64, []string, []string, string, error) {
	// An id resolves as an id, before the words get a turn. Every session-start
	// block prints one and tells the agent to follow up with this tool, and an
	// id is also a searchable string: any transcript that mentions one matches
	// it lexically, so the search answered first and the fallback below never
	// ran. Measured on a real store, asking for the 120 most recent sessions by
	// their own id prefix brought the right session back 24 times — the other
	// 96 answers were a different session, stated as confidently (#3717). The
	// CLI has had this order since #1614; this is the surface an agent uses.
	if looksLikeSessionID(q) {
		if text, id, ok := contextByID(dir, q); ok {
			return text, 1, id.size, []string{id.session}, nil, id.note, nil
		}
	}
	live, cut := liveLineageCut(dir)
	o := search.Options{Query: nfcfold.Compose(q), Harness: harness, All: true, RecallWorn: usage.WornSessions(dir), ExcludeSessions: live, ExcludeUpTo: cut, Window: 1, Activation: policy.ActivationMCP}
	if stale, err := index.EnsureForSearchStale(dir, o, mcpProgress()); err != nil {
		return "", 0, 0, nil, nil, "", err
	} else if stale && !warmupRunning(dir) {
		requestWarmup(dir)
	}
	result, err := index.SearchWithRecoveryDetailed(dir, o, mcpProgress())
	if err != nil {
		return "", 0, 0, nil, nil, "", err
	}
	ss := withoutLiveSessions(dir, result.Sessions)
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
		hits = markStrictHits(search.RelevanceHitsWeighted(ss, index.RelevanceMatchTerms(q), result.TermIDF), result)
	} else if hits, err = search.Run(ss, o); err != nil {
		return "", 0, 0, nil, nil, "", err
	}
	hits, policyHidden := policyFilterHitsCounted(policy.ActivationMCP, hits)
	policyHidden += unreadWithheld(result)
	var semantic bool
	hits, semantic = maybeSemantic(dir, hits, o, os.Stderr)
	if semantic {
		o.Tier = search.TierSemantic
		// Semantic hits skip the policy scoping the lexical ones got; apply it
		// here too, or recall_context leaks a withheld imported session.
		hits, _ = policyFilterHitsCounted(policy.ActivationMCP, hits)
	}
	hits = dropCopiedHits(dir, hits)
	if len(hits) == 0 {
		// The words found nothing, so try the string as an id. Every line deja
		// prints carries one, and `deja ctx <id>` opens the session it names —
		// but the tool an agent is told to call took words only, and answered
		// "no prior deja sessions matched" about a session deja holds (#1622).
		// After the search, like the CLI does since #1614: there is no answer
		// left for the id to shadow.
		if text, id, ok := contextByID(dir, q); ok {
			return text, 1, id.size, []string{id.session}, nil, id.note, nil
		}
		return emptyRecallAnswerPolicy(dir, q, policyHidden), 0, 0, nil, nil, "", nil
	}
	// The same order the search screen shows: this handed the agent a session
	// the reader had rejected, while search demoted it and said why (#1099).
	attachLifecycles(dir, hits)
	demoteRejected(hits)
	// The hit carries only the matching messages; recall_context is the "full
	// story" tool, so upgrade to the whole session before printing — otherwise
	// an answer worded nothing like the question (the decision itself) never
	// reached the agent, the same gap CLI ctx closed (#1011).
	whole := hits[0].Session
	if full, ok, ferr := wholeSessionForMCP(dir, whole); ferr == nil && ok {
		whole = cutSession(full, o)
	}
	index.MarkUserRules(dir, &whole, howScope(howCwd(), "", false))
	search.ScrubMessages(&whole)
	var b bytes.Buffer
	search.PrintContext(&b, whole, q)
	text := withChanges(b.String(), whole) + contextOthersNote(len(hits)+unreadSessions(result), unreadEstimated(result))
	if hits[0].Tier != search.TierExact {
		// The words below say which one was dropped, and a session matched
		// without it does not name what was asked either — the dropped word
		// is the one that identified the question.
		names := !namedSomethingAbsent(result.Variants) &&
			sessionNamesTheAsked(whole, q, result.TermIDF)
		// Holding most of a long query names it too, as on the recall page.
		if !names && hits[0].Tier == search.TierRelevance && !hits[0].Strict {
			names = bestCoverage([]search.Hit{{Session: whole}}, q, result.TermIDF).aboutIt()
		}
		text = contextTierLead(hits[0].Tier, hits[0].Strict, names) +
			contextIgnoredWords(result) + text
	}
	return text, 1, rawSize([]model.Session{whole}), []string{whole.ID}, projectsOf(whole),
		// The search path reaches a promoted note as often as the id path
		// does — the note carries the id in its own text.
		forgottenSourceNote(whole, q, false), nil
}

// contextIgnoredWords names the query words the search could not use, the way
// the counted page already does.
//
// When the subject of a question is a word no session holds, the stemmed tier
// answers on what is left. recall says which words it threw away — "ignored: no
// session matches it with the rest" — and this tool, which hands an agent a
// whole session rather than a line, printed only `[stemmed]`. So the surface
// carrying the most text was the one that did not say the question's subject
// had been dropped, which is what an agent then answers from (#2827).
func contextIgnoredWords(result index.SearchResult) string {
	if !result.Stemmed && !result.Fuzzy {
		return ""
	}
	summary := fuzzySummary(result.Variants)
	if len(summary) == 0 {
		return ""
	}
	what := "word forms"
	if result.Fuzzy && !result.Stemmed {
		what = "close spellings"
	}
	return fmt.Sprintf("No exact match; using %s: %s\n", what, strings.Join(summary, ", "))
}

// emptyStoreNote is what a tool says when the store holds nothing at all,
// rather than reporting a real absence. An agent told a thing was never done
// concludes exactly that and starts over (#680); on a first run every tool is
// in this state, and recall and blame already say so (#2862, #2863).
//
// Empty when the store has something in it, so a caller can append it blind.
func emptyStoreNote(dir string) string {
	if metas, err := index.AllMeta(dir); err == nil && len(metas) == 0 {
		return " " + emptyStoreSentence("so nothing can be found")
	}
	return ""
}

// noBlameHistorySentence is what blame says when the store has history and this
// file has none. The count is the part an agent can act on: it separates "deja
// looked and nobody touched it" from "deja has barely anything indexed".
func noBlameHistorySentence(name string, indexed int) string {
	return fmt.Sprintf("No session on this machine mentions %s — searched %d indexed session%s. "+
		"Read this as no history for that file, not as a tool that failed.", name, indexed, pluralS(indexed))
}

// emptyStoreSentence says why an empty store is empty.
//
// "No indexed history yet" is right on a first run and wrong after the reader
// forgot everything: the history existed, and "yet" claims it never did. deja
// can tell the two apart — forgetting leaves tombstones — and the search screen
// already separates them on its own output.
func emptyStoreSentence(because string) string {
	if n := len(index.Tombstones()); n > 0 {
		return fmt.Sprintf("This machine has no indexed history left, %s — %d session%s %s been forgotten here (`deja forget --list`). This is a deliberate removal, not an absence of work.",
			because, n, pluralS(n), pluralHave(n))
	}
	return "This machine has no indexed history yet, " + because + " — `deja sources` shows where deja looked for it. Do not read this as the work never happening."
}

// pluralHave keeps the sentence above readable for one session and for many.
func pluralHave(n int) string {
	if n == 1 {
		return "has"
	}
	return "have"
}

// contextOthersNote says how many other sessions matched the question this
// digest answers.
//
// The tool returns one session on purpose. What it did not say is how many it
// chose from, so a digest picked out of forty read exactly like the only thing
// deja held — the misread #1308 fixed for the counted page, where "(5
// match(es))" reads as five exist. blame already names what it left out.
//
// Silent on a single match: a sentence about others would be untrue, and an
// agent told to go looking spends a call finding nothing.
func contextOthersNote(hits int, about bool) string {
	if hits < 2 {
		return ""
	}
	return fmt.Sprintf("\n%s other session%s matched this question — call recall for the list if this one does not answer it.\n",
		aboutCount(hits-1, about), pluralS(hits-1))
}

// relevanceHitsAreAboutIt reports whether some session being served is about
// the question, or whether the page is only the nearest wording to it.
//
// The test is whether one session holds the whole question. The tier is
// reached whenever the exact AND misses, which happens both when the store has
// never held the subject and when it holds it and the reader phrased it
// differently — and the strong warning was printed over both: measured on this
// store, four of five questions about that same day's work were headed "No
// session is about this" while the session below was exactly about it (#657).
//
// A session that speaks the words identifying the question is about it as far
// as deja can tell. Every word was the bar before, and an agent's own phrasing
// rarely clears it: "make test failing repository test suite command" was
// headed "No session is about this" above the session that answers it verbatim,
// because that session says "fails" and never says "command". The question's
// filler decided the warning.
//
// The identifying words are the ones the ranking already judges a session on —
// the rarest leadTermsKept of them, ordered by what the index says each is
// worth. A page where those are spread across different sessions — one holds
// half, another the other half — is still not about it, and keeps the warning
// it earned in #2074, as does a question carrying a word the store has never
// held: the caller decides that one before this (#657).
func relevanceHitsAreAboutIt(hits []search.Hit, terms []string, idf map[string]float64) bool {
	if len(hits) == 0 || len(terms) == 0 || idf == nil {
		return false
	}
	known := make([]string, 0, len(terms))
	for _, t := range terms {
		if _, ok := idf[t]; ok {
			known = append(known, t)
		}
	}
	if len(known) == 0 {
		return false
	}
	lead := byIdentifying(known, idf)
	if len(lead) > leadTermsKept {
		lead = lead[:leadTermsKept]
	}
	// A store of a few sessions collapses every ratio to zero, so byIdentifying
	// orders by shape and the "identifying" words are whichever are longest.
	// There is no way to tell the subject from the filler on such a store, and
	// the cost of guessing is asymmetric: judging the hit on one long ordinary
	// word made a page about a subject the store had never held read as an
	// answer about it, which is the whole of #2074. So the old rule stands
	// exactly where the ranking cannot help — every word, or the caveat.
	if flatIDF(known, idf) {
		lead = known
	}
	for _, h := range hits {
		if sessionSpeaksEvery(h.Session, lead) {
			return true
		}
	}
	return false
}

// flatIDF reports a store that cannot separate these words: every one of them
// is worth the same, which on a real store means a handful of sessions.
func flatIDF(terms []string, idf map[string]float64) bool {
	first, seen := 0.0, false
	for _, t := range terms {
		v, ok := idf[t]
		if !ok {
			continue
		}
		if !seen {
			first, seen = v, true
			continue
		}
		if v != first {
			return false
		}
	}
	return seen
}

// sessionSpeaksEvery reports whether one session says every one of these words
// somewhere in what a person or the agent said.
func sessionSpeaksEvery(s model.Session, terms []string) bool {
	for _, t := range terms {
		if !search.SpeechCarriesAnyTerm(s, []string{t}) {
			return false
		}
	}
	return true
}

// namedSomethingAbsent reports whether a word of the question was dropped for
// having nothing in the store: the recovery tiers record such a term with an
// empty variant, which is how the "ignored" note is written.
func namedSomethingAbsent(variants map[string][]string) bool {
	return index.DroppedAWord(variants)
}

// deepReadFrom is how many matches a served session needs before the page
// says to open it. A hit quotes at most three of its matches, so four already
// leaves something behind; ten is where "the rest" is most of it, and it is
// far below the hundreds a real long session matches.
const deepReadFrom = 10

// deepestServed is the session on this page with the most matches behind it,
// or nil when every served hit fits in its own excerpts.
func deepestServed(hits []search.Hit, served int) *search.Hit {
	var out *search.Hit
	for i := range hits {
		if i >= served {
			break
		}
		if hits[i].Count < deepReadFrom {
			continue
		}
		if out == nil || hits[i].Count > out.Count {
			out = &hits[i]
		}
	}
	return out
}

// nothingIsAboutThis is the half both surfaces share. The wording was tuned in
// #2074 and then written twice — once in the plural over a page of sessions,
// once in the singular over one — so an edit to either would have drifted from
// the other without anything noticing.
const nothingIsAboutThis = "No session is about this. Nothing matched the query,"

// contextTierLead says what the session below it is, for a tier that is not an
// exact match.
//
// A bare `[relevance]` marker was all an agent got above a whole session that
// matched nothing — recall says "No session is about this" in a sentence, and
// this tool, which returns far more text, said it in one word that reads like
// a label on an answer (#2787, the shape #2074 fixed for the counted page).
func contextTierLead(tier string, strict, namesTheAsked bool) string {
	if tier == search.TierRelevance {
		if strict {
			// Not a reading of the wording but a fact about retrieval: this
			// session holds every word that was asked, and it is under the
			// relevance label because the strict answer was too thin to
			// stand on its own (#3815).
			return "This session holds every word of the query; it is under the relevance label because few others do. Check that it describes what is happening now before acting on it.\n"
		}
		if namesTheAsked {
			// The tier says the exact match failed; it does not say the answer
			// is unrelated. On a store with any competition an ordinary
			// question rarely survives the exact AND, so the sentence below
			// fired on 20 of 20 questions lifted verbatim out of indexed
			// sessions — disowning the right answer teaches an agent to
			// ignore the line, which costs what #2074 bought with it (#2827).
			return "No session matched every word, so this one was ranked — it does name what you asked about. Check that it describes what is happening now before acting on it.\n"
		}
		return nothingIsAboutThis + " so the session below is the nearest by wording — treat it as a lead to check, not as a record, and say plainly if it does not answer.\n"
	}
	return "[" + tier + "]\n"
}

// sessionNamesTheAsked reports whether the served session holds the query's
// most identifying word — the one the ranking itself weighted highest.
//
// That is the difference between "nothing matched exactly" and "nothing here is
// about this". A subject the store has never held has none of the query's rare
// words anywhere; a question whose answer is served at rank 1 has them in the
// session below the line (#2827).
//
// Two things about TermIDF decide the shape here, both established by reading
// relevantMetasCounts rather than assumed: it is keyed by the query's terms as
// typed, not by their stem forms, and a term the corpus does not hold never
// reaches it at all. So the terms are RelevanceTerms, not RelevanceMatchTerms —
// the latter adds stem forms that are absent from the map by construction, and
// reading them made every question look like it named something unknown. And a
// term missing from the map is the subject of an absent question, which is why
// it answers no rather than being skipped.
func sessionNamesTheAsked(s model.Session, q string, idf map[string]float64) bool {
	terms := index.RelevanceTerms(q)
	if len(terms) == 0 {
		return false
	}
	rarest, best := "", -1.0
	for _, t := range terms {
		w, ok := idf[t]
		if !ok {
			return false
		}
		if w > best {
			rarest, best = t, w
		}
	}
	// The stem forms belong here, where the text is read: the session may say
	// "pooling" where the question said "pool".
	forms := index.RelevanceMatchTerms(rarest)
	holds := func(text string) bool {
		low := strings.ToLower(text)
		for _, f := range forms {
			if f != "" && strings.Contains(low, strings.ToLower(f)) {
				return true
			}
		}
		return false
	}
	if holds(s.Title) {
		return true
	}
	for _, m := range s.Messages {
		if holds(m.Text) {
			return true
		}
	}
	return false
}
