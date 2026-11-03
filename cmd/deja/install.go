package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

type installResult struct {
	Path, Action string
	// Note is what the caller should say besides the action: a file deja knows
	// about, could not act on, and left as it found it. Empty in every ordinary
	// case, so a printer can append it blind (#2218).
	Note string
	// also holds the other files an -auto target changed in the same run;
	// the printer folds them into Note, the kept-snapshot line needs the paths.
	also []string
}

// touched is every file this result changed: the one it is named for and the
// ones that rode along in the note (#3171).
func (r installResult) touched() []string {
	return append([]string{r.Path}, r.also...)
}

func runInstall(dir string, args []string, uninstall bool) error {
	// Before this run prints anything: stdout may be a pipe that closes while
	// the run still has state to write — `deja install aider | head -1` takes
	// one line and goes, with the config edits already on disk and
	// `defer recordWiring` still to come. Go answers the next write on such a
	// stdout with SIGPIPE and dies, skipping every deferred call, so those
	// edits were never recorded and uninstall had nothing to put the reader's
	// file back with (#5322). Ignored, the write fails as EPIPE and the run
	// goes on losing lines rather than state.
	ignoreSIGPIPE()
	// Every path deja writes hangs off the home directory, and homeDir()
	// answers "" when it cannot find one. filepath.Join("", ".claude") is
	// ".claude", so with HOME unset install wrote .claude/, .claude.json and
	// .config/deja/wiring.json into whatever directory it was run from — a
	// repository, usually — and reported success (#1690). A config directory
	// only means anything at an absolute location.
	if homeDir() == "" {
		verb := "install"
		if uninstall {
			verb = "uninstall"
		}
		return fmt.Errorf("%s cannot find your home directory — set HOME to the account deja should wire", verb)
	}
	// What this run reads is what its writes are checked against (#4561).
	readBytesMu.Lock()
	readBytes = map[string][]byte{}
	runFirstSeen = map[string]*runSeen{}
	readBytesMu.Unlock()
	defer func() {
		readBytesMu.Lock()
		readBytes = nil
		runFirstSeen = nil
		readBytesMu.Unlock()
	}()
	removingWiring = uninstall
	defer func() { removingWiring = false }()
	guidance, noIndex, targetArgs := parseInstallArgs(args)
	skippingGuidance = !guidance
	defer func() { skippingGuidance = false }()
	verb := "install"
	if uninstall {
		verb = "uninstall"
	}
	targets, nothing, err := resolveInstallTargets(verb, targetArgs, uninstall)
	if err != nil {
		return err
	}
	if nothing {
		return nil
	}
	// One install at a time. Every writer here reads a config, edits it and
	// writes it back, which is three steps a second process can land in the
	// middle of: with `deja install claude-auto` and `deja install statusline`
	// started together, eight runs in twelve lost one of the two wirings —
	// both edit ~/.claude/settings.json, and the later write was built on the
	// file as it was before the earlier one. The pairing is not contrived: the
	// repair after an upgrade runs an install from the hook path, so a session
	// starting while someone types `deja install` is exactly this (#3691).
	// An uninstall on a machine that never had deja must leave nothing at all
	// behind, the lock included (#676), so it only takes one where deja's own
	// directory is already there. Taken once the targets are known, and only
	// when one of them is real: `deja install frobnicate` created
	// ~/.config/deja/install.lock for a run that wires nothing (#5052).
	if slices.ContainsFunc(targets, knownInstallTarget) && (!uninstall || isRealDir(filepath.Dir(installLockPath()))) {
		if release, ok, _ := holdInstallLock(true); ok {
			defer release()
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	// Remembered so a later upgrade can refresh exactly these and nothing
	// else: the generators change, the files on disk do not.
	if uninstall {
		removingTargets = make(map[string]bool, len(targets))
		for _, t := range targets {
			removingTargets[guidanceHarness(t)] = true
		}
		defer func() { removingTargets = nil }()
	}
	defer recordWiring(targets, uninstall)
	ir := &installRun{
		exe:       exe,
		uninstall: uninstall,
		guidance:  guidance,
		banner:    !uninstall && (targetArgs[0] == "--auto" || targetArgs[0] == "--all") && logoWanted(os.Stdout),
		saidNotes: map[string]bool{},
	}
	banner := ir.banner
	// Install builds the index itself once the targets are written. goose-auto
	// reads recall on the way to write its block, and that read started a
	// detached build: install then waited on it, built nothing and printed
	// "index: built (0 sessions, 0 messages)" over a full store (#4268).
	installBuildsIndex = !uninstall && !noIndex
	defer func() { installBuildsIndex = false }()
	for _, t := range targets {
		ir.target(t)
	}
	// The CLI skill rides with guidance, like the command file: both are text
	// telling the agent deja exists, and --no-guidance is the switch for
	// someone who wants the plumbing without either. It goes in beside the MCP
	// skill rather than instead of it — a session holding the tools uses them,
	// one that does not still knows the shell path (#1320).
	if guidance {
		ir.cliSkill(targets)
	}
	// Held, not returned. "finished what it could" is a summary of the run, and
	// returning it here made it an early exit from reporting the run: the
	// banner never rendered, so nobody was told which harnesses had been wired,
	// and the index build below was skipped, leaving those harnesses wired with
	// nothing to read (#2721).
	refusal := ir.refusal()
	// Every install builds, not only --auto and --all. Installing is the one
	// moment a person has already accepted a wait — they just ran an installer
	// — and spending the build here is what keeps the first real use, usually
	// the first agent turn, instant. A target that refused does not change that
	// for the ones that did not (#2721).
	// Something has to have landed. When every target refused there is nothing
	// to build a store for and nothing to celebrate, and printing the whole
	// success screen ahead of the refusal is worse than the silence #2721 was
	// about.
	wired := len(ir.done) > 0 || ir.mcpCount+ir.hookCount+ir.guidanceCount > 0
	if !uninstall && !noIndex && wired {
		installIndexWarmup(dir, ir.mcpCount, ir.hookCount, ir.guidanceCount,
			targetArgs[0] == "--auto" || targetArgs[0] == "--all")
	}
	// An uninstall keeps the snapshot it took of a config the reader already
	// had — neither the config nor its snapshot is deja's to delete (#840) —
	// and said nothing about them, on a screen that otherwise names what it
	// keeps. #2487 added gemini's line for exactly that reason; nine files on
	// disk are the same shape (#2676).
	if uninstall {
		if line := keptSnapshotsLine(ir.touchedPaths); line != "" {
			fmt.Fprint(os.Stderr, line)
		}
	}
	// What happens next, for a run that named its targets: the wiring is read
	// when an agent session starts, so one already open does not have it yet,
	// and one already open keeps calling deja after an uninstall.
	if !banner && ir.changed > 0 && targetArgs[0] != "--auto" && targetArgs[0] != "--all" {
		if uninstall {
			fmt.Fprintln(os.Stderr, "next: restart sessions already open in that agent so they stop calling deja")
		} else {
			fmt.Fprintln(os.Stderr, "next: start a new session in that agent; the wiring loads at session start")
		}
	}
	if banner && wired {
		printInstallBanner(dir, ir.done, targetArgs[0] == "--all")
	}
	if n := usage.StatusCounters(dir); !uninstall {
		if tip := maybeStarTip(dir, n.Recalls+n.Injected); tip != "" {
			fmt.Fprintf(os.Stderr, "\ndeja has answered %d recalls on this machine — if it helps: gh repo star vshulcz/deja-vu\n", n.Recalls+n.Injected)
		}
	}
	return refusal
}

// keptSnapshotsLine names the .bak files still beside the configs this run
// touched. Only those: a snapshot next to a file deja handled is one deja took,
// while an unrelated .bak in the tree belongs to whoever made it.
func keptSnapshotsLine(touched []string) string {
	seen := map[string]bool{}
	var paths []string
	for _, p := range touched {
		if p == "" {
			continue
		}
		bak := p + ".bak"
		if seen[bak] {
			continue
		}
		if _, err := os.Stat(bak); err != nil {
			continue
		}
		seen[bak] = true
		paths = append(paths, bak)
	}
	if len(paths) == 0 {
		return ""
	}
	sort.Strings(paths)
	where := reportPath(paths[0])
	if len(paths) > 1 {
		where = reportPath(paths[0]) + " and " + fmt.Sprintf("%d more", len(paths)-1)
	}
	return fmt.Sprintf("deja: kept %d snapshot%s of configs you already had — %s — yours to remove\n",
		len(paths), pluralS(len(paths)), where)
}

// indexBuiltLine is what an install says about the store it just built. The
// harness lines above it count what the parser read; this counts what reached
// the index, and the two disagree by deja's own recall blocks — stripped
// before anything counts them. Both numbers are true and they answer different
// questions, so the difference is named rather than left to be noticed (#3386).
func indexBuiltLine(b index.BuildSummary) string {
	line := fmt.Sprintf("index: built (%d session%s, %d message%s",
		b.Sessions, pluralS(b.Sessions), b.Messages, pluralS(b.Messages))
	if b.Dropped > 0 {
		line += fmt.Sprintf(" — %d of deja's own blocks not indexed", b.Dropped)
	}
	return line + ")\n"
}

// installBuiltLine is indexBuiltLine for what install's Ensure did. When
// another deja built the store while this one waited on the lock, this process
// counted nothing, and the line said the store was empty; it names what the
// store holds instead (#4268).
func installBuiltLine(dir string) string {
	if b := index.LastBuild; b.Sessions == 0 && b.Messages == 0 {
		if n, err := index.SessionCount(dir); err == nil && n > 0 {
			return fmt.Sprintf("index: built (%d session%s)\n", n, pluralS(n))
		}
	}
	return indexBuiltLine(index.LastBuild)
}

// installBuildsIndex is set while an install that builds the index after its
// targets runs them, so nothing on the way asks for a build of its own.
var installBuildsIndex bool

func installIndexWarmup(dir string, mcp, hooks, guidance int, summary bool) {
	built := false
	detected := 0
	if !index.HasManifest(dir) {
		for _, check := range doctorStoreChecks() {
			store, _ := inspectDoctorStore(check)
			if store.Files > 0 {
				detected++
				// With the progress display, so twenty seconds of silence does
				// not look like a hang on the very first command.
				prepareFirstIndexGreeting(dir)
				err := withBuildProgress(func() error { return index.Ensure(dir, "", false, os.Stderr) })
				index.SuppressHarnessNarration = false
				built = err == nil
				break
			}
		}
	}
	if !summary {
		if built {
			fmt.Fprint(os.Stderr, installBuiltLine(dir))
		}
		return
	}
	fmt.Fprintf(os.Stderr, "installed: %d MCP, %d hooks, %d guidance files\n", mcp, hooks, guidance)
	if built {
		fmt.Fprint(os.Stderr, installBuiltLine(dir))
	} else if !index.HasManifest(dir) && detected > 0 {
		fmt.Fprintln(os.Stderr, "next: run `deja index` to finish building memory")
	} else if n := deniedStoreCount(); !index.HasManifest(dir) && n > 0 {
		// "no agent history detected" is a claim about the machine, and the
		// install said it over a store deja is not allowed to open — the very
		// first step told the newcomer they had nothing, and only doctor, three
		// commands later, named the permission wall. Same reading as #1020.
		fmt.Fprintf(os.Stderr, "index: %d agent store%s could not be read (permission denied) — `deja doctor` names %s\n",
			n, pluralS(n), pluralWhich(n))
	} else if !index.HasManifest(dir) {
		fmt.Fprintln(os.Stderr, "index: no agent history detected")
	} else {
		fmt.Fprintln(os.Stderr, "index: already built")
	}
	// A question from their own history, already run, beats the placeholder:
	// the placeholder is the reader's problem to solve at the moment they are
	// least able to (#575). The placeholder stays for a machine with too little
	// history to build one from.
	if p, ok := buildTryPrompt(dir); ok {
		printTryPrompt(os.Stderr, p)
	} else {
		fmt.Fprintln(os.Stderr, "try: deja \"something you fixed weeks ago\"")
	}
	printInstallProof(dir)
}

// printInstallProof shows the "starts full" moment right in the install: a
// few real sessions deja already indexed from this machine's history, so the
// value is visible before the first agent session ever runs.
func printInstallProof(dir string) {
	printMemoryProofOf(dir, "deja already knows this machine:", nil, installLead(dir))
	if xray := projectXRayLine(dir, howCwd()); xray != "" {
		fmt.Fprintln(os.Stderr, "  "+xray)
	}
	// A blank line and a glyph set it apart from the proof: an aside, not a
	// step of the install, and the last thing on the screen rather than the
	// first.
	if line := starLine(dir); line != "" {
		fmt.Fprintln(os.Stderr, "\n  ★ "+line)
	}
}

// printMemoryProofOf is the proof narrowed to the rows a caller can honestly
// claim. `sync import` says "from the machine you came from" and was handed
// the recent list, so on a batch that added nothing visible it offered this
// machine's own sessions as the evidence a transfer had landed (#988).
// lead is one line printed under the heading before the listing, or "". Only
// the install passes one: `sync import` shows sessions from another machine
// under a heading that says so, and the error *this* machine keeps hitting
// would be a lie there (#2966).
func printMemoryProofOf(dir, heading string, keep func(model.Session) bool, lead string) {
	if !index.HasManifest(dir) {
		return
	}
	want := 12
	if keep != nil {
		want = 60
	}
	recent, err := index.Recent(dir, want)
	if err != nil || len(recent) == 0 {
		return
	}
	if keep != nil {
		var only []model.Session
		for _, s := range recent {
			if keep(s) {
				only = append(only, s)
			}
		}
		recent = only
		if len(recent) == 0 {
			return
		}
	}
	// The proof is a listing, and a listing obeys the trust policy (#937). On a
	// machine whose rule keeps imported sessions out of recall this block was
	// printing their project and first line, and closing with "ask your agent
	// about any of these — it will remember", which is exactly what the rule
	// prevents (#951).
	recent, hidden := policyFilterSessionsCounted(policy.ActivationSearch, recent)
	if len(recent) == 0 {
		if hidden > 0 {
			fmt.Fprintf(os.Stderr, "\n%s\n", policyHiddenNote(policy.ActivationSearch, hidden))
		}
		return
	}
	seenProject := map[string]bool{}
	shown := 0
	var lines []string
	for _, s := range recent {
		if seenProject[s.Project] || s.Project == "" || s.Project == "-" {
			continue
		}
		title := firstUserTitle(s)
		if title == "" {
			title = s.Title
		}
		if title == "" {
			continue
		}
		if len(title) > 76 {
			title = digest.UTF8SafeCut(title, 76) + "…"
		}
		seenProject[s.Project] = true
		// A sync batch carries no timestamps, so an imported session has no
		// date at all — and an empty slot left `[claude · imported:solo · ]`
		// on the one screen that exists to show the memory arrived. `last`
		// says the same thing with a dash (#964).
		// In the reader's calendar, the way every other date deja prints is:
		// an imported record keeps the sender's offset, so a batch from +14
		// was dated a day ahead of what `last` said on the same machine
		// seconds later (#1047, #1050 — found twice, from both ends).
		date := "-"
		if !s.Updated.IsZero() {
			date = s.Updated.Local().Format("Jan 2")
		}
		lines = append(lines, fmt.Sprintf("  [%s · %s · %s] %s", s.Harness, s.Project, date, title))
		shown++
		if shown == 3 {
			break
		}
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "\n"+heading)
	// The one thing here the reader does not already know. The titles below
	// are their own words from last week; the error their agents hit in a
	// dozen sessions is what nobody kept score of, and it is the reason to
	// keep reading (#2966).
	if lead != "" {
		fmt.Fprintln(os.Stderr, "  "+lead)
	}
	for _, l := range lines {
		fmt.Fprintln(os.Stderr, l)
	}
	fmt.Fprintln(os.Stderr, "ask your agent about any of these — it will remember.")
	// Under the proof, because it is the moment the reader has just been shown
	// something real and might want to know where the rest is (#3030).
	if line := whereToLook(dir); line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
}

// recurringErrorLine is the top row of `deja friction`, as one sentence, or ""
// when nothing clears the threshold — a pattern claimed over a single sighting
// would be the tool inventing one.
func recurringErrorLine(dir string) string {
	scan, err := scanFriction(dir, policy.Load())
	if err != nil {
		return ""
	}
	// The line promises the agent will be told, and a settled wall is one it
	// will not be told about (#5628).
	settled := policy.LoadSettled()
	var rows []frictionRow
	for _, r := range scan.rows {
		if !settled.Hides(r.id, r.started) {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return ""
	}
	r := rows[0]
	if r.n < index.FrictionMinSessions {
		return ""
	}
	return fmt.Sprintf("this machine has hit `%s` in %d sessions (%s) — your agent will be told before it runs into it again", //nolint:gocritic // %#q falls back to double quotes for text with a backquote
		trimFriction(r.line), r.n, strings.Join(r.harnesses, ", "))
}

// shortHome contracts the home directory to ~ for display.
func shortHome(p string) string {
	if h := homeDir(); h != "" && strings.HasPrefix(p, h) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

func installIndexHint(dir string) string {
	if index.HasManifest(dir) {
		return ""
	}
	checks := doctorStoreChecks()
	detected := 0
	onlyMissingOrEmpty := true
	var paths []string
	seen := map[string]bool{}
	for _, check := range checks {
		store, _ := inspectDoctorStore(check)
		if store.Files > 0 {
			detected++
		}
		// A store whose disk is not attached is not history found here either;
		// the text rows have separated the two since #933 and the JSON form
		// learned to in #999.
		if store.State != "missing" && store.State != "empty" && store.State != "unplugged" {
			onlyMissingOrEmpty = false
		}
		for _, path := range store.Paths {
			if path != "" && !seen[path] {
				seen[path] = true
				paths = append(paths, shortHome(path))
			}
		}
	}
	if onlyMissingOrEmpty {
		return "no agent history found on this machine; checked " + strings.Join(paths, ", ")
	}
	if detected == 0 {
		// Every store deja found is one it cannot open: "index 0 agent stores"
		// is a zero with an instruction that would change nothing.
		if n := deniedStoreCount(); n > 0 {
			return fmt.Sprintf("%d agent store%s could not be read (permission denied) — `deja doctor` names %s", n, pluralS(n), pluralWhich(n))
		}
	}
	return fmt.Sprintf("next: run `deja index` to index %d agent stores", detected)
}

// refusalRemedy picks the sentence that closes an install summary. It was one
// hardcoded line telling the reader to check permissions, which is the wrong
// place to send someone whose config merely has a comment in it: five JSON
// targets refused for a syntax error and every one was blamed on file
// permissions (#1663). Same shape as #808, #931, #907 and #1116 — an error
// wearing a permissions label it had not earned.
func refusalRemedy(errs []error) string {
	perms := 0
	for _, err := range errs {
		if errors.Is(err, fs.ErrPermission) {
			perms++
		}
	}
	if perms > 0 && perms == len(errs) {
		if len(errs) == 1 {
			return "check that path's permissions and run it again"
		}
		return "check those paths' permissions and run it again"
	}
	if len(errs) == 1 {
		return "fix what it reports and run it again"
	}
	return "fix what each one reports and run it again"
}

func existingTargets() []string {
	checks := existingTargetChecks()
	var out []string
	for name, p := range checks {
		if _, err := os.Stat(p); err == nil {
			out = append(out, name)
		} else if name == "claude-code" {
			if _, err := os.Stat(sources.ClaudeJSONPath()); err == nil {
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// installTargetAliases are the other names a target answers to. Each is a name
// something already prints: `claude` is what the plugin pages and half the
// harness notes call Claude Code, and `dsh` is what the DeepSeek Harness calls
// itself in its own binary, its plugin and its guide page — which printed
// `deja install dsh-auto` while only `deepseek-auto` resolved.
//
// One map rather than an extra case label per target, because the name has to
// reach `guidanceHarness` too: aliasing only the wiring installed the server
// and skipped the skill.
var installTargetAliases = map[string]string{
	"claude":   "claude-code",
	"dsh":      "deepseek",
	"dsh-auto": "deepseek-auto",
}

// knownInstallTarget reports whether installTarget has a case for target.
func knownInstallTarget(target string) bool {
	if _, ok := installTargetAliases[target]; ok {
		return true
	}
	return target == "sync-timer" || slices.Contains(installTargetNames(), target)
}

func installTarget(target, exe string, uninstall bool) (installResult, error) {
	if canonical, ok := installTargetAliases[target]; ok {
		target = canonical
	}
	if f, ok := installTargetCase(target); ok {
		return f(exe, uninstall)
	}
	return installResult{}, unknownTargetError(target)
}

// wroteAll folds what an -auto target did into the one result its caller
// prints. Reporting only the last write said "unchanged" about a run that had
// just rewired the MCP entry, and named the hook file while doing it (#2396).
// The first write that changed something is the answer; anything else it
// changed rides along in the note, and when nothing changed the last write
// stands, since that is the file the target is named for — unless only an
// earlier one has something to say. Kiro's switched-off MCP entry read as an
// unchanged steering file, with the note gone (#4302).
func wroteAll(rs ...installResult) installResult {
	out := rs[len(rs)-1]
	if out.Note == "" {
		for _, r := range slices.Backward(rs) {
			if r.Path != "" && r.Note != "" {
				out = r
				break
			}
		}
	}
	for _, r := range rs {
		if r.Path != "" && r.Action != "unchanged" {
			out = r
			break
		}
	}
	var also []string
	seen := map[string]bool{}
	for _, r := range rs {
		if r.Path != "" && r.Path == out.Path && r.Note != "" && !strings.Contains(out.Note, r.Note) {
			// A second write to the same file with something to say: the
			// status line left in the settings file the hooks went into.
			also = append(also, r.Note)
		}
		if r.Path == "" || r.Path == out.Path {
			continue
		}
		// Every file this target handled rides along, changed or not, and so
		// does anything the other result was already carrying: the
		// kept-snapshot line reads these paths, and a second run that changes
		// nothing still has a snapshot beside each of them (review of #3389).
		if !slices.Contains(out.also, r.Path) {
			out.also = append(out.also, r.Path)
		}
		out.also = append(out.also, r.also...)
		if r.Action == "unchanged" {
			// A write that changed nothing can still have something to say: an
			// entry left switched off is off whether or not this run touched
			// the file, and dropping the note read as a clean install (#4302).
			if r.Note != "" {
				also = append(also, fmt.Sprintf("%s unchanged — %s", shortHome(r.Path), r.Note))
			}
			continue
		}
		// Two writes to one file — TRAE's MCP entry and status line — are one
		// line in the report, not "created" and then "updated" about it.
		if seen[r.Path] {
			if r.Note != "" {
				also = append(also, r.Note)
			}
			continue
		}
		seen[r.Path] = true
		line := fmt.Sprintf("also %s %s", r.Action, shortHome(r.Path))
		// The other write's own note rides with its line: gemini's extension
		// says what switch it left on, and that was lost with the result.
		if r.Note != "" {
			line += " — " + r.Note
		}
		also = append(also, line)
	}
	for _, line := range also {
		if out.Note != "" {
			out.Note += "; "
		}
		out.Note += line
	}
	return out
}

func installClaudeAuto(exe string, uninstall bool) (installResult, error) {
	// ~/.claude.json lives outside ~/.claude, so it went first and was wired
	// by a run that then refused on the directory, .bak and all (#4560). The
	// halves that can fail on ~/.claude go first, and the MCP file is asked
	// whether it can be written before either of them.
	if err := configWritable(sources.ClaudeJSONPath()); err != nil {
		return installResult{}, err
	}
	hook, err := installClaudeHook(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	cmds, err := installClaudeCommands(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	mcp, err := installClaude(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, cmds, hook), nil
}

func installAntigravityAuto(exe string, uninstall bool) (installResult, error) {
	mcp, err := installMCPJSON(filepath.Join(antigravityConfigHome(), "mcp_config.json"), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	plugin, err := installAntigravityPlugin(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	status, err := installAntigravityStatusline(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	ideStatus, err := installForkStatusItemIfPresent("Antigravity", ".antigravity", exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	if ideStatus.Path != "" {
		return wroteAll(mcp, plugin, status, ideStatus), nil
	}
	return wroteAll(mcp, plugin, status), nil
}

func installOpenClawAuto(exe string, uninstall bool) (installResult, error) {
	// Every section first: a refusal after the MCP entry went in would leave
	// the target half wired.
	if !uninstall {
		if err := openclawIncludeRefuse(exe, true); err != nil {
			return installResult{}, err
		}
	}
	// The entry alone: the refusal above covered it, and the note below names
	// what is left in included files for all three.
	mcp, err := installOpenClawMCPEntry(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	// The plugin covers the prompt; the bootstrap hook covers the session.
	plugin, err := installOpenClawPlugin(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	hooks, err := installOpenClawHooks(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	if !uninstall {
		fmt.Println("openclaw: " + openclawLocalTUINote)
	}
	res := wroteAll(mcp, plugin, hooks)
	if uninstall {
		res.Note = joinNotesWith("; ", res.Note, openclawIncludedLeftNote(true))
	}
	return res, nil
}

// openclawLocalTUINote is said where openclaw-auto reads as wired. OpenClaw's
// embedded TUI loads plugins and runs no hook of theirs, nor any internal hook
// (2026.10.4, checked with a plugin registered on every hook name), so deja
// cannot give it a digest, recall or a live stamp (#5271).
const openclawLocalTUINote = "`openclaw chat` and `openclaw tui --local` run no plugin or hook — no digest or recall there; `openclaw tui` on the gateway and `openclaw agent --local` have both"

func installHermesAuto(exe string, uninstall bool) (installResult, error) {
	if !uninstall {
		if err := hermesPluginsPreflight(); err != nil {
			return installResult{}, err
		}
	}
	mcp, err := installHermesMCP(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	plugin, err := installHermesPlugin(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	provider, err := installHermesMemoryProvider(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, plugin, provider), nil
}

func installPiAuto(exe string, uninstall bool) (installResult, error) {
	mcp, err := installPiMCP(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	ext, err := installPiExtension(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, ext), nil
}

// cursorAutoPreflight runs the refusals of cursor-auto's hook and status
// line halves before the MCP half writes anything. Only hooks.json was
// checked, so a read-only cli-config.json was refused after mcp.json and
// hooks.json had been written, and the target reported a refusal while
// wired (#5164).
func cursorAutoPreflight(exe string, uninstall bool) error {
	hookExe := exe
	if !uninstall {
		// The launcher path the writers will name, without writing it yet.
		if p := dejaLauncherPath(); p != "" {
			hookExe = p
		}
	}
	if _, err := cursorHooksUpdate(hookExe, uninstall, false); err != nil {
		return err
	}
	// Not on the way out: the plain cursor target removes mcp.json whatever
	// this says, so refusing here would only leave the hooks running.
	if uninstall {
		return nil
	}
	_, err := jsonStatuslineUpdate(cursorCLIConfigPath(), []string{"statusLine"},
		map[string]any{"type": "command", "command": hookRun(hookExe, "statusline")}, uninstall, false)
	return err
}

func installCursorAuto(exe string, uninstall bool) (installResult, error) {
	mcp, err := installCursor(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	hooks, err := installCursorHooks(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	status, err := installCursorStatusline(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	ideStatus, err := installForkStatusItemIfPresent("Cursor", ".cursor", exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	if ideStatus.Path != "" {
		return wroteAll(mcp, hooks, status, ideStatus), nil
	}
	return wroteAll(mcp, hooks, status), nil
}

func installCodexAuto(exe string, uninstall bool) (installResult, error) {
	mcp, err := installCodex(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	res, err := installCodexHooks(exe, uninstall)
	if err == nil {
		res = wroteAll(mcp, res)
	}
	// Writing the file is not the same as codex agreeing to run it. Said here
	// because this is the moment someone is watching, and because the state it
	// warns about is invisible: everything on disk looks right and no memory
	// arrives.
	if err == nil && !uninstall {
		if !codexHasSeenItsHook() {
			fmt.Println("codex: open codex once and approve the hook (/hooks) — until then it runs nothing, `codex exec` included")
		} else if st := codexHookWiringState(); st.state == "wired" && st.approved < st.pinned {
			// Trust is per hook, so a machine that approved the older ones
			// runs those and skips one this install added: SessionEnd, on an
			// upgrade past #4545. Only doctor said so (#4572).
			n := st.pinned - st.approved
			verb, them := "are", "them"
			if n == 1 {
				verb, them = "is", "it"
			}
			fmt.Printf("codex: %d of %d hooks %s new or changed — open codex once and approve %s (/hooks); until then codex runs only the others\n", n, st.pinned, verb, them)
		}
	}
	return res, err
}

func installOpencodeAuto(exe string, uninstall bool) (installResult, error) {
	mcp, mcpErr := installOpencode(exe, uninstall)
	// The two halves are independent: the plugin shells out to the hook
	// launcher and reads nothing from the config. A config deja declines to
	// edit — one that keeps its servers under `mcp.servers`, say — used to end
	// the whole target, so the machine was left with no digest and no
	// per-prompt recall either, and running the install again after adding the
	// server by hand was the only way out (#3937).
	plugin, err := installOpencodePlugin(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	tui, err := installOpencodeTUI(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	if mcpErr != nil {
		return wroteAll(plugin, tui), mcpErr
	}
	return wroteAll(mcp, plugin, tui), nil
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func antigravityConfigHome() string {
	return filepath.Join(homeDir(), ".gemini", "config")
}
