// Command deja indexes the session transcripts of the coding agents on this
// machine and answers recall, blame and fix questions from them.
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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
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
	var ss []model.Session //nolint:prealloc // the count is whatever each store holds
	for _, harness := range []string{"claude", "codex", "aider", "gemini", "cursor", "antigravity", "grok", "qwen", "kimi", "goose", "hermes"} {
		ss = append(ss, loadAll(harness)...)
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
	"completion": true, "update": true,
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
	"blame": runBlame,
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
	case s.Kind != "" && strings.HasPrefix(s.Project, "imported:"):
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
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--json":
			o.json = true
		case "--harness", "--offset", "--limit":
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs value", a)
			}
			i++
			if strings.TrimSpace(args[i]) == "" { //nolint:gosec // bounds checked before i++
				// Empty is how "no filter" is spelled inside deja (#1612).
				return o, fmt.Errorf("%s needs value", a)
			}
			if a == "--harness" {
				o.harness = args[i] //nolint:gosec // bounds checked before i++
				continue
			}
			v := args[i] //nolint:gosec // bounds checked before i++
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
	if full, ok, ferr := findByPrefix(dir, whole.ID); ferr == nil && ok {
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
		id := "deja-note-" + strings.ReplaceAll(k, ":", "-")
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

// ambiguousJSONPrefix refuses a --json read whose prefix names more than one
// session, and says what separates them.
func ambiguousJSONPrefix(dir, id string) error {
	pol := policy.Load()
	n := index.PrefixMatchesAllowed(dir, id, func(project string) bool {
		return pol.Allows(policy.ActivationSearch, project)
	})
	if n <= 1 {
		return nil
	}
	if hs := index.PrefixHarnesses(dir, id); len(hs) > 1 {
		return fmt.Errorf("%d sessions share the id %q — --json reads one; add --harness %s", len(hs), id, strings.Join(hs, " or --harness "))
	}
	return fmt.Errorf("%d sessions match %q — --json reads one; use a longer prefix (`deja last` prints ids whole)", n, id)
}

// searchAllows is the rule the ambiguity counts are taken under (#2401).
func searchAllows() func(string) bool {
	pol := policy.Load()
	return func(project string) bool { return pol.Allows(policy.ActivationSearch, project) }
}

// ambiguousJSONPrefixIn is ambiguousJSONPrefix within one harness: --harness
// narrows the prefix, and a prefix it still leaves ambiguous is refused the
// same way.
func ambiguousJSONPrefixIn(dir, id, harness string) error {
	if n := index.PrefixMatchesIn(dir, id, harness, searchAllows()); n > 1 {
		return fmt.Errorf("%d %s sessions match %q — --json reads one; use a longer prefix (`deja last` prints ids whole)", n, harness, id)
	}
	return nil
}

// noteAmbiguousPrefixIn is noteAmbiguousPrefix within one harness.
func noteAmbiguousPrefixIn(dir, id, harness, action string) {
	if n := index.PrefixMatchesIn(dir, id, harness, searchAllows()); n > 1 {
		fmt.Fprintf(os.Stderr, "deja: %d %s sessions match %q — %s the most recent; use a longer prefix for another\n", n, harness, id, action)
	}
}

// pickedOnScreen is the session the interactive screen handed to resume or
// handoff. It is looked up by its exact identity, with no refresh first: after
// an upgrade the refresh is a full rebuild, and the person who picked a
// session watched "waiting for it to finish" under a closed screen. And an id
// is not a prefix: one that starts another session's id handed off "the most
// recent" of them, which need not be the one picked.
var pickedOnScreen *model.Session

func findByPrefix(dir, p string) (model.Session, bool, error) { //nolint:unparam // handoff.go and coverage_extra_test.go read the error
	if s := pickedOnScreen; s != nil && p == s.ID {
		if full, ok, err := index.FindByIdentity(dir, s.Harness, s.ID); err == nil && ok {
			return full, true, nil
		}
	}
	if err := ensureForCLIRead(dir); err == nil {
		if s, ok, err := lookupUnlessNew(dir, func() (model.Session, bool, error) { return index.FindByPrefix(dir, p) }); err == nil {
			if ok {
				noteForgottenSource(s, p, true)
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
// The prefix is resolved among that harness's sessions: picking the newest
// across every harness and then refusing it said no session matches while one
// in the named harness started with the prefix.
func findByPrefixHarness(dir, p, harness string) (model.Session, bool) {
	if err := ensureForCLIRead(dir); err == nil {
		if s, ok, err := lookupUnlessNew(dir, func() (model.Session, bool, error) { return index.FindByPrefixIn(dir, p, harness) }); err == nil {
			if ok {
				noteForgottenSource(s, p, true)
			}
			return s, ok
		}
	}
	var ss []model.Session
	for _, s := range append(loadFileSources(), sources.LoadOpencodePrefix(p)...) {
		if strings.EqualFold(s.Harness, harness) {
			ss = append(ss, s)
		}
	}
	return search.FindByPrefix(ss, p)
}

// ensureForCLIRead brings the index up to date before `last` or `show` reads
// it, unless another deja holds the index lock. A hook refresh or `deja index`
// holds it for its whole pass, and a scheduled `deja last` sat at no CPU for
// over an hour behind them while the snapshot on disk had the answer. The
// holder may be a reader that updates nothing, so the refresh goes to the
// detached warmup, as a search's does. The line goes out once: `show
// --harness` comes through here twice when the exact id misses.
func ensureForCLIRead(dir string) error {
	busy, err := index.EnsureUnlessBusy(dir, os.Stderr)
	answeredFromSnapshot.Store(busy)
	if line := manualIndexLine(dir); err == nil && line != "" {
		saidManualIndex.Do(func() { fmt.Fprintln(os.Stderr, line) })
	}
	if busy {
		requestWarmup(dir)
		saidReadingAsItWas.Do(func() {
			c := index.SnapshotCoverage(dir)
			c.Stale = true
			fmt.Fprintln(os.Stderr, "deja: "+coverageLine(c))
		})
	}
	return err
}

var saidReadingAsItWas sync.Once

// saidManualIndex keeps the index-date line to one per command.
var saidManualIndex sync.Once

// answeredFromSnapshot is whether the last ensureForCLIRead skipped the update
// because another deja held the lock.
var answeredFromSnapshot atomic.Bool

// lookupUnlessNew runs an id lookup against the snapshot and, when it misses
// while another deja holds the lock, waits for the index and looks again. A
// lock held at session start is a hook refresh reading the session that just
// began, so the id someone copied from it is the one the snapshot cannot have
// yet: answering "no session matches" for it at once was worse than the wait
// the snapshot read saves.
func lookupUnlessNew(dir string, find func() (model.Session, bool, error)) (model.Session, bool, error) {
	s, ok, err := find()
	if err != nil || ok || !answeredFromSnapshot.Load() {
		return s, ok, err
	}
	if index.Ensure(dir, "", false, os.Stderr) != nil {
		return s, ok, err
	}
	return find()
}

func recent(dir string, n int) ([]model.Session, error) {
	return recentMatching(dir, n, search.Options{})
}

func recentMatching(dir string, n int, o search.Options) ([]model.Session, error) {
	if err := ensureForCLIRead(dir); err == nil {
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
		} else if ss, _, err := index.RecentMatchingCounted(dir, n, o); err == nil {
			return ss, nil
		}
	}
	ss := filterRecentSources(loadFileSources(), o)
	if o.Harness == "" || o.Harness == "opencode" {
		ss = append(ss, filterRecentSources(sources.LoadOpencodeRecent(n), o)...)
	}
	return search.Recent(ss, n), nil
}

var lastFlags = []string{"--json", "--project", "--harness", "--from", "--since", "--role", "--limit"}

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
			v := args[i] //nolint:gosec // bounds checked before i++
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
				if flagName(a) == "--limit" {
					// The two commands beside this one take --limit, so the
					// reader is carrying it over rather than guessing; the
					// count last takes is a bare argument (#3405).
					return n, o, sinceRaw, fmt.Errorf("last: unknown flag %q — the count is a bare argument, `deja last 3`", a)
				}
				return n, o, sinceRaw, unknownFlag("last", a, lastFlags)
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
		case "--harness", "--project", "--since", "--role", "--limit", "--session", "--sort":
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
			case "--sort":
				switch strings.ToLower(v) {
				case search.SortDate, "newest":
					o.Sort = search.SortDate
				case search.SortRelevance:
					o.Sort = search.SortRelevance
				default:
					return o, fmt.Errorf("--sort takes date or relevance, not %q", v)
				}
			case "--limit":
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 || n > 100 {
					return o, errors.New("--limit needs an integer from 1 to 100")
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
			if nearestSearchFlag(a) != "" {
				return o, unknownFlag("search", a, searchFlags)
			}
			// A flag deja takes elsewhere is not a typo and is nowhere near a
			// search flag by edit distance, so it went into the query and the
			// search that would have found everything reported nothing (#2249).
			if cmd := flagsOfOtherCommands[a]; cmd != "" {
				return o, fmt.Errorf("%s is a flag of `deja %s`, not of search — put it after `--` to search for the text", a, cmd)
			}
			// A --word after the query is a flag deja does not have. Taken as
			// a query term it turned `deja goroutines --bogus` into a search
			// for both words that found nothing, where the word alone had 40
			// hits. A query that starts with one (`--retry budget`), a phrase
			// with a dash inside, or a single dash is still a query.
			if len(q) > 0 && flagShaped(a) {
				return o, fmt.Errorf("search: unknown flag %q — `deja search --help` lists its flags; put it after `--` to search for the text", a)
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
		return o, errors.New("query required")
	}
	return o, nil
}

// flagShaped reports whether a token reads as a long flag: two dashes, then
// letters, digits and inner dashes, nothing else.
func flagShaped(a string) bool {
	if len(a) < 3 || !strings.HasPrefix(a, "--") {
		return false
	}
	for i, r := range a[2:] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9', r == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

// flagsOfOtherCommands names the command each flag belongs to, for the tokens
// that are real deja flags somewhere but not here. Only exact matches: a query
// may legitimately start with a dash, and `--` still ends option parsing.
var flagsOfOtherCommands = map[string]string{
	"--offset":           "show",
	"--span":             "restore",
	"--out":              "restore",
	"--force":            "restore",
	"--tag":              "remember",
	"--deep":             "doctor",
	"--offline":          "doctor",
	"--dry-run":          "forget",
	"--all-matches":      "forget",
	"--list":             "forget",
	"--unforget":         "forget",
	"--before":           "forget",
	"--to":               "handoff",
	"--exec":             "resume",
	"--write-back":       "resume",
	"--app":              "resume",
	"--plain":            "hook-prompt",
	"--crush":            "hook-tool",
	"--junie":            "hook-prompt",
	"--no-open":          "view",
	"--full":             "sync export",
	"--include-imported": "sync export",
	"--peer":             "sync export",
	"--pull":             "sync ssh",
	"--both":             "sync ssh",
	"--from":             "last",
	"--last":             "log",
	"--seed":             "bench",
}

// searchFlags is every flag the bare search form accepts, for the typo check.
// The flags each converted command takes, in the order its own parser lists
// them. A list that drifts from its parser costs a wrong suggestion, not a
// wrong refusal, and unknown_flag_lists_test.go holds them together.
var (
	indexFlags  = []string{"--rebuild", "--quiet"}
	showFlags   = []string{"--json", "--harness", "--offset", "--limit"}
	forgetFlags = []string{
		"--list", "--dry-run", "--all-matches",
		"--session", "--project", "--before", "--unforget",
	}
	statsFlags = []string{
		"--json", "--html", "--redaction", "--impact", "--card", "--year",
		"--harness", "--project", "--since", "--role",
	}
	doctorFlags   = []string{"--json", "--offline", "--deep", "--all"}
	viewFlags     = []string{"--out", "--no-open"}
	logFlags      = []string{"--json", "--last"}
	rememberFlags = []string{"--project", "--tag"}
	restoreFlags  = []string{"--force", "--span", "--out", "--all"}
	promoteFlags  = []string{"--state", "--note", "--to", "--tag"}
	handoffFlags  = []string{"--to", "--exec"}
)

var searchFlags = []string{
	"--json", "--re", "--all", "--no-embed", "--rebuild",
	"--harness", "--project", "--since", "--role", "--limit", "--session", "--sort",
}

// nearestSearchFlag names the search flag a token was probably meant to be.
// The rule it introduced is now every command's, in nearestKnownFlag; this is
// the search-shaped name for it, kept because search is the one command where
// the answer also decides whether a dashed token is a query term.
//
// --sort is left out of the near misses: --soft, --port and --short are one
// edit from it and are things people search for (`git reset --soft`).
func nearestSearchFlag(a string) string {
	return nearestKnownFlag(a, searchNearMissFlags)
}

var searchNearMissFlags = func() []string {
	var out []string
	for _, f := range searchFlags {
		if f != "--sort" {
			out = append(out, f)
		}
	}
	return out
}()

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
	if before, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(before)
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
	"hook-codewhale":    true,
	"hook-context":      true,
	"hook-goose":        true,
	"hook-goose-prompt": true,
	"hook-mcp-call":     true,
	"hook-precompact":   true,
	"hook-refresh":      true,
	"hook-session-end":  true,
	"hook-stop":         true,
	"reasonix-ext":      true,
	"warmup-status":     true,
}

func printUsage() {
	fmt.Print(wrapUsage(usageText(), printableWidth(os.Stdout)))
}

// usageWidth is the column budget for the parts of help that are laid out
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
			return fmt.Sprintf(" — the days regrouped when this machine's zone changed; it is %#q now", want)
		}
	}
	return ""
}

// showNeedsID is the refusal show gives with no argument. "id-prefix" names a
// thing the reader has no way to produce on their own; promote has pointed at
// `deja last` all along, and show, share and resume are the three commands
// reached for right after a search result (#1063).
const showNeedsID = "show needs an id prefix (see `deja last`)"

// noSessionMatches is the refusal for an id that names no session: the bucket
// that moved when there is one, otherwise where the ids are listed (#1063).
func noSessionMatches(dir, id string) error {
	if hint := movedBucketHint(dir, id); hint != "" {
		return fmt.Errorf("no session matches %q%s", id, hint)
	}
	return fmt.Errorf("no session matches %q — `deja last` lists recent ones", id)
}

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

// indexIsEmpty reports whether the index holds no session at all. An index
// that cannot be counted counts as empty on purpose, so the caller falls back
// to the empty-index hint it printed before.
func indexIsEmpty(dir string) bool {
	n, err := index.SessionCount(dir)
	return err != nil || n == 0
}

// emptyIndexReason opens the empty-index sentence. "Nothing to index yet" is
// for a machine deja has never seen history from; a run that has just evicted a
// store says what went away instead, because the line above it has already told
// the reader what was lost and the two must not contradict each other (#1762).
func emptyIndexReason(_ index.BuildSummary, evicted int) string {
	if evicted > 0 {
		return emptyIndexHint(fmt.Sprintf("%d indexed file%s went away with the store that held %s, and nothing is left to index",
			evicted, pluralS(evicted), pluralWhich(evicted)))
	}
	return emptyIndexHint("nothing to index yet")
}

// deniedStoresLine names the stores deja could not read on a pass whose index
// still holds sessions, with the files the pass evicted when there were any.
func deniedStoresLine(denied, evicted int) string {
	line := fmt.Sprintf("%d store%s could not be read (permission denied); `deja doctor` names %s",
		denied, pluralS(denied), pluralWhich(denied))
	if evicted > 0 {
		line = fmt.Sprintf("%d indexed file%s went away with the store that held %s — %s",
			evicted, pluralS(evicted), pluralWhich(evicted), line)
	}
	return "deja: " + line
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
	// A store deja is not allowed to open, before anything else. The sessions
	// are there, behind a permission wall doctor and sources both name (#1020)
	// — and the branch used to sit inside the no-history one, so it was
	// unreachable in the case that matters: a store whose files deja can see
	// and cannot read counts as history, so the answer was "run `deja index`",
	// which is what the reader had just done and cannot help (#3585).
	if denied := deniedStoreCount(); denied > 0 {
		return fmt.Sprintf("deja: %s — %d store%s could not be read (permission denied); `deja doctor` names %s",
			what, denied, pluralS(denied), pluralWhich(denied))
	}
	if noAgentHistoryFound() {
		return "deja: " + what + " — no agent history was found on this machine; `deja sources` shows where deja looked"
	}
	return "deja: " + what + " — run `deja index`, or `deja doctor` to see which agent stores were found"
}

// emptyStoreLine is emptyIndexHint for a command whose answer goes to stdout
// without deja's prefix, when the index holds no session at all. tests,
// secrets, recap and rules candidates each said their own nothing there —
// "no provider keys … in your agent history" read as an all-clear on a machine
// with no history to check.
func emptyStoreLine(dir, what string) (string, bool) {
	if n, err := index.SessionCount(dir); err != nil || n > 0 {
		return "", false
	}
	// At a terminal, at its width and between words; piped, as one line.
	return fitLine(os.Stdout, strings.TrimPrefix(emptyIndexHint(what), "deja: ")), true
}

// newestSessionNote is the tail of an empty windowed answer: when the newest
// session it may show is older than the window, the date and the --since that
// reaches it. The demo store of a machine idle for three months answered
// `deja recap` with "nothing in the last 7d" and nothing else.
func newestSessionNote(dir, command string, window time.Duration) string {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return ""
	}
	pol := policy.Load()
	var newest time.Time
	for _, m := range metas {
		if m.Updated.After(newest) && pol.Allows(policy.ActivationSearch, m.Project) {
			newest = m.Updated
		}
	}
	if newest.IsZero() || time.Since(newest) <= window {
		return ""
	}
	days := int(time.Since(newest).Hours()/24) + 1
	return fmt.Sprintf(" — the newest session is from %s; `deja %s --since %dd` reaches it",
		newest.Local().Format("2006-01-02"), command, days)
}

// fitLine wraps a sentence between words at the width of the terminal w is,
// and leaves it whole for a pipe.
func fitLine(w io.Writer, line string) string {
	if n := printableWidth(w); n > 0 {
		return wrapProse(line, n)
	}
	return line
}

// sinceArg is the --since value as the reader typed it.
func sinceArg(args []string) string {
	for i, a := range args {
		if a == "--since" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// countNoun is "1 session" or "3 sessions"; the forget dry run printed
// "1 session(s)".
func countNoun(n int, noun string) string {
	return fmt.Sprintf("%d %s%s", n, noun, pluralS(n))
}

func forgetCounts(sessions, messages int) string {
	return countNoun(sessions, "session") + ", " + countNoun(messages, "message")
}

// deniedStoreCount reports how many harness stores exist but cannot be opened.
// Opened, not parsed: the probe stops before doctor's parser (#4272).
func deniedStoreCount() int {
	n := 0
	for _, check := range deniedStoreChecks() {
		if store, _, _ := probeDoctorStore(check); store.State == "denied" {
			n++
		}
	}
	return n
}

// deniedStoreChecks is doctorStoreChecks, swappable so a test can count what
// the probe parses.
var deniedStoreChecks = doctorStoreChecks

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
	if !index.Unwritable(err) && !errors.Is(err, syscall.ENOSPC) {
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
	// Worded for the reader already, command included (#5385).
	if index.IsManualStop(err) {
		return err
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
		// Only a write inside the build directory is a build the disk went
		// away from. A read of an index never built fails the same way, and
		// sent a fresh machine to reconnect a disk that was fine (#5057).
		var pe *fs.PathError
		if errors.As(err, &pe) && !pathUnder(pe.Path, dir+".tmp") {
			return fmt.Errorf("nothing is indexed yet (%s) — `deja index` builds it", dir)
		}
		return fmt.Errorf("the index directory went away mid-build (%s) — the disk it lives on may have been unmounted; the index already there is unharmed, so reconnect it and run `deja index` again, or point DEJA_INDEX_DIR somewhere local", dir)
	}
	// Already worded where it was raised — the leftover-swap case names the
	// directory to remove and the command to rerun, the refused index path
	// names the file it will not delete, and "ensure:" in front of either is
	// internal noise (#1009).
	for _, worded := range []string{"an earlier index swap left ", "the index path "} {
		if strings.HasPrefix(err.Error(), worded) {
			return err
		}
	}
	return fmt.Errorf("ensure: %w", err)
}

// pathUnder reports whether p is dir or lies inside it.
func pathUnder(p, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// indexLooksUnmounted reports an index whose directory and parent are both
// missing where a build could not simply create them: a volume that went
// away, not a machine that never built an index. It returns the parent to
// name.
func indexLooksUnmounted(dir string) (string, bool) {
	if dir == "" {
		dir = index.DefaultDir()
	}
	parent := filepath.Dir(dir)
	if dirExists(dir) || dirExists(parent) {
		return parent, false
	}
	return parent, !freshHomePath(parent)
}

// searchValueFlags take a value, so "--flag=value" has to become two arguments
// before the parser sees it. Anything else keeps its equals sign: a query may
// legitimately contain one.
var searchValueFlags = map[string]bool{
	"--harness": true, "--project": true, "--since": true,
	"--role": true, "--limit": true, "--session": true, "--sort": true,
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

// flagName is the flag without its value, so `--limit=3` and `--limit 3` are
// recognised as the same spelling.
func flagName(a string) string {
	if before, _, ok := strings.Cut(a, "="); ok {
		return before
	}
	return a
}

// capTierHits bounds the two tiers that build their own hits — the ranked
// relevance list and the error signature — which never went through the cap in
// RunDetailed: `--limit 3` printed the whole retrieval window of fifty
// sessions there while the exact tier printed three (#3345). With no flag they
// keep the window they have always served, which the JSON envelope's own test
// pins; only what the reader asked for binds them.
func capTierHits(hits []search.Hit, o search.Options) ([]search.Hit, bool) {
	if o.Limit == 0 {
		return hits, false
	}
	// A limit the reader typed binds even beside --all, which is how the exact
	// tier has always read the pair: --all lifts the default, and the number
	// asked for is still the number wanted. Letting --all win here made the
	// same two flags mean opposite things depending on which tier answered
	// (review of #3345).
	return search.CapHits(hits, o.Limit, false)
}

// newestFirstNote is the line above a date-ordered answer. The exact tiers
// read the newest of the whole match, so the line says only the order; the
// relevance and error tiers rank a window first, and the newest of that
// window is not the newest of the match when it ran past it.
func newestFirstNote(o search.Options, shown int, tierWindow bool, window int) string {
	switch {
	case tierWindow && window > 0:
		return fmt.Sprintf("newest first among the %d closest of %s", window, grouped(o.Total))
	case o.Total > shown:
		// Under --limit the list is the top of the order, and "all 5
		// matches" sat under "showing 5 of 40".
		return "newest first"
	}
	return fmt.Sprintf("all %d matches, newest first", shown)
}
