package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func cmdShow(dir string, rest []string, sourceInstance string) error {
	o, err := parseShow(rest)
	if err != nil {
		// The missing id is the one refusal that depends on the store: on a
		// machine with nothing indexed the honest answer is that there is
		// nothing to show, not that an argument is missing. parseShow has no
		// dir to ask, so the store-aware phrasing is applied here (#1063).
		if err.Error() == showNeedsID {
			return idPrefixNeeded(dir, "show needs an id prefix", showNeedsID)
		}
		return err
	}
	// An id arrives from a chat wrapped in quotes or backticks and with the
	// harness deja itself printed in front of it (#921).
	o.id = index.PastedSelector(o.id)
	if o.id == "" {
		return idPrefixNeeded(dir, "show needs an id prefix", showNeedsID)
	}
	// A harness that does not exist matches nothing, and the refusal then
	// blamed the id the reader typed correctly (#2251). search, last, blame
	// and the MCP tools have always named the value they did not recognise.
	if herr := checkHarness(&o.harness); herr != nil {
		return herr
	}
	var s model.Session
	var ok, prefixed bool
	if o.harness != "" {
		// The same pass the prefix form runs. Without it the exact-identity
		// path read whatever was on disk, and a store below the redaction
		// floor is fresh — so `show --harness` printed text this build would
		// not write while `show <prefix>` re-read the sources first (#3617).
		// A store that cannot be rebuilt — read-only, no space — falls through
		// to the loader, which refuses and says why.
		_ = ensureForCLIRead(dir)
		// Exact identity first — that is what --harness is for. But the usage line documents an id *prefix*, and
		// routing --harness straight to the exact lookup made every
		// prefix+harness call fail: "deja show 019fa282 --harness codex" said
		// no session matches while the same prefix without --harness worked.
		s, ok, err = index.FindByIdentity(dir, o.harness, o.id)
		if err == nil && !ok {
			s, ok = findByPrefixHarness(dir, o.id, o.harness)
			prefixed = true
			if ok && o.json {
				err = ambiguousJSONPrefixIn(dir, o.id, o.harness)
			}
		}
	} else {
		s, ok, err = findByPrefix(dir, o.id)
		// A machine reader cannot notice it was handed the newest of several
		// matches, so --json answers only a prefix that names one session.
		if err == nil && ok && o.json {
			err = ambiguousJSONPrefix(dir, o.id)
		}
	}
	if err != nil {
		return err
	}
	if !ok {
		if n, cerr := index.SessionCount(dir); cerr == nil && n == 0 {
			return errors.New(strings.TrimPrefix(emptyIndexHint(fmt.Sprintf("no session matches %q", o.id)), "deja: "))
		}
		return noSessionMatches(dir, o.id)
	}
	if err := denyPolicyHidden(o.id, s, os.Stderr); err != nil {
		return err
	}
	if o.json {
		return printSessionJSON(os.Stdout, s, o.offset, o.limit, sourceInstance)
	}
	if o.sliced {
		// Both flags are documented for `show` and only the JSON path honoured
		// them; the text output printed the whole session (#709).
		total := len(s.Messages)
		s.Messages = sliceMessages(s.Messages, o.offset, o.limit)
		// The JSON has reported the window all along; the terminal printed the
		// slice and said nothing, so five turns of two hundred read the same as
		// a session that is five turns long (#2296). search says it in this
		// shape two commands away.
		if line := showWindowNote(o.offset, len(s.Messages), total); line != "" {
			fmt.Fprintln(os.Stderr, line)
		}
	} else if n := len(s.Messages); n > showLargeSession {
		fmt.Fprintf(os.Stderr, "deja: %d messages — `--offset n --limit n` reads a slice\n", n)
	}
	if o.harness == "" {
		noteAmbiguousPrefix(dir, o.id, "showing")
	} else if prefixed {
		noteAmbiguousPrefixIn(dir, o.id, o.harness, "showing")
	}
	// A day bucket's date is the day the index was built in, not the moment a
	// line was written: read in another zone, `show` lists a record dated the
	// day before the id says, and nothing connected the two (#1006).
	if note := bucketDayNote(s); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}
	printSpawnEdges(os.Stderr, dir, s)
	if s.Harness == "claude" && sources.IsPromptLog(s.Path) {
		fmt.Fprintln(os.Stderr, "deja: only the prompts are left — Claude Code deleted the transcript, and history.jsonl kept what was typed")
	}
	// A pasted log is one message, and the index keeps the head of it. The
	// window note above says when messages were left out; nothing said when a
	// message was, so a reader searching a log for the line that explains a
	// failure searched one they believed was whole (#2467).
	if line := clippedMessageNote(dir, s); line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
	search.PrintSessionStyled(os.Stdout, s, search.ColorOK(os.Stdout), printableWidth(os.Stdout))
	return nil
}

// clippedMessageNote says that a message in this session was stored short of
// what the transcript holds. deja records the count per file at ingest, and a
// store like Zed's threads.db keeps every session in one file, so the file's
// count put the note on every session in it (#4340). The file says a clip
// happened; the per-session split says which of its sessions holds it.
func clippedMessageNote(dir string, s model.Session) string {
	if s.Path == "" {
		return ""
	}
	files := index.IngestFilesReport(dir)
	e, ok := files[s.Path]
	if !ok || e.Clipped == 0 {
		return ""
	}
	n := e.ClippedSessions[s.ID]
	if e.ClippedSessions == nil {
		// A store built before the split: the file's count is all there is.
		n = e.Clipped
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("deja: %s stored short of what the transcript holds — the rest of %s is in the file itself",
		pluralMessages(n), pluralThem(n))
}

func pluralMessages(n int) string {
	if n == 1 {
		return "one message was"
	}
	return fmt.Sprintf("%d messages were", n)
}

func pluralThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// showWindowNote is what the terminal says about a slice: which messages it
// printed and how many there are. Empty when the slice is the whole session,
// because a reader who asked for everything needs no arithmetic.
func showWindowNote(offset, returned, total int) string {
	if total == 0 {
		// Not the offset's fault, and asked before the full-window case below:
		// a session deja holds with nothing readable in it answers every
		// window the same way, and printing a bare header for it is the silent
		// empty this note exists to end.
		return "deja: this session has no messages to show"
	}
	if offset <= 0 && returned == total {
		return ""
	}
	if returned == 0 {
		return fmt.Sprintf("deja: --offset %d is past the end — the session has %d message%s", offset, total, pluralS(total))
	}
	first := offset + 1
	last := offset + returned
	if last >= total {
		// The last slice has no next one, and pointing at the offset after it
		// sent the reader to "--offset 16 is past the end — the session has 16
		// messages", which is deja answering its own advice (#3578).
		return fmt.Sprintf("deja: showing message%s %d-%d of %d — the end of the session",
			pluralS(returned), first, last, total)
	}
	return fmt.Sprintf("deja: showing message%s %d-%d of %d — `--offset %d` reads the next slice",
		pluralS(returned), first, last, total, last)
}

// printSpawnEdges names the sessions around this one when an agent spawned it
// or spawned others from it. A subagent's work lives in its own session, so a
// reader who found the parent could not get to it and a reader who found the
// child could not say what asked for it (#1385).
func printSpawnEdges(w io.Writer, dir string, s model.Session) {
	switch {
	case s.Parent != "" && s.Kind == "fork":
		// A Codex fork names the thread it branched from; nothing spawned it.
		fmt.Fprintf(w, "deja: forked from %s — `deja show %s`\n", digest.Short(s.Parent), pasteSafe(digest.Short(s.Parent)))
	case s.Parent != "":
		by := ""
		if s.Agent != "" {
			by = " as " + s.Agent
		}
		fmt.Fprintf(w, "deja: spawned from %s%s — `deja show %s`\n", digest.Short(s.Parent), by, pasteSafe(digest.Short(s.Parent)))
	case s.Kind != "" && s.IsImported():
		// The parent is on the machine that sent it.
		fmt.Fprintf(w, "deja: %s session from another machine\n", s.Kind)
	case s.Kind != "":
		// A kind with no parent is all the harness recorded; saying which
		// session asked for it would be a guess.
		fmt.Fprintf(w, "deja: %s session — the harness records no parent for it\n", s.Kind)
	}
	children, err := ChildrenOfSession(dir, s.ID)
	if err != nil || len(children) == 0 {
		return
	}
	// A long session spawns a hundred agents; naming them all buries the line
	// that says how many there were.
	const named = 3
	ids := make([]string, 0, named)
	for _, c := range children[:min(named, len(children))] {
		ids = append(ids, digest.Short(c.ID))
	}
	rest := ""
	if len(children) > len(ids) {
		rest = fmt.Sprintf(" and %d more", len(children)-len(ids))
	}
	fmt.Fprintf(w, "deja: spawned %d session%s: %s%s\n", len(children), pluralS(len(children)), strings.Join(ids, ", "), rest)
}

// ChildrenOfSession is a thin seam so the surface can be tested without an
// index on disk.
var ChildrenOfSession = index.ChildrenOf

// bucketDayNote explains a note bucket's date when the records inside it fall
// on a different local day than the id claims — and stays silent otherwise, so
// the ordinary case gains no line.
func bucketDayNote(s model.Session) string {
	if s.Harness != "deja" || !strings.HasPrefix(s.ID, "deja-20") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(s.ID, "deja-"), "-")
	if len(parts) < 3 {
		return ""
	}
	day := strings.Join(parts[:3], "-")
	for _, m := range s.Messages {
		if m.Time.IsZero() {
			continue
		}
		if m.Time.Local().Format("2006-01-02") != day {
			return fmt.Sprintf("deja: %s in this id is the day the index was built in; the times below are this machine's — `deja index` regroups them", day)
		}
	}
	return ""
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
	args = splitKnownValueFlags(args, "--harness", "--offset", "--limit")
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--json":
			o.json = true
		case "--harness", "--offset", "--limit":
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs value", a)
			}
			i++
			if strings.TrimSpace(args[i]) == "" {
				// Empty is how "no filter" is spelled inside deja (#1612).
				return o, fmt.Errorf("%s needs value", a)
			}
			if a == "--harness" {
				o.harness = args[i]
				continue
			}
			v := args[i]
			n, e := strconv.Atoi(v)
			// 0 is where a session starts, so the two flags have different
			// floors; the words recall uses for the same flags (#5650).
			if a == "--offset" {
				if e != nil || n < 0 {
					return o, fmt.Errorf("show: --offset wants a number from 0 up, got %q", v)
				}
				o.offset = n
			} else {
				if e != nil || n <= 0 {
					return o, fmt.Errorf("show: --limit wants a positive number, got %q", v)
				}
				o.limit = n
			}
			o.sliced = true
		default:
			if strings.HasPrefix(a, "-") {
				return o, unknownFlag("show", a, showFlags)
			}
			// A whitespace-only token is no id at all: without this,
			// `deja show "   "` sailed past the missing-id refusal and a
			// leading empty argument was silently ignored (#5936).
			if strings.TrimSpace(a) == "" {
				return o, errors.New(showNeedsID)
			}
			if o.id != "" {
				return o, errors.New("show accepts one session id")
			}
			o.id = a
		}
	}
	// The id first: it is what the command is for, and checking the flag ahead
	// of it cost two runs to learn two missing things (#820).
	if o.id == "" {
		return o, errors.New(showNeedsID)
	}
	if o.limit > 200 {
		return o, errors.New("show --limit must not exceed 200")
	}
	return o, nil
}

// ctxFromIDPrefix answers from the session an id-prefix names, and reports
// whether it did. Six characters is where cmdCtx starts reading an argument as
// an id at all — below that a token is far likelier to be a word — so this also
// runs after the text search comes up empty, where there is no answer left to
// shadow (#1614).
func ctxFromIDPrefix(dir, q string) (bool, error) {
	s, ok, err := findByPrefix(dir, q)
	if err != nil || !ok {
		return false, err
	}
	// Every other reading surface stops here when a rule denies the session's
	// origin; ctx handed the whole session over, and it is the command the hook
	// tells an agent to call (#1026).
	if kept, hidden := policyFilterSessionsCounted(policy.ActivationSearch, []model.Session{s}); len(kept) == 0 {
		fmt.Fprint(os.Stderr, policyHiddenNote(policy.ActivationSearch, hidden))
		return false, fmt.Errorf("no session matches %q", q)
	}
	// The one command an agent is told to call — the hook's lead line names
	// recall_context — answered from one of several sessions behind an elided
	// id without saying it was a choice, while show, share, resume, promote,
	// forget and handoff all said so (#923).
	noteAmbiguousPrefix(dir, q, "answering from")
	// A decision that was taken back must say so here too. The search screen and
	// ctx's own query path both mark it; the id path handed an agent the whole
	// transcript of a withdrawn decision, reading like current truth (#1643).
	hits := []search.Hit{{Session: s}}
	attachLifecycles(dir, hits)
	if line := lifecycleLine(hits[0]); line != "" {
		fmt.Fprintln(os.Stdout, line)
	}
	cw := &countingWriter{w: os.Stdout}
	// The digest marks its own cuts; a message the index stored short is a
	// cut made before it, and `deja show` says so the same way (#5392).
	if line := clippedMessageNote(dir, s); line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
	search.PrintContextStyled(cw, s, "", search.ColorOK(os.Stdout), printableWidth(os.Stdout))
	if blk := changesBlock(s, search.ContextBudget-cw.n); blk != "" {
		fmt.Fprint(os.Stdout, "\n"+blk)
	}
	return true, nil
}

func cmdCtx(dir string, rest []string) error {
	if len(rest) < 1 {
		return idPrefixNeeded(dir, "ctx needs a query or an id-prefix",
			"ctx needs query or id-prefix (see `deja last`)")
	}
	// ctx takes no flags, and --json/--harness/--project/--since all exist on
	// neighbouring commands — so reaching for one here is the obvious mistake.
	// Folding it into the query answered "no session matches", which is a
	// false statement about the store (#721).
	// Except behind a leading `--`, which is how search, fix and how already
	// spell "the rest is the query". Without it no caller can ask about a
	// query that starts with a dash, and the refusal reads to a plugin exactly
	// like an empty store.
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	} else {
		for _, a := range rest {
			if strings.HasPrefix(a, "--") {
				return fmt.Errorf("ctx takes no flags, only a query or id-prefix — got %q; a question that starts with a dash goes after `--`", a)
			}
		}
	}
	q := strings.Join(rest, " ")
	if !strings.Contains(q, " ") {
		q = index.PastedSelector(q)
	}
	// Blank is not a query: it matched nothing and the search handed back the
	// first session in the store, so `deja ctx "$TOPIC"` with the variable
	// unset printed a transcript nobody asked for (#2259).
	if strings.TrimSpace(q) == "" {
		return idPrefixNeeded(dir, "ctx needs a query or an id-prefix",
			"ctx needs query or id-prefix (see `deja last`)")
	}
	if !strings.Contains(q, " ") && len(q) >= 6 {
		done, err := ctxFromIDPrefix(dir, q)
		if err != nil || done {
			return err
		}
	}
	o := search.Options{Query: nfcfold.Compose(q), All: true}
	if err := ensureForCLISearch(dir, o, false, os.Stderr); err != nil {
		if !staleUnwritableIndex(dir, err) {
			return err
		}
		fmt.Fprintf(os.Stderr, "deja: answering from the index as it was — %v\n", ensureError(dir, err))
	}
	// Detailed, not the plain form: retrieval answers a sentence by dropping
	// the terms nothing carries and returning the session under the close or
	// relevance tier. Handing those sessions to Run without the tier and its
	// variant map made scoring hunt for the whole literal query, score zero,
	// and report "no session matches" — about a store `deja search` answered
	// on the same words. ctx is the command the hook names to an agent, and
	// agents ask in sentences (#R8).
	result, err := index.SearchWithRecoveryDetailed(dir, o, os.Stderr)
	if err != nil {
		return err
	}
	ss := result.Sessions
	o.Tier = result.Tier
	// Which rung answered, said out loud on stderr as the search screen says
	// it — stdout stays the context block an agent parses. ctx served an
	// answer to a word the caller never typed and said nothing about it.
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
		printSpellings(os.Stderr, result.Variants)
		o.Fuzzy = true
		o.FuzzyVariants = result.Variants
	}
	if result.Tier == search.TierClose && o.FuzzyVariants == nil {
		o.FuzzyVariants = result.Variants
	}
	var hits []search.Hit
	if result.Tier == search.TierError {
		fmt.Fprintln(os.Stderr, "deja: matched by error signature; showing the sessions that hit it")
		// Both tiers below build their own hits and never went through the cap
		// in RunDetailed, so `--limit 3` printed the whole window — fifty
		// sessions — and so did a query with no flag (#3345).
		hits, _ = capTierHits(search.ErrorHits(ss, o.Query), o)
	} else if result.Tier == search.TierRelevance {
		o.Strict = result.Strict
		fmt.Fprintln(os.Stderr, relevanceLead(result.Strict))
		hits, _ = capTierHits(markStrictHits(search.RelevanceHitsWeighted(ss, index.RelevanceMatchTerms(o.Query), result.TermIDF), result), o)
	} else if hits, err = search.Run(ss, o); err != nil {
		return err
	}
	hits, policyHidden := policyFilterHitsCounted(policy.ActivationSearch, hits)
	if len(hits) == 0 && policyHidden > 0 {
		fmt.Fprint(os.Stderr, policyHiddenNote(policy.ActivationSearch, policyHidden))
		return fmt.Errorf("no session matches %q", q)
	}
	// The same order the search screen shows: ctx took the top hit off its own
	// ranking, so a session the reader had rejected was handed to the agent as
	// the answer while search demoted it and said why (#1099, #974).
	attachLifecycles(dir, hits)
	demoteRejected(hits)
	if len(hits) == 0 {
		// A prefix shorter than six characters never reached the id branch
		// above, so a session `deja show` opens on the same argument was
		// reported as no session at all (#1614). The query has already failed
		// here, so there is no answer for the id to shadow.
		if !strings.Contains(q, " ") && len(q) < 6 {
			done, ferr := ctxFromIDPrefix(dir, q)
			if ferr != nil {
				return ferr
			}
			if done {
				return nil
			}
		}
		// Same as #834 in files and restore: an empty store is not a miss on
		// the query.
		if n, cerr := index.SessionCount(dir); cerr == nil && n == 0 {
			return errors.New(strings.TrimPrefix(emptyIndexHint(fmt.Sprintf("no session matches %q", q)), "deja: "))
		}
		// The agent asking by a bucket id that moved got the dead end while
		// the human on `show` got the way forward (#1043).
		return fmt.Errorf("no session matches %q%s", q, movedBucketHint(dir, q))
	}
	// A short selector never reaches the id branch above, so a forgotten
	// session's note arrives as an ordinary hit — and the answer still has to
	// say the session is gone (#971).
	noteForgottenSource(hits[0].Session, q, false)
	// The hit carries the matching snippets, not the session: for a promoted
	// note that meant the correction — which rarely repeats the words of the
	// decision — was missing, and the command whose whole job is packaging
	// context handed an agent a decision that had been withdrawn (#1011).
	whole := hits[0].Session
	if full, ok, ferr := index.FindByIdentity(dir, whole.Harness, whole.ID); ferr == nil && ok {
		whole = full
	}
	cw := &countingWriter{w: os.Stdout}
	if line := clippedMessageNote(dir, whole); line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
	search.PrintContextStyled(cw, whole, q, search.ColorOK(os.Stdout), printableWidth(os.Stdout))
	if blk := changesBlock(whole, search.ContextBudget-cw.n); blk != "" {
		fmt.Fprint(os.Stdout, "\n"+blk)
	}
	return nil
}

// clearedNoteIDs names the notes whose borrowed title was just cleared. The
// line used to offer `--session deja-note-…`, an id that matches nothing —
// deja knows them here, and the ellipsis sent the reader to `deja last` to
// look up what this command was already holding (#1030).
func clearedNoteIDs(keys []string) string {
	// Only keys a note was actually promoted from: the dropped set also holds
	// the notes' own rows, and turning one of those into an id produced
	// `deja-note-deja-deja-note-claude-s1`, which matches nothing.
	known := promotedNoteSources()
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		id := model.PromotedNoteID(k)
		if _, ok := known[id]; !ok {
			continue
		}
		// Quoted one by one: the join renders them as separate code spans, so
		// quoting the finished list made every id one shell word (#2768).
		ids = append(ids, pasteSafe(id))
	}
	sort.Strings(ids)
	const shown = 3
	if len(ids) > shown {
		return strings.Join(ids[:shown], "`, `") + fmt.Sprintf("` and %d more", len(ids)-shown)
	}
	return strings.Join(ids, "`, `")
}
