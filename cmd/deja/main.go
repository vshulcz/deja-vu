package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

var version = "dev"

func main() {
	// A command that lands mid-rebuild waits for the whole of it, and silence
	// there reads as a hang rather than as a queue (#994).
	index.LockWaitNotice = func() {
		fmt.Fprintln(os.Stderr, "deja: another deja is building the index — waiting for it to finish")
	}
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
		// Not a rebuild, so the manifest is missing for some other reason —
		// and on a machine where the cache cannot be created that reached the
		// reader as `open …/manifest.gob: no such file or directory`, which is
		// the shape #798 replaced everywhere else (#2267).
		if d := index.DefaultDir(); !dirExists(d) {
			if a := nearestExistingDir(filepath.Dir(d)); a != "" && !dirWritable(a) {
				return fmt.Errorf("cannot create the index directory (%s) — %s is not writable; check its permissions, or point DEJA_INDEX_DIR somewhere writable", filepath.Dir(d), a)
			}
		}
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
	"hook-plan": func(dir string, _ []string) error {
		return runHookPlan(dir, os.Stdin, os.Stdout)
	},
	"hook-tool": func(dir string, _ []string) error {
		return runHookTool(dir, os.Stdin, os.Stdout)
	},
	"hook-tool-after": func(dir string, _ []string) error {
		return runHookToolAfter(dir, os.Stdin, os.Stdout)
	},
	"check": func(dir string, rest []string) error {
		return runCheck(dir, rest, os.Stdin, os.Stdout)
	},
	// The screen `deja` prints on a terminal, under a name, so it can be paged,
	// captured into a bug report or read over a pipe — and so the obvious guess
	// stops being a search for the word (#2108).
	// The arguments were thrown away here, so `deja brief --json` printed the
	// human screen and exited 0 — the shape #2253 fixed for friction and
	// restore (#2265). statusline stays as it is on purpose: a harness runs it
	// on every redraw, and a refusal would land in someone's prompt.
	"brief": func(dir string, rest []string) error {
		for _, a := range rest {
			return fmt.Errorf("brief takes no arguments — got %q", a)
		}
		return runBrief(dir, os.Stdout)
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
	"fix":             func(dir string, rest []string) error { return runFix(dir, rest, os.Stdout) },
	"how":             func(dir string, rest []string) error { return runHow(dir, rest, os.Stdout) },
	"log":             runLog,
	"sync":            runSync,
	"ctx":             cmdCtx,
	// Wrappers, but only with arguments: bare `deja aider` is far more
	// likely someone searching for the word than someone asking to launch
	// an editor, and launching one from a search is not a mistake worth
	// making. See cmdAider/cmdGoose.
	"hook-goose": cmdGooseHook,
	"hook-goose-prompt": func(dir string, _ []string) error {
		return refreshGooseForPrompt(dir, readHookStdin())
	},
	"blame": runBlame,
}

func run(args []string) error {
	dir := index.DefaultDir()
	if len(args) == 0 {
		// briefWanted, not logoWanted: a reader who turned colour off still
		// has an index and a terminal, and the brief is what that reader came
		// for (#1596).
		if briefWanted(os.Stdout) {
			return runBrief(dir, os.Stdout)
		}
		printUsage()
		return nil
	}
	sourceInstance := os.Getenv("DEJA_SOURCE_INSTANCE")
	warnBrokenPolicy(args[0], os.Stderr)
	if wantsHelp(args[1:]) {
		if h := helpForCommand(args[0]); h != "" {
			fmt.Print(h)
			return nil
		}
	}
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
	return runBareSearch(dir, args, sourceInstance)
}

func cmdVersion(_ string, _ []string) error {
	fmt.Fprintf(os.Stdout, "deja %s\n", version)
	return nil
}

func cmdWarmup(dir string, _ []string) error {
	stop := publishBuildProgress(dir)
	defer stop()
	prepareFirstIndexGreeting(dir)
	if err := withBuildProgress(func() error { return index.Ensure(dir, "", false, os.Stderr) }); err != nil {
		// Every other build path runs through ensureError; this one returned
		// the raw error, so a read-only index directory read as
		// `open /…/index.lock: permission denied` — an internal lock file and
		// a syscall, the shape #798 replaced everywhere else.
		return ensureError(dir, err)
	}
	maybeFirstIndexGreeting(dir)
	// The CLI-only path ends here: binary, warmup, search. Without this the
	// agent on such a machine has a working index and no instruction to reach
	// for it, because the skill deja shipped described MCP tools that were
	// never installed (#1320). A failure is not the warmup's failure — the
	// index is built and search works either way.
	_ = writeCLISkill()
	return nil
}

// countingWriter passes writes through and remembers whether there were any.
type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// indexQuietOutcome is the line an index run ends with when the build itself
// said nothing — another deja had already done the work. It reports what is
// actually on disk: a store can change again in the moment between the build
// finishing and this line, and claiming "up to date" there would be a guess.
func indexQuietOutcome(fresh bool, sessions int) string {
	if !fresh {
		return fmt.Sprintf("deja: another deja finished the build (%d session%s indexed); newer sessions are not in it yet", sessions, pluralS(sessions))
	}
	return fmt.Sprintf("deja: index is up to date (%d session%s)", sessions, pluralS(sessions))
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
	// The freshness check walks every store, which on a slow volume is the
	// longest part of the whole command, and it ran before the progress sink
	// existed (#1021).
	stopProgress := publishBuildProgress(dir)
	index.SweepStaleTmp(dir)
	fresh, n := index.UpToDate(dir, "")
	// A note bucket's id names the local day of whichever process indexed the
	// line, so a `remember` under TZ=UTC and one under the machine's own zone
	// split the same moment into two buckets. `show` tells the reader this
	// command regroups them, and it answered that the index was up to date
	// (#1058).
	if fresh && !force && index.NotesZoneDrift(dir) {
		fmt.Fprintln(os.Stderr, "deja: some notes were grouped in another time zone — regrouping them")
		fresh, force = false, true
	}
	if fresh && !force {
		stopProgress()
		// The warmup child runs this command, so returning before the sentinel
		// is cleared leaves a build that is not running: the next request is
		// suppressed until the retry window, and readWarmupStatus tells the
		// agent memory is on its way (#839).
		clearWarmupSentinel()
		fmt.Fprintf(os.Stderr, "deja: index is up to date (%d session%s)\n", n, pluralS(n))
		// "Up to date" is the most misleading place to stay quiet about it:
		// nothing changed on disk, so this is exactly where an exclusion set
		// after the build looks applied and is not.
		if index.ExclusionsChanged(dir) {
			fmt.Fprintln(os.Stderr, "deja: the exclude list changed since this index was built — `deja index --rebuild` applies it to sessions already indexed")
		}
		return nil
	}
	stopProgress()
	prepareFirstIndexGreeting(dir)
	// The detached warmup publishes its progress so hooks can tell the user
	// memory is on its way; an interactive run draws the live display.
	// Counted, because a run that waited for another build prints nothing of
	// its own: Ensure finds the index current under the lock and returns. The
	// command then owed a closing line and had none, leaving "waiting for it
	// to finish" as the last thing on screen (#1751).
	progress := &countingWriter{w: os.Stderr}
	build := func() error { return index.Ensure(dir, "", force, progress) }
	if err := withWarmupStatus(dir, func() error { return withBuildProgress(build) }); err != nil {
		// The command whose whole job is building the index used to pass the
		// syscall through — `mkdir /…/index.db.tmp: permission denied` names
		// an internal temp path and no fix, while every reading command has
		// said what to change since ensureError was written (#798).
		return ensureError(dir, err)
	}
	clearWarmupSentinel()
	if fresh, n := index.UpToDate(dir, ""); progress.n == 0 {
		fmt.Fprintln(os.Stderr, indexQuietOutcome(fresh, n))
	}
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
		// "deja: deja: 1 path could not be read" is the notes file, and reads
		// like a stutter. It is the user's own writing, so it says so (#901).
		name := h
		if h == "deja" {
			name = "your notes"
		}
		fmt.Fprintf(os.Stderr, "deja: %s: %d path%s could not be read — `deja doctor` names %s\n",
			name, e.FailedFiles, pluralS(e.FailedFiles), pluralWhich(e.FailedFiles))
	}
	// An exclusion applies at ingest, so it covers nothing already indexed —
	// and the sequence someone actually performs is to set the pattern, run
	// this command, and sync. That used to return silently having applied
	// nothing, and the export that followed still carried the project (#1307).
	if index.ExclusionsChanged(dir) {
		fmt.Fprintln(os.Stderr, "deja: the exclude list changed since this index was built — `deja index --rebuild` applies it to sessions already indexed")
	}
	// The parse count and the indexed count differ by exactly these, and this
	// is where both are on screen (#868).
	if n := index.ReportEmptySessions(); n > 0 {
		fmt.Fprintf(os.Stderr, "deja: %d transcript%s held no message deja could index — not counted as %s\n",
			n, pluralS(n), pluralSessionWord(n))
	}
	if n := index.ReportCollisions(); n > 0 {
		fmt.Fprintf(os.Stderr, "deja: %d session%s %s an id with another transcript — each pair is filed under one project, the one whose file sorts first\n", n, pluralS(n), verbShare(n))
	}
	// The per-harness lines above count transcripts, so once any of them
	// merged, those lines add up to more than the index holds — and the
	// reconciling line below is TTY-only, which is not where anyone is
	// counting. Say the real totals whenever the sums have parted (#1091).
	//
	// Every merge, not only the ones deja warns about: a goose session that
	// came back from both of that harness's stores is one conversation and
	// gets no warning, but it is still two transcripts against one row (#2066).
	if b := index.LastBuild; index.ReportMerged() > 0 && b.Messages > 0 {
		fmt.Fprintf(os.Stderr, "deja: indexed %d session%s, %d message%s — the per-harness lines above count transcripts, not rows\n",
			b.Sessions, pluralS(b.Sessions), b.Messages, pluralS(b.Messages))
	}
	// A machine with no agent history built an empty index and said nothing:
	// the step whose whole job is filling memory returned to the prompt after
	// a bare "indexing ..." line, and the state (no history anywhere, or a
	// store behind a permission wall) only surfaced on the next command.
	if b := index.LastBuild; b.Sessions == 0 && b.Messages == 0 && (noAgentHistoryFound() || deniedStoreCount() > 0) {
		fmt.Fprintln(os.Stderr, emptyIndexReason(b, index.ReportEvictedFiles()))
	}
	maybeFirstIndexGreeting(dir)
	// The live display erases itself on the way out, so a rebuild on a
	// terminal ended with an empty screen — three seconds of animation and no
	// record of what was built. Piped output has said it all along; this is
	// the same two numbers for the reader who watched it happen (#867).
	if b := index.LastBuild; !b.Initial && b.Messages > 0 && logoWanted(os.Stdout) && os.Getenv("DEJA_WARMUP_SENTINEL") == "" {
		fmt.Fprintf(os.Stderr, "deja: indexed %d session%s, %d message%s\n", b.Sessions, pluralS(b.Sessions), b.Messages, pluralS(b.Messages))
	}
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
		// The missing id is the one refusal that depends on the store: on a
		// machine with nothing indexed the honest answer is that there is
		// nothing to show, not that an argument is missing. parseShow has no
		// dir to ask, so the store-aware phrasing is applied here (#1063).
		if err.Error() == showNeedsID {
			return idPrefixNeeded(dir, "show needs an id-prefix", showNeedsID)
		}
		return err
	}
	// An id arrives from a chat wrapped in quotes or backticks and with the
	// harness deja itself printed in front of it (#921).
	o.id = index.PastedSelector(o.id)
	if o.id == "" {
		return idPrefixNeeded(dir, "show needs an id-prefix", showNeedsID)
	}
	// A harness that does not exist matches nothing, and the refusal then
	// blamed the id the reader typed correctly (#2251). search, last, blame
	// and the MCP tools have always named the value they did not recognise.
	if err := checkHarness(&o.harness); err != nil {
		return err
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
		if n, cerr := index.SessionCount(dir); cerr == nil && n == 0 {
			return errors.New(strings.TrimPrefix(emptyIndexHint(fmt.Sprintf("no session matches %q", o.id)), "deja: "))
		}
		return fmt.Errorf("no session matches %q%s", o.id, movedBucketHint(dir, o.id))
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
	}
	// A day bucket's date is the day the index was built in, not the moment a
	// line was written: read in another zone, `show` lists a record dated the
	// day before the id says, and nothing connected the two (#1006).
	if note := bucketDayNote(s); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}
	printSpawnEdges(os.Stderr, dir, s)
	// A pasted log is one message, and the index keeps the head of it. The
	// window note above says when messages were left out; nothing said when a
	// message was, so a reader searching a log for the line that explains a
	// failure searched one they believed was whole (#2467).
	if line := clippedMessageNote(dir, s); line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
	search.PrintSession(os.Stdout, s)
	return nil
}

// clippedMessageNote says that a message in this session was stored short of
// what the transcript holds. The count is the store's, not this session's —
// deja records it per file at ingest — so the line names the session's own
// file and leaves the arithmetic to `deja doctor`.
func clippedMessageNote(dir string, s model.Session) string {
	if s.Path == "" {
		return ""
	}
	files := index.IngestFilesReport(dir)
	e, ok := files[s.Path]
	if !ok || e.Clipped == 0 {
		return ""
	}
	return fmt.Sprintf("deja: %s stored short of what the transcript holds — the rest of %s is in the file itself",
		pluralMessages(e.Clipped), pluralThem(e.Clipped))
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
	return fmt.Sprintf("deja: showing message%s %d-%d of %d — `--offset %d` reads the next slice",
		pluralS(returned), first, last, total, last)
}

// printSpawnEdges names the sessions around this one when an agent spawned it
// or spawned others from it. A subagent's work lives in its own session, so a
// reader who found the parent could not get to it and a reader who found the
// child could not say what asked for it (#1385).
func printSpawnEdges(w io.Writer, dir string, s model.Session) {
	if s.Parent != "" {
		by := ""
		if s.Agent != "" {
			by = " as " + s.Agent
		}
		fmt.Fprintf(w, "deja: spawned from %s%s — `deja show %s`\n", digest.Short(s.Parent), by, digest.Short(s.Parent))
	} else if s.Kind != "" {
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
		return o, errors.New(showNeedsID)
	}
	if o.json && o.harness == "" {
		return o, fmt.Errorf("show --json requires --harness for exact identity")
	}
	if o.limit > 200 {
		return o, fmt.Errorf("show --limit must not exceed 200")
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
	search.PrintContext(os.Stdout, s, "")
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
	for _, a := range rest {
		if strings.HasPrefix(a, "--") {
			return fmt.Errorf("ctx takes no flags, only a query or id-prefix — got %q", a)
		}
	}
	q := strings.Join(rest, " ")
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
	if result.Neighbour {
		printNeighbour(os.Stderr, result.Variants)
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	} else if result.Stemmed {
		printStemmed(os.Stderr, result.Variants)
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	} else if result.Fuzzy {
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
		hits = search.ErrorHits(ss)
	} else if result.Tier == search.TierRelevance {
		fmt.Fprintln(os.Stderr, "deja: no exact match; showing sessions ranked by relevance to the whole query")
		hits = search.RelevanceHitsWeighted(ss, index.RelevanceMatchTerms(o.Query), result.TermIDF)
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
	noteForgottenSource(hits[0].Session, q)
	// The hit carries the matching snippets, not the session: for a promoted
	// note that meant the correction — which rarely repeats the words of the
	// decision — was missing, and the command whose whole job is packaging
	// context handed an agent a decision that had been withdrawn (#1011).
	whole := hits[0].Session
	if full, ok, ferr := findByPrefix(dir, whole.ID); ferr == nil && ok {
		whole = full
	}
	search.PrintContext(os.Stdout, whole, q)
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
		id := "deja-note-" + strings.ReplaceAll(k, ":", "-")
		if _, ok := known[id]; !ok {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	const shown = 3
	if len(ids) > shown {
		return strings.Join(ids[:shown], "`, `") + fmt.Sprintf("` and %d more", len(ids)-shown)
	}
	return strings.Join(ids, "`, `")
}

func cmdLast(dir string, rest []string, sourceInstance string) error {
	n, o, sinceRaw, err := parseLast(rest)
	if err != nil {
		return err
	}
	harnessRaw := o.Harness
	if err := checkHarness(&o.Harness); err != nil {
		return err
	}
	if err := checkRole(o.Role); err != nil {
		return err
	}
	ss, err := recentMatching(dir, n, o)
	if err != nil {
		return err
	}
	// The listing is titles and project names, which is exactly what the trust
	// policy exists to keep off the screen — and it was the one path that never
	// consulted it, while `search` under the same rule refused and said so
	// (#937). Listing counts as browsing: the search activation governs it.
	ss, policyHidden := policyFilterSessionsCounted(policy.ActivationSearch, ss)
	// Printing nothing at all and exiting 0 leaves no way to tell whether the
	// command worked, found nothing, or failed silently — which is what a
	// fresh install sees. blame already answers this shape of question.
	if note := policyHiddenNote(policy.ActivationSearch, policyHidden); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}
	// And the other rule that withholds rows here. It filters inside the index
	// (#2541), so the count comes from the manifest rather than from what came
	// back — the same walk the listing has just done, over metas already read.
	if note := ignoredHiddenNote(index.IgnoredMatching(dir, o)); note != "" {
		fmt.Fprint(os.Stderr, note)
	}
	if len(ss) == 0 {
		if o.JSON {
			return printRecentJSONWithheld(os.Stdout, nil, sourceInstance, policyHidden)
		}
		// The rule that emptied the list was named a line above; "no sessions
		// indexed yet — run `deja index`" is advice for a state deja is not in,
		// and the backside of teaching the listing to filter at all (#937,
		// #949).
		if policyHidden > 0 {
			return nil
		}
		// "Run deja index" is advice for an empty store. With a filter set it
		// is advice for a state the tool is not in: indexing changes nothing
		// and doctor reports the stores as found. Name what emptied the result
		// instead (#637).
		if where := activeFilters(o, sinceRaw, harnessRaw); where != "" {
			fmt.Fprintf(os.Stderr, "deja: no sessions match %s\n", where)
			fmt.Fprint(os.Stderr, olderThanWindow(dir, o.Since))
			return nil
		}
		// "run `deja index`" cannot bring back what the reader forgot, and on a
		// store emptied by their own settings it is the wrong instruction —
		// search has said so since #844; the listing did not (#1007).
		if note := hiddenByOwnSettings(); note != "" {
			fmt.Fprint(os.Stderr, "deja: no sessions indexed yet\n"+note)
			return nil
		}
		fmt.Fprintln(os.Stderr, emptyIndexHint("no sessions indexed yet"))
		return nil
	}
	if o.JSON {
		return printRecentJSONWithheld(os.Stdout, ss, sourceInstance, policyHidden)
	}
	for _, s := range ss {
		// A session whose timestamp was missing or unparseable carries the Go
		// zero time, and "0001-01-01" reads as corrupted data rather than as a
		// missing field. Search prints a dash here and the first screen leaves
		// such sessions out of its range; this was the one place that did not
		// follow the convention (#765).
		when := "-"
		if !s.Updated.IsZero() {
			// The reader's zone, like the brief and stats: a session stamped
			// 22:00 UTC is 01:00 tomorrow for its author, and this line put it
			// on the day before the other two screens did (#849).
			when = s.Updated.Local().Format("2006-01-02")
		}
		// The id's own day is not used here, unlike search: this line prints
		// the id whole, so nothing has to be rebuilt from the date (#883),
		// while borrowing the id's day made the column run 06, 07, 04 down
		// the screen for a reader far enough east of the writer (#1038).

		// Project, id and title are text a harness wrote, and this is one
		// line: an escape byte in any of them recolours the rest of the
		// listing and a carriage return rewinds it (#1090).
		fmt.Printf("[%s · %s · %s · %s]", s.Harness, redact.SafeForDisplay(displayProject(s)), when, redact.SafeForDisplay(s.ID))
		title := s.Title
		if title == "" {
			title = firstUserTitle(s)
		}
		// The title is transcript text going straight to a terminal: an escape
		// in it repaints the screen and a carriage return rewinds the line.
		// SafeForDisplay keeps a newline on purpose — the reading surfaces are
		// the session's own layout — but this is one row of a listing, and a
		// note title carries whatever a person wrote by hand (#2058).
		if title = search.SafeNoteTitle(redact.SafeForDisplay(title)); title != "" {
			// A session with no user turn borrows the assistant's opening line
			// (#692), and unmarked it read like the reader's own question
			// (#1100).
			if s.AgentTitle {
				fmt.Printf(" agent: %s", title)
			} else {
				fmt.Printf(" %s", title)
			}
		}
		fmt.Println()
	}
	// The listing is ordered by a date, so one that has not happened leads it
	// and nothing else on the screen says why. The first screen carries the
	// same sentence beside the same list, because leaving it unexplained makes
	// the counters and the list disagree for no visible reason (#696, #2104).
	if n := stampedAheadCount(ss, time.Now()); n > 0 {
		fmt.Fprintf(os.Stderr, "deja: %d session%s stamped later than this machine's clock — %s at the top of this list\n",
			n, pluralS(n), pluralThatThose(n))
	}
	return nil
}

// stampedAheadCount counts the listed sessions whose stamp is after now, by the
// same rule the first screen counts them with.
func stampedAheadCount(ss []model.Session, now time.Time) int {
	n := 0
	for _, s := range ss {
		if index.StampedAhead(s.Updated, now) {
			n++
		}
	}
	return n
}

// pluralThatThose keeps the sentence above readable for one session and for
// several, the way pluralThem does for the ingest lines.
func pluralThatThose(n int) string {
	if n == 1 {
		return "that one is"
	}
	return "those are"
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
		return index.EnsureForSearch(dir, o, true, progress)
	}
	stale, err := index.EnsureForSearchStale(dir, o, progress)
	if err != nil {
		return err
	}
	if stale {
		requestWarmup(dir)
		fmt.Fprintln(progress, "deja: answering from the index as it was — refreshing in the background")
	}
	return nil
}

// runBareSearch is `deja <words>` — the form where the first word stood where a
// command name goes. That is the only form the mistyped-command hint is about:
// `deja search doctro` is someone searching for the word (#2197).
func runBareSearch(dir string, args []string, sourceInstance string) error {
	return searchWithOptions(dir, args, sourceInstance, true)
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
	if err := checkHarness(&o.Harness); err != nil {
		return err
	}
	if err := checkRole(o.Role); err != nil {
		return err
	}
	sinceRaw := sinceRawArg(filtered)
	o.SourceInstance = sourceInstance
	o.RecallWorn = usage.WornSessions(dir)
	prepareFirstIndexGreeting(dir)
	if err := withBuildProgress(func() error { return ensureForCLISearch(dir, o, force, os.Stderr) }); err != nil {
		// A store that cannot be written still has an index that can be read:
		// answering from it beats answering nothing (#904).
		if !staleUnwritableIndex(dir, err) {
			return ensureError(dir, err)
		}
		fmt.Fprintf(os.Stderr, "deja: answering from the index as it was — %v\n", ensureError(dir, err))
	}
	maybeFirstIndexGreeting(dir)
	result, err := index.SearchWithRecoveryDetailed(dir, o, os.Stderr)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	// Before ranking, not after: the cap is applied while ranking, so a rule
	// that runs later still lets denied sessions occupy the result slots and
	// the allowed ones never reach the page (#1060).
	ss, policyHidden := policyFilterSessionsCounted(policy.ActivationSearch, search.WithoutIgnored(result.Sessions))
	o.PolicyWithheld = policyHidden
	o.Tier = result.Tier
	if result.Neighbour {
		printNeighbour(os.Stderr, result.Variants)
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	} else if result.Stemmed {
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
	switch result.Tier {
	case search.TierError:
		// A pasted error IS a match; ErrorHits keeps the ranking and the error
		// neighbourhood the tier found. Re-scoring it as an ordinary run would
		// zero it out — the word ladder already failed, which is why this tier
		// fired at all.
		fmt.Fprintln(os.Stderr, "deja: matched by error signature; showing the sessions that hit it")
		hits = search.ErrorHits(ss)
		o.Total, o.Capped = result.Total, result.Capped
		if o.Total < len(hits) {
			o.Total = len(hits)
		}
	case search.TierRelevance:
		fmt.Fprintln(os.Stderr, "deja: no exact match; showing sessions ranked by relevance to the whole query")
		hits = search.RelevanceHitsWeighted(ss, index.RelevanceMatchTerms(o.Query), result.TermIDF)
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
	default:
		// RunDetailed rather than Run: the JSON envelope reports how many
		// sessions matched before the cap, and that is not recoverable from a
		// list the cap has already trimmed.
		detailed, rerr := search.RunDetailed(ss, o)
		if rerr != nil {
			// "run:" is the name of a function, and the reader of this line is
			// someone who mistyped a pattern (#2286). The only error that
			// reaches here in practice is the regexp compile, so it says which
			// input to look at.
			if o.Regex {
				return fmt.Errorf("--re pattern: %w", rerr)
			}
			return rerr
		}
		hits, o.Total, o.Capped = detailed.Hits, detailed.Total, detailed.Capped
	}
	if !o.NoEmbed && os.Getenv("DEJA_EMBED") != "off" {
		hits = maybeRerank(dir, hits, o, os.Stderr)
	}
	var semantic bool
	hits, semantic = maybeSemantic(dir, hits, o, os.Stderr)
	if semantic {
		// The lexical hits were scoped by the trust policy above, but the
		// semantic tier reaches the whole sidecar and brings back sessions of
		// its own — an imported peer's content the policy withholds from every
		// other read path leaked straight through the embedding fallback. Scope
		// the semantic hits the same way before they are shown.
		hits, _ = policyFilterHitsCounted(policy.ActivationSearch, hits)
	}
	o.Semantic = semantic
	// Policy scoping, reranking and the semantic tier all run after the cap, so
	// the pre-cap count can no longer describe what is being returned. When
	// nothing was capped the honest total is simply what survived; when it was,
	// there is no way to know how the filters would have treated the hidden
	// ones, so the pre-cap figure stands and `capped` says to distrust it.
	attachLifecycles(dir, hits)
	demoted := demoteRejected(hits)
	attachMoved(hits)
	if !o.Capped {
		o.Total = len(hits)
	}
	if note := demotedNote(hits, demoted); note != "" {
		fmt.Fprintf(os.Stderr, "deja: %s\n", note)
	}
	if note := otherWordFormsNote(dir, o, hits); note != "" {
		fmt.Fprint(os.Stderr, note)
	}
	if len(hits) == 0 {
		// The policy is named before the generic advice: "try fewer words" is
		// wrong counsel for someone whose words were fine (#680). A filter the
		// caller set is the same kind of fact, and `deja last` has named it all
		// along (#715).
		switch note := policyHiddenNote(policy.ActivationSearch, policyHidden); {
		case note != "":
			fmt.Fprint(os.Stderr, note)
		case activeFilters(o, sinceRaw, harnessRaw) != "":
			fmt.Fprintf(os.Stderr, "deja: %q matched nothing under %s\n", o.Query, activeFilters(o, sinceRaw, harnessRaw))
			fmt.Fprint(os.Stderr, emptyRoleNote(dir, o.Role))
			fmt.Fprint(os.Stderr, olderThanWindow(dir, o.Since))
		default:
			printNoMatches(os.Stderr, dir, o.Query, o.Regex)
		}
	}
	if o.Capped && len(hits) > 0 {
		// The cap is silent everywhere else: 15 results look like the whole
		// answer, and nothing says another N are waiting behind --all. deja
		// narrates every other place the ladder hides a session, so it says
		// this one too.
		//
		// Except to a reader who already typed --all: there the list was cut by
		// an explicit --limit, or by the relevance tier's own retrieval window,
		// and neither of those lifts with the flag they are being sent to
		// (#1608). Say what is shown and leave the advice out.
		if o.All {
			fmt.Fprintf(os.Stderr, "deja: showing %d of %d\n", len(hits), o.Total)
		} else {
			fmt.Fprintf(os.Stderr, "deja: showing %d of %d — add --all to see the rest\n", len(hits), o.Total)
		}
	}
	// The answer can also be short for a reason no flag lifts. Counted over the
	// sessions holding every term, so an ordinary search in a store with a big
	// ignored tree stays quiet (#2562).
	if note := ignoredHiddenNoteFor("answer", index.IgnoredWithAllTerms(dir, query.Tokens(o.Query))); note != "" {
		fmt.Fprint(os.Stderr, note)
	}
	// The window this is being printed into, so the lines can be budgeted
	// rather than assumed 80 wide. Only for a terminal: a pipe gets the whole
	// line, since a script reading deja wants the text and not the layout
	// (#604).
	o.Width = printableWidth(os.Stdout)
	// Through a counter, so the log records what actually went out rather than
	// a guess at it. `deja log` is the audit of what deja did, and the search
	// kind has been named in the docs, in the comment over the kind constants
	// and in the empty-log line since #47 while nothing ever wrote one (#2471).
	// It stays out of every count: servedKind and injectedKind both exclude it,
	// so the statusline and the impact screen still speak only for memory that
	// reached an agent.
	counted := &countingWriter{w: os.Stdout}
	search.Print(counted, hits, o)
	usage.RecordResult(dir, usage.KindSearch, counted.n, len(hits), len(hits) == 0)
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
	if bare && len(hits) > 0 && len(strings.Fields(o.Query)) == 1 {
		fmt.Fprint(os.Stderr, commandHint(o.Query))
	}
	return nil
}

// printNeighbour narrates a co-occurrence swap. It is not a word form: the
// corpus says these two words keep company, which is a different claim and
// reads as a typo correction if it borrows the other sentence (#1786).
func printNeighbour(w io.Writer, variants map[string][]string) {
	keys := make([]string, 0, len(variants))
	for token := range variants {
		keys = append(keys, token)
	}
	sort.Strings(keys)
	for _, token := range keys {
		for _, variant := range variants[token] {
			if variant != "" && variant != token {
				fmt.Fprintf(w, "deja: no session has those words together, so deja tried one this corpus keeps beside %q: %s\n", token, variant)
			}
		}
	}
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

// otherWordFormsNote says that a word form the query did not use has sessions
// of its own that this answer does not contain.
//
// The exact tier wins and stops, so `retry` returns what wrote "retry" and
// never reaches "retries" — the stem tier below it only runs when exact comes
// up empty. deja narrates every other time the ladder shapes an answer (close
// spellings, dropped terms, the trust policy); this rung was the silent one,
// and silence here reads as "that is all there is". Only the forms that bring
// sessions the answer does not already hold are worth a line.
func otherWordFormsNote(dir string, o query.Options, hits []search.Hit) string {
	if o.Regex || o.Tier != search.TierExact || len(hits) == 0 {
		return ""
	}
	terms := query.Tokens(o.Query)
	if len(terms) == 0 || len(terms) > 4 {
		return ""
	}
	forms := index.OtherWordForms(dir, terms)
	if len(forms) == 0 {
		return ""
	}
	var flat []string
	for _, list := range forms {
		flat = append(flat, list...)
	}
	counts := index.TermSessionCounts(dir, flat)
	var parts []string
	for _, term := range terms {
		for _, form := range forms[term] {
			extra := counts[form] - sessionsHolding(hits, form)
			if extra <= 0 {
				continue
			}
			parts = append(parts, fmt.Sprintf("%q in %d more", form, extra))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("deja: word forms this answer leaves out: %s — search the form to see them\n", strings.Join(parts, ", "))
}

// sessionsHolding counts the returned sessions that already contain a word
// form, so the note reports what is missing rather than what is on the page.
func sessionsHolding(hits []search.Hit, form string) int {
	n := 0
	for _, h := range hits {
		for _, m := range h.Session.Messages {
			if containsWord(strings.ToLower(m.Text), form) {
				n++
				break
			}
		}
	}
	return n
}

// containsWord matches a whole word, not a substring: "retry" inside
// "retrying" is a different form and counting it would hide the one the note
// exists to name.
func containsWord(low, word string) bool {
	for i := 0; ; {
		j := strings.Index(low[i:], word)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(word)
		beforeOK := start == 0 || !isWordByte(low[start-1])
		afterOK := end == len(low) || !isWordByte(low[end])
		if beforeOK && afterOK {
			return true
		}
		i = start + 1
	}
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b >= 0x80
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
func printNoMatches(w io.Writer, dir, q string, regex bool) {
	// An empty store is not a query problem: "fewer words" cannot help when
	// nothing is indexed, and `last`, `blame` and the brief all say what to do
	// instead. Search is the command a new machine reaches for first (#832).
	if n, err := index.SessionCount(dir); err == nil && n == 0 {
		// "Zero indexed sessions" has two very different causes, and the early
		// return named only the first: a machine with no history, and a machine
		// where the reader forgot everything themselves. `deja index` cannot
		// bring back a tombstoned session (#844).
		if note := hiddenByOwnSettings(); note != "" {
			fmt.Fprintf(w, "deja: no matches for %q\n", q)
			fmt.Fprint(w, note)
			return
		}
		fmt.Fprintln(w, emptyIndexHint(fmt.Sprintf("no matches for %q", q)))
		return
	}
	// Nothing to look up: very short tokens are dropped and punctuation is
	// trimmed, so this query never reached the index. "Try fewer words"
	// cannot be followed with one word, or none (#828). The message does not
	// state the rule — the cut is on bytes, so "л" and "舵" are long enough
	// while "p" is not, and a rule that reads false to half the world's
	// alphabets is worse than none.
	// Both sentences below are about the word index, and --re does not use it:
	// a regex is matched against the text itself, so an emoji it did not find
	// is a miss like any other rather than something deja cannot look up.
	if !regex && len(query.Tokens(q)) == 0 {
		// Length is the wrong reason for a query that holds no word at all: an
		// emoji is four bytes, and it was dropped for being a symbol. Sending
		// that reader after a longer word sends them after nothing (#2133).
		if !hasIndexableRune(q) {
			fmt.Fprintf(w, "deja: nothing to search for in %q — deja indexes words, and emoji, symbols and punctuation are not words\n", q)
			return
		}
		fmt.Fprintf(w, "deja: nothing to search for in %q — every word in it is too short to look up\n", q)
		return
	}
	// The size worth naming is the part of the store this path may read. Under
	// a trust rule the two differ, and "no matches in 1 indexed session — try
	// fewer words" sent the reader after a wording problem in a session the
	// rule never let search open (#986).
	if reach, total, ok := reachableSessionCount(dir); ok && reach == 0 && total > 0 {
		fmt.Fprintf(w, "deja: no matches for %q\n", q)
		fmt.Fprintf(w, "deja: the trust policy withholds every indexed session from this path (%s: %s) — see %s\n",
			policy.ActivationSearch, policy.Load().Describe(policy.ActivationSearch), policy.Path())
		return
	} else if ok {
		fmt.Fprintf(w, "deja: no matches in %d indexed session%s — try fewer words or --re (query %q)\n", reach, pluralS(reach), q)
	} else if n, err := index.SessionCount(dir); err == nil {
		fmt.Fprintf(w, "deja: no matches in %d indexed session%s — try fewer words or --re (query %q)\n", n, pluralS(n), q)
	} else {
		fmt.Fprintf(w, "deja: no matches — try fewer words or --re (query %q)\n", q)
	}
	// Before advising fewer words: the sessions that hold every one of them may
	// exist and be covered by the ignore rule, in which case rewording is the
	// wrong advice and the count is the answer (#2562).
	if note := ignoredHiddenNoteFor("answer", index.IgnoredWithAllTerms(dir, query.Tokens(q))); note != "" {
		fmt.Fprint(w, note)
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
	// A hint is about a guess at a command, and a guess is short: a command
	// with an argument or two. "hook up the pool" is a sentence, and a hint
	// there is noise on top of a search that already failed — the same lesson
	// #715 learned about short words (#2115).
	if !looksLikeAnInvocation(q) {
		return ""
	}
	low := strings.ToLower(first)
	// A guess with an exact answer behind it. `deja hook context` is the
	// hyphen-free spelling of a command that exists, and the candidate list
	// below drops every `hook-` name on the grounds that plumbing is not
	// something anyone means to type — which is right about proposing it and
	// wrong about hiding it from the reader who typed its own stem (#2115).
	if hint := hyphenatedCommandHint(q, low); hint != "" {
		return hint
	}
	// The word deja's own MCP tool is called, and the one the documentation
	// uses throughout, so `deja recall <words>` is a fair guess at the shell
	// form. Nothing is near it by name, so the hint said nothing at all —
	// the same shape as the curated answers below (#2115).
	if low == "recall" {
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(q), first))
		if rest != "" {
			return fmt.Sprintf("deja: %q is what the MCP tool is called — from a shell it is `deja %q`, and `deja hook-prompt` is the per-prompt recall\n", first, rest)
		}
		return fmt.Sprintf("deja: %q is what the MCP tool is called — from a shell it is `deja <query>`, and `deja hook-prompt` is the per-prompt recall\n", first)
	}
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
	// The same shape for the other undo nobody can guess: removing a note is
	// `forget` on the note's own id, and the reader who typed this has no way
	// to know a note has an id at all (#1085).
	if strings.EqualFold(first, "unpromote") || strings.EqualFold(first, "demote") {
		return "deja: \"" + first + "\" is not a command — `deja promote <id> --state rejected` takes a decision back, and `deja forget --session deja-note-<harness>-<id>` removes the note itself\n"
	}
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
	// "unforget" is the one wrong guess with a real answer behind it, and
	// `deja forget` alone sends the reader back to a list that used to name
	// nothing (#919).
	if strings.EqualFold(first, "unforget") {
		return "deja: \"unforget\" is not a command — `deja forget --unforget <id>` is, and `deja forget --list` names the ids\n"
	}
	return fmt.Sprintf("deja: %q is not a command — did you mean `deja %s`?\n", first, near)
}

// hyphenatedCommandHint answers the guess that spelled a hyphenated command
// with a space: the two words joined name a real one, or the first word is the
// stem of some and there is no second word to join (#2115).
func hyphenatedCommandHint(q, low string) string {
	rest := strings.Fields(strings.TrimSpace(q))
	if len(rest) > 1 {
		joined := low + "-" + strings.ToLower(rest[1])
		if _, ok := commands[joined]; ok {
			return fmt.Sprintf("deja: %q is not a command — did you mean `deja %s`?\n", low+" "+rest[1], joined)
		}
	}
	// Only the stem on its own. With words after it that do not join into a
	// command, the reader is searching — "hook up the pool" is a sentence, and
	// a hint about `hook-context` there is noise on top of a failed search.
	if len(rest) != 1 {
		return ""
	}
	var stems []string
	for name := range commands {
		if strings.HasPrefix(name, low+"-") {
			stems = append(stems, name)
		}
	}
	if len(stems) == 0 {
		return ""
	}
	sort.Strings(stems)
	if len(stems) > 3 {
		stems = stems[:3]
	}
	return fmt.Sprintf("deja: %q is not a command on its own — `deja %s` and the rest of that family are; `deja help` names them\n",
		low, strings.Join(stems, "`, `deja "))
}

// looksLikeAnInvocation separates a guess at a command from prose. A command
// and an argument or two is short; "recall the decision we made about the pool"
// is a sentence, and the hint is about the first kind (#2115).
func looksLikeAnInvocation(q string) bool {
	return len(strings.Fields(q)) <= 3
}

// hasIndexableRune says whether a query holds anything the index could have
// kept. Letters and digits are what the tokenizer keeps; everything else is a
// separator, which is why an emoji-only query reaches the index as nothing.
func hasIndexableRune(q string) bool {
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// sinceRawArg recovers the text the reader typed after --since. parseSearch
// keeps only the parsed duration, and the search path had nothing else to hand
// activeFilters, so it reported "since 720h0m0s" for `--since 30d` (#1059).
// Last wins, as in parseSearch.
func sinceRawArg(args []string) string {
	args = splitEqualsForms(args)
	raw := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--since" {
			raw = args[i+1]
		}
	}
	return raw
}

// olderThanWindow names the store's own range when --since cut off everything
// in it. Without it someone returning to a year-old store is told their query
// matched nothing — advice about the query, when it was the window that
// emptied the result and no query under it could have returned anything.
// `deja stats` has said this since #854; search and `last` had not (#1059).
func olderThanWindow(dir string, since time.Duration) string {
	if since <= 0 {
		return ""
	}
	ov, err := index.Overview(dir)
	if err != nil || ov.Sessions == 0 || ov.Newest.IsZero() {
		return ""
	}
	if !ov.Newest.Before(time.Now().Add(-since)) {
		return ""
	}
	return fmt.Sprintf("deja: every one of the %d indexed sessions is older than that window — the newest is %s\n",
		ov.Sessions, ov.Newest.Local().Format("2006-01-02"))
}

// checkHarness rejects a --harness value deja does not know. A typo used to be
// indistinguishable from a real harness with no sessions — both said "matched
// nothing under harness X" — so `--harness cluade` read as "you have no claude
// history" instead of "that is not a harness". A known-but-empty harness is
// still valid; only an unknown name is refused (#1113).
// It also normalises, which is why it takes a pointer: the notes store is
// stored as "deja" and narrated by the index run as "notes", and retrieval has
// accepted both since #1888 — but this check ran first and refused the printed
// name as a typo, so nothing below it ever saw the alias, and `deja stats`
// compares the stored name exactly (#2191).
func checkHarness(name *string) error {
	if *name == "notes" {
		*name = "deja"
	}
	if *name == "" || sources.IsKnownHarness(*name) {
		return nil
	}
	return fmt.Errorf("%q is not a harness deja knows — one of: %s", *name, strings.Join(sources.HarnessNames(), ", "))
}

// knownRoles is the set `--role` accepts. It lists the documented spellings the
// help text prints plus "tool-output", the stored form "tool" is an alias for —
// both reach tool records, so both must be accepted.
var knownRoles = []string{
	"user", "assistant", "tool", sources.RoleToolOutput,
	sources.RoleFiles, sources.RoleCommand, sources.RoleEdit,
}

// checkRole rejects a --role value that is not a known role. Like an unknown
// --harness, a typo used to be indistinguishable from a real role with no
// matches: `--role toool` said "matched nothing under role toool" instead of
// naming the mistake (#1113).
func checkRole(role string) error {
	if role == "" {
		return nil
	}
	for _, r := range knownRoles {
		if r == role {
			return nil
		}
	}
	return fmt.Errorf("%q is not a role deja knows — one of: %s", role, strings.Join(knownRoles, ", "))
}

// emptyRoleNote says when a role found nothing because the store holds none of
// that kind, rather than because the query missed.
//
// deja advertises that it indexes the work and not only the talk, and a harness
// that records no tool calls gives a transcript that looks complete: `--role
// tool` came back the same way a bad query does, and the reader had no way to
// tell "not found" from "not recorded" (#1321). Only asked when a role-filtered
// search already returned nothing.
func emptyRoleNote(dir, role string) string {
	if role == "" {
		return ""
	}
	stored := role
	if role == "tool" {
		stored = sources.RoleToolOutput
	}
	if index.HasRecordOfRole(dir, stored) {
		return ""
	}
	return fmt.Sprintf("deja: this index holds no %s records at all — the harnesses it read do not write them, or wrote none yet\n", role)
}

// activeFilters names the filters a caller set, so an empty result can say
// which of them emptied it rather than blaming the index. sinceRaw carries
// what the reader actually typed: "168h0m0s" is not the flag they passed.
func activeFilters(o search.Options, sinceRaw, harnessRaw string) string {
	var parts []string
	if o.Harness != "" {
		// Same reason as sinceRaw: `--harness notes` is stored as "deja", and
		// telling someone their "deja" filter matched nothing names a flag
		// they did not pass (#2191).
		name := o.Harness
		if harnessRaw != "" {
			name = harnessRaw
		}
		parts = append(parts, fmt.Sprintf("harness %q", name))
	}
	if o.Project != "" {
		parts = append(parts, fmt.Sprintf("project %q", o.Project))
	}
	if o.Role != "" {
		parts = append(parts, fmt.Sprintf("role %q", o.Role))
	}
	if o.Session != "" {
		parts = append(parts, fmt.Sprintf("session %q", o.Session))
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

// printSpellings names the words the close tier substituted. ctx uses this
// rather than printFuzzy: "did you mean the command" is advice for a person
// at a shell, and ctx is called by an agent that typed nothing.
func printSpellings(w io.Writer, variants map[string][]string) {
	keys := make([]string, 0, len(variants))
	for token := range variants {
		keys = append(keys, token)
	}
	sort.Strings(keys)
	for _, token := range keys {
		for _, variant := range variants[token] {
			if variant != token {
				fmt.Fprintf(w, "deja: no exact match, trying close spellings: %s -> %s\n", token, variant)
			}
		}
	}
}

func printFuzzy(w io.Writer, variants map[string][]string) {
	printSpellings(w, variants)
	keys := make([]string, 0, len(variants))
	for token := range variants {
		keys = append(keys, token)
	}
	sort.Strings(keys)
	for _, token := range keys {
		for _, variant := range variants[token] {
			// A misspelled subcommand is searched for as a word: `deja
			// isntall` corrects to "install" and returns sessions that
			// mention installing, which is not what the typist wanted.
			// Spelled correctly it would have run the command, so the only
			// case this fires on is the one where the hint is wanted.
			if variant != token && isSubcommand(variant) {
				fmt.Fprintf(w, "deja: `%s` is also a command — run `deja %s` if that is what you meant\n", variant, variant)
				return
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

// noteAmbiguousPrefix says when a selector reached more than one session.
//
// The prefix picks the newest of its matches, which is the right default and
// was a silent one: "2" resolved eleven sessions on a real store. `show`
// learned to say so in #719 and #859; promote, handoff, resume and share
// resolve the same way and still picked in silence — promote records a state
// against whichever session it chose (#872).
func noteAmbiguousPrefix(dir, id, action string) {
	// Counted under the rule the reader is searching by: a session the policy
	// withholds is not one they can reach with a longer prefix (#2401).
	pol := policy.Load()
	n := index.PrefixMatchesAllowed(dir, id, func(project string) bool {
		return pol.Allows(policy.ActivationSearch, project)
	})
	if n <= 1 {
		return
	}
	// When the matches are the same id in different harnesses there is no
	// longer prefix to reach for, and naming the harness is the only thing
	// that separates them (#719). The `harness:id` form is the one every
	// command that resolves a selector accepts — show/last also take
	// --harness, but promote/handoff/resume/share do not, so advising the
	// flag sent those readers into "unknown flag --harness".
	if hs := index.PrefixHarnesses(dir, id); len(hs) > 1 {
		forms := make([]string, len(hs))
		for i, h := range hs {
			forms[i] = h + ":" + id
		}
		fmt.Fprintf(os.Stderr, "deja: %d sessions share the id %q — %s the most recent; name one as %s\n",
			len(hs), id, action, strings.Join(forms, " or "))
		return
	}
	// "A longer prefix" is not available when the reader copied an elided id
	// off a result line: the characters that would disambiguate are the ones
	// the elision replaced (#859).
	advice := "use a longer prefix for another"
	if strings.Contains(id, "…") {
		advice = "the ids differ in the middle the line elides — `deja last` prints them whole"
	}
	fmt.Fprintf(os.Stderr, "deja: %d sessions match %q — %s the most recent; %s\n", n, id, action, advice)
}

func findByPrefix(dir, p string) (model.Session, bool, error) {
	if err := index.Ensure(dir, "", false, os.Stderr); err == nil {
		if s, ok, err := index.FindByPrefix(dir, p); err == nil {
			if ok {
				noteForgottenSource(s, p)
			}
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
			// The role has to travel into the index query, not just the filter
			// below: a scan with no role set drops file, command and edit
			// records on the way out — they are indexed but served only when
			// asked for — so `last --role files` saw sessions with the very
			// records it was selecting on already removed.
			ss, err := index.SearchWithRecovery(dir, search.Options{All: true, Role: o.Role}, io.Discard)
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
		case "--harness", "--project", "--since", "--role", "--from":
			if i+1 >= len(args) {
				return n, o, sinceRaw, fmt.Errorf("%s needs value", a)
			}
			i++
			v := args[i]
			// Empty is how "no filter" is spelled inside deja, so an empty
			// argument reached the search as no filter at all and a scripted
			// `--project "$PROJECT"` with the variable unset quietly returned
			// the whole store (#1612). It is the same mistake as leaving the
			// value off, so it gets the same sentence.
			if strings.TrimSpace(v) == "" {
				return n, o, sinceRaw, fmt.Errorf("%s needs value", a)
			}
			switch a {
			case "--harness":
				o.Harness = v
			case "--project":
				o.Project = v
			case "--role":
				o.Role = v
			case "--from":
				o.From = v
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
			// The only bare argument last takes is the count. Dropping anything
			// else in silence answered `deja last api-gateway` — the filter the
			// help spells `--project api-gateway` — with ten sessions from every
			// project and no sign the word did nothing (#1618).
			x, err := strconv.Atoi(a)
			if err != nil || x < 1 {
				return n, o, sinceRaw, fmt.Errorf("last: %q is not a count — use `deja last 5`, or --project/--harness to narrow", a)
			}
			if seenN {
				return n, o, sinceRaw, fmt.Errorf("last takes one count, got %d and %q", n, a)
			}
			n, seenN = x, true
		}
	}
	return n, o, sinceRaw, nil
}

func filterRecentSources(ss []model.Session, o search.Options) []model.Session {
	// These are sessions read straight off this machine's stores, so they are
	// local by definition: asking for another machine's work must not hand
	// back this one's.
	if o.From != "" && !strings.EqualFold(o.From, "local") {
		return nil
	}
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
		if a == "--" {
			// End of options: the rest is the query verbatim, even a word that
			// spells a flag. Without this there is no way to search for the
			// literal text of a flag name — `deja -- --json` kept parsing
			// --json as the flag and left "--" stranded in the query.
			q = append(q, args[i+1:]...)
			break
		}
		switch a {
		case "--json":
			o.JSON = true
		case "--re":
			o.Regex = true
		case "--all":
			o.All = true
		case "--no-embed":
			o.NoEmbed = true
		case "--harness", "--project", "--since", "--role", "--limit", "--session":
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs value", a)
			}
			i++
			v := args[i]
			// Empty is how "no filter" is spelled inside deja, so an empty
			// argument reached the search as no filter at all and a scripted
			// `--project "$PROJECT"` with the variable unset quietly returned
			// the whole store (#1612). It is the same mistake as leaving the
			// value off, so it gets the same sentence.
			if strings.TrimSpace(v) == "" {
				return o, fmt.Errorf("%s needs value", a)
			}
			switch a {
			case "--harness":
				o.Harness = v
			case "--project":
				o.Project = v
			case "--role":
				o.Role = v
			case "--session":
				o.Session = v
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
			// A flag deja takes elsewhere is not a typo and is nowhere near a
			// search flag by edit distance, so it went into the query and the
			// search that would have found everything reported nothing (#2249).
			if cmd := flagsOfOtherCommands[a]; cmd != "" {
				return o, fmt.Errorf("%s is a flag of `deja %s`, not of search — put it after `--` to search for the text", a, cmd)
			}
			q = append(q, a)
		}
	}
	// Match the NFC canonicalisation ingest applies to stored text, so a query
	// typed in either normalisation reaches the same records (#1098). Trim
	// surrounding space: a leading space left the exact-match tier hunting for
	// " token" and an exact-only term (an error code, a coined name) missed,
	// while a real word was rescued by its word-forms and hid the gap.
	o.Query = nfcfold.Compose(strings.TrimSpace(strings.Join(q, " ")))
	if o.Query == "" {
		return o, fmt.Errorf("query required")
	}
	return o, nil
}

// flagsOfOtherCommands names the command each flag belongs to, for the tokens
// that are real deja flags somewhere but not here. Only exact matches: a query
// may legitimately start with a dash, and `--` still ends option parsing.
var flagsOfOtherCommands = map[string]string{
	"--offset":      "show",
	"--span":        "restore",
	"--out":         "restore",
	"--force":       "restore",
	"--tag":         "remember",
	"--deep":        "doctor",
	"--offline":     "doctor",
	"--dry-run":     "forget",
	"--all-matches": "forget",
	"--list":        "forget",
	"--unforget":    "forget",
	"--before":      "forget",
	"--to":          "handoff",
	"--exec":        "resume",
	"--plain":       "hook-prompt",
	"--no-open":     "view",
	"--full":        "sync export",
	"--peer":        "sync export",
	"--pull":        "sync ssh",
	"--both":        "sync ssh",
	"--from":        "last",
	"--last":        "log",
	"--seed":        "bench",
}

// searchFlags is every flag the bare search form accepts, for the typo check.
var searchFlags = []string{
	"--json", "--re", "--all", "--no-embed", "--rebuild",
	"--harness", "--project", "--since", "--role", "--limit", "--session",
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
			if strings.TrimSpace(args[i]) == "" {
				// Empty is how "no filter" is spelled inside deja (#1612).
				return "", o, false, fmt.Errorf("%s needs value", a)
			}
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
	// A typo'd harness must name the mistake, not read as "nobody touched this
	// file under harness X" — the same reason search and the MCP blame validate
	// it (#1113). blame has no --role to check.
	if err := checkHarness(&o.Harness); err != nil {
		return err
	}
	target, err := search.ResolveBlamePath(path)
	if err != nil {
		return err
	}
	hits, hidden, total, err := findBlameHits(dir, target, o, policy.ActivationSearch, os.Stderr)
	if err != nil {
		return fmt.Errorf("blame search: %w", err)
	}
	if jsonOutput {
		search.PrintBlame(os.Stdout, hits, true)
		return nil
	}
	if len(hits) == 0 {
		// The file was edited — a rule withheld the session that touched it.
		// "no sessions mention it" reads as looked-and-absent, the misread
		// search and last already avoid by naming the rule (#686, #680).
		if note := policyHiddenNote(policy.ActivationSearch, hidden); note != "" {
			fmt.Fprint(os.Stderr, note)
			return nil
		}
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
	// blame answers "who touched this file". Ten blocks and nothing else reads
	// as all of them, the misread search already avoids with the same sentence
	// (#2299). --json keeps its bare array: a machine consumer reading a total
	// would need a different shape, and that is a break, not a note.
	if total > len(hits) {
		fmt.Fprintf(os.Stderr, "deja: showing %d of %d — add --all to see the rest\n", len(hits), total)
	}
	return nil
}

func findBlameHits(dir string, target search.BlameTarget, o search.BlameOptions, activation string, progress io.Writer) ([]search.BlameHit, int, int, error) {
	hits, hidden, total, _, err := blameHits(dir, target, o, activation, progress, false)
	return hits, hidden, total, err
}

// findBlameHitsStale is findBlameHits for a caller that must not wait: it
// serves the snapshot on disk while a rebuild runs, the way recall does, and
// hands the rebuild to the detached warmup. blame is the tool an agent calls
// before editing a file, so declining for the length of a refresh means the
// edit happens without the history (#1784). The CLI keeps the blocking path —
// someone typed it and is watching the progress (#1306).
func findBlameHitsStale(dir string, target search.BlameTarget, o search.BlameOptions, activation string, progress io.Writer) ([]search.BlameHit, int, int, bool, error) {
	return blameHits(dir, target, o, activation, progress, true)
}

func blameHits(dir string, target search.BlameTarget, o search.BlameOptions, activation string, progress io.Writer, stale bool) ([]search.BlameHit, int, int, bool, error) {
	query := search.Options{Query: target.Stem, Harness: o.Harness, Project: o.Project, Since: o.Since, All: true}
	refreshing := false
	if stale {
		var err error
		if refreshing, err = index.EnsureForSearchStale(dir, query, progress); err != nil {
			return nil, 0, 0, false, err
		} else if refreshing {
			requestWarmup(dir)
		}
	} else if err := index.EnsureForSearch(dir, query, false, progress); err != nil {
		return nil, 0, 0, false, err
	}
	result, err := index.SearchWithRecoveryDetailed(dir, query, progress)
	if err != nil {
		return nil, 0, 0, false, err
	}
	all := search.Blame(withFileTouchers(dir, result.Sessions, target), target, o)
	visible := policyFilterBlame(activation, all)
	// Cap after the trust filter, not before: cutting first meant a withheld
	// session ate one of the ten slots, so a reader could get eight hits with
	// forty sessions still waiting behind --all.
	hits := search.CapBlame(visible, o)
	attachBlameLifecycles(hits)
	return hits, len(all) - len(visible), len(visible), refreshing, nil
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
	// Pick the identities first and read them in one pass: per-identity reads
	// stream the whole record log each time, so the cap alone did not bound
	// the cost — 50 sessions meant 50 scans (#1069).
	var want []index.Identity
	for _, meta := range metas {
		// A file like main.go is touched by everything, and each addition is a
		// record read. Ten hits are printed; this is far more than enough to
		// rank them.
		if len(want) >= blameToucherCap {
			break
		}
		for _, p := range meta.Touched {
			if strings.ToLower(filepath.Base(filepath.FromSlash(p))) != base {
				continue
			}
			want = append(want, index.Identity{Harness: meta.Harness, ID: meta.ID})
			break
		}
	}
	full, err := index.FindManyByIdentity(dir, want)
	if err != nil {
		return ss
	}
	for _, s := range full {
		// The manifest is keyed by identity, so each session reaches this once
		// — there is no second visit to record a position for.
		if i, present := at[s.Harness+":"+s.ID]; present {
			ss[i] = s
		} else {
			ss = append(ss, s)
		}
	}
	return ss
}

// parseDur is the window a reader asked to look back over, so it has to be one:
// zero or less used to parse and then disappear, because every caller cuts on
// `Since > 0` — `--since -1d` searched the whole store and nothing in the answer
// said the filter had been dropped (#1610). parseDurAny is the raw form, for
// `forget --before`, which has its own word for the same mistake.
func parseDur(s string) (time.Duration, error) {
	d, err := parseDurAny(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q is not a window — --since counts back from now, so it needs a positive duration like 30d", s)
	}
	return d, nil
}

// maxDurationDays is how many whole days fit in a time.Duration: past it the
// multiplication wraps, and `--since 365000d` — the way to say "all of it" —
// came out negative and dropped the filter it was asked for.
const maxDurationDays = int(math.MaxInt64 / (24 * int64(time.Hour)))

func parseDurAny(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, durationError(s)
		}
		if n > maxDurationDays {
			return time.Duration(math.MaxInt64), nil
		}
		if n < -maxDurationDays {
			return time.Duration(math.MinInt64), nil
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

// presentFiles is one path if it exists, nothing otherwise: a store deja names
// but does not have should weigh nothing rather than the directory around it.
func presentFiles(paths ...string) []string {
	var out []string
	for _, p := range paths {
		if p == "" {
			continue
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func cursorReadFiles() []string {
	return append(sources.CursorTranscripts(), sources.CursorDBs()...)
}

// filesSize sums what the parser would open, counting each path once: cursor
// and hermes list a file under more than one discovery rule.
func filesSize(paths []string) int64 {
	seen := map[string]bool{}
	var total int64
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			total += fi.Size()
		}
	}
	return total
}

// commonDir is the deepest directory holding every path, which is the answer to
// "where is my history" — the configured root can be a parent of it, or, when a
// harness keeps its store somewhere else entirely, not contain it at all.
func commonDir(paths []string) string {
	dir := ""
	for _, p := range paths {
		if p == "" {
			continue
		}
		d := filepath.Dir(p)
		if dir == "" {
			dir = d
			continue
		}
		for dir != d && !strings.HasPrefix(d+string(filepath.Separator), dir+string(filepath.Separator)) {
			parent := filepath.Dir(dir)
			if parent == dir {
				return dir
			}
			dir = parent
		}
	}
	return dir
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
	// files names what the parser opens. The screen answers "where is my history
	// and how much of it is there", and roots are where deja looks rather than
	// what it reads: a codex root reported 108 MB of plugins, caches and sqlite
	// WALs around 1.3 MB of transcripts, and hermes named a directory holding
	// one crash log while the store it reads sat elsewhere (#654).
	items := []struct {
		name, location string
		roots          []string
		files          func() []string
		load           func() []model.Session
	}{
		{"claude", sources.ClaudeRoot(), []string{sources.ClaudeRoot()}, sources.ClaudeFiles, sources.LoadClaude},
		{"codex", sources.CodexRoot(), []string{sources.CodexRoot()}, sources.CodexFiles, sources.LoadCodex},
		{"gemini", sources.GeminiRoot(), []string{filepath.Join(sources.GeminiRoot(), "tmp")}, sources.GeminiChatFiles, sources.LoadGemini},
		{"cursor", strings.Join([]string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, string(os.PathListSeparator)), []string{sources.CursorUserRoot(), sources.CursorCLIRoot()}, cursorReadFiles, sources.LoadCursor},
		{"antigravity", antigravityLocation, antigravityRoots, sources.AntigravityTranscripts, sources.LoadAntigravity},
		{"grok", sources.GrokRoot(), []string{sources.GrokRoot()}, sources.GrokSessionFiles, sources.LoadGrok},
		{"qwen", filepath.Join(sources.QwenRoot(), "projects"), []string{filepath.Join(sources.QwenRoot(), "projects")}, sources.QwenSessionFiles, sources.LoadQwen},
		{"kimi", filepath.Join(sources.KimiRoot(), "sessions"), []string{filepath.Join(sources.KimiRoot(), "sessions")}, sources.KimiSessionFiles, sources.LoadKimi},
		{"goose", filepath.Join(sources.GooseRoot(), "sessions"), []string{filepath.Join(sources.GooseRoot(), "sessions")}, sources.GooseSessionFiles, sources.LoadGoose},
		{"hermes", sources.HermesProfilesRoot(), []string{sources.HermesProfilesRoot()}, sources.HermesSessionFiles, sources.LoadHermes},
		{"copilot", sources.CopilotRoot(), []string{sources.CopilotRoot()}, sources.CopilotSessionFiles, sources.LoadCopilot},
		{"cline", sources.ClineSessionsDir(), append([]string{sources.ClineSessionsDir()}, sources.ClineLegacyRoots()...), sources.ClineSessionFiles, sources.LoadCline},
		{"roo", strings.Join(sources.RooRoots(), string(os.PathListSeparator)), sources.RooRoots(), sources.RooTaskFiles, sources.LoadRoo},
		{"pi", sources.PiRoot(), []string{sources.PiRoot()}, sources.PiSessionFiles, sources.LoadPi},
		{"omp", sources.OmpRoot(), []string{sources.OmpRoot()}, sources.OmpSessionFiles, sources.LoadOmp},
		{"openclaw", sources.OpenClawRoot(), []string{sources.OpenClawRoot()}, sources.OpenClawSessionFiles, sources.LoadOpenClaw},
		{"deepseek", sources.DeepSeekRoot(), []string{sources.DeepSeekRoot()}, sources.DeepSeekSessionFiles, sources.LoadDeepSeek},
		{"zed", sources.ZedDB(), []string{sources.ZedDB()}, func() []string { return presentFiles(sources.ZedDB()) }, sources.LoadZed},
		{"deja", sources.NotesFile(), []string{sources.NotesFile()}, func() []string { return presentFiles(sources.NotesFile()) }, sources.LoadNotes},
	}
	for _, it := range items {
		redacted := 0
		for _, root := range it.roots {
			redacted += redactionsUnder(redactions, root)
		}
		// Only paths that are files on this machine: a discovery rule can name
		// something that is not one — hermes returns a token for a Postgres
		// store — and neither a size nor a directory can be read off that.
		read := presentFiles(it.files()...)
		size := filesSize(read)
		location := it.location
		if where := commonDir(read); where != "" {
			location = where
		}
		raw := it.load()
		ss := sources.FilterSessions(raw)
		excluded := len(raw) - len(ss)
		msg := 0
		for _, s := range ss {
			msg += len(s.Messages)
		}
		note := ""
		// `deja sources` is where the empty-machine advice sends people, and a
		// store deja is not allowed to read looked exactly like one nobody has
		// used: `sessions=0 messages=0 size=0 B` (#1000).
		if denied, whole := firstDeniedDir(it.roots); denied != "" {
			note = "\t(cannot be read — permission denied on " + denied + ")"
		} else if !whole {
			note = "\t(permissions not fully checked — too many directories to walk)"
		}
		if it.name == "cursor" && len(sources.CursorDBs()) > 0 && !sources.SQLite3Available() {
			note = "\t(sqlite3 CLI not found — Cursor IDE sessions unavailable)"
		}
		// Zed needs zstd as well as sqlite3: sqlite3 alone opens the store and
		// reads nothing out of it, since every thread body is a compressed
		// frame. SkipReason already words which of the two is missing.
		if it.name == "zed" {
			if reason := sources.SkipReason("zed"); reason != "" {
				note = "\t(" + reason + " — Zed threads unavailable)"
			}
		}
		if n := len(sources.ExclusionPatterns()); n > 0 {
			note += fmt.Sprintf("\texcluded-patterns=%d", n)
		}
		if excluded > 0 {
			note += fmt.Sprintf("\texcluded-sessions=%d", excluded)
		}
		fmt.Printf("%s\t%s\tsessions=%d messages=%d size=%s redacted=%d%s\n", it.name, location, sources.CountSessions(ss), msg, humanBytes(size), redacted, note)
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
	fmt.Printf("aider\t%s\tsessions=%d messages=%d size=%s redacted=%d%s\n", aiderLocation, sources.CountSessions(aiderSessions), aiderMessages, humanBytes(aiderSize), aiderRedactions, note)
	var size int64
	if fi, err := os.Stat(sources.OpencodeDB()); err == nil {
		size = fi.Size()
	}
	s, m, _ := sources.OpencodeCounts()
	// The counts come out of sqlite, which knows nothing about the exclude
	// list, so with a pattern in force this row kept reporting sessions that
	// are not indexed, not searchable and not exported while every other row
	// subtracted them (#2247). Only then is the store loaded: counting by SQL
	// is why this row is cheap on a large database.
	opencodeExcluded := 0
	if len(sources.ExclusionPatterns()) > 0 {
		raw := sources.LoadOpencode()
		kept := sources.FilterSessions(raw)
		opencodeExcluded = len(raw) - len(kept)
		// Subtracted from the SQL numbers rather than recounted from what
		// loaded: the loader drops a session holding no text at all, which the
		// row has always counted, so recounting would move the numbers for a
		// reason that has nothing to do with the exclude list.
		dropped := 0
		for _, x := range raw {
			if sources.ExcludedProject(x.Project) {
				dropped += len(x.Messages)
			}
		}
		s, m = max(0, s-opencodeExcluded), max(0, m-dropped)
	}
	note = ""
	if size > 0 && !sources.SQLite3Available() {
		note = "\t(sqlite3 CLI not found — opencode sessions unavailable)"
	}
	if n := len(sources.ExclusionPatterns()); n > 0 {
		note += fmt.Sprintf("\texcluded-patterns=%d", n)
	}
	if opencodeExcluded > 0 {
		note += fmt.Sprintf("\texcluded-sessions=%d", opencodeExcluded)
	}
	fmt.Printf("opencode\t%s\tsessions=%d messages=%d size=%s redacted=%d%s\n", sources.OpencodeDB(), s, m, humanBytes(size), redactions[sources.OpencodeDB()], note)
}

// forgetScopeRefusal stops a destructive run whose selector reaches further
// than the reader can have meant.
//
// `--session` takes a prefix, which is documented and useful for a day's ids —
// but it also let one exact-looking id drop twelve sessions, and the count
// arrived in the past tense afterwards (#870). An id copied off a result line
// is worse still: the characters that tell the sessions apart are the ones the
// line elided, so no longer prefix exists to reach for (#859).
func forgetScopeRefusal(selector string, matches int, allMatches bool) error {
	if matches <= 1 || allMatches {
		return nil
	}
	if strings.Contains(selector, "…") {
		return fmt.Errorf("%q matches %d sessions — the ids differ in the middle the line elides; `deja last` prints them whole", selector, matches)
	}
	return fmt.Errorf("%q is a prefix of %d sessions — `deja forget --session %s --dry-run` lists what would go; add --all-matches to drop them all", selector, matches, selector)
}

func runForget(dir string, args []string) error {
	var o index.ForgetOptions
	list := false
	allMatches := false
	unforget, unforgetGiven := "", false
	given := map[string]bool{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--list":
			list = true
		case "--dry-run":
			o.DryRun = true
		case "--all-matches":
			allMatches = true
		case "--session", "--project", "--before", "--unforget":
			// Given twice, the last one used to win in silence: `deja forget
			// --session a --session b` deleted b, reported one session and
			// left a behind, which the reader believes is gone (#2271).
			// stats already refuses this shape for its own flags.
			if given[args[i]] {
				return fmt.Errorf("forget: %s specified twice", args[i])
			}
			given[args[i]] = true
			if i+1 >= len(args) {
				return fmt.Errorf("forget: %s needs a value", args[i])
			}
			i++
			// Empty is how "no filter" is spelled inside deja, and here it
			// would widen a deletion rather than a search (#1612). --unforget
			// keeps its own refusal, which asks for an id rather than offering
			// the flags that forget instead of restoring.
			if strings.TrimSpace(args[i]) == "" && args[i-1] != "--unforget" {
				return fmt.Errorf("forget: %s needs a value", args[i-1])
			}
			switch args[i-1] {
			case "--session":
				o.Session = index.PastedSelector(args[i])
			case "--project":
				o.Project = args[i]
			case "--unforget":
				unforget, unforgetGiven = index.PastedSelector(args[i]), true
			case "--before":
				if d, err := parseDurAny(args[i]); err == nil {
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
	// An empty `--unforget` was answered with the selectors for forgetting:
	// the reader asked to bring something back and was told how to drop more
	// (#1041).
	if unforgetGiven && unforget == "" {
		return fmt.Errorf("forget: --unforget needs an id — `deja forget --list` names the ids")
	}
	if !list && unforget == "" && o.Session == "" && o.Project == "" && o.Before.IsZero() {
		// Naming the selectors here is what separates one call from hundreds:
		// forgetting 100 sessions one id at a time took 10.5s against 0.2s for
		// `--project`, and nothing on this path said the second form exists
		// (#1022).
		return fmt.Errorf("forget: selector required — `--session <id>`, `--project <name>` or `--before <date>`; `--dry-run` shows what would go, `--list` what already went")
	}
	if list {
		keys := index.Tombstones()
		for _, key := range keys {
			fmt.Fprintln(os.Stdout, key)
		}
		// The list is where someone who dropped more than they meant to lands,
		// and it named no way back: `--unforget` lived in `deja help` only, and
		// the hint for a guessed `deja unforget` pointed at this same list
		// (#919).
		if len(keys) == 0 {
			// Silence here is the one answer a reader cannot act on: it looks
			// the same as a command that did not run, and every other empty
			// result says so out loud (#1040). On stderr, so a pipe still
			// counts only ids.
			fmt.Fprintln(os.Stderr, "deja: nothing is forgotten on this machine")
		}
		if len(keys) > 0 {
			fmt.Fprintf(os.Stderr, "deja: `deja forget --unforget %s` brings one back and rebuilds the index\n", keys[0])
		}
		return nil
	}
	if unforget != "" {
		// The destructive direction refuses a prefix that reaches more than one
		// session; the undo restored them all and said so afterwards, while the
		// hint printed beside the list promises one (#961).
		if n := index.TombstoneMatches(unforget); n > 1 && !allMatches {
			// "`deja forget --list` names them" sends the reader to a list of
			// everything this machine ever forgot, where the three that match
			// are theirs to find. Name them here instead (#1014).
			return fmt.Errorf("%q brings back %d forgotten sessions (%s) — add --all-matches to restore them all, or name one",
				unforget, n, joinCapped(index.TombstonesMatching(unforget), 5))
		}
		var lifted int
		// The ids to name afterwards are the tombstones this call lifts, read
		// before it lifts them — after the rebuild the manifest holds every
		// session the selector matches, restored or never forgotten (#1095).
		lifting := index.TombstonesMatching(unforget)
		if o.DryRun {
			// --dry-run on the undo brought the session back anyway: the
			// destructive side runs a dry probe and never touches disk, but
			// this path went straight to Unforget, which lifts the tombstone
			// and rebuilds. Name what it would restore and stop (#1066).
			if len(lifting) == 0 {
				return fmt.Errorf("no tombstone matches %q — `deja forget --list` shows what is forgotten", unforget)
			}
			fmt.Fprintf(os.Stdout, "dry run — nothing was changed\nwould restore %d tombstone%s and rebuild the index — %s\n",
				len(lifting), pluralS(len(lifting)), joinCapped(lifting, 5))
			return nil
		}
		if err := withBuildProgress(func() error {
			var err error
			lifted, err = index.Unforget(dir, unforget, os.Stderr)
			return err
		}); err != nil {
			return err
		}
		if lifted == 0 {
			// A restore that restored nothing used to exit 0, so a script
			// could not tell it from a restore that worked: the typo, the id
			// that was never forgotten and the session still gone all read as
			// success (#2263). Every neighbour refuses what it cannot find.
			return fmt.Errorf("no tombstone matches %q — `deja forget --list` shows what is forgotten", unforget)
		}
		// Lifting a tombstone brings a session back only if the transcript is
		// still on this machine. An imported one lives only in the index, so
		// forget took the last copy — and the undo reported a restore that did
		// not happen (#967).
		back, gone, names, missing := restoredSessions(dir, unforget, lifted, lifting)
		restorePromotedTitles(dir, unforget)
		if back > 0 {
			// The ids, not just the count: this is the moment someone checks
			// that they got back exactly what they lost, and both the list and
			// the ambiguity refusal name them a step earlier (#1095, #1014).
			fmt.Fprintf(os.Stdout, "restored %d session%s and rebuilt the index — %s\n", back, pluralS(back), joinCapped(names, 5))
		}
		for _, line := range unforgetGoneLines(missing) {
			fmt.Fprintln(os.Stdout, line)
		}
		if gone > 0 && len(missing) == 0 {
			// The selector was a prefix rather than whole keys, so which ones
			// did not come back is not known by name here.
			fmt.Fprintf(os.Stdout, "%d of them did not come back — the tombstone is lifted, but the records are not in the index; `deja forget --list` shows what is left\n", gone)
		}
		return nil
	}
	// The scope check runs on a dry pass first: a refusal printed after
	// index.Forget has already written the tombstones is not a refusal.
	//
	// The same pass answers what is about to go: after the real Forget the rows
	// are out of the manifest, so a row that covered two conversations can no
	// longer be recognised (#970).
	shared := 0
	if !o.DryRun {
		probe := o
		probe.DryRun = true
		pr, perr := index.Forget(dir, probe)
		if perr != nil {
			// The probe fails the same ways the real pass does — a vanished
			// volume, a read-only cache — and handing its syscall back was the
			// shape #798 replaced everywhere else.
			return ensureError(dir, perr)
		}
		if o.Session != "" {
			if err := forgetScopeRefusal(o.Session, pr.Sessions, allMatches); err != nil {
				return err
			}
		}
		shared = sharedRowsAmong(dir, pr.Keys)
	}
	result, err := index.Forget(dir, o)
	if err != nil {
		// The tombstone is already written; what failed is the rebuild that
		// takes the records out. Handing back `mkdir /…/idx.tmp: permission
		// denied` names an internal path and leaves the reader believing the
		// session is gone while search still returns it (#976).
		if !o.DryRun && index.Tombstoned(forgetKeyOf(dir, o)) {
			return fmt.Errorf("%v\nthe session is marked forgotten and is still in the index until it can be rebuilt — search keeps returning it until then", ensureError(dir, err))
		}
		return ensureError(dir, err)
	}
	if o.DryRun {
		// The dry run is where someone checks the scope, so it says the same
		// thing the real run would refuse with rather than erroring: nothing
		// is being changed here, and the note is the answer they came for.
		// It goes above the counts, and the counts say whose run they are:
		// under an ambiguous selector the numbers were those of
		// `--all-matches`, while the command as typed drops nothing (#1032).
		scope := error(nil)
		if o.Session != "" {
			scope = forgetScopeRefusal(o.Session, result.Sessions, allMatches)
		}
		if scope != nil {
			fmt.Fprintln(os.Stdout, scope.Error())
			fmt.Fprintf(os.Stdout, "dry run — nothing was changed\nas it stands this run drops nothing; with --all-matches it would drop: %d session(s), %d message(s)\nwould add: %d tombstone(s)\n",
				result.Sessions, result.Messages, result.Tombstones)
		} else {
			fmt.Fprintf(os.Stdout, "dry run — nothing was changed\nwould drop: %d session(s), %d message(s)\nwould add: %d tombstone(s)\n",
				result.Sessions, result.Messages, result.Tombstones)
		}
		if line := forgetNotesLine(result); line != "" {
			fmt.Fprintln(os.Stdout, line)
		}
		if n := usage.CountSnapshots(dir, forgetDigestMatcher(o, result.Keys)); n > 0 {
			fmt.Fprintf(os.Stdout, "would remove: %d stored digest(s) from the injection log\n", n)
		}
		// Two transcripts can write the same harness:id, and then one manifest
		// row holds both conversations. The build says so once (#698); forget
		// said "1 session" and took two, from two projects (#970).
		if o.DryRun {
			shared = sharedRowsAmong(dir, result.Keys)
		}
		if n := shared; n > 0 {
			fmt.Fprintf(os.Stdout, "%s covered more than one conversation — transcripts that share an id are filed under one row, and both go\n",
				doctorCount(n, "of them"))
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
		// Only when the reader named a session: a note swept up by --project or
		// --before is a decision they deliberately kept, and destroying it is
		// what #690 exists to prevent. Naming the note's own id is the one
		// case where "forget this" can only mean its text (#841).
		if o.Session != "" {
			if gone, err := sources.ForgetPromotedNotes(func(noteID string) bool {
				return dropped["deja:"+noteID]
			}); err != nil {
				fmt.Fprintf(os.Stdout, "could not remove %s from %s: %v\n", pluralNote(result.Notes), sources.NotesFile(), err)
			} else if gone > 0 {
				fmt.Fprintf(os.Stdout, "removed %d promoted note%s from %s\n", gone, pluralS(gone), sources.NotesFile())
			}
		}
		// The digests those sessions were served in are content too, and the
		// page publishes them: forget already reaches the note log for the
		// same reason (#690, #841). The usage log stays — an event is what
		// happened, not what was said (#2325).
		if gone, err := usage.ForgetSnapshots(dir, forgetDigestMatcher(o, result.Keys)); err != nil {
			fmt.Fprintf(os.Stdout, "could not remove stored digests from the injection log: %v\n", err)
		} else if gone > 0 {
			fmt.Fprintf(os.Stdout, "removed %d stored digest%s from the injection log\n", gone, pluralS(gone))
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
			// The note keeps the decision it was promoted for — often the
			// reason the raw session was safe to forget (#666) — so say that
			// its content is still there rather than let the line read as
			// "the note was handled" (#841).
			// The ids, not an ellipsis: this line knows which notes it just
			// cleared, and `deja forget --session deja-note-…` matched
			// nothing — it sent the reader to `deja last` to look up
			// something the command was already holding (#1030).
			what := "it"
			if n > 1 {
				what = "them"
			}
			fmt.Fprintf(os.Stdout, "cleared the borrowed title from %d promoted note%s; %s still holds what you wrote there — `deja forget --session %s` removes %s\n",
				n, pluralS(n), sources.NotesFile(), clearedNoteIDs(result.Keys), what)
		}
	}
	// Nothing matched is a different answer from nothing was dropped: the
	// first means the selector found no session, and reporting it as a
	// successful removal of zero leaves the reader believing they deleted
	// something that is in fact still there under a different id.
	if result.Sessions == 0 && result.Messages == 0 {
		// A note whose own project was forgotten keeps its line in
		// notes.jsonl while its index row is gone, and the id the forget
		// line offers then matched nothing at all — the file, not the
		// index, is what holds a note (#1030).
		if o.Session != "" {
			if gone, err := sources.ForgetPromotedNotes(func(noteID string) bool {
				return noteID == strings.TrimPrefix(o.Session, "deja:")
			}); err == nil && gone > 0 {
				fmt.Fprintf(os.Stdout, "removed %d promoted note%s from %s\n", gone, pluralS(gone), sources.NotesFile())
				return nil
			}
		}
		fmt.Fprintf(os.Stdout, "nothing matched %s — no session was dropped%s\n", forgetSelector(o), movedBucketHint(dir, o.Session))
		return nil
	}
	fmt.Fprintf(os.Stdout, "sessions dropped: %d\nmessages dropped: %d\ntombstones added: %d\n", result.Sessions, result.Messages, result.Tombstones)
	// "1 session" can be two conversations: transcripts that share an id are
	// filed under one row, and the message count is the only tell (#970).
	if shared > 0 {
		fmt.Fprintf(os.Stdout, "%s covered more than one conversation — transcripts that share an id are filed under one row, and both went\n",
			doctorCount(shared, "of them"))
	}
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

// wrapTargets lays install target names out over as many indented lines as they
// need. Naming them inline in the usage text is what let help drift: eleven of
// the thirty-one were listed, so `deja install aider` and `deja install
// openclaw-auto` — both of them in the README — read as invalid (#1106).
func wrapTargets(names []string, indent string, width int) string {
	var b strings.Builder
	line := indent
	for i, n := range names {
		piece := n
		if i < len(names)-1 {
			piece += ","
		}
		if len(line) > len(indent) && len(line)+1+len(piece) > width {
			b.WriteString(line + "\n")
			line = indent
		}
		if len(line) > len(indent) {
			line += " "
		}
		line += piece
	}
	return b.String() + line
}

// helpHidden names the commands deliberately kept out of `deja help`. They are
// wired by `deja install` and read by a harness, not typed by a person: the
// session-start and compaction hooks, goose's two, the refresh worker and the
// warmup probe. The list exists so the omission is a decision with a reason
// rather than drift — help listed five hook commands and hid five, including
// hook-context, because the text is hand-maintained and nothing compared it
// with the dispatch table (#1654). TestHelpCoversEveryCommand is that
// comparison.
var helpHidden = map[string]bool{
	"help":              true,
	"hook-context":      true,
	"hook-goose":        true,
	"hook-goose-prompt": true,
	"hook-precompact":   true,
	"hook-refresh":      true,
	"warmup-status":     true,
}

func printUsage() {
	fmt.Print(usageText())
}

// usageText renders the usage block so `--help` on a single command can quote
// the lines that belong to it instead of the whole page.
func usageText() string {
	return fmt.Sprintf(`deja - persistent memory for coding agents

Usage:
  deja [flags] <query>
  deja search [flags] <query>   (same, but a query may start with a dash)
  deja show <id-prefix> [--json --harness name] [--offset n] [--limit n]
  deja share <id-prefix>
  deja resume <id-prefix> [--exec]
  deja handoff [--to <agent>] [id-prefix] [--exec]
  deja hook-prompt [--plain]  (UserPromptSubmit hook: relevance recall per prompt)
  deja hook-antigravity (Antigravity PreInvocation hook: inject on first turn)
  deja hook-plan     (PreToolUse ExitPlanMode hook: factual plan/history co-occurrences)
  deja hook-tool     (PreToolUse Bash/Edit hook: one line on what this command or file already has)
  deja hook-tool-after  (PostToolUse Bash hook: the command that followed this error before)
  deja check -       (read a plan from stdin and print factual co-occurrences)
  deja view [--no-open]  (browse your memory: sessions, recalls, notes — one local HTML)
  deja ctx <query|id-prefix>
  deja blame <path> [--all] [--json] [--project name] [--harness name] [--since 30d]
  deja files <topic> [--project name] [--limit n]
  deja restore <path> [--span n] [-o|--out file] [--force]
  deja friction [--limit n]
  deja fix "<error text>" [--limit n]  (what was run after this error before)
  deja how <what> [--project name] [--limit n]  (commands this machine actually ran)
  deja sync export <dir> [--full] [--peer name]
  deja sync import <dir>
  deja sync                       (exchange with every machine deja knows, both ways)
  deja sync ssh <host> [--pull] [--both] [--full]
  deja sync forget <host>
  deja last [n] [--json] [--project name] [--harness name] [--from machine|local] [--since duration] [--role user|assistant|tool|files|command|edit]
  deja sources
  deja completion <bash|zsh|fish|powershell>
  deja forget --session <id-prefix> [--project <substring>] [--before <duration|date>] [--dry-run] [--all-matches]
  deja forget --list | --unforget <id>
  deja doctor [--json] [--deep] [--offline]
  deja warmup
  deja index [--rebuild]
  deja embed
  deja bench recall|context|prompt [--json] [--seed n]
  deja brief         (the screen a bare deja prints on a terminal)
  deja log [n] [--last] [--json]
  deja statusline
  deja stats [--json] [--impact] [--redaction] [--card [path]] [--html [path]]
  deja remember "text" [--project name] [--tag name]
  deja promote <id-prefix> [--state accepted|rejected|superseded|stale] [--note "text"] [--tag name] [--to path]
  deja mcp
  deja version
  deja <command> --help
  deja update [--force]
  deja install <target> | --all | --auto  [--no-guidance] [--no-index]
  deja uninstall <target> | --all | --auto
    targets:
%s

Search flags (the bare "deja [flags] <query>" form above):
  --harness <name>              only sessions from one harness (claude, codex, ...)
  --project <name>              only sessions from one project
  --since <duration>            only sessions newer than e.g. 30d, 12h
  --role <name>                 only match turns from one role: user, assistant,
                                tool (tool output), files, command, edit
  --session <id>                only one session, by the id a hit prints
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
  deja --session 01a00feb --role tool "go build"   (what ran inside one session)
  deja --re "timeout|deadline exceeded"
  deja ctx "schema migration rollback" > deja-context.md
  deja install --all

See README.md for the full CLI reference.
`, wrapTargets(installTargetNames(), "      ", usageWidth()))
}

// usageWidth is the column budget for the one block in help that is laid out
// rather than typed. It follows the terminal, as the brief, files, restore and
// search already do — the list was computed to a fixed 76 and so came out the
// same six lines on a 30-column pane and a 120-column one (#1660).
//
// Off a terminal printableWidth answers 0, and the fixed 76 stands: piped and
// redirected help keeps the byte-identical output that goldens and CI compare.
func usageWidth() int {
	if w := printableWidth(os.Stdout); w > 0 {
		return w
	}
	return 76
}

// helpForCommand answers `deja <cmd> --help`. Every command rejected it as an
// unknown flag, and a couple did worse: `deja statusline --help` printed a
// statusline and `deja mcp --help` started the server and hung the terminal
// (#1111).
func helpForCommand(name string) string {
	var out []string
	usage := usageText()
	if i := strings.Index(usage, "\nUsage:\n"); i >= 0 {
		usage = usage[i+len("\nUsage:\n"):]
	}
	if i := strings.Index(usage, "\nExamples:\n"); i >= 0 {
		usage = usage[:i]
	}
	// A usage line can carry indented continuations under it — the install
	// target list sits under the install/uninstall pair — so a match keeps
	// collecting until the next "deja …" line that does not match.
	matched := false
	for _, line := range strings.Split(usage, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "deja "+name || strings.HasPrefix(t, "deja "+name+" "):
			out = append(out, line)
			matched = true
		case matched && t != "" && strings.HasPrefix(line, "    ") && !strings.HasPrefix(t, "deja "):
			out = append(out, line)
		case matched && strings.HasPrefix(t, "deja "):
			// install and uninstall share one target list, printed under the
			// second of the pair; every other command's help ends at the next
			// command line.
			pair := name == "install" || name == "uninstall"
			matched = pair && (strings.HasPrefix(t, "deja install ") || strings.HasPrefix(t, "deja uninstall "))
		default:
			matched = false
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\nSee `deja help` for every command and flag.\n"
}

// wantsHelp reports whether a command line asks for help rather than work.
func wantsHelp(rest []string) bool {
	for _, a := range rest {
		if a == "--help" || a == "-h" {
			return true
		}
		if a == "--" {
			return false
		}
	}
	return false
}

// movedBucketHint explains a note-bucket id that stopped resolving. The id
// carries the day it was minted in, so a machine that changed zone renames its
// buckets on the next build — and every id refusal then read as "that note is
// gone" while the note sat under the neighbouring day (#1039).
func movedBucketHint(dir, id string) string {
	if !strings.HasPrefix(id, "deja-") || len(id) < 15 {
		return ""
	}
	day, rest := id[5:15], id[15:]
	when, err := time.Parse("2006-01-02", day)
	if err != nil {
		return ""
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		return ""
	}
	pol := policy.Load()
	for _, shift := range []int{-1, 1} {
		want := "deja-" + when.AddDate(0, 0, shift).Format("2006-01-02") + rest
		for _, m := range metas {
			if m.ID != want {
				continue
			}
			// Naming it is recalling it: a rule that hides the session hides
			// the fact that it moved too, or the hint becomes the way around
			// the rule (#1043).
			if !pol.Allows(policy.ActivationSearch, m.Project) {
				return ""
			}
			return fmt.Sprintf(" — the days regrouped when this machine's zone changed; it is `%s` now", want)
		}
	}
	return ""
}

// showNeedsID is the refusal show gives with no argument. "id-prefix" names a
// thing the reader has no way to produce on their own; promote has pointed at
// `deja last` all along, and show, share and resume are the three commands
// reached for right after a search result (#1063).
const showNeedsID = "show needs id-prefix (see `deja last`)"

// idPrefixNeeded is the refusal for a command that needs a session named on the
// command line. "see `deja last`" is a step the reader can take and learn
// nothing from when the store is empty: the listing answers with the same
// emptiness deja already knows about here (#992).
func idPrefixNeeded(dir, subject, refusal string) error {
	if n, err := index.SessionCount(dir); err == nil && n == 0 {
		return errors.New(strings.TrimPrefix(emptyIndexHint(subject+", and nothing is indexed yet"), "deja: "))
	}
	return errors.New(refusal)
}

// emptyIndexReason opens the empty-index sentence. "Nothing to index yet" is
// for a machine deja has never seen history from; a run that has just evicted a
// store says what went away instead, because the line above it has already told
// the reader what was lost and the two must not contradict each other (#1762).
func emptyIndexReason(b index.BuildSummary, evicted int) string {
	if evicted > 0 {
		return emptyIndexHint(fmt.Sprintf("%d indexed file%s went away with the store that held %s, and nothing is left to index",
			evicted, pluralS(evicted), pluralWhich(evicted)))
	}
	return emptyIndexHint("nothing to index yet")
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
		// "no agent history was found" is a claim about the machine, and it
		// was made over a store deja is not allowed to open: the sessions are
		// there, behind a permission wall doctor and sources both name (#1020).
		if denied := deniedStoreCount(); denied > 0 {
			return fmt.Sprintf("deja: %s — %d store%s could not be read (permission denied); `deja doctor` names %s",
				what, denied, pluralS(denied), pluralWhich(denied))
		}
		return "deja: " + what + " — no agent history was found on this machine; `deja sources` shows where deja looked"
	}
	return "deja: " + what + " — run `deja index`, or `deja doctor` to see which agent stores were found"
}

// deniedStoreCount reports how many harness stores exist but cannot be opened.
func deniedStoreCount() int {
	n := 0
	for _, check := range doctorStoreChecks() {
		if store, _ := inspectDoctorStore(check); store.State == "denied" {
			n++
		}
	}
	return n
}

// noAgentHistoryFound reports whether the stores themselves are empty, as
// opposed to an index that merely has not been built yet.
func noAgentHistoryFound() bool {
	for _, check := range doctorStoreChecks() {
		// The count, not the inspection. `store.Files` is `len(check.files)`
		// and nothing else ever sets it (doctor_report.go:471), so asking
		// `inspectDoctorStore` for it paid a stat per path, a listing per
		// directory, and the newest file of the store opened and run through
		// that store's parser — SQLite for opencode and cursor — to learn a
		// number already in hand. Measured on a real home, 514 ms against
		// 6.6 ms for the same answer (#1991).
		if len(check.files) == 0 {
			continue
		}
		// By content, not by existence: an empty notes file — one `deja
		// remember` later forgotten, or a file someone touched — counted as
		// history and turned the answer into "run `deja index`", which is the
		// one piece of advice this branch exists to avoid (#996).
		for _, f := range check.files {
			if fi, err := os.Stat(f); err == nil && fi.Size() > 0 {
				return false
			}
		}
	}
	return true
}

// pluralNote words the notes in the failure line above.
func pluralNote(n int) string {
	if n == 1 {
		return "the promoted note"
	}
	return "the promoted notes"
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

// pluralSessionWord words the tail of the empty-transcript line.
func pluralSessionWord(n int) string {
	if n == 1 {
		return "a session"
	}
	return "sessions"
}

// pluralWhich matches the pronoun to the count in the ingest warning.
func pluralWhich(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// staleUnwritableIndex reports that a build could not run because the store
// cannot be written, while an index that can still answer is right there.
// Refusing the whole search then is deja withholding what it has: the reader
// gets nothing instead of slightly old memory and a line saying why (#904).
// A full disk belongs here next to a denied one: it is the commoner of the
// two, and it took every answer with it — empty stdout and exit 1 while a
// complete index sat in the store.
func staleUnwritableIndex(dir string, err error) bool {
	if !errors.Is(err, fs.ErrPermission) && !errors.Is(err, syscall.ENOSPC) {
		return false
	}
	return index.HasManifest(dir)
}

// deniedPath names the file a permission error was actually about, so the fix
// deja suggests points at it rather than at the index directory (#1031).
func deniedPath(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Path
	}
	return ""
}

// existingNonDirAncestor names the first ancestor of p that exists and is not
// a directory. Such a path can never hold an index, and the errno differs by
// platform, so the shape is worth naming rather than the syscall.
func existingNonDirAncestor(p string) string {
	for cur := filepath.Clean(p); ; {
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		if fi, err := os.Stat(parent); err == nil && !fi.IsDir() {
			return parent
		}
		cur = parent
	}
}

// nearestExistingDir walks up until it finds a directory that is there, so a
// refusal can name the thing that actually refused rather than the path that
// could not be created under it.
func nearestExistingDir(p string) string {
	for cur := filepath.Clean(p); ; {
		parent := filepath.Dir(cur)
		if parent == cur {
			if dirExists(cur) {
				return cur
			}
			return ""
		}
		if dirExists(parent) {
			return parent
		}
		cur = parent
	}
}

// dirWritable reports whether this process can create something in dir.
// Permission bits alone answer for the wrong user on the wrong platform, so it
// asks the filesystem.
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".deja-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// ensureError turns a failed build into something the reader can act on.
// A denied write surfaced as `ensure: open /…/index.db.lock: permission
// denied` — the path of an internal lock file and a syscall error, which says
// nothing about what to change.
func ensureError(dir string, err error) error {
	if dir == "" {
		dir = index.DefaultDir()
	}
	if errors.Is(err, fs.ErrPermission) {
		// An ejected volume takes its mount point with it, and creating that
		// point back fails with EPERM on macOS — so the errno says permissions
		// while the disk is simply gone. The same trap the notes and export
		// paths hit (#893, #906), and the reader is sent to check the
		// permissions of a directory that no longer exists (#931).
		if parent := filepath.Dir(dir); !dirExists(dir) && !dirExists(parent) {
			// Unless something above it is there and simply refuses: a
			// locked-down ~/.cache cannot be written into either, and reading
			// that as an ejected volume sent the reader to reconnect a disk
			// that never left (#2267).
			if a := nearestExistingDir(parent); a != "" && !dirWritable(a) {
				return fmt.Errorf("cannot create the index directory (%s) — %s is not writable; check its permissions, or point DEJA_INDEX_DIR somewhere writable", parent, a)
			}
			return fmt.Errorf("the index directory is not there (%s) — the disk it lives on may have been unmounted; reconnect it, or point DEJA_INDEX_DIR somewhere local", parent)
		}
		// The denial is not always about the index: forget writes the
		// tombstone file first, and a read-only ~/.config/deja arrived here
		// as "check the index directory", which was writable — the reader was
		// sent to change a permission that was already right (#1031, #808).
		if p := deniedPath(err); p != "" && !strings.HasPrefix(p, dir) {
			return fmt.Errorf("cannot write %s — check that file's permissions", p)
		}
		return fmt.Errorf("cannot write the index at %s — check the directory's permissions, or point DEJA_INDEX_DIR somewhere writable", dir)
	}
	// A full disk arrived as `ensure: write /…/index.db.tmp/records.bin: no
	// space left on device`: an internal path nobody can act on, and the same
	// shape #798 replaced for permissions. The build needs room beside the
	// index, so the directory to free is the one named here (#888).
	// A volume that went away mid-write — an unmounted disk, a network share
	// that dropped — arrives as `write /…/index.db.tmp/records.bin:
	// input/output error`: the same internal path as #888, and a reader who
	// cannot tell that the disk is simply gone (#899).
	if errors.Is(err, syscall.EIO) || errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.ENODEV) {
		return fmt.Errorf("the index directory is not reachable (%s) — the disk it lives on may have been unmounted or dropped; reconnect it, or point DEJA_INDEX_DIR somewhere local", filepath.Dir(dir))
	}
	if errors.Is(err, syscall.ENOSPC) {
		return fmt.Errorf("no space left where the index is built (%s) — free some room there, or point DEJA_INDEX_DIR at a disk that has it", filepath.Dir(dir))
	}
	// A volume ejected cleanly mid-build leaves its mount point behind as an
	// empty directory, so the write fails with ENOENT rather than the EIO of a
	// disk yanked out (#899) — an internal `idx.tmp/buckets/…` path and a
	// syscall for what is simply a disconnected disk. The index that was
	// already there is untouched, since the build writes beside it and
	// renames, and saying so is the part that decides whether the reader goes
	// looking for damage (#1068).
	// An index path that points inside a file is not a disconnected disk: on
	// unix the write fails with ENOTDIR and fell through to the raw syscall,
	// on windows it fails with ENOENT and read as an unmounted volume (found
	// by CI on windows after #1068).
	if p := existingNonDirAncestor(dir); p != "" {
		return fmt.Errorf("the index path runs through %s, which is a file — point DEJA_INDEX_DIR at a directory", p)
	}
	if errors.Is(err, fs.ErrNotExist) && !dirExists(dir) {
		return fmt.Errorf("the index directory went away mid-build (%s) — the disk it lives on may have been unmounted; the index already there is unharmed, so reconnect it and run `deja index` again, or point DEJA_INDEX_DIR somewhere local", dir)
	}
	// Already worded where it was raised — the leftover-swap case names the
	// directory to remove and the command to rerun, and "ensure:" in front of
	// it is internal noise (#1009).
	if strings.HasPrefix(err.Error(), "an earlier index swap left ") {
		return err
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

// forgetNotesLine says how much of what is going is the user's own writing.
// Everything deja files under its own harness counted as a "promoted note",
// so forgetting a day of `remember` notes named something the reader had never
// done (#957).
func forgetNotesLine(result index.ForgetResult) string {
	switch {
	case result.Notes == 0:
		return ""
	case result.Promoted == result.Notes:
		return fmt.Sprintf("%d of them %s promoted note%s — the decisions you kept, not raw sessions",
			result.Notes, verbWere(result.Notes), pluralS(result.Notes))
	case result.Promoted == 0:
		if result.Notes == 1 {
			return "1 of them is a day of your own notes, not raw sessions"
		}
		return fmt.Sprintf("%d of them are %s of your own notes, not raw sessions",
			result.Notes, dayWord(result.Notes))
	default:
		return fmt.Sprintf("%d of them %s your own writing — %d promoted, %s of notes — not raw sessions",
			result.Notes, verbWere(result.Notes), result.Promoted, dayWord(result.Notes-result.Promoted))
	}
}

// dayWord names day buckets the way the reader wrote them: by the day.
func dayWord(n int) string {
	if n == 1 {
		return "a day"
	}
	return fmt.Sprintf("%d days", n)
}

// pluralIsLifted and pluralTranscript keep the two sentences readable for one
// key and for several; joinCapped names up to five.
func pluralIsLifted(n int) string {
	if n == 1 {
		return " is"
	}
	return "s are"
}

func pluralTranscript(n int) string {
	if n == 1 {
		return " it"
	}
	return "s they"
}

// unforgetGoneLines says why a lifted tombstone restored nothing. Two causes
// with two answers: a session deja only ever held in its own index came from
// another machine and sync import brings it back (#967), while a transcript
// this machine wrote and then deleted is simply not on disk — telling that
// reader to import from another machine names one they never had (#1755).
func unforgetGoneLines(missing []string) []string {
	var imported, local []string
	for _, key := range missing {
		id := key
		if i := strings.IndexByte(key, ':'); i >= 0 {
			id = key[i+1:]
		}
		if strings.HasPrefix(id, "imported-") {
			imported = append(imported, key)
			continue
		}
		local = append(local, key)
	}
	var out []string
	if len(imported) > 0 {
		out = append(out, fmt.Sprintf("%s came from another machine and deja held the only copy — the tombstone%s lifted, but the records are gone; `deja sync import` brings them back", joinCapped(imported, 5), pluralIsLifted(len(imported))))
	}
	if len(local) > 0 {
		out = append(out, fmt.Sprintf("%s %s no longer on this machine — the tombstone%s lifted, but the transcript%s named %s gone, so there is nothing to index",
			joinCapped(local, 5), verbIs(len(local)), pluralIsLifted(len(local)), pluralTranscript(len(local)), verbIs(len(local))))
	}
	return out
}

// restoredSessions splits what a lifted tombstone actually returned from what
// it could not: a session whose transcript is on this machine is re-read by the
// rebuild, while an imported one existed only in the index that forget rewrote.
func restoredSessions(dir, selector string, lifted int, lifting []string) (back, gone int, names, missing []string) {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return lifted, 0, lifting, nil
	}
	// A session counts as back only if its row is in the manifest again: an
	// imported one lives only in the index, so forget took the last copy and
	// lifting its tombstone restores nothing (#967).
	here := map[string]bool{}
	for _, m := range metas {
		here[m.Harness+":"+m.ID] = true
	}
	for _, key := range lifting {
		if here[key] {
			back++
			names = append(names, key)
			continue
		}
		missing = append(missing, key)
	}
	if back == 0 && lifted > 0 {
		// Selectors that are not whole keys (a bare id, a prefix) still count
		// through the manifest, as before.
		for _, m := range metas {
			if index.SelectorMatches(m, selector) {
				back++
				names = append(names, m.Harness+":"+m.ID)
			}
		}
		if back > lifted {
			back, names = lifted, names[:lifted]
		}
		if back > 0 {
			// The prefix path found them by manifest row, so nothing here is
			// missing by name.
			missing = nil
		}
	}
	sort.Strings(names)
	sort.Strings(missing)
	return back, lifted - back, names, missing
}

// restorePromotedTitles gives a promoted note back the title it borrowed from a
// session that has just been restored. forget clears it so the text of a
// forgotten session does not live on in the note (#666); unforget is the moment
// that reason stops applying (#969).
func restorePromotedTitles(dir, selector string) {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return
	}
	titles := map[string]string{}
	for _, m := range metas {
		if !index.SelectorMatches(m, selector) {
			continue
		}
		s, ok, err := index.FindByPrefix(dir, m.ID)
		if err != nil || !ok {
			continue
		}
		if t := firstUserTitle(s); t != "" {
			titles[m.Harness+":"+m.ID] = t
		}
	}
	if len(titles) == 0 {
		return
	}
	_, _ = sources.RestorePromotedTitles(func(src string) string { return titles[src] })
}

// sharedRowsAmong counts the manifest rows in keys that hold more than one
// conversation, so forget can say what it is about to take (#970).
func sharedRowsAmong(dir string, keys []string) int {
	if len(keys) == 0 {
		return 0
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		return 0
	}
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	n := 0
	for _, m := range metas {
		if m.Shared && want[m.Harness+":"+m.ID] {
			n++
		}
	}
	return n
}

// noteForgottenSource says when an id lookup landed on the note promoted from a
// session that has been forgotten. Asking for the session by the id you
// remember answered with the note and said nothing, so the reply looked like
// the session itself until you noticed the id had changed (#971).
func noteForgottenSource(s model.Session, selector string) {
	if s.Harness != "deja" || !strings.HasPrefix(s.ID, "deja-note-") {
		return
	}
	src, ok := strings.CutPrefix(s.ID, "deja-note-")
	if !ok || src == "" {
		return
	}
	// The selector named the source, not the note: a reader who typed the note
	// id knows what they asked for.
	if strings.HasPrefix(s.ID, selector) {
		return
	}
	key := strings.Replace(src, "-", ":", 1)
	// Only when the reader named that session: an ordinary topical query that
	// happens to land on the note is not asking about the forgotten source, and
	// the line would be noise on every such search.
	if selector == "" || !strings.Contains(key, selector) {
		return
	}
	if !index.Tombstoned(key) {
		return
	}
	fmt.Fprintf(os.Stderr, "deja: %s is forgotten — this is the note promoted from it; `deja forget --list` names what is gone\n", key)
}

// forgetKeyOf names the exact session a selector reached, so a failed forget
// can tell whether the tombstone landed (#976).
func forgetKeyOf(dir string, o index.ForgetOptions) string {
	if o.Session == "" {
		return ""
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		return ""
	}
	for _, m := range metas {
		if index.SelectorMatches(m, o.Session) {
			return m.Harness + ":" + m.ID
		}
	}
	return ""
}

// reachableSessionCount reports how many indexed sessions the search
// activation may read, and how many are indexed in all.
func reachableSessionCount(dir string) (int, int, bool) {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return 0, 0, false
	}
	pol := policy.Load()
	reach := 0
	for _, m := range metas {
		if pol.Allows(policy.ActivationSearch, m.Project) {
			reach++
		}
	}
	return reach, len(metas), true
}

// joinCapped lists at most n items and says how many it left out, so a refusal
// stays one line on a machine with many tombstones.
func joinCapped(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(", +%d more", len(items)-n)
}

// forgetDigestMatcher decides which stored digests belong to what a forget just
// took. A record written since #2324 names its own projects, which is exact; an
// older one is recognised by the ids and project name inside its text, the same
// weaker test the view page falls back to.
func forgetDigestMatcher(o index.ForgetOptions, keys []string) func(usage.Snapshot) bool {
	// Both spellings: a listing renders `claude:abc` and a hook block quotes
	// the bare id, so a sweep that knew only one of them missed half the
	// records it was meant to take.
	ids := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		ids = append(ids, k)
		if _, id, ok := strings.Cut(k, ":"); ok && id != "" {
			ids = append(ids, id)
		}
	}
	return func(s usage.Snapshot) bool {
		if o.Project != "" {
			for _, p := range s.Projects {
				if p == o.Project {
					return true
				}
			}
			if len(s.Projects) == 0 && digestNames(s.Digest, o.Project) {
				return true
			}
		}
		for _, id := range ids {
			if digestNames(s.Digest, id) {
				return true
			}
		}
		return false
	}
}

// digestNames reports whether a digest names a project or a session where it
// renders one, rather than anywhere in its text. Searching the whole text made
// `forget --project api` delete a digest about work/web whose prose read "a
// late api call": the reader forgot one project and lost another's record
// (#2330). The two shapes deja writes are the recall listing,
//
//  1. [claude] work/app · claude:abc · 2 matches
//
// and the hook block,
//
//   - **work/app** `abc` · 2026-08-28
//
// A digest in neither shape names nothing, so a sweep keeps it: with `projects`
// recorded since #2324 the guess is only needed for older records, and deleting
// one on a guess cannot be undone.
func digestNames(digest, want string) bool {
	if want == "" {
		return false
	}
	for _, line := range strings.Split(digest, "\n") {
		for _, field := range digestNamedFields(line) {
			if field == want {
				return true
			}
		}
	}
	return false
}

// digestNamedFields pulls the project and id positions out of one rendered
// line: the fields that follow the harness in brackets, and anything a hook
// block emphasises or quotes.
func digestNamedFields(line string) []string {
	var out []string
	if _, after, ok := strings.Cut(line, "] "); ok {
		for _, field := range strings.Split(after, " · ") {
			out = append(out, strings.TrimSpace(field))
		}
	}
	out = append(out, betweenAll(line, "**")...)
	out = append(out, betweenAll(line, "`")...)
	return out
}

// betweenAll returns every substring wrapped by the given marker.
func betweenAll(s, marker string) []string {
	var out []string
	for {
		i := strings.Index(s, marker)
		if i < 0 {
			return out
		}
		rest := s[i+len(marker):]
		j := strings.Index(rest, marker)
		if j < 0 {
			return out
		}
		out = append(out, strings.TrimSpace(rest[:j]))
		s = rest[j+len(marker):]
	}
}
