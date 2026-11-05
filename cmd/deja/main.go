// Command deja indexes the session transcripts of the coding agents on this
// machine and answers recall, blame and fix questions from them.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

var version = "dev"

// errAlreadySaid exits non-zero for a command that has already told the reader
// what went wrong. Without it the choice was between a silent success and the
// same sentence twice: `deja unforget x` printed "that is not a command, here
// is the one that is" and exited 0, so a script that checked the code was told
// the work had been done.
var errAlreadySaid = errors.New("already said")

func main() {
	// The detached look an interactive run started once a day (#4622).
	if stamp, ok := releaseLookRequested(os.Args[1:], index.DefaultDir()); ok {
		runReleaseLook(stamp, defaultDoctorVersionLookup())
		return
	}
	// A command that lands mid-rebuild waits for the whole of it, and silence
	// there reads as a hang rather than as a queue (#994).
	installLockWait()
	// A detached warmup is index work nobody asked for at a moment somebody is
	// working, so it takes less than a foreground run would (#3500).
	takeWarmupBudget()
	stopProfiling := startProfiling()
	// Only `deja doctor` said a newer release exists. Look at most once a day,
	// in the background, and only when a person is at the terminal (#4622).
	notice := startReleaseNotice(os.Args[1:], briefWanted(os.Stdout) && briefWanted(os.Stderr),
		index.DefaultDir(), time.Now(), spawnReleaseLook)
	err := run(os.Args[1:])
	if exe, exeErr := os.Executable(); exeErr == nil {
		notice.finish(os.Stderr, exe)
	}
	var status exitStatus
	if errors.As(err, &status) {
		stopProfiling()
		os.Exit(int(status))
	}
	if err != nil {
		stopProfiling()
		// Already said, on stderr, in the words that fit what happened: a
		// second sentence here would repeat it. The exit code is the point —
		// a command nobody typed correctly must not look like one that worked.
		if !errors.Is(err, errAlreadySaid) {
			fmt.Fprintln(os.Stderr, "deja:", rebuildWindowError(err))
		}
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
	return errors.New("the index is being rebuilt right now — run this again in a moment")
}

func loadAll(h string) []model.Session {
	var ss []model.Session
	for _, store := range sources.StoresToRead() {
		if (h == "" || store.Name == h) && store.Load != nil {
			ss = append(ss, store.Load()...)
		}
	}
	return ss
}

func loadFileSources() []model.Session {
	var ss []model.Session
	for _, store := range sources.StoresToRead() {
		if store.Name != "opencode" && store.Load != nil {
			ss = append(ss, store.Load()...)
		}
	}
	return ss
}

// worksWithNoHome names the commands that never write under a home directory
// they cannot find: they print, or they diagnose the very state the guard is
// about, or they carry their own refusal.
//
// `version` and `help` write nothing at all and are what a packager runs;
// `completion` is sourced from a shell rc file; `update` replaces the binary
// and is how somebody gets out of a broken state; `doctor` and `sources` are
// where every other message sends the reader to find out what deja can see;
// and install carries #1690's own guard, whose advice is the right one for it.
var worksWithNoHome = map[string]bool{
	"version": true, "--version": true, "-version": true,
	"help": true, "--help": true, "-h": true,
	"completion": true, "shell": true, "update": true,
	"doctor": true, "sources": true,
	"install": true, "uninstall": true,
}

// dispatchKnows reports whether a word is a command deja runs, from either
// half of the dispatcher: the map, and the switch above it that holds the five
// commands taking their own argument parsing.
func dispatchKnows(name string) bool {
	if _, ok := commands[name]; ok {
		return true
	}
	switch name {
	case "show", "last", "search", "aider", "goose":
		return true
	}
	return false
}

// homelessRefusal answers what to do when deja cannot find a home directory
// and nothing else says where its index goes.
//
// A command somebody typed says why it cannot run. A hook does not: it runs
// unattended on every prompt, a refusal would cost the turn, and the whole
// design of the hooks is to fail open and stay quiet (#1692). Either way
// nothing is written, which is the part that matters — the index is a database,
// and it was landing in whatever repository the agent was working in.
func homelessRefusal(name string) (bool, error) {
	// Both roots: the index answers to DEJA_INDEX_DIR, and the config
	// directory to XDG_CONFIG_HOME, so one of them being absolute says
	// nothing about the other.
	if filepath.IsAbs(index.DefaultDir()) && filepath.IsAbs(xdgConfigHome()) {
		return false, nil
	}
	if worksWithNoHome[name] {
		// Only a name the dispatcher will actually take: `-v` is not a
		// command, so allowlisting it let it through the guard, miss the map
		// and land in the bare-query path — which builds an index (#1692).
		if _, known := commands[name]; known {
			return false, nil
		}
	}
	if strings.HasPrefix(name, "hook-") || name == "statusline" || name == "warmup-status" {
		return true, nil
	}
	// The command's own name, or deja's: for a bare query the first word is
	// the reader's, and "retry cannot find your home directory" reads as if
	// `retry` were a command.
	if !dispatchKnows(name) {
		name = "deja"
	}
	return true, fmt.Errorf("%s cannot find your home directory — set HOME, or DEJA_INDEX_DIR and XDG_CONFIG_HOME to absolute paths", name)
}

type command func(dir string, rest []string) error

var commands = map[string]command{
	"version":   cmdVersion,
	"help":      cmdHelp,
	"--help":    cmdHelp,
	"-h":        cmdHelp,
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
	"statusline": func(dir string, _ []string) error {
		return endLineOnTerminal(os.Stdout, func(w io.Writer) error { return runStatusline(dir, os.Stdin, w) })
	},
	"stats":    runStats,
	"remember": runRemember,
	"promote":  func(dir string, rest []string) error { return runPromote(dir, rest, os.Stdout) },
	"rules":    runRules,
	"forget":   runForget,
	"delete":   func(dir string, rest []string) error { return runDelete(dir, rest, os.Stdout) },
	"mcp": func(dir string, rest []string) error {
		return serveMCPProcess(dir, os.Stdin, os.Stdout, mcpContextFlag(rest))
	},
	"hook-prompt": func(dir string, rest []string) error {
		if sayIfTypedByHand("hook-prompt") {
			return nil
		}
		plain := false
		// --mcode answers in MiniMax Code's shape (hook_mcode.go).
		mcodeHookOutput = mcodeHookFlag(rest)
		for _, a := range rest {
			switch a {
			case "--plain", "-plain":
				plain = true
			// --copilot: the flat answer Copilot CLI reads (see hook_strict.go).
			case "--copilot", "-copilot":
				copilotHookOutput = true
			// --strict: ZCode discards a reply with a key it does not know,
			// recall and all, so the receipt line stays out (hook_strict.go).
			case "--strict", "-strict":
				strictHookOutput = true
			}
		}
		return endLineOnTerminal(os.Stdout, func(w io.Writer) error { return runHookPromptMode(dir, os.Stdin, w, plain) })
	},
	"hook-antigravity": func(dir string, _ []string) error {
		return runHookAntigravity(dir, os.Stdin, os.Stdout)
	},
	"hook-session-end": func(dir string, rest []string) error {
		runHookSessionEnd(dir, os.Stdin)
		// --json: a host that reads every hook's answer as JSON gets the
		// empty object that means "nothing to add" (Antigravity's Stop).
		if len(rest) > 0 && (rest[0] == "--json" || rest[0] == "-json") {
			fmt.Fprintln(os.Stdout, "{}")
		}
		return nil
	},
	"hook-codewhale": func(dir string, rest []string) error {
		return runHookCodeWhale(dir, rest, os.Stdin, os.Stdout)
	},
	"hook-stop": func(dir string, _ []string) error {
		return runHookStop(dir, os.Stdin, os.Stdout)
	},
	"hook-mcp-call": func(dir string, _ []string) error {
		runHookMCPCall(dir, os.Stdin, os.Stdout)
		return nil
	},
	"hook-plan": func(dir string, _ []string) error {
		if sayIfTypedByHand("hook-plan") {
			return nil
		}
		return runHookPlan(dir, os.Stdin, os.Stdout)
	},
	"hook-tool": func(dir string, rest []string) error {
		if sayIfTypedByHand("hook-tool") {
			return nil
		}
		return endLineOnTerminal(os.Stdout, func(w io.Writer) error {
			return runHookToolMode(dir, os.Stdin, w, hookToolShapeOf(rest))
		})
	},
	"hook-tool-after": func(dir string, rest []string) error {
		if sayIfTypedByHand("hook-tool-after") {
			return nil
		}
		plain := len(rest) > 0 && (rest[0] == "--plain" || rest[0] == "-plain")
		// --copilot: the flat answer Copilot CLI reads (see hook_strict.go).
		if len(rest) > 0 && (rest[0] == "--copilot" || rest[0] == "-copilot") {
			copilotHookOutput = true
		}
		// --mcode answers in MiniMax Code's shape (hook_mcode.go).
		mcodeHookOutput = mcodeHookFlag(rest)
		return endLineOnTerminal(os.Stdout, func(w io.Writer) error { return runHookToolAfterMode(dir, os.Stdin, w, plain) })
	},
	"shell": func(dir string, rest []string) error {
		return runShell(dir, rest, os.Stdout, os.Stderr)
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
	"hook-precompact": cmdHookPrecompact,
	"wip":             func(dir string, args []string) error { return runWIP(dir, args, os.Stdout) },
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
	"secrets":         func(dir string, rest []string) error { return runSecrets(dir, rest, os.Stdout) },
	"tests":           func(dir string, rest []string) error { return runTests(dir, rest, os.Stdout) },
	"recap":           func(dir string, rest []string) error { return runRecap(dir, rest, os.Stdout) },
	"fix":             func(dir string, rest []string) error { return runFix(dir, rest, os.Stdout) },
	"how":             func(dir string, rest []string) error { return runHow(dir, rest, os.Stdout) },
	"recall":          func(dir string, rest []string) error { return runRecall(dir, rest, os.Stdout) },
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
	// The code extension `deja install reasonix-auto` registers. Reasonix
	// starts it and speaks JSON-RPC on its stdin; at a terminal it says so
	// rather than sitting on a read nobody will answer.
	"reasonix-ext": func(dir string, _ []string) error {
		if sayIfTypedByHand("reasonix-ext") {
			return nil
		}
		return runReasonixExt(dir, os.Stdin, os.Stdout)
	},
	"blame":  runBlame,
	"origin": func(dir string, rest []string) error { return runOrigin(dir, rest, os.Stdout) },
	"review": func(dir string, rest []string) error { return runReview(dir, rest, os.Stdout) },
}

func run(args []string) error {
	if len(args) > 0 && args[0] == framedFlag {
		return runFramed(args[1:])
	}
	dir := index.DefaultDir()
	// Before anything reads or writes: every path deja writes hangs off the
	// home directory, and it answers "" when there is none, so
	// `filepath.Join("", ".cache", "deja")` is `.cache/deja` — deja built its
	// index in whatever directory it happened to be run from, on the commands
	// an agent runs unattended as much as on the ones a person types. #1690
	// fixed install; this is the rest (#1692).
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	if stop, err := homelessRefusal(name); stop {
		return err
	}
	// A typo in DEJA_STORES silences every store rather than narrowing to one,
	// and what follows reads like a machine with no history instead of like a
	// mistake. Checked once, before any command runs.
	if err := sources.StoresSelectionError(); err != nil {
		return err
	}
	if len(args) == 0 {
		// briefWanted, not logoWanted: a reader who turned colour off still
		// has an index and a terminal, and the brief is what that reader came
		// for (#1596).
		if tuiWanted() {
			return runTUI(dir)
		}
		if briefWanted(os.Stdout) {
			return runBrief(dir, os.Stdout)
		}
		// Piped, the full page was 106 lines nobody asked for. Nothing deja
		// installs runs a bare deja, and the brief would build an index into a
		// pipe, so the reader gets what is indexed and where to go next (#4621).
		fmt.Print(bareDejaPointer(dir))
		return nil
	}
	sourceInstance := os.Getenv("DEJA_SOURCE_INSTANCE")
	warnBrokenPolicy(args[0], os.Stderr)
	if wantsHelp(args[1:]) {
		// `deja aider --help` is aider's: the help page promises the rest of
		// the line goes to the tool, and answering with deja's page left no
		// way to reach the tool's own short of calling it directly.
		if toolOwnsHelp(args[0]) {
			return runToolHelp(args[0], args[1:])
		}
		if h := helpForCommand(args[0]); h != "" {
			// The same width the page itself respects: `deja last --help`
			// quotes the widest line in it, so a single command's help was
			// the one most likely to wrap (#1661).
			fmt.Print(wrapUsage(h, printableWidth(os.Stdout)))
			return nil
		}
		// Not a command, so the words are a bare search, and --help after
		// them was searched for along with them.
		if !dispatchKnows(args[0]) && !strings.HasPrefix(args[0], "hook-") {
			fmt.Print(wrapUsage(helpForCommand("search"), printableWidth(os.Stdout)))
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
		if deferrableHooks[args[0]] {
			return runHookDeferred(dir, args[0], args[1:], cmd)
		}
		return cmd(dir, args[1:])
	}
	// A `hook-…` this build does not have is a line in a config an older deja
	// wrote, not a query: reading it as one ran a search on every session
	// start — and built the whole index to answer it — while stdout, where a
	// hook's output is read from, stayed empty (#2718). commandHint already
	// treats these names as plumbing nobody types.
	//
	// Only when a harness is calling: at a terminal this is somebody who
	// mistyped `hook-context`, and telling them a config calls it is a claim
	// about their machine that is not true — they keep the search and the near
	// miss every other mistyped word gets (#674). And only when it is the whole
	// command line: `deja hook-session timeout` is a query somebody typed, and
	// no harness passes a retired hook extra arguments.
	if len(args) == 1 && strings.HasPrefix(args[0], "hook-") && !stdinIsTerminal() {
		// On stderr: a harness reads stdout as the hook's answer, and on some
		// of them a bare line there lands in the model's context. This is a
		// note for whoever reads the logs, not something to inject.
		// What to do, and only what is true: install writes the hooks this
		// build has and leaves a line under a name it does not recognise where
		// it is (#2719), so pointing at it would be advice that does nothing.
		fmt.Fprintf(os.Stderr, "deja: %q is not a hook this build has — a config still calls it on every session; that line is safe to delete\n", args[0])
		return nil
	}
	return runBareSearch(dir, args, sourceInstance)
}

func cmdVersion(_ string, rest []string) error {
	if len(rest) > 0 {
		return fmt.Errorf("version takes no arguments — got %q", rest[0])
	}
	fmt.Fprintf(os.Stdout, "deja %s\n", version)
	return nil
}

func cmdWarmup(dir string, rest []string) error {
	// The arguments were thrown away here, so `deja warmup --rebuild` ran the
	// ordinary incremental build, said nothing and exited 0 (#4109). The
	// rebuild lives on index; a near miss of it is pointed there too.
	if len(rest) > 0 {
		a := rest[0]
		if a == "--rebuild" || a == "-rebuild" || nearestKnownFlag(a, []string{"--rebuild"}) != "" {
			return fmt.Errorf("warmup: unknown flag %q — the rebuild is deja index --rebuild", a)
		}
		return fmt.Errorf("warmup takes no arguments — got %q", a)
	}
	// warmup is what hooks and installs start; with automatic indexing off
	// it reports the index instead of building it (#5385).
	if index.Manual() {
		if err := index.ManualStatus(dir); err != nil {
			return err
		}
		if line := manualIndexLine(dir); line != "" {
			fmt.Fprintln(os.Stderr, line)
		}
		_, _ = writeCLISkill()
		return nil
	}
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
	_, _ = writeCLISkill()
	return nil
}

// countingWriter passes writes through and remembers whether there were any.
type countingWriter struct {
	w io.Writer
	n int
}

// Unwrap is the writer underneath, so a printer deciding whether it is
// talking to a terminal sees the terminal rather than the counter (#4620).
func (c *countingWriter) Unwrap() io.Writer { return c.w }

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
	quiet := false
	for _, a := range rest {
		switch a {
		case "--rebuild", "-rebuild":
			force = true
		case "--quiet", "-quiet":
			quiet = true
		default:
			return unknownFlag("index", a, indexFlags)
		}
	}
	// The detached warmup runs this command too. With automatic indexing off
	// it is not the user asking, so it writes nothing; anything else here is
	// the explicit build the switch leaves on (#5385).
	if os.Getenv("DEJA_WARMUP_SENTINEL") != "" && index.Manual() {
		clearWarmupSentinel()
		return nil
	}
	// Before the freshness walk and the index lock, either of which can take
	// minutes on a contended disk: from here on the claim names a live owner.
	adoptWarmupClaim()
	defer index.AllowBuild()()
	// Run by hand, it is also what turns search-only on or off (#5395).
	if os.Getenv("DEJA_WARMUP_SENTINEL") == "" {
		defer index.ApplySearchOnly()()
	}
	// Where the command reports that it worked. Nobody is watching a run out
	// of a shell profile or a hook, and a line printed over the prompt on
	// every new shell is the kind of thing that gets a tool uninstalled
	// rather than reported; `deja index >/dev/null` also swallows the errors
	// you would want to see (#1827).
	//
	// Only the success reporting goes here. A run that cannot read a store or
	// cannot write the index still says so on stderr, or a scheduled job
	// stops working and nothing tells anyone. The counts and the outcome line
	// are what quiet is about; a warning that the exclude list is not applied,
	// a path that could not be read, and an index that came out empty for a
	// reason are not.
	said := io.Writer(os.Stderr)
	if quiet {
		said = io.Discard
	}
	// Silence reads as "it did not run". `update` on the newest release and
	// `doctor` on a fresh index both say so; this one returned to the prompt
	// with nothing (#824). Only here: on a search the same line would be noise.
	// The freshness check walks every store, which on a slow volume is the
	// longest part of the whole command, and it ran before the progress sink
	// existed (#1021).
	stopProgress := publishBuildProgress(dir)
	index.SweepStaleTmp(dir)
	fresh, n, check := index.CheckUpToDate(dir, "")
	// A note bucket's id names the local day of whichever process indexed the
	// line, so a `remember` under TZ=UTC and one under the machine's own zone
	// split the same moment into two buckets. `show` tells the reader this
	// command regroups them, and it answered that the index was up to date
	// (#1058).
	if fresh && !force && index.NotesZoneDrift(dir) {
		fmt.Fprintln(os.Stderr, "deja: some notes were grouped in another time zone — regrouping them")
		fresh, force = false, true
	}
	// aider headers carry no zone and are read in this one; after a zone
	// change the warmup this command runs must reread them (#5053).
	if fresh && !force && index.AiderZoneDrift(dir) {
		fmt.Fprintln(os.Stderr, "deja: aider sessions were read in another time zone — reading them again")
		fresh, force = false, true
	}
	if fresh && !force {
		stopProgress()
		// The warmup child runs this command, so returning before the sentinel
		// is cleared leaves a build that is not running: the next request is
		// suppressed until the retry window, and readWarmupStatus tells the
		// agent memory is on its way (#839).
		clearWarmupSentinel()
		fmt.Fprintf(said, "deja: index is up to date (%d session%s)\n", n, pluralS(n))
		printRetentionDue(said, time.Now())
		// Up to date with what it could read: a path it may not read is
		// still missing from recall on every run, not only the one that
		// found it (#5393).
		unread := sayUnreadable(dir)
		// "Up to date" is the most misleading place to stay quiet about it:
		// nothing changed on disk, so this is exactly where an exclusion set
		// after the build looks applied and is not.
		if index.ExclusionsChanged(dir) {
			fmt.Fprintln(os.Stderr, "deja: the exclude list changed since this index was built — `deja index --rebuild` applies it to sessions already indexed")
		}
		sayAliasesChanged(dir)
		return partialIndex(unread, 0)
	}
	stopProgress()
	if !quiet {
		prepareFirstIndexGreeting(dir)
	}
	// The detached warmup publishes its progress so hooks can tell the user
	// memory is on its way; an interactive run draws the live display.
	// Counted, because a run that waited for another build prints nothing of
	// its own: Ensure finds the index current under the lock and returns. The
	// command then owed a closing line and had none, leaving "waiting for it
	// to finish" as the last thing on screen (#1751).
	progress := &countingWriter{w: said}
	// Piped or under cron there is no live display, and a long run printed
	// "indexing ..." and then nothing until the per-store counts: one line
	// per phase says where it is (#5396). Not for the warmup, which
	// publishes its phases to the status file.
	phases := !quiet && !logoWanted(os.Stdout) && os.Getenv("DEJA_WARMUP_SENTINEL") == ""
	skippedBefore := index.SkippedThisRun()
	build := func() error {
		if phases {
			index.SetPhaseLines(said)
			defer index.SetPhaseLines(nil)
		}
		if check != nil {
			return index.EnsureChecked(check, force, progress)
		}
		return index.Ensure(dir, "", force, progress)
	}
	draw := func() error { return withBuildProgress(build) }
	if quiet {
		// The live display paints the same progress the sink above is
		// discarding, and it paints it to stdout.
		draw = func() error { return timeBuild(build) }
	}
	if err := withWarmupStatus(dir, draw); err != nil {
		// The command whose whole job is building the index used to pass the
		// syscall through — `mkdir /…/index.db.tmp: permission denied` names
		// an internal temp path and no fix, while every reading command has
		// said what to change since ensureError was written (#798).
		return ensureError(dir, err)
	}
	clearWarmupSentinel()
	// Inside the branch, not in its initializer: Go runs an `if` initializer
	// before it tests the condition, so the freshness walk — every registered
	// store, every candidate statted, the longest part of this command on a slow
	// volume — ran on every `deja index` and its answer was thrown away whenever
	// progress had already said what happened (#3500).
	if progress.n == 0 {
		fresh, n := index.UpToDate(dir, "")
		fmt.Fprintln(said, indexQuietOutcome(fresh, n))
	}
	// After the counts, beside the "no longer on disk" line the build printed:
	// the sessions a harness is about to delete, which the index keeps.
	printRetentionDue(said, time.Now())
	// Two transcripts can carry the same harness:id — two files with the same
	// name in different projects. Both stay searchable, but one manifest row
	// holds them, so one project name covers both. Silence was the worst part
	// of it: the indexer counted every session on disk while the manifest held
	// fewer, and nothing connected the two numbers (#698).
	// A store deja could not read loses its sessions from recall, and the index
	// run is where someone is looking at the counts — doctor knowing about it
	// is not the same as saying it here (#818).
	unread := sayUnreadable(dir)
	// An exclusion applies at ingest, so it covers nothing already indexed —
	// and the sequence someone actually performs is to set the pattern, run
	// this command, and sync. That used to return silently having applied
	// nothing, and the export that followed still carried the project (#1307).
	if index.ExclusionsChanged(dir) {
		fmt.Fprintln(os.Stderr, "deja: the exclude list changed since this index was built — `deja index --rebuild` applies it to sessions already indexed")
	}
	sayAliasesChanged(dir)
	// The parse count and the indexed count differ by exactly these, and this
	// is where both are on screen (#868).
	if n := index.ReportEmptySessions(); n > 0 {
		fmt.Fprintf(said, "deja: %d transcript%s held no message deja could index — not counted as %s\n",
			n, pluralS(n), pluralSessionWord(n))
	}
	// Said on every pass that reads a shared id's copies again, so it is a
	// count like the ones above and --quiet keeps it out.
	if n := index.ReportCollisions(); n > 0 {
		fmt.Fprintf(said, "deja: %d session%s %s an id with another transcript — each pair is filed under one project, the one whose file sorts first\n", n, pluralS(n), verbShare(n))
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
		fmt.Fprintf(said, "deja: indexed %d session%s, %d message%s%s — the per-harness lines above count transcripts, not rows\n",
			b.Sessions, pluralS(b.Sessions), b.Messages, pluralS(b.Messages), tookSuffix(b.Took))
	}
	// A machine with no agent history built an empty index and said nothing:
	// the step whose whole job is filling memory returned to the prompt after
	// a bare "indexing ..." line, and the state (no history anywhere, or a
	// store behind a permission wall) only surfaced on the next command.
	// Only when the index is empty too: a pass that found no transcript left
	// on disk still holds the sessions it keeps searchable, and the line
	// above has just said so (#4221).
	//
	// A store behind a permission wall is still named when the index holds
	// other sessions, without the "nothing to index" half: that line is the
	// only pointer to the store this pass could not read.
	if b := index.LastBuild; b.Sessions == 0 && b.Messages == 0 {
		denied := deniedStoreCount()
		switch {
		case indexIsEmpty(dir) && (denied > 0 || noAgentHistoryFound()):
			fmt.Fprintln(os.Stderr, emptyIndexReason(b, index.ReportEvictedFiles()))
		case denied > 0:
			fmt.Fprintln(os.Stderr, deniedStoresLine(denied, index.ReportEvictedFiles()))
		}
	}
	if !quiet {
		maybeFirstIndexGreeting(dir)
	}
	// The live display erases itself on the way out, so a rebuild on a
	// terminal ended with an empty screen — three seconds of animation and no
	// record of what was built. Piped output has said it all along; this is
	// the same two numbers for the reader who watched it happen (#867).
	if b := index.LastBuild; !b.Initial && b.Messages > 0 && logoWanted(os.Stdout) && os.Getenv("DEJA_WARMUP_SENTINEL") == "" {
		fmt.Fprintf(said, "deja: indexed %d session%s, %d message%s%s\n", b.Sessions, pluralS(b.Sessions), b.Messages, pluralS(b.Messages), tookSuffix(b.Took))
	}
	return partialIndex(unread, index.SkippedThisRun()-skippedBefore)
}

// exitPartial is the status of a `deja index` that wrote the index but left
// part of the input unread: a path it could not read, or lines it skipped in
// what it read this run. A cron job or a timer could not tell that from a
// clean run, because both exited 0 (#5396). 1 stays "the index was not
// written"; 2 is left alone, since a hook host reads it as "block".
const exitPartial = 3

// exitStatus is an error that has already been said and carries the status
// the process exits with.
type exitStatus int //nolint:errname // index_partial_exit_test.go names it

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// partialIndex is the outcome of an index run that wrote the index: nil, or
// exitPartial when something it was given was not read.
func partialIndex(unreadable, skipped int) error {
	if unreadable > 0 || skipped > 0 {
		return exitStatus(exitPartial)
	}
	return nil
}

// sayAliasesChanged says the project alias file changed since the build. The
// aliases name sessions at ingest, so the edit renames nothing already indexed;
// the rebuild is the reader's to start, it rereads every store (#5373).
func sayAliasesChanged(dir string) {
	if index.ProjectAliasesChanged(dir) {
		fmt.Fprintf(os.Stderr, "deja: %s changed since this index was built — `deja index --rebuild` renames the sessions already indexed\n", reportPath(sources.ProjectAliasPath()))
	}
}

// sayUnreadable names, per store, the paths the index could not read. A store
// deja could not read loses its sessions from recall, and the index run is
// where someone is looking at the counts — doctor knowing about it is not the
// same as saying it here (#818). Returns how many there were.
func sayUnreadable(dir string) int {
	health := index.IngestHealth(dir)
	total := 0
	for _, h := range sortedHarnesses(health) {
		e := health[h]
		if e.FailedFiles == 0 {
			continue
		}
		total += e.FailedFiles
		// "deja: deja: 1 path could not be read" is the notes file, and reads
		// like a stutter. It is the user's own writing, so it says so (#901).
		name := h
		if h == "deja" {
			name = "your notes"
		}
		fmt.Fprintf(os.Stderr, "deja: %s: %d path%s could not be read — `deja doctor` names %s\n",
			name, e.FailedFiles, pluralS(e.FailedFiles), pluralWhich(e.FailedFiles))
	}
	return total
}

func cmdHookContext(dir string, rest []string) error {
	plain := false
	once := false
	// --mcode answers in MiniMax Code's shape (hook_mcode.go).
	mcodeHookOutput = mcodeHookFlag(rest)
	for _, a := range rest {
		switch a {
		case "--plain", "-plain":
			plain = true
		// --once is for a host with no usable session-start event, where the
		// digest has to ride the per-prompt hook instead: it turns the payload
		// flag on from the command line, so the same session gets it once.
		case "--once", "-once":
			once = true
		// --strict is for a host that validates the hook's JSON and discards
		// the whole output over a key it does not know. It drops the receipt
		// line and keeps the context (see hook_strict.go).
		case "--strict", "-strict":
			strictHookOutput = true
		// --copilot answers in Copilot CLI's flat shape, the only one it reads
		// (see hook_strict.go).
		case "--copilot", "-copilot":
			copilotHookOutput = true
		// --notes is for a host that shows a person nothing from its hooks —
		// dsh and OpenClaw put the plain digest in front of the model only. It
		// prints the notes meant for the person, and nothing else, for the
		// plugin to add to a reply the person does read.
		// A plugin reply can land in a group chat (OpenClaw's /deja), so the
		// notes here never quote the person's own questions.
		case "--notes", "-notes":
			if notes := safeNote(humanNotesFor(dir, false)); notes != "" {
				fmt.Fprintln(os.Stdout, notes)
			}
			return nil
		}
	}
	_ = runHookContextMode(dir, plain, once)
	return nil
}

func cmdLast(dir string, rest []string, sourceInstance string) error {
	n, o, sinceRaw, err := parseLast(rest)
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
	// Uncut, because the count the reader is told about has to be the count
	// they would get by asking again — and the trust policy filters below,
	// after the cut, so a total taken before it promised rows the policy would
	// not hand over (#2638). RecentMatching builds the whole matching set
	// before cutting anyway, so nothing is read twice.
	ss, err := recentMatching(dir, 0, o)
	if err != nil {
		return err
	}
	// The listing is titles and project names, which is exactly what the trust
	// policy exists to keep off the screen — and it was the one path that never
	// consulted it, while `search` under the same rule refused and said so
	// (#937). Listing counts as browsing: the search activation governs it.
	ss, policyHidden := policyFilterSessionsCounted(policy.ActivationSearch, ss)
	total := len(ss)
	if n > 0 && len(ss) > n {
		ss = ss[:n]
	}
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
	// The cut is silent everywhere else on this screen: ten rows read as the
	// whole answer, and the two rules that withhold rows here already have a
	// line above the list. `last` takes its count as a bare argument, so that
	// is the form the way out takes (#2638).
	if total > len(ss) {
		fmt.Fprintf(os.Stderr, "deja: showing %d of %d — `deja last %d` shows the rest\n", len(ss), total, total)
	}
	markForkTitles(dir, ss)
	printLastRows(os.Stdout, ss)
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

// printLastRows lists sessions one per line in the form the search result
// header uses — harness, project, day, short id — with the columns padded so
// a screenful scans down, and the title after them. On a terminal the harness
// is coloured, the day dimmed and the title cut to the width; a pipe gets every
// title whole.
func printLastRows(w io.Writer, ss []model.Session) {
	type row struct{ tag, project, when, id, title string }
	rows := make([]row, 0, len(ss))
	wTag, wProject, wWhen, wID := 0, 0, 0, 0
	for _, s := range ss {
		// A session whose timestamp was missing or unparseable carries the Go
		// zero time, and "0001-01-01" reads as corrupted data rather than as a
		// missing field (#765). The reader's zone, like the brief and stats
		// (#849).
		when := "-"
		if !s.Updated.IsZero() {
			when = search.DisplayDate(s.Updated)
		}
		// Project, id and title are text a harness wrote, and this is one
		// line: an escape byte in any of them recolours the rest of the
		// listing and a carriage return rewinds it (#1090).
		r := row{
			tag:     "[" + s.Harness + "]",
			project: redact.SafeForDisplay(displayProject(s)),
			when:    when,
			id:      redact.SafeForDisplay(search.ShortID(s.ID)),
		}
		title := s.Title
		if title == "" {
			title = firstUserTitle(s)
		}
		// SafeForDisplay keeps a newline on purpose — the reading surfaces are
		// the session's own layout — but this is one row of a listing, and a
		// note title carries whatever a person wrote by hand (#2058).
		if title = search.SafeNoteTitle(redact.SafeForDisplay(title)); title != "" {
			// A session with no user turn borrows the assistant's opening line
			// (#692), and unmarked it read like the reader's own question
			// (#1100).
			if s.AgentTitle {
				title = "agent: " + title
			}
		}
		r.title = title
		wTag = max(wTag, termwidth.Columns(r.tag))
		wProject = max(wProject, termwidth.Columns(r.project))
		wWhen = max(wWhen, termwidth.Columns(r.when))
		wID = max(wID, termwidth.Columns(r.id))
		rows = append(rows, r)
	}
	// A long project name would push every title off a narrow screen; past
	// this it stops setting the column for the rest.
	wProject = min(wProject, 24)
	color, width := search.ColorOK(w), printableWidth(w)
	pad := func(s string, n int) string {
		if gap := n - termwidth.Columns(s); gap > 0 {
			return s + strings.Repeat(" ", gap)
		}
		return s
	}
	for i, r := range rows {
		tag := pad(r.tag, wTag)
		if color {
			tag = search.HarnessTag(ss[i].Harness, true) + strings.Repeat(" ", len(tag)-len(r.tag))
		}
		when := pad(r.when, wWhen)
		if color {
			when = statDim + when + statReset
		}
		head := tag + " " + pad(r.project, wProject) + "  " + when + "  " + pad(r.id, wID)
		line := head
		if r.title != "" {
			title := r.title
			if width > 0 {
				used := wTag + 1 + max(wProject, termwidth.Columns(r.project)) + 2 + wWhen + 2 + wID + 2
				// Too little room beside the columns and the title goes under
				// them, the way a search hit's quote does, rather than being
				// cut to a few words.
				if width-used < 32 {
					fmt.Fprintln(w, strings.TrimRight(line, " "))
					fmt.Fprintln(w, "  "+cutToWidth(title, width-2))
					continue
				}
				title = cutToWidth(title, width-used)
			}
			line += "  " + title
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
}

// cutToWidth shortens s to width columns with an ellipsis, ending on a whole
// word where one is near. Width zero or less leaves it alone.
func cutToWidth(s string, width int) string {
	if width <= 0 || termwidth.Columns(s) <= width {
		return s
	}
	if width < 8 {
		width = 8
	}
	cut := strings.TrimRight(termwidth.Cut(s, width-1), " ")
	if at := strings.LastIndex(cut, " "); at > 0 && termwidth.Columns(cut[at:]) <= 14 {
		cut = strings.TrimRight(cut[:at], " ")
	}
	return cut + "…"
}

// fitNotice wraps a one-line notice to the terminal w writes to, at spaces,
// with the continuation indented under "deja: ". A pipe gets it unchanged.
func fitNotice(w io.Writer, note string) string {
	body, nl := strings.CutSuffix(note, "\n")
	if strings.Contains(body, "\n") {
		return note
	}
	out := termwidth.Indent(body, printableWidth(w), "", "      ")
	if nl {
		out += "\n"
	}
	return out
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

// stampedAheadHits counts the same thing over a result set rather than a
// listing. Search ranks on recency among other things, so the sessions whose
// stamp cannot place them are the ones it places first.
func stampedAheadHits(hits []search.Hit, now time.Time) int {
	n := 0
	for _, h := range hits {
		if index.StampedAhead(h.Session.Updated, now) {
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
