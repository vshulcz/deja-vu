package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

var version = "dev"

func main() {
	stopProfiling := startProfiling()
	if err := run(os.Args[1:]); err != nil {
		stopProfiling()
		fmt.Fprintln(os.Stderr, "deja:", rebuildWindowError(err))
		os.Exit(1)
	}
	stopProfiling()
}

// rebuildInProgress is a seam: a test can put the process in the window
// without racing a real rebuild.
var rebuildInProgress = index.RebuildInProgress

// rebuildWindowError names the one state that looks like a broken store and is
// not: a rebuild recreates the index directory, and a reader that lands in that
// window gets `open …/manifest.gob: no such file or directory` (#822).
func rebuildWindowError(err error) error {
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !rebuildInProgress(index.DefaultDir()) {
		return err
	}
	return fmt.Errorf("the index is being rebuilt right now — run this again in a moment")
}

func loadAll(h string) []model.Session {
	var ss []model.Session
	if h == "" || h == "claude" {
		ss = append(ss, sources.LoadClaude()...)
	}
	if h == "" || h == "codex" {
		ss = append(ss, sources.LoadCodex()...)
	}
	if h == "" || h == "opencode" {
		ss = append(ss, sources.LoadOpencode()...)
	}
	if h == "" || h == "aider" {
		ss = append(ss, sources.LoadAider()...)
	}
	if h == "" || h == "gemini" {
		ss = append(ss, sources.LoadGemini()...)
	}
	if h == "" || h == "cursor" {
		ss = append(ss, sources.LoadCursor()...)
	}
	if h == "" || h == "antigravity" {
		ss = append(ss, sources.LoadAntigravity()...)
	}
	if h == "" || h == "grok" {
		ss = append(ss, sources.LoadGrok()...)
	}
	if h == "" || h == "qwen" {
		ss = append(ss, sources.LoadQwen()...)
	}
	if h == "" || h == "kimi" {
		ss = append(ss, sources.LoadKimi()...)
	}
	if h == "" || h == "goose" {
		ss = append(ss, sources.LoadGoose()...)
	}
	if h == "" || h == "hermes" {
		ss = append(ss, sources.LoadHermes()...)
	}
	return ss
}

func loadFileSources() []model.Session {
	var ss []model.Session
	for _, harness := range []string{"claude", "codex", "aider", "gemini", "cursor", "antigravity", "grok", "qwen", "kimi", "goose", "hermes"} {
		ss = append(ss, loadAll(harness)...)
	}
	return ss
}

type command func(dir string, rest []string) error

var commands = map[string]command{
	"version":   cmdVersion,
	"help":      func(_ string, _ []string) error { printUsage(); return nil },
	"--help":    func(_ string, _ []string) error { printUsage(); return nil },
	"-h":        func(_ string, _ []string) error { printUsage(); return nil },
	"--version": cmdVersion,
	"-version":  cmdVersion,
	"sources": func(dir string, rest []string) error {
		// The command takes nothing, and --json exists on seven of its
		// neighbours — a script that reaches for it here got the
		// tab-separated table back and parsed it as JSON (#747).
		for _, a := range rest {
			return fmt.Errorf("sources takes no arguments — got %q", a)
		}
		printSources(dir)
		return nil
	},
	"completion":    func(_ string, rest []string) error { return runCompletion(rest) },
	"doctor":        func(dir string, rest []string) error { return runDoctor(os.Stdout, rest, doctorLookup, dir) },
	"warmup":        cmdWarmup,
	"warmup-status": cmdWarmupStatus,
	"index":         cmdIndex,
	"embed":         runEmbed,
	"bench":         func(_ string, rest []string) error { return runBench(rest) },
	"statusline":    func(dir string, _ []string) error { return runStatusline(dir, os.Stdin, os.Stdout) },
	"stats":         runStats,
	"remember":      runRemember,
	"promote":       func(dir string, rest []string) error { return runPromote(dir, rest, os.Stdout) },
	"forget":        runForget,
	"mcp":           func(dir string, _ []string) error { return serveMCP(dir, os.Stdin, os.Stdout) },
	"hook-prompt": func(dir string, rest []string) error {
		plain := len(rest) > 0 && (rest[0] == "--plain" || rest[0] == "-plain")
		return runHookPromptMode(dir, os.Stdin, os.Stdout, plain)
	},
	"hook-antigravity": func(dir string, _ []string) error {
		return runHookAntigravity(dir, os.Stdin, os.Stdout)
	},
	"hook-context":    cmdHookContext,
	"hook-precompact": func(dir string, _ []string) error { runHookPrecompact(dir); return nil },
	"hook-refresh":    func(dir string, _ []string) error { runHookRefresh(dir); return nil },
	"view":            runView,
	"install":         func(dir string, rest []string) error { return runInstall(dir, rest, false) },
	"uninstall":       func(dir string, rest []string) error { return runInstall(dir, rest, true) },
	"update":          func(_ string, rest []string) error { return runUpdate(rest, os.Stdout) },
	"share":           func(dir string, rest []string) error { return runShare(dir, rest, os.Stdout) },
	"resume":          func(dir string, rest []string) error { return runResume(dir, rest, os.Stdout) },
	"handoff":         func(dir string, rest []string) error { return runHandoff(dir, rest, os.Stdout) },
	"files":           func(dir string, rest []string) error { return runFiles(dir, rest, os.Stdout) },
	"restore":         func(dir string, rest []string) error { return runRestore(dir, rest, os.Stdout) },
	"friction":        func(dir string, rest []string) error { return runFriction(dir, rest, os.Stdout) },
	"log":             runLog,
	"sync":            runSync,
	"ctx":             cmdCtx,
	// Wrappers, but only with arguments: bare `deja aider` is far more
	// likely someone searching for the word than someone asking to launch
	// an editor, and launching one from a search is not a mistake worth
	// making. See cmdAider/cmdGoose.
	"hook-goose": cmdGooseHook,
	"blame":      runBlame,
}

func run(args []string) error {
	dir := index.DefaultDir()
	if len(args) == 0 {
		if logoWanted(os.Stdout) {
			return runBrief(dir, os.Stdout)
		}
		printUsage()
		return nil
	}
	sourceInstance := os.Getenv("DEJA_SOURCE_INSTANCE")
	switch args[0] {
	case "show":
		return cmdShow(dir, args[1:], sourceInstance)
	case "last":
		return cmdLast(dir, args[1:], sourceInstance)
	case "search":
		return cmdSearch(dir, args[1:], sourceInstance)
	case "aider":
		return cmdAider(dir, args[1:], sourceInstance)
	case "goose":
		return cmdGoose(dir, args[1:], sourceInstance)
	}
	if cmd, ok := commands[args[0]]; ok {
		return cmd(dir, args[1:])
	}
	return runSearch(dir, args, sourceInstance)
}

func cmdVersion(_ string, _ []string) error {
	fmt.Fprintf(os.Stdout, "deja %s\n", version)
	return nil
}

func cmdWarmup(dir string, _ []string) error {
	prepareFirstIndexGreeting(dir)
	if err := withBuildProgress(func() error { return index.Ensure(dir, "", false, os.Stderr) }); err != nil {
		return err
	}
	maybeFirstIndexGreeting(dir)
	return nil
}

func cmdIndex(dir string, rest []string) error {
	force := false
	for _, a := range rest {
		if a == "--rebuild" || a == "-rebuild" {
			force = true
			continue
		}
		return fmt.Errorf("index: unknown flag %q", a)
	}
	// Silence reads as "it did not run". `update` on the newest release and
	// `doctor` on a fresh index both say so; this one returned to the prompt
	// with nothing (#824). Only here: on a search the same line would be noise.
	if fresh, n := index.UpToDate(dir, ""); fresh && !force {
		fmt.Fprintf(os.Stderr, "deja: index is up to date (%d session%s)\n", n, pluralS(n))
		return nil
	}
	prepareFirstIndexGreeting(dir)
	// The detached warmup publishes its progress so hooks can tell the user
	// memory is on its way; an interactive run draws the live display.
	build := func() error { return index.Ensure(dir, "", force, os.Stderr) }
	if err := withWarmupStatus(dir, func() error { return withBuildProgress(build) }); err != nil {
		// The command whose whole job is building the index used to pass the
		// syscall through — `mkdir /…/index.db.tmp: permission denied` names
		// an internal temp path and no fix, while every reading command has
		// said what to change since ensureError was written (#798).
		return ensureError(dir, err)
	}
	clearWarmupSentinel()
	// Two transcripts can carry the same harness:id — two files with the same
	// name in different projects. Both stay searchable, but one manifest row
	// holds them, so one project name covers both. Silence was the worst part
	// of it: the indexer counted every session on disk while the manifest held
	// fewer, and nothing connected the two numbers (#698).
	// A store deja could not read loses its sessions from recall, and the index
	// run is where someone is looking at the counts — doctor knowing about it
	// is not the same as saying it here (#818).
	for _, h := range sortedHarnesses(index.IngestHealth(dir)) {
		e := index.IngestHealth(dir)[h]
		if e.FailedFiles == 0 {
			continue
		}
		fmt.Fprintf(os.Stderr, "deja: %s: %d path%s could not be read — `deja doctor` names %s\n",
			h, e.FailedFiles, pluralS(e.FailedFiles), pluralWhich(e.FailedFiles))
	}
	if n := index.ReportCollisions(); n > 0 {
		fmt.Fprintf(os.Stderr, "deja: %d session%s %s an id with another transcript — each pair is filed under one project, the one whose file sorts first\n", n, pluralS(n), verbShare(n))
	}
	maybeFirstIndexGreeting(dir)
	return nil
}

func cmdHookContext(dir string, rest []string) error {
	plain := len(rest) > 0 && rest[0] == "--plain"
	_ = runHookContext(dir, plain)
	return nil
}

func cmdShow(dir string, rest []string, sourceInstance string) error {
	o, err := parseShow(rest)
	if err != nil {
		return err
	}
	if o.id == "" {
		return fmt.Errorf("show needs id-prefix")
	}
	var s model.Session
	var ok bool
	if o.harness != "" {
		// Exact identity first — that is what --harness is for, and what
		// --json requires. But the usage line documents an id *prefix*, and
		// routing --harness straight to the exact lookup made every
		// prefix+harness call fail: "deja show 019fa282 --harness codex" said
		// no session matches while the same prefix without --harness worked.
		s, ok, err = index.FindByIdentity(dir, o.harness, o.id)
		if err == nil && !ok {
			s, ok, err = findByPrefixHarness(dir, o.id, o.harness)
		}
	} else {
		s, ok, err = findByPrefix(dir, o.id)
	}
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no session matches %q", o.id)
	}
	if o.json {
		return printSessionJSON(os.Stdout, s, o.offset, o.limit, sourceInstance)
	}
	if o.sliced {
		// Both flags are documented for `show` and only the JSON path honoured
		// them; the text output printed the whole session (#709).
		s.Messages = sliceMessages(s.Messages, o.offset, o.limit)
	} else if n := len(s.Messages); n > showLargeSession {
		fmt.Fprintf(os.Stderr, "deja: %d messages — `--offset n --limit n` reads a slice\n", n)
	}
	// The prefix picks the newest of its matches, which is the right default
	// and, until now, a silent one: "2" resolved eleven sessions on a real
	// store and the reader had no way to know they were shown a choice.
	if o.harness == "" {
		if n := index.PrefixMatches(dir, o.id); n > 1 {
			// When the matches are the same id in different harnesses there is
			// no longer prefix to reach for, and --harness is the only thing
			// that separates them (#719).
			if hs := index.PrefixHarnesses(dir, o.id); len(hs) > 1 {
				fmt.Fprintf(os.Stderr, "deja: %d sessions share the id %q — showing the most recent; use --harness %s\n",
					len(hs), o.id, strings.Join(hs, "|"))
			} else {
				fmt.Fprintf(os.Stderr, "deja: %d sessions start with %q — showing the most recent; use a longer prefix for another\n", n, o.id)
			}
		}
	}
	search.PrintSession(os.Stdout, s)
	return nil
}

type showOptions struct {
	id, harness   string
	json          bool
	offset, limit int
	// sliced records that the reader asked for a slice, as opposed to the
	// JSON default: applying 50 to the text output would silently truncate
	// `deja show` for everyone who reads a session whole (#709).
	sliced bool
}

// showLargeSession is where `deja show` mentions the flags that read a slice.
// A 200k-message transcript prints 600 001 lines.
const showLargeSession = 1000

func parseShow(args []string) (showOptions, error) {
	o := showOptions{limit: 50}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--json":
			o.json = true
		case "--harness", "--offset", "--limit":
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs value", a)
			}
			i++
			if a == "--harness" {
				o.harness = args[i]
				continue
			}
			n, e := strconv.Atoi(args[i])
			if e != nil || n < 0 || (a == "--limit" && n == 0) {
				return o, fmt.Errorf("%s needs a positive integer", a)
			}
			if a == "--offset" {
				o.offset = n
				o.sliced = true
			} else {
				o.limit = n
				o.sliced = true
			}
		default:
			if strings.HasPrefix(a, "-") {
				return o, fmt.Errorf("show: unknown flag %q", a)
			}
			if o.id != "" {
				return o, fmt.Errorf("show accepts one session id")
			}
			o.id = a
		}
	}
	// The id first: it is what the command is for, and checking the flag ahead
	// of it cost two runs to learn two missing things (#820).
	if o.id == "" {
		return o, fmt.Errorf("show needs id-prefix")
	}
	if o.json && o.harness == "" {
		return o, fmt.Errorf("show --json requires --harness for exact identity")
	}
	if o.limit > 200 {
		return o, fmt.Errorf("show --limit must not exceed 200")
	}
	return o, nil
}

func cmdCtx(dir string, rest []string) error {
	if len(rest) < 1 {
		return fmt.Errorf("ctx needs query or id-prefix")
	}
	// ctx takes no flags, and --json/--harness/--project/--since all exist on
	// neighbouring commands — so reaching for one here is the obvious mistake.
	// Folding it into the query answered "no session matches", which is a
	// false statement about the store (#721).
	for _, a := range rest {
		if strings.HasPrefix(a, "--") {
			return fmt.Errorf("ctx takes no flags, only a query or id-prefix — got %q", a)
		}
	}
	q := strings.Join(rest, " ")
	if !strings.Contains(q, " ") && len(q) >= 6 {
		s, ok, err := findByPrefix(dir, q)
		if err != nil {
			return err
		}
		if ok {
			search.PrintContext(os.Stdout, s, "")
			return nil
		}
	}
	o := search.Options{Query: q, All: true}
	if err := index.EnsureForSearch(dir, o, false, os.Stderr); err != nil {
		return err
	}
	ss, err := index.SearchWithRecovery(dir, o, os.Stderr)
	if err != nil {
		return err
	}
	hits, err := search.Run(ss, o)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		return fmt.Errorf("no session matches %q", q)
	}
	search.PrintContext(os.Stdout, hits[0].Session, q)
	return nil
}

func cmdLast(dir string, rest []string, sourceInstance string) error {
	n, o, sinceRaw, err := parseLast(rest)
	if err != nil {
		return err
	}
	ss, err := recentMatching(dir, n, o)
	if err != nil {
		return err
	}
	// Printing nothing at all and exiting 0 leaves no way to tell whether the
	// command worked, found nothing, or failed silently — which is what a
	// fresh install sees. blame already answers this shape of question.
	if len(ss) == 0 {
		if o.JSON {
			return printRecentJSON(os.Stdout, nil, sourceInstance)
		}
		// "Run deja index" is advice for an empty store. With a filter set it
		// is advice for a state the tool is not in: indexing changes nothing
		// and doctor reports the stores as found. Name what emptied the result
		// instead (#637).
		if where := activeFilters(o, sinceRaw); where != "" {
			fmt.Fprintf(os.Stderr, "deja: no sessions match %s\n", where)
			return nil
		}
		fmt.Fprintln(os.Stderr, emptyIndexHint("no sessions indexed yet"))
		return nil
	}
	if o.JSON {
		return printRecentJSON(os.Stdout, ss, sourceInstance)
	}
	for _, s := range ss {
		// A session whose timestamp was missing or unparseable carries the Go
		// zero time, and "0001-01-01" reads as corrupted data rather than as a
		// missing field. Search prints a dash here and the first screen leaves
		// such sessions out of its range; this was the one place that did not
		// follow the convention (#765).
		when := "-"
		if !s.Updated.IsZero() {
			when = s.Updated.Format("2006-01-02")
		}
		fmt.Printf("[%s · %s · %s · %s]", s.Harness, s.Project, when, s.ID)
		title := s.Title
		if title == "" {
			title = firstUserTitle(s)
		}
		if title != "" {
			fmt.Printf(" %s", title)
		}
		fmt.Println()
	}
	return nil
}

// cmdSearch is the explicit form. Bare `deja <words>` also searches, but a
// single word that happens to name a subcommand runs that instead, which is
// how `/deja uninstall` inside a plugin came back with "nothing matches".
// Anything shelling out to deja with user text should use this.
func cmdSearch(dir string, rest []string, sourceInstance string) error {
	if len(rest) == 0 {
		return fmt.Errorf("search needs a query")
	}
	return runSearch(dir, rest, sourceInstance)
}

func runSearch(dir string, args []string, sourceInstance string) error {
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
	o.SourceInstance = sourceInstance
	o.RecallWorn = usage.WornSessions(dir)
	prepareFirstIndexGreeting(dir)
	if err := withBuildProgress(func() error { return index.EnsureForSearch(dir, o, force, os.Stderr) }); err != nil {
		return ensureError(dir, err)
	}
	maybeFirstIndexGreeting(dir)
	result, err := index.SearchWithRecoveryDetailed(dir, o, os.Stderr)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	ss := result.Sessions
	o.Tier = result.Tier
	if result.Stemmed {
		printStemmed(os.Stderr, result.Variants)
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	} else if result.Fuzzy {
		printFuzzy(os.Stderr, result.Variants)
		o.Fuzzy = true
		o.FuzzyVariants = result.Variants
	}
	if result.Tier == search.TierClose && o.FuzzyVariants == nil {
		o.FuzzyVariants = result.Variants
	}
	var hits []search.Hit
	if result.Tier == search.TierRelevance {
		fmt.Fprintln(os.Stderr, "deja: no exact match; showing sessions ranked by relevance to the whole query")
		hits = search.RelevanceHits(ss, index.RelevanceMatchTerms(o.Query))
		// This tier ranks and truncates inside retrieval, so counting the
		// sessions it handed back measures its window, not the match: every
		// query deeper than the window reported the window's own size and
		// capped: false, which told a consumer to stop checking exactly when
		// there was something to check. Take the tier's figures when it has
		// them; the paths that report none (a quoted query retried without
		// its quotes, served here under the relevance label) never truncated,
		// so what arrived is the whole of it.
		o.Total, o.Capped = result.Total, result.Capped
		if o.Total < len(hits) {
			o.Total = len(hits)
		}
	} else {
		// RunDetailed rather than Run: the JSON envelope reports how many
		// sessions matched before the cap, and that is not recoverable from a
		// list the cap has already trimmed.
		detailed, rerr := search.RunDetailed(ss, o)
		if rerr != nil {
			return fmt.Errorf("run: %w", rerr)
		}
		hits, o.Total, o.Capped = detailed.Hits, detailed.Total, detailed.Capped
	}
	hits, policyHidden := policyFilterHitsCounted(policy.ActivationSearch, hits)
	if !o.NoEmbed && os.Getenv("DEJA_EMBED") != "off" {
		hits = maybeRerank(dir, hits, o, os.Stderr)
	}
	var semantic bool
	hits, semantic = maybeSemantic(dir, hits, o, os.Stderr)
	o.Semantic = semantic
	// Policy scoping, reranking and the semantic tier all run after the cap, so
	// the pre-cap count can no longer describe what is being returned. When
	// nothing was capped the honest total is simply what survived; when it was,
	// there is no way to know how the filters would have treated the hidden
	// ones, so the pre-cap figure stands and `capped` says to distrust it.
	attachLifecycles(hits)
	demoted := demoteRejected(hits)
	attachMoved(hits)
	if !o.Capped {
		o.Total = len(hits)
	}
	if note := demotedNote(hits, demoted); note != "" {
		fmt.Fprintf(os.Stderr, "deja: %s\n", note)
	}
	if len(hits) == 0 {
		// The policy is named before the generic advice: "try fewer words" is
		// wrong counsel for someone whose words were fine (#680). A filter the
		// caller set is the same kind of fact, and `deja last` has named it all
		// along (#715).
		switch note := policyHiddenNote(policy.ActivationSearch, policyHidden); {
		case note != "":
			fmt.Fprint(os.Stderr, note)
		case activeFilters(o, "") != "":
			fmt.Fprintf(os.Stderr, "deja: %q matched nothing under %s\n", o.Query, activeFilters(o, ""))
		default:
			printNoMatches(os.Stderr, dir, o.Query)
		}
	}
	search.Print(os.Stdout, hits, o)
	return nil
}

func printStemmed(w io.Writer, variants map[string][]string) {
	keys := make([]string, 0, len(variants))
	for token := range variants {
		keys = append(keys, token)
	}
	sort.Strings(keys)
	for _, token := range keys {
		for _, variant := range variants[token] {
			if variant == "" {
				fmt.Fprintf(w, "deja: ignoring %q — no session matches it together with the rest\n", token)
				continue
			}
			if variant != token {
				fmt.Fprintf(w, "deja: no exact match, trying word forms: %s -> %s\n", token, variant)
			}
		}
	}
}

// termCountLine names the terms that do match on their own, so "try fewer
// words" says which. It stays quiet when every term is already unknown to the
// store — there is nothing to drop then, and a row of zeroes is noise.
func termCountLine(dir, q string) string {
	terms := query.Tokens(q)
	if len(terms) < 2 || len(terms) > 6 {
		return ""
	}
	counts := index.TermSessionCounts(dir, terms)
	var parts []string
	for _, t := range terms {
		if counts[t] > 0 {
			parts = append(parts, fmt.Sprintf("%q in %d", t, counts[t]))
		}
	}
	if len(parts) == 0 || len(parts) == len(terms) && len(terms) > 3 {
		return ""
	}
	return fmt.Sprintf("deja: on their own: %s — no session has them together\n", strings.Join(parts, ", "))
}

// printNoMatches says how big the store actually is, not how many sessions the
// query happened to load.
//
// On the index-backed path the loaded set is empty by definition when nothing
// matched, so this printed "no matches in 0 indexed sessions" for a perfectly
// healthy index — and internal/index/manifest.go reserves that exact sentence
// as the signature of a corrupt store, on the grounds that a reader told the
// index holds zero sessions concludes the tool is broken rather than that
// their query missed. It fired on every ordinary miss, so the signature could
// not be used to recognise the failure it was written for (#637).
func printNoMatches(w io.Writer, dir, q string) {
	// Nothing to look up: very short tokens are dropped and punctuation is
	// trimmed, so this query never reached the index. "Try fewer words"
	// cannot be followed with one word, or none (#828). The message does not
	// state the rule — the cut is on bytes, so "л" and "舵" are long enough
	// while "p" is not, and a rule that reads false to half the world's
	// alphabets is worse than none.
	if len(query.Tokens(q)) == 0 {
		fmt.Fprintf(w, "deja: nothing to search for in %q — every word in it is too short to look up\n", q)
		return
	}
	if n, err := index.SessionCount(dir); err == nil {
		fmt.Fprintf(w, "deja: no matches in %d indexed session%s — try fewer words or --re (query %q)\n", n, pluralS(n), q)
	} else {
		fmt.Fprintf(w, "deja: no matches — try fewer words or --re (query %q)\n", q)
	}
	// Which word to drop is the reader's next question, and deja read the
	// per-term counts to decide there was no intersection (#826).
	if line := termCountLine(dir, q); line != "" {
		fmt.Fprint(w, line)
	}
	// The first thing anyone types is a guess at a command name, and a wrong
	// guess falls through to search — where the advice is to use fewer words,
	// which cannot help someone who was not searching (#674). Falling through
	// stays the default; an empty result is where the other reading is worth
	// naming.
	if hint := commandHint(q); hint != "" {
		fmt.Fprint(w, hint)
	}
	if note := hiddenByOwnSettings(); note != "" {
		fmt.Fprint(w, note)
	}
}

// hiddenByOwnSettings names the states the reader created themselves and can
// undo. "Forgotten", "excluded by my own pattern" and "never happened" were one
// answer, and "try fewer words" is wrong counsel for the first two (#686).
func hiddenByOwnSettings() string {
	var parts []string
	if n := len(index.Tombstones()); n > 0 {
		parts = append(parts, fmt.Sprintf("%d session%s forgotten (`deja forget --list`)", n, pluralS(n)))
	}
	if pats := sources.ExclusionPatterns(); len(pats) > 0 {
		parts = append(parts, fmt.Sprintf("%d project pattern%s excluded from indexing (%s)", len(pats), pluralS(len(pats)), sources.ExcludePath()))
	}
	if len(parts) == 0 {
		return ""
	}
	return "deja: this machine also has " + strings.Join(parts, ", ") + "\n"
}

// commandHint reads an empty search as a possible mistyped subcommand. It
// stays quiet unless a command name is within one edit of the first word: an
// empty search is usually an empty search, and a hint on every miss is noise
// that teaches people to skip the last line.
func commandHint(q string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(q), " ")
	if first == "" || strings.HasPrefix(first, "-") {
		return ""
	}
	low := strings.ToLower(first)
	// A one- or two-letter word is a word, not a mistyped command name. Any
	// prefix counts as "near" — so "a" reached `deja aider` and every sentence
	// starting with a short word got a hint (#715, my own regression from
	// #674).
	if len([]rune(low)) < 4 {
		return ""
	}
	if _, ok := commands[low]; ok {
		return "" // reached search only because it was used as a word
	}
	for _, name := range []string{"search", "show", "last", "aider", "goose"} {
		if low == name {
			return ""
		}
	}
	// The switch in run() handles these before the map is consulted, so the
	// map alone would miss the commonest words of all — "serch" landed on
	// "bench" while `search` was one letter away.
	names := []string{"search", "show", "last", "aider", "goose"}
	for name := range commands {
		// Hidden plumbing is not something anyone means to type.
		if strings.HasPrefix(name, "-") || strings.HasPrefix(name, "hook-") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	near := nearestTarget(first, names)
	if near == "" {
		return ""
	}
	return fmt.Sprintf("deja: %q is not a command — did you mean `deja %s`?\n", first, near)
}

// activeFilters names the filters a caller set, so an empty result can say
// which of them emptied it rather than blaming the index. sinceRaw carries
// what the reader actually typed: "168h0m0s" is not the flag they passed.
func activeFilters(o search.Options, sinceRaw string) string {
	var parts []string
	if o.Harness != "" {
		parts = append(parts, fmt.Sprintf("harness %q", o.Harness))
	}
	if o.Project != "" {
		parts = append(parts, fmt.Sprintf("project %q", o.Project))
	}
	if o.Role != "" {
		parts = append(parts, fmt.Sprintf("role %q", o.Role))
	}
	// The same predicate filterRecentSources uses. parseDur accepts a negative
	// duration, and a negative Since filters nothing — naming it would report a
	// filter that was never applied and hide the empty-store advice, which is
	// the right answer there.
	if o.Since > 0 {
		since := sinceRaw
		if since == "" {
			since = o.Since.String()
		}
		parts = append(parts, "since "+since)
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

func clearWarmupSentinel() {
	if path := os.Getenv("DEJA_WARMUP_SENTINEL"); path != "" {
		_ = os.Remove(path)
	}
}

func printFuzzy(w io.Writer, variants map[string][]string) {
	keys := make([]string, 0, len(variants))
	for token := range variants {
		keys = append(keys, token)
	}
	sort.Strings(keys)
	hinted := false
	for _, token := range keys {
		for _, variant := range variants[token] {
			if variant != token {
				fmt.Fprintf(w, "deja: no exact match, trying close spellings: %s -> %s\n", token, variant)
				// A misspelled subcommand is searched for as a word: `deja
				// isntall` corrects to "install" and returns sessions that
				// mention installing, which is not what the typist wanted.
				// Spelled correctly it would have run the command, so the only
				// case this fires on is the one where the hint is wanted.
				if !hinted && isSubcommand(variant) {
					fmt.Fprintf(w, "deja: `%s` is also a command — run `deja %s` if that is what you meant\n", variant, variant)
					hinted = true
				}
			}
		}
	}
}

// isSubcommand reports whether a word names something deja can run.
func isSubcommand(word string) bool {
	if _, ok := commands[word]; ok {
		return true
	}
	switch word {
	case "show", "last", "help":
		return true
	}
	return false
}

func findByPrefix(dir, p string) (model.Session, bool, error) {
	if err := index.Ensure(dir, "", false, os.Stderr); err == nil {
		if s, ok, err := index.FindByPrefix(dir, p); err == nil {
			return s, ok, nil
		}
	}
	ss := loadFileSources()
	ss = append(ss, sources.LoadOpencodePrefix(p)...)
	s, ok := search.FindByPrefix(ss, p)
	return s, ok, nil
}

// findByPrefixHarness resolves an id prefix within one harness, so the
// documented "deja show <id-prefix> --harness name" form works.
func findByPrefixHarness(dir, p, harness string) (model.Session, bool, error) {
	s, ok, err := findByPrefix(dir, p)
	if err != nil || !ok {
		return model.Session{}, false, err
	}
	if s.Harness != harness {
		return model.Session{}, false, nil
	}
	return s, true, nil
}

func recent(dir string, n int) ([]model.Session, error) {
	return recentMatching(dir, n, search.Options{})
}

func recentMatching(dir string, n int, o search.Options) ([]model.Session, error) {
	if err := index.Ensure(dir, "", false, os.Stderr); err == nil {
		if o.Role != "" {
			ss, err := index.SearchWithRecovery(dir, search.Options{All: true}, io.Discard)
			if err == nil {
				ss = filterRecentSources(ss, o)
				return search.Recent(ss, n), nil
			}
		} else if ss, err := index.RecentMatching(dir, n, o); err == nil {
			return ss, nil
		}
	}
	ss := filterRecentSources(loadFileSources(), o)
	if o.Harness == "" || o.Harness == "opencode" {
		ss = append(ss, filterRecentSources(sources.LoadOpencodeRecent(n), o)...)
	}
	return search.Recent(ss, n), nil
}

func parseLast(args []string) (int, search.Options, string, error) {
	sinceRaw := ""
	n := 10
	seenN := false
	o := search.Options{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			o.JSON = true
		case "--harness", "--project", "--since", "--role":
			if i+1 >= len(args) {
				return n, o, sinceRaw, fmt.Errorf("%s needs value", a)
			}
			i++
			v := args[i]
			switch a {
			case "--harness":
				o.Harness = v
			case "--project":
				o.Project = v
			case "--role":
				o.Role = v
			default:
				d, err := parseDur(v)
				if err != nil {
					return n, o, sinceRaw, err
				}
				o.Since = d
				sinceRaw = v
			}
		default:
			if strings.HasPrefix(a, "-") {
				return n, o, sinceRaw, fmt.Errorf("last: unknown flag %q", a)
			}
			if !seenN {
				if x, err := strconv.Atoi(a); err == nil {
					n = x
					seenN = true
				}
			}
		}
	}
	return n, o, sinceRaw, nil
}

func filterRecentSources(ss []model.Session, o search.Options) []model.Session {
	if o.Harness == "" && o.Project == "" && o.Since <= 0 && o.Role == "" {
		return ss
	}
	cut := time.Time{}
	if o.Since > 0 {
		cut = time.Now().Add(-o.Since)
	}
	out := make([]model.Session, 0, len(ss))
	project := strings.ToLower(o.Project)
	for _, s := range ss {
		if o.Harness != "" && s.Harness != o.Harness {
			continue
		}
		if project != "" && !strings.Contains(strings.ToLower(s.Project), project) {
			continue
		}
		if !cut.IsZero() && s.Updated.Before(cut) {
			continue
		}
		if o.Role != "" && !sessionHasRole(s, o.Role) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// sessionHasRole accepts the role names the help text documents. `--role tool`
// is what `deja help` promises and "tool-output" is what is stored, so the
// documented spelling matched nothing here while `deja search --role tool` —
// which grew the alias in #623 — worked (#717).
func sessionHasRole(s model.Session, role string) bool {
	for _, m := range s.Messages {
		if m.Role == role || (role == "tool" && m.Role == "tool-output") {
			return true
		}
	}
	return false
}

func firstUserTitle(s model.Session) string {
	for _, msg := range s.Messages {
		if msg.Role != "user" {
			continue
		}
		t := strings.Join(strings.Fields(msg.Text), " ")
		r := []rune(t)
		if len(r) > 60 {
			return strings.TrimSpace(string(r[:60])) + "…"
		}
		return t
	}
	return ""
}

func parseSearch(args []string) (search.Options, error) {
	o := search.Options{}
	var q []string
	// --limit=100 used to fall through and be searched for as a query term,
	// silently returning results for a different question than the one asked.
	args = splitEqualsForms(args)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			o.JSON = true
		case "--re":
			o.Regex = true
		case "--all":
			o.All = true
		case "--no-embed":
			o.NoEmbed = true
		case "--harness", "--project", "--since", "--role", "--limit":
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs value", a)
			}
			i++
			v := args[i]
			switch a {
			case "--harness":
				o.Harness = v
			case "--project":
				o.Project = v
			case "--role":
				o.Role = v
			case "--limit":
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 || n > 100 {
					return o, fmt.Errorf("--limit needs an integer from 1 to 100")
				}
				o.Limit = n
			default:
				d, err := parseDur(v)
				if err != nil {
					return o, err
				}
				o.Since = d
			}
		default:
			// A query may legitimately start with a dash — `deja search
			// "--retry budget"` is a real search. A token one edit away from a
			// real flag is not: folding `--limti` into the query turned a
			// working search into "you have no such memory" (#755).
			if near := nearestSearchFlag(a); near != "" {
				return o, fmt.Errorf("unknown flag %q — did you mean %s?", a, near)
			}
			q = append(q, a)
		}
	}
	o.Query = strings.Join(q, " ")
	if o.Query == "" {
		return o, fmt.Errorf("query required")
	}
	return o, nil
}

// searchFlags is every flag the bare search form accepts, for the typo check.
var searchFlags = []string{
	"--json", "--re", "--all", "--no-embed", "--rebuild",
	"--harness", "--project", "--since", "--role", "--limit",
}

// nearestSearchFlag names the flag a token was probably meant to be. It stays
// silent unless the token looks like a flag and is within one edit of a real
// one, so ordinary queries — including those containing a dash — are untouched.
func nearestSearchFlag(a string) string {
	if !strings.HasPrefix(a, "--") || len([]rune(a)) < 4 {
		return ""
	}
	for _, f := range searchFlags {
		if a == f {
			return ""
		}
	}
	return nearestTarget(a, searchFlags)
}

func parseBlame(args []string) (string, search.BlameOptions, bool, error) {
	o := search.BlameOptions{}
	jsonOutput := false
	var path string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json":
			jsonOutput = true
		case "--all":
			o.All = true
		case "--harness", "--project", "--since":
			if i+1 >= len(args) {
				return "", o, false, fmt.Errorf("%s needs value", a)
			}
			i++
			switch a {
			case "--harness":
				o.Harness = args[i]
			case "--project":
				o.Project = args[i]
			case "--since":
				d, err := parseDur(args[i])
				if err != nil {
					return "", o, false, err
				}
				o.Since = d
			}
		default:
			if strings.HasPrefix(a, "-") {
				return "", o, false, fmt.Errorf("blame: unknown flag %q", a)
			}
			if path != "" {
				return "", o, false, fmt.Errorf("blame accepts one path")
			}
			path = a
		}
	}
	if path == "" {
		return "", o, false, fmt.Errorf("blame needs a path — `deja blame internal/index/sync.go` says who last worked on it")
	}
	return path, o, jsonOutput, nil
}

func runBlame(dir string, args []string) error {
	path, o, jsonOutput, err := parseBlame(args)
	if err != nil {
		return err
	}
	target, err := search.ResolveBlamePath(path)
	if err != nil {
		return err
	}
	hits, err := findBlameHits(dir, target, o, policy.ActivationSearch, os.Stderr)
	if err != nil {
		return fmt.Errorf("blame search: %w", err)
	}
	if jsonOutput {
		search.PrintBlame(os.Stdout, hits, true)
		return nil
	}
	if len(hits) == 0 {
		// "run deja index" is advice for an empty store. With sessions in the
		// index it is advice for a state the tool is not in — indexing changes
		// nothing and doctor reports the stores as found — and it sends the
		// reader to fix an index that is fine (#637, same shape).
		if n, err := index.SessionCount(dir); err == nil && n > 0 {
			fmt.Fprintf(os.Stderr, "deja: no sessions mention %s — searched %d indexed session%s\n", target.Base, n, pluralS(n))
		} else {
			fmt.Fprintln(os.Stderr, emptyIndexHint("no sessions mention "+target.Base))
		}
		return nil
	}
	search.PrintBlame(os.Stdout, hits, false)
	return nil
}

func findBlameHits(dir string, target search.BlameTarget, o search.BlameOptions, activation string, progress io.Writer) ([]search.BlameHit, error) {
	query := search.Options{Query: target.Stem, Harness: o.Harness, Project: o.Project, Since: o.Since, All: true}
	if err := index.EnsureForSearch(dir, query, false, progress); err != nil {
		return nil, err
	}
	result, err := index.SearchWithRecoveryDetailed(dir, query, progress)
	if err != nil {
		return nil, err
	}
	return policyFilterBlame(activation, search.Blame(withFileTouchers(dir, result.Sessions, target), target, o)), nil
}

// blameToucherCap bounds how many extra sessions a blame reads from the
// manifest. Measured on 500 sessions all touching one file: 0.08s uncapped
// against 0.02s capped, with the same ten at the top.
const blameToucherCap = 50

// withFileTouchers adds the sessions that edited or opened the file but never
// said its name.
//
// blame picks its candidates with an ordinary search for the file's stem, and
// the paths a tool call recorded do not answer one: the newer session that
// actually changed pool.go was invisible while an older one that only
// mentioned it in passing was the answer (#688). The manifest already knows
// which files each session touched, so this costs one metadata read.
func withFileTouchers(dir string, ss []model.Session, target search.BlameTarget) []model.Session {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return ss
	}
	// Where each session sits, not merely whether it is here: search returns
	// sessions with only their matching messages attached, so one that matched
	// on speech arrives with its `files` record stripped. Skipping it as
	// "already present" is how mentioning the subject removed a session from
	// its own file's blame (#723) — it has to be replaced by the full record.
	at := make(map[string]int, len(ss))
	for i, s := range ss {
		at[s.Harness+":"+s.ID] = i
	}
	base := strings.ToLower(filepath.Base(target.FullPath))
	// Newest first, so the cap below keeps the sessions a "who last worked on
	// this" question is actually about.
	// Identity breaks ties, or which sessions survive the cap depends on Go's
	// map order and two runs disagree — the same failure as #668.
	sort.Slice(metas, func(i, j int) bool { return newestFirst(metas[i], metas[j]) })
	added := 0
	for _, meta := range metas {
		// A file like main.go is touched by everything, and each addition is a
		// record read. Ten hits are printed; this is far more than enough to
		// rank them.
		if added >= blameToucherCap {
			break
		}
		key := meta.Harness + ":" + meta.ID
		for _, p := range meta.Touched {
			if strings.ToLower(filepath.Base(filepath.FromSlash(p))) != base {
				continue
			}
			full, ok, err := index.FindByIdentity(dir, meta.Harness, meta.ID)
			if err == nil && ok {
				// The manifest is keyed by identity, so each session reaches
				// this once — there is no second visit to record a position for.
				if i, present := at[key]; present {
					ss[i] = full
				} else {
					ss = append(ss, full)
				}
				added++
			}
			break
		}
	}
	return ss
}
func parseDur(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, durationError(s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		// time.ParseDuration's own message names Go's syntax, not deja's, and
		// it does not mention days — which is the unit people reach for here.
		return 0, durationError(s)
	}
	return d, nil
}

func durationError(s string) error {
	return fmt.Errorf("%q is not a duration deja understands — try 30d, 12h, or 90m", s)
}

func printSources(dir string) {
	redactions := map[string]int{}
	if red, err := index.Redactions(dir); err == nil {
		redactions = red.Files
	}
	antigravityRoots := sources.AntigravityRoots()
	antigravityLocation := strings.Join(antigravityRoots, string(os.PathListSeparator))
	if antigravityLocation == "" {
		antigravityLocation = filepath.Join(sources.Home(), ".gemini", "antigravity*")
	}
	items := []struct {
		name, location string
		roots          []string
		load           func() []model.Session
	}{
		{"claude", sources.ClaudeRoot(), []string{sources.ClaudeRoot()}, sources.LoadClaude},
		{"codex", sources.CodexRoot(), []string{sources.CodexRoot()}, sources.LoadCodex},
		{"gemini", sources.GeminiRoot(), []string{filepath.Join(sources.GeminiRoot(), "tmp")}, sources.LoadGemini},
		{"cursor", strings.Join([]string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, string(os.PathListSeparator)), []string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, sources.LoadCursor},
		{"antigravity", antigravityLocation, antigravityRoots, sources.LoadAntigravity},
		{"grok", sources.GrokRoot(), []string{sources.GrokRoot()}, sources.LoadGrok},
		{"qwen", filepath.Join(sources.QwenRoot(), "projects"), []string{filepath.Join(sources.QwenRoot(), "projects")}, sources.LoadQwen},
		{"kimi", filepath.Join(sources.KimiRoot(), "sessions"), []string{filepath.Join(sources.KimiRoot(), "sessions")}, sources.LoadKimi},
		{"goose", filepath.Join(sources.GooseRoot(), "sessions"), []string{filepath.Join(sources.GooseRoot(), "sessions")}, sources.LoadGoose},
		{"hermes", sources.HermesProfilesRoot(), []string{sources.HermesProfilesRoot()}, sources.LoadHermes},
		{"copilot", sources.CopilotRoot(), []string{sources.CopilotRoot()}, sources.LoadCopilot},
		{"cline", sources.ClineSessionsDir(), append([]string{sources.ClineSessionsDir()}, sources.ClineLegacyRoots()...), sources.LoadCline},
		{"roo", strings.Join(sources.RooRoots(), string(os.PathListSeparator)), sources.RooRoots(), sources.LoadRoo},
		{"pi", sources.PiRoot(), []string{sources.PiRoot()}, sources.LoadPi},
		{"openclaw", sources.OpenClawRoot(), []string{sources.OpenClawRoot()}, sources.LoadOpenClaw},
		{"deja", sources.NotesFile(), []string{sources.NotesFile()}, sources.LoadNotes},
	}
	for _, it := range items {
		var size int64
		redacted := 0
		for _, root := range it.roots {
			size += pathSize(root)
			redacted += redactionsUnder(redactions, root)
		}
		raw := it.load()
		ss := sources.FilterSessions(raw)
		excluded := len(raw) - len(ss)
		msg := 0
		for _, s := range ss {
			msg += len(s.Messages)
		}
		note := ""
		if it.name == "cursor" && len(sources.CursorDBs()) > 0 && !sources.SQLite3Available() {
			note = "\t(sqlite3 CLI not found — Cursor IDE sessions unavailable)"
		}
		if n := len(sources.ExclusionPatterns()); n > 0 {
			note += fmt.Sprintf("\texcluded-patterns=%d", n)
		}
		if excluded > 0 {
			note += fmt.Sprintf("\texcluded-sessions=%d", excluded)
		}
		fmt.Printf("%s\t%s\tsessions=%d messages=%d size=%s redacted=%d%s\n", it.name, it.location, len(ss), msg, humanBytes(size), redacted, note)
	}
	aiderFiles := sources.AiderFiles()
	var aiderSize int64
	aiderRedactions := 0
	for _, p := range aiderFiles {
		if fi, err := os.Stat(p); err == nil {
			aiderSize += fi.Size()
		}
		aiderRedactions += redactions[p]
	}
	rawAiderSessions := sources.LoadAider()
	aiderSessions := sources.FilterSessions(rawAiderSessions)
	aiderMessages := 0
	for _, s := range aiderSessions {
		aiderMessages += len(s.Messages)
	}
	aiderLocation := filepath.Join(sources.Home(), ".aider.chat.history.md")
	if roots := os.Getenv("DEJA_AIDER_ROOTS"); roots != "" {
		aiderLocation += string(os.PathListSeparator) + roots
	}
	note := ""
	if n := len(sources.ExclusionPatterns()); n > 0 {
		note = fmt.Sprintf("\texcluded-patterns=%d", n)
	}
	if excluded := len(rawAiderSessions) - len(aiderSessions); excluded > 0 {
		note += fmt.Sprintf("\texcluded-sessions=%d", excluded)
	}
	fmt.Printf("aider\t%s\tsessions=%d messages=%d size=%s redacted=%d%s\n", aiderLocation, len(aiderSessions), aiderMessages, humanBytes(aiderSize), aiderRedactions, note)
	var size int64
	if fi, err := os.Stat(sources.OpencodeDB()); err == nil {
		size = fi.Size()
	}
	s, m, _ := sources.OpencodeCounts()
	note = ""
	if size > 0 && !sources.SQLite3Available() {
		note = "\t(sqlite3 CLI not found — opencode sessions unavailable)"
	}
	if n := len(sources.ExclusionPatterns()); n > 0 {
		note += fmt.Sprintf("\texcluded-patterns=%d", n)
	}
	fmt.Printf("opencode\t%s\tsessions=%d messages=%d size=%s redacted=%d%s\n", sources.OpencodeDB(), s, m, humanBytes(size), redactions[sources.OpencodeDB()], note)
}

func runForget(dir string, args []string) error {
	var o index.ForgetOptions
	list := false
	unforget := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--list":
			list = true
		case "--dry-run":
			o.DryRun = true
		case "--session", "--project", "--before", "--unforget":
			if i+1 >= len(args) {
				return fmt.Errorf("forget: %s needs value", args[i])
			}
			i++
			switch args[i-1] {
			case "--session":
				o.Session = args[i]
			case "--project":
				o.Project = args[i]
			case "--unforget":
				unforget = args[i]
			case "--before":
				if d, err := parseDur(args[i]); err == nil {
					// "older than 0 days" is the whole store, and the typo that
					// produces it is 0 for 30. The neighbouring value behaves
					// the opposite way — --before 9999d matches nothing — and a
					// negative duration filters nothing in `last` while
					// deleting everything here (#739).
					if d <= 0 {
						return fmt.Errorf("forget: --before %s means older than now, which is every session — use a real age like 30d, or --project/--session", args[i])
					}
					o.Before = time.Now().Add(-d)
				} else if t, e := parseForgetDate(args[i]); e == nil {
					o.Before = t
				} else {
					// --before takes either form, so naming only one of them
					// leaves the reader guessing which they got wrong.
					return fmt.Errorf("forget: %q is neither a duration nor a date — try 30d, 12h, or 2026-01-31", args[i])
				}
			}
		default:
			return fmt.Errorf("forget: unknown flag %q", args[i])
		}
	}
	if !list && unforget == "" && o.Session == "" && o.Project == "" && o.Before.IsZero() {
		return fmt.Errorf("forget: selector required")
	}
	if list {
		for _, key := range index.Tombstones() {
			fmt.Fprintln(os.Stdout, key)
		}
		return nil
	}
	if unforget != "" {
		var lifted int
		if err := withBuildProgress(func() error {
			var err error
			lifted, err = index.Unforget(dir, unforget, os.Stderr)
			return err
		}); err != nil {
			return err
		}
		if lifted == 0 {
			fmt.Fprintf(os.Stdout, "no tombstone matches %q — `deja forget --list` shows what is forgotten\n", unforget)
			return nil
		}
		fmt.Fprintf(os.Stdout, "restored %d session%s and rebuilt the index\n", lifted, pluralS(lifted))
		return nil
	}
	result, err := index.Forget(dir, o)
	if err != nil {
		return err
	}
	// A dry run changes nothing, so reporting it in the past tense — the same
	// three lines the real command prints — tells the reader their sessions are
	// gone when they are not.
	if o.DryRun {
		fmt.Fprintf(os.Stdout, "dry run — nothing was changed\nwould drop: %d session(s), %d message(s)\nwould add: %d tombstone(s)\n",
			result.Sessions, result.Messages, result.Tombstones)
		if result.Notes > 0 {
			fmt.Fprintf(os.Stdout, "%d of them %s promoted note%s — the decisions you kept, not raw sessions\n",
				result.Notes, verbWere(result.Notes), pluralS(result.Notes))
		}
		return nil
	}
	// A promoted note borrows the source session's first line as its title, and
	// the note is a separate record — so forgetting a session left that line on
	// screen in `deja last` (#666). The note stays; only the borrowed title
	// goes, and the parser falls back to "promoted from <src>".
	//
	// Matching on the keys that were actually dropped rather than on the
	// selector: --project and --before drop sessions too, and keying off
	// o.Session left the borrowed line — a customer name, in the case that
	// found this — sitting in notes.jsonl after the project was forgotten
	// (#690).
	if len(result.Keys) > 0 {
		dropped := make(map[string]bool, len(result.Keys))
		for _, k := range result.Keys {
			dropped[k] = true
		}
		n, err := sources.ForgetPromotedTitles(func(src string) bool {
			return dropped[src]
		})
		switch {
		case err != nil:
			// The borrowed title is the forgotten session's first turn — a
			// customer name in the case that found this (#666, #690). Dropping
			// the error left three success lines standing over a line still on
			// disk, which is the one thing the privacy command must not do
			// (#804).
			fmt.Fprintf(os.Stdout, "could not clear the title borrowed from the forgotten session%s: %v\n", pluralS(len(result.Keys)), err)
			// Name the fix by the cause: on a full disk the first version of
			// this line sent people to chmod a file that was already writable
			// (#808).
			fmt.Fprintf(os.Stdout, "it is still in %s — %s and run this again\n", sources.NotesFile(), forgetTitleFix(err))
		case n > 0:
			fmt.Fprintf(os.Stdout, "cleared the borrowed title from %d promoted note%s\n", n, pluralS(n))
		}
	}
	// Nothing matched is a different answer from nothing was dropped: the
	// first means the selector found no session, and reporting it as a
	// successful removal of zero leaves the reader believing they deleted
	// something that is in fact still there under a different id.
	if result.Sessions == 0 && result.Messages == 0 {
		fmt.Fprintf(os.Stdout, "nothing matched %s — no session was dropped\n", forgetSelector(o))
		return nil
	}
	fmt.Fprintf(os.Stdout, "sessions dropped: %d\nmessages dropped: %d\ntombstones added: %d\n", result.Sessions, result.Messages, result.Tombstones)
	// Forgetting keeps the session out of later pushes but cannot reach a
	// machine that already has it. Three lines about what was dropped read as
	// "it is gone everywhere" to someone forgetting a customer name (#788).
	if len(result.Peers) > 0 {
		fmt.Fprintf(os.Stdout, "already pushed to %s — forgetting here does not remove it there\n", strings.Join(result.Peers, ", "))
	} else if result.Exported {
		fmt.Fprintln(os.Stdout, "already exported once — forgetting here does not remove copies elsewhere")
	}
	// The notes are decisions the reader deliberately kept, so folding them
	// into the session count reads as "four conversations" when half of it is
	// their own writing (#690).
	if result.Notes > 0 {
		fmt.Fprintf(os.Stdout, "%d of them %s promoted note%s — the decisions you kept, not raw sessions\n",
			result.Notes, verbWere(result.Notes), pluralS(result.Notes))
	}
	return nil
}

// verbShare keeps "1 session share" off the screen.
func verbShare(n int) string {
	if n == 1 {
		return "shares"
	}
	return "share"
}

// verbWere keeps "1 of them are promoted notes" off the screen.
func verbWere(n int) string {
	if n == 1 {
		return "is a"
	}
	return "are"
}

// forgetSelector names what the caller asked to forget, so the empty answer
// says which selector came back empty.
func forgetSelector(o index.ForgetOptions) string {
	var parts []string
	if o.Session != "" {
		parts = append(parts, fmt.Sprintf("session %q", o.Session))
	}
	if o.Project != "" {
		parts = append(parts, fmt.Sprintf("project %q", o.Project))
	}
	if !o.Before.IsZero() {
		parts = append(parts, "before "+o.Before.Format("2006-01-02"))
	}
	if len(parts) == 0 {
		return "the given selector"
	}
	return strings.Join(parts, " and ")
}

func parseForgetDate(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date")
}

func redactionsUnder(files map[string]int, root string) int {
	total := 0
	for p, n := range files {
		if p == root || strings.HasPrefix(p, root+string(os.PathSeparator)) {
			total += n
		}
	}
	return total
}

func pathSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type()&os.ModeSymlink == 0 && !d.IsDir() {
			if fi, e := d.Info(); e == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}

func humanBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}
func printUsage() {
	fmt.Println(`deja - persistent memory for coding agents

Usage:
  deja [flags] <query>
  deja search [flags] <query>   (same, but a query may start with a dash)
  deja show <id-prefix> [--json --harness name] [--offset n] [--limit n]
  deja share <id-prefix>
  deja resume <id-prefix> [--exec]
  deja handoff [--to <agent>] [id-prefix] [--exec]
  deja hook-prompt   (UserPromptSubmit hook: relevance recall per prompt)
  deja hook-antigravity (Antigravity PreInvocation hook: inject on first turn)
  deja view [--no-open]  (browse your memory: sessions, recalls, notes — one local HTML)
  deja ctx <query|id-prefix>
  deja blame <path> [--all] [--json] [--project name] [--harness name] [--since 30d]
  deja files <topic> [--project name] [--limit n]
  deja restore <path> [--span n] [-o|--out file] [--force]
  deja friction [--limit n]
  deja sync export <dir> [--full]
  deja sync import <dir>
  deja sync ssh <host> [--pull] [--full]
  deja last [n] [--json] [--project name] [--harness name] [--since duration] [--role user|assistant|tool]
  deja sources
  deja completion <bash|zsh|fish>
  deja forget --session <id-prefix> [--project <substring>] [--before <duration|date>] [--dry-run]
  deja forget --list | --unforget <id>
  deja doctor [--json] [--deep] [--offline]
  deja warmup
  deja index [--rebuild]
  deja embed
  deja bench recall|context|prompt [--json] [--seed n]
  deja log [n] [--last] [--json]
  deja statusline
  deja stats [--json] [--impact] [--redaction] [--card [path]] [--html [path]]
	deja remember "text" [--project name] [--tag name]
  deja promote <id-prefix> [--state accepted|rejected|superseded|stale] [--note "text"] [--tag name] [--to path]
  deja mcp
  deja version
  deja update [--force]
  deja install <claude-code|codex|opencode|cursor|gemini|antigravity|grok|qwen|kimi|cline|statusline|--all|--auto>
  deja uninstall <claude-code|codex|opencode|cursor|gemini|antigravity|grok|qwen|kimi|statusline|--all|--auto>

Search flags (the bare "deja [flags] <query>" form above):
  --harness <name>              only sessions from one harness (claude, codex, ...)
  --project <name>              only sessions from one project
  --since <duration>            only sessions newer than e.g. 30d, 12h
  --role <user|assistant|tool>  only match turns from one role
  --limit <1-100>               max sessions to return (default 15)
  --all                         return every match, no cap
  --re                          treat the query as a regular expression
  --json                        machine-readable output
  --no-embed                    skip the semantic (embedding) tier

Examples:
  deja "jwt refresh token bug"
  deja '"connection pool exhausted"'
  deja "exhaustd"  # zero exact results try close spellings
  deja --harness claude --since 30d "panic in indexer"
  deja --all "connection pool"  # every match, not just the first 15
  deja last 20 --harness codex
  deja last --project api-gateway
  deja last --since 7d --role user
  deja --re "timeout|deadline exceeded"
  deja ctx "schema migration rollback" > deja-context.md
  deja install --all

See README.md for the full CLI reference.`)
}

// emptyIndexHint phrases the nothing-here answer the same way everywhere, and
// points at the next command rather than leaving the user to guess.
//
// Which command depends on why it is empty. Every path that reaches here has
// already built the index — the build narration prints a line above this one —
// so telling someone to run `deja index` sends them to do again what just
// happened, and they are left where they started. When no agent history was
// found at all, the useful next step is finding out where deja looked.
func emptyIndexHint(what string) string {
	if noAgentHistoryFound() {
		return "deja: " + what + " — no agent history was found on this machine; `deja sources` shows where deja looked"
	}
	return "deja: " + what + " — run `deja index`, or `deja doctor` to see which agent stores were found"
}

// noAgentHistoryFound reports whether the stores themselves are empty, as
// opposed to an index that merely has not been built yet.
func noAgentHistoryFound() bool {
	for _, check := range doctorStoreChecks() {
		if store, _ := inspectDoctorStore(check); store.Files > 0 {
			return false
		}
	}
	return true
}

// forgetTitleFix names the fix by the cause. The first version of this line
// said "permissions" whatever had happened, so a full disk sent the reader to
// chmod a file that was already writable (#808).
func forgetTitleFix(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "fix that file's permissions"
	case errors.Is(err, syscall.ENOSPC):
		return "free some space on that filesystem"
	}
	return "clear the problem above"
}

// sortedHarnesses orders the ingest-health entries so a run reports them the
// same way twice.
func sortedHarnesses(m map[string]index.HarnessIngest) []string {
	out := make([]string, 0, len(m))
	for h := range m {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// pluralWhich matches the pronoun to the count in the ingest warning.
func pluralWhich(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// ensureError turns an index-build failure into something a reader can act on.
// A denied write surfaced as `ensure: open /…/index.db.lock: permission denied`
// — the path of an internal lock file and a syscall error, which says nothing
// about what to change.
func ensureError(dir string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		if dir == "" {
			dir = index.DefaultDir()
		}
		return fmt.Errorf("cannot write the index at %s — check the directory's permissions, or point DEJA_INDEX_DIR somewhere writable", dir)
	}
	return fmt.Errorf("ensure: %w", err)
}

// searchValueFlags take a value, so "--flag=value" has to become two arguments
// before the parser sees it. Anything else keeps its equals sign: a query may
// legitimately contain one.
var searchValueFlags = map[string]bool{
	"--harness": true, "--project": true, "--since": true,
	"--role": true, "--limit": true,
}

func splitEqualsForms(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		name, value, found := strings.Cut(a, "=")
		if found && searchValueFlags[name] {
			out = append(out, name, value)
			continue
		}
		out = append(out, a)
	}
	return out
}
