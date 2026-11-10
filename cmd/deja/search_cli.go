package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// cmdSearch is the explicit form. Bare `deja <words>` also searches, but a
// single word that happens to name a subcommand runs that instead, which is
// how `/deja uninstall` inside a plugin came back with "nothing matches".
// Anything shelling out to deja with user text should use this.
func cmdSearch(dir string, rest []string, sourceInstance string) error {
	if len(rest) == 0 {
		return errors.New("search needs a query")
	}
	return runSearch(dir, rest, sourceInstance)
}

// ensureForCLISearch keeps a search off the rewrite path. Appending the new
// bytes of a grown transcript is cheap and stays inline; work that rewrites the
// index — a store that rewrote itself, a removed file, a harness with no append
// path — is handed to the detached warmup, and the search answers from the
// index it already has. MCP made that trade at #1305 because a rebuild blows
// the client's timeout; the CLI kept waiting, and on a 1.7 GB store a query
// that matched nothing took 194 s while a live Grok session grew (#1521).
//
// `--rebuild` still waits: someone who asked for the rebuild wants its result.
func ensureForCLISearch(dir string, o search.Options, force bool, progress io.Writer) error {
	if force {
		// Asked for by hand, so it builds with automatic indexing off too.
		defer index.AllowBuild()()
		return index.EnsureForSearch(dir, o, true, progress)
	}
	cov, err := index.EnsureForSearchCoverage(dir, o, progress)
	if err != nil {
		return err
	}
	if line := manualIndexLine(dir); line != "" && progress != io.Discard {
		saidManualIndex.Do(func() { fmt.Fprintln(progress, line) })
	}
	if cov.Stale {
		requestWarmup(dir)
		// Only when there is an index to answer from. On a first build there is
		// not, and the two lines contradicted each other on the worst run to
		// contradict anything — a new machine's first search said "answering
		// from the index as it was" and then "the index is being rebuilt right
		// now, run this again in a moment" (#3574).
		if index.ReadableSnapshot(dir) {
			fmt.Fprintln(progress, "deja: "+coverageLine(cov))
		}
	}
	return nil
}

// runBareSearch is `deja <words>` — the form where the first word stood where a
// command name goes. That is the only form the mistyped-command hint is about:
// `deja search doctro` is someone searching for the word (#2197).
func runBareSearch(dir string, args []string, sourceInstance string) error {
	return searchWithOptions(dir, args, sourceInstance, true)
}

// oneEditCommand names the command a bare search's first word is one edit
// from, so the hint comes before the index build and the results (#4628). It
// is a hint, not a refusal: lost, list, logs and fixes are one edit from a
// command and are also words people search for. The broader prefix and
// two-edit hints stay for after the search; a quoted multi-word query is
// still a query.
func oneEditCommand(first string) string {
	low := strings.ToLower(first)
	if len([]rune(low)) < 4 || strings.HasPrefix(low, "-") || strings.ContainsAny(low, " \t\r\n") || dispatchKnows(low) {
		return ""
	}
	for _, name := range commandHintNames() {
		if editDistance(low, name) == 1 {
			return name
		}
	}
	return ""
}

func runSearch(dir string, args []string, sourceInstance string) error {
	return searchWithOptions(dir, args, sourceInstance, false)
}

func searchWithOptions(dir string, args []string, sourceInstance string, bare bool) error {
	force := false
	var filtered []string
	for _, a := range args {
		if a == "--rebuild" || a == "-rebuild" {
			force = true
			continue
		}
		filtered = append(filtered, a)
	}
	o, err := parseSearch(filtered)
	if err != nil {
		return err
	}
	harnessRaw := o.Harness
	if herr := checkHarness(&o.Harness); herr != nil {
		return herr
	}
	if rerr := checkRole(o.Role); rerr != nil {
		return rerr
	}
	sinceRaw := sinceRawArg(filtered)
	// Before the index build, so the reader who meant a command sees it first,
	// and then the search runs as typed. On stderr like every other command
	// hint, and never for --json, where a script reads the results (#4628).
	hinted := false
	if bare && !o.JSON && len(args) > 0 {
		if near := oneEditCommand(args[0]); near != "" {
			fmt.Fprintf(os.Stderr, "deja: %q is not a command — did you mean `deja %s`?\n", args[0], near)
			hinted = true
		}
	}
	o.SourceInstance = sourceInstance
	o.RecallWorn = usage.WornSessions(dir)
	prepareFirstIndexGreeting(dir)
	if err = withBuildProgress(func() error { return ensureForCLISearch(dir, o, force, os.Stderr) }); err != nil {
		// A store that cannot be written still has an index that can be read:
		// answering from it beats answering nothing (#904).
		if !staleUnwritableIndex(dir, err) {
			return ensureError(dir, err)
		}
		fmt.Fprintf(os.Stderr, "deja: answering from the index as it was — %v\n", ensureError(dir, err))
	}
	maybeFirstIndexGreeting(dir)
	// The page is all a capped search shows, so retrieval may rank a large
	// match from the postings first and read only what can reach it (#5467).
	if !o.All {
		o.Window = o.Limit
		if o.Window <= 0 {
			o.Window = search.DefaultLimit
		}
		if pool := rerankCandidateLimit(dir, o); pool > o.Window {
			o.Window = pool
		}
		o.Activation = policy.ActivationSearch
	}
	result, err := index.SearchWithRecoveryDetailed(dir, o, os.Stderr)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	o.Unread = result.Unread
	// Before ranking, not after: the cap is applied while ranking, so a rule
	// that runs later still lets denied sessions occupy the result slots and
	// the allowed ones never reach the page (#1060).
	ss, policyHidden := policyFilterSessionsCounted(policy.ActivationSearch, search.WithoutIgnored(result.Sessions))
	policyHidden += unreadWithheld(result)
	o.PolicyWithheld = policyHidden
	o.Tier = result.Tier
	applySearchLadder(&o, result)
	// The relevance and error tiers hand back a ranked window, not the
	// whole match: date order over them is the newest of that window, and
	// tierWindow says the match ran past it.
	// --all reads newest first unless --sort relevance says otherwise; --json
	// keeps the ranked order a script may rely on unless --sort date asks.
	dated := o.Sort == search.SortDate || o.All && !o.JSON && o.Sort == ""
	hits, tierWindow, windowRead, err := searchTierHits(dir, &o, result, ss, dated)
	if err != nil {
		return err
	}
	hits, demoted := searchAfterRank(dir, hits, &o)
	if note := demotedNote(hits, demoted); note != "" {
		fmt.Fprintf(os.Stderr, "deja: %s\n", note)
	}
	if note := otherWordFormsNote(dir, o, hits); note != "" {
		fmt.Fprint(os.Stderr, fitNotice(os.Stderr, note))
	}
	mistyped := false
	if len(hits) == 0 {
		mistyped = printSearchMiss(dir, o, policyHidden, sinceRaw, harnessRaw, hinted)
	}
	if o.Capped && len(hits) > 0 {
		printSearchCapped(o, len(hits))
	}
	// The answer can also be short for a reason no flag lifts. Counted over the
	// sessions holding every term, so an ordinary search in a store with a big
	// ignored tree stays quiet (#2562).
	//
	// Only where there is an answer to be short: printNoMatches says the same
	// thing on the way to "try fewer words", and a miss printed it twice
	// (#2632).
	if len(hits) > 0 {
		if note := ignoredHiddenNoteFor("answer", index.IgnoredWithAllTerms(dir, query.Tokens(o.Query))); note != "" {
			fmt.Fprint(os.Stderr, note)
		}
	}
	// The window this is being printed into, so the lines can be budgeted
	// rather than assumed 80 wide. Only for a terminal: a pipe gets the whole
	// line, since a script reading deja wants the text and not the layout
	// (#604).
	o.Width = printableWidth(os.Stdout)
	// --all is the whole list, and past the first screen the scores are near
	// ties a reader cannot see, so the order read as random: Jun 2, May 13,
	// Mar 4, then Jun 22 (#45). The whole list reads newest first unless
	// --sort relevance says otherwise; --json keeps the ranked order a script
	// may rely on unless --sort date asks for this one (#5577).
	if dated {
		// The semantic tier and the demotion of rejected sessions reorder
		// after retrieval; the date is the last word.
		search.ByDate(hits)
		if !o.JSON && len(hits) > 1 {
			fmt.Fprintln(os.Stderr, "deja: "+newestFirstNote(o, len(hits), tierWindow, windowRead))
		}
	}
	// Through a counter, so the log records what actually went out rather than
	// a guess at it. `deja log` is the audit of what deja did, and the search
	// kind has been named in the docs, in the comment over the kind constants
	// and in the empty-log line since #47 while nothing ever wrote one (#2471).
	// It stays out of every count: servedKind and injectedKind both exclude it,
	// so the statusline and the impact screen still speak only for memory that
	// reached an agent.
	counted := &countingWriter{w: os.Stdout}
	// The first recall that finds something is a moment for the mark, once per
	// index, only at a terminal and never into a pipe or as JSON (#4624).
	if len(hits) > 0 && !o.JSON && logoWanted(os.Stdout) && firstRecall(dir) {
		mood, line := searchCatMoment(false)
		printLogoMood(os.Stdout, []string{line}, mood)
	}
	search.Print(counted, hits, o)
	usage.RecordResult(dir, usage.KindSearch, counted.n, len(hits), len(hits) == 0)
	// A stamp that has not happened is a date the reader cannot use, and recency
	// is half of what puts a hit where it is. The listing has said so since
	// #2104, the brief and doctor count them, recall marks the one it quotes —
	// search was the surface that printed "Jan 1 2099" beside an answer and left
	// the reader to work out what it meant (#3595).
	if n := stampedAheadHits(hits, time.Now()); n > 0 {
		fmt.Fprintf(os.Stderr, "deja: %d session%s here stamped later than this machine's clock — a stamp that has not happened cannot be placed against the others\n",
			n, pluralS(n))
	}
	return searchAfterPrint(dir, o, len(hits), bare, hinted, mistyped)
}

// searchAfterPrint is what search says once the results are out: the hint
// for a mistyped command, its exit code, and the daily tip.
func searchAfterPrint(dir string, o search.Options, found int, bare, hinted, mistyped bool) error {
	// A wrong guess at a command name falls through to search, and the hint
	// that names it ran only on an empty result — so a typo whose word happens
	// to be in the history got a conversation back and nothing about the
	// command it meant (#2197). After the results, on stderr, and only for the
	// bare form: `deja search doctro` is someone searching for the word.
	//
	// One word only, which is narrower than the empty-result hint on purpose.
	// There, a hint costs nothing over a failed search; here it lands on an
	// answer the reader may well have wanted, and "doctors pool" is a search
	// however close its first word sits to a command name.
	if bare && !hinted && !o.JSON && found > 0 && len(strings.Fields(o.Query)) == 1 {
		fmt.Fprint(os.Stderr, commandHint(o.Query))
	}
	// A search that found nothing is not a failure — that is the ordinary
	// answer to a question the history cannot settle. A command name that does
	// not exist is: `deja unforget x` did nothing, said so, and exited 0, so a
	// script that ran it and checked the code was told otherwise. The hint is
	// narrow enough to carry the distinction — it stays quiet unless a real
	// command is within one edit of what was typed.
	if mistyped {
		return errAlreadySaid
	}
	// One line, at most once a day, naming a command the reader may not have
	// met (#4629). Only after an answer, only in a terminal and never for
	// --json: a pipe, a script or a hook reads the results, not advice. On
	// stderr, so the results themselves stay what they were, and only when
	// stderr is a terminal too: `2>file` is a log, not a reader.
	if found > 0 && !o.JSON && briefWanted(os.Stdout) && briefWanted(os.Stderr) {
		maybeTip(os.Stderr, dir+".tip", time.Now())
	}
	return nil
}

// searchAfterRank runs what comes after the tier ranked: the rerank, the
// semantic tier and the lifecycle marks. It returns the hits and how many
// rejected ones were moved down.
func searchAfterRank(dir string, hits []search.Hit, o *search.Options) ([]search.Hit, int) {
	// A rerank reorders by meaning, which date order throws away again.
	if !o.Regex && !o.NoEmbed && !embedOff() && o.Sort != search.SortDate {
		hits = maybeRerank(dir, hits, *o, os.Stderr)
	}
	var semantic bool
	hits, semantic = maybeSemantic(dir, hits, *o, os.Stderr)
	if semantic {
		// The lexical hits were scoped by the trust policy above, but the
		// semantic tier reaches the whole sidecar and brings back sessions of
		// its own — an imported peer's content the policy withholds from every
		// other read path leaked straight through the embedding fallback. Scope
		// the semantic hits the same way before they are shown.
		hits, _ = policyFilterHitsCounted(policy.ActivationSearch, hits)
	}
	o.Semantic = semantic
	// What the semantic tier and reranking brought in, uncapped: a fork's hit
	// quoting its source again goes here too (#5320).
	hits = dropCopiedHits(dir, hits)
	// Policy scoping, reranking and the semantic tier all run after the cap, so
	// the pre-cap count can no longer describe what is being returned. When
	// nothing was capped the honest total is simply what survived; when it was,
	// there is no way to know how the filters would have treated the hidden
	// ones, so the pre-cap figure stands and `capped` says to distrust it.
	attachLifecycles(dir, hits)
	demoted := demoteRejected(hits)
	attachMoved(hits)
	if !o.Capped {
		o.Total, o.TotalEstimated = len(hits), false
	}
	if !o.All {
		var pageCapped bool
		hits, pageCapped = capTierHits(hits, *o)
		o.Capped = o.Capped || pageCapped
	}
	return hits, demoted
}

// applySearchLadder records which rung of the word ladder answered, and says
// so on stderr.
func applySearchLadder(o *search.Options, result index.SearchResult) {
	switch {
	case result.Neighbour:
		printNeighbour(os.Stderr, result.Variants)
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	case result.Stemmed:
		printStemmed(os.Stderr, result.Variants)
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	case result.Fuzzy:
		printFuzzy(os.Stderr, result.Variants)
		o.Fuzzy = true
		o.FuzzyVariants = result.Variants
	}
	if result.Tier == search.TierClose && o.FuzzyVariants == nil {
		o.FuzzyVariants = result.Variants
	}
}

// searchTierHits turns what retrieval handed back into hits, the way the tier
// that answered ranks them. tierWindow says the tier's window cut the match
// short and windowRead how much of it was read in date order.
func searchTierHits(dir string, o *search.Options, result index.SearchResult, ss []model.Session, dated bool) (hits []search.Hit, tierWindow bool, windowRead int, err error) {
	rankOpt := *o
	if pool := rerankCandidateLimit(dir, *o); pool > rankOpt.Limit {
		rankOpt.Limit = pool
	}
	switch result.Tier {
	case search.TierError:
		// A pasted error IS a match; ErrorHits keeps the ranking and the error
		// neighbourhood the tier found. Re-scoring it as an ordinary run would
		// zero it out — the word ladder already failed, which is why this tier
		// fired at all.
		fmt.Fprintln(os.Stderr, "deja: matched by error signature; showing the sessions that hit it")
		hits = search.ErrorHits(ss, o.Query)
		o.Total, o.Capped = result.Total, result.Capped
		tierWindow = result.Capped
		errAt := len(hits)
		hits = dropCopiedHits(dir, hits)
		if d := errAt - len(hits); o.Total >= d {
			o.Total -= d
		}
		if o.Total < len(hits) {
			o.Total = len(hits)
		}
		if o.Sort == search.SortDate {
			search.ByDate(hits)
			windowRead = len(hits)
		}
		var capped bool
		hits, capped = capTierHits(hits, rankOpt)
		o.Capped = o.Capped || capped
	case search.TierRelevance:
		o.Strict = result.Strict
		lead := relevanceLead(result.Strict)
		if dated && result.Strict > 0 {
			lead = strings.Replace(lead, "the rest below are ranked by relevance", "they come first and the rest after, each newest first", 1)
		}
		fmt.Fprintln(os.Stderr, lead)
		hits = markStrictHits(search.RelevanceHitsWeighted(ss, index.RelevanceMatchTerms(o.Query), result.TermIDF), result)
		relAt := len(hits)
		hits = dropCopiedHits(dir, hits)
		relAt -= len(hits)
		// This tier ranks and truncates inside retrieval, so counting the
		// sessions it handed back measures its window, not the match: every
		// query deeper than the window reported the window's own size and
		// capped: false, which told a consumer to stop checking exactly when
		// there was something to check. Take the tier's figures when it has
		// them; the paths that report none (a quoted query retried without
		// its quotes, served here under the relevance label) never truncated,
		// so what arrived is the whole of it.
		o.Total, o.Capped = result.Total, result.Capped
		if o.Total >= relAt {
			o.Total -= relAt
		}
		if o.Total < len(hits) {
			o.Total = len(hits)
		}
		tierWindow = result.Capped
		if o.Sort == search.SortDate {
			search.ByDate(hits)
			windowRead = len(hits)
		}
		var relCapped bool
		hits, relCapped = capTierHits(hits, rankOpt)
		o.Capped = o.Capped || relCapped
	default:
		// RunDetailed rather than Run: the JSON envelope reports how many
		// sessions matched before the cap, and that is not recoverable from a
		// list the cap has already trimmed.
		// Copies go before the cap: a fork repeating its source took a slot
		// and counted in the total (#5320, #5335).
		keep := func(hs []search.Hit) []search.Hit { return dropCopiedHits(dir, hs) }
		detailed, rerr := search.RunDetailedKeeping(ss, rankOpt, keep)
		// A date page drawn from the newest matches by their postings comes
		// back short when few of them say a quoted phrase and the rest were
		// too many to check. It is read whole then, as the screen does.
		if rerr == nil && o.Sort == search.SortDate && o.Window > 0 && len(detailed.Hits) < o.Window && detailed.Total > len(detailed.Hits) {
			whole := *o
			whole.Window, whole.Unread = 0, nil
			if r2, err2 := index.SearchWithRecoveryDetailed(dir, whole, io.Discard); err2 == nil && r2.Tier == result.Tier {
				ss2, _ := policyFilterSessionsCounted(policy.ActivationSearch, search.WithoutIgnored(r2.Sessions))
				whole.Unread = r2.Unread
				if d2, e2 := search.RunDetailedKeeping(ss2, whole, keep); e2 == nil {
					detailed, o.Unread = d2, r2.Unread
				}
			}
		}
		if rerr != nil {
			// "run:" is the name of a function, and the reader of this line is
			// someone who mistyped a pattern (#2286). The only error that
			// reaches here in practice is the regexp compile, so it says which
			// input to look at, in the words the other bad flags use (#1602).
			if o.Regex {
				return nil, false, 0, rePatternError(o.Query, rerr)
			}
			return nil, false, 0, rerr
		}
		hits, o.Total, o.Capped, o.TotalEstimated = detailed.Hits, detailed.Total, detailed.Capped, detailed.Estimated
	}
	return hits, tierWindow, windowRead, nil
}
