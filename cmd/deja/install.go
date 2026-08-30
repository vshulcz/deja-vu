package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
)

type installResult struct {
	Path, Action string
	// Note is what the caller should say besides the action: a file deja knows
	// about, could not act on, and left as it found it. Empty in every ordinary
	// case, so a printer can append it blind (#2218).
	Note string
}

func runInstall(dir string, args []string, uninstall bool) error {
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
	removingWiring = uninstall
	defer func() { removingWiring = false }()
	guidance := true
	noIndex := false
	var targetArgs []string
	for _, arg := range args {
		if arg == "--no-guidance" {
			guidance = false
			continue
		}
		// For scripted installs that do not want to spend the build here.
		if arg == "--no-index" {
			noIndex = true
			continue
		}
		// Take deja's version of a skill back, over one that has been edited.
		// Without it an edited skill is kept and named rather than replaced.
		if arg == "--force" {
			forceGuidance = true
			continue
		}
		targetArgs = append(targetArgs, arg)
	}
	verb := "install"
	if uninstall {
		verb = "uninstall"
	}
	// A flag deja does not know is named plainly by every other command; here
	// it fell into the target list, and the refusal then said the target was
	// missing while printing the target it had been given (#1078). At one
	// argument it fell through anyway and `deja install --nosuch` was reported
	// as an unknown target, with thirty-eight harness names for a remedy
	// (#1680). No target begins with a dash, so the shape alone settles it.
	for _, a := range targetArgs {
		if strings.HasPrefix(a, "-") && a != "--all" && a != "--auto" {
			return fmt.Errorf("%s: unknown flag %q — it takes a target plus --no-guidance, --no-index or --force", verb, a)
		}
	}
	if len(targetArgs) != 1 {
		// The first command a new machine runs, so a bare word they have to go
		// look up is the worst possible answer. Every other command in this
		// position prints the shape it wants (#830), and this one can do
		// better still: name the agents actually present here.
		if found := existingTargets(); len(found) > 0 {
			sort.Strings(found)
			return fmt.Errorf("%s needs a target — found here: %s (or --all, --auto)", verb, strings.Join(found, ", "))
		}
		return fmt.Errorf("%s needs a target — no agent config found here; `deja help` lists every target deja knows", verb)
	}
	targets := []string{targetArgs[0]}
	if targetArgs[0] == "--auto" {
		targets = nil
		for _, t := range existingTargets() {
			targets = append(targets, autoTargetFor(t))
		}
		if len(targets) == 0 {
			fmt.Println("no known agent config directories found")
			return nil
		}
	}
	if targetArgs[0] == "--all" {
		targets = existingTargets()
		if len(targets) == 0 {
			fmt.Println("no known agent config directories found")
			return nil
		}
	}
	// Uninstalling has to reach further than installing: --all wires MCP, but
	// --auto may have written hooks and plugins too, and leaving those behind
	// means every agent keeps shelling out to a binary the user just removed.
	if uninstall {
		targets = withAutoTargets(targets)
		// A name it does not know drops out of that expansion and leaves
		// nothing to do, so `deja uninstall claude-cod` printed not one word
		// and exited 0 — while `deja install claude-cod` names the near miss
		// (#2273). Someone removing deja by a half-remembered name was told it
		// worked, with the wiring still in place.
		if len(targets) == 0 {
			return unknownTargetError(targetArgs[0])
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
	saidNotes := map[string]bool{}
	banner := !uninstall && (targetArgs[0] == "--auto" || targetArgs[0] == "--all") && logoWanted(os.Stdout)
	type lineItem struct{ target, action, path, note string }
	// The real paths, not the display ones: an uninstall reports the snapshots
	// it left beside the configs it handled, and ~ does not stat (#2676).
	var touchedPaths []string
	var done []lineItem
	guidanceCount := 0
	mcpCount := 0
	hookCount := 0
	// One target that refuses to be written used to end the run, so an
	// uninstall stopped halfway: the hooks were gone, one guidance file was
	// left behind, and the reader was handed a syscall for a path they did not
	// choose. Everything else is still done, and what could not be is named at
	// the end (#902).
	var refused []string
	var refusedErrs []error
	note := func(t string, err error) {
		refused = append(refused, fmt.Sprintf("%s: %v", t, err))
		refusedErrs = append(refusedErrs, err)
	}
	for _, t := range targets {
		r, err := installTarget(t, exe, uninstall)
		if err != nil {
			note(t, err)
			continue
		}
		if guidance {
			gr, err := guidanceResult(t, uninstall)
			if err != nil {
				note(t, err)
				continue
			}
			// Two targets can share one guidance harness — `gemini` and
			// `gemini-auto` do — and a note about a file is about the file,
			// not about the target that noticed it.
			if gr.Note != "" && saidNotes[gr.Note] {
				gr.Note = ""
			} else if gr.Note != "" {
				saidNotes[gr.Note] = true
			}
			if gr.Path != "" && !banner {
				fmt.Println(guidanceOutput(t, gr))
			}
			if gr.Path != "" && uninstall {
				pruneGuidanceDirs(gr.Path)
			}
			if gr.Path != "" && !uninstall {
				guidanceCount++
			}
			// The command rides with guidance rather than with the wiring: both
			// are text telling the agent deja exists, and --no-guidance is the
			// switch for someone who wants the plumbing without either.
			cr, err := installCommandFile(guidanceHarness(t), exe, uninstall)
			if err != nil {
				note(t, err)
				continue
			}
			if cr.Path != "" && !banner {
				fmt.Printf("%s: command %s %s\n", t, cr.Action, cr.Path)
			}
			if cr.Path != "" && uninstall {
				pruneGuidanceDirs(cr.Path)
			}
		}
		touchedPaths = append(touchedPaths, r.Path)
		if banner {
			done = append(done, lineItem{t, r.Action, shortHome(r.Path), r.Note})
		} else {
			if r.Path == "" {
				fmt.Printf("%s: %s\n", t, r.Action)
			} else {
				fmt.Printf("%s: %s %s\n", t, r.Action, r.Path)
			}
			// Under the action rather than in place of it, the way guidance
			// prints its own note: the action is still what happened (#2218).
			if r.Note != "" {
				fmt.Printf("%s%s\n", strings.Repeat(" ", len(t)+2), r.Note)
			}
		}
		if !uninstall && t != "statusline" {
			mcpCount++
		}
		if !uninstall && strings.HasSuffix(t, "-auto") {
			hookCount++
		}
	}
	// The CLI skill rides with guidance, like the command file: both are text
	// telling the agent deja exists, and --no-guidance is the switch for
	// someone who wants the plumbing without either. It goes in beside the MCP
	// skill rather than instead of it — a session holding the tools uses them,
	// one that does not still knows the shell path (#1320).
	if guidance {
		var err error
		switch {
		case !uninstall:
			err = writeCLISkill()
		case !cliSkillStillWanted(targets):
			err = removeCLISkill()
		}
		if err != nil {
			note(cliSkillName, err)
		}
	}
	// Held, not returned. "finished what it could" is a summary of the run, and
	// returning it here made it an early exit from reporting the run: the
	// banner never rendered, so nobody was told which harnesses had been wired,
	// and the index build below was skipped, leaving those harnesses wired with
	// nothing to read (#2721).
	var refusal error
	if len(refused) > 0 {
		verb := "install"
		if uninstall {
			verb = "uninstall"
		}
		refusal = fmt.Errorf("%s finished what it could; %d target%s refused: %s — %s",
			verb, len(refused), pluralS(len(refused)), strings.Join(refused, "; "), refusalRemedy(refusedErrs))
	}
	// Every install builds, not only --auto and --all. Installing is the one
	// moment a person has already accepted a wait — they just ran an installer
	// — and spending the build here is what keeps the first real use, usually
	// the first agent turn, instant. A target that refused does not change that
	// for the ones that did not (#2721).
	// Something has to have landed. When every target refused there is nothing
	// to build a store for and nothing to celebrate, and printing the whole
	// success screen ahead of the refusal is worse than the silence #2721 was
	// about.
	wired := len(done) > 0 || mcpCount+hookCount+guidanceCount > 0
	if !uninstall && !noIndex && wired {
		installIndexWarmup(dir, mcpCount, hookCount, guidanceCount,
			targetArgs[0] == "--auto" || targetArgs[0] == "--all")
	}
	// An uninstall keeps the snapshot it took of a config the reader already
	// had — neither the config nor its snapshot is deja's to delete (#840) —
	// and said nothing about them, on a screen that otherwise names what it
	// keeps. #2487 added gemini's line for exactly that reason; nine files on
	// disk are the same shape (#2676).
	if uninstall {
		if line := keptSnapshotsLine(touchedPaths); line != "" {
			fmt.Fprint(os.Stderr, line)
		}
	}
	if banner && wired {
		info := append(brandInfo(), "")
		nameW := 0
		for _, d := range done {
			if len(d.target) > nameW {
				nameW = len(d.target)
			}
		}
		for _, d := range done {
			info = append(info, fmt.Sprintf("%-*s  %s%-9s%s %s", nameW, d.target, logoBold, d.action, logoReset, logoDim+d.path+logoReset))
			// Under its own row. The table is the documented path — the README
			// tells people to run `deja install --auto` — so a note that only
			// the line-by-line output carried was a note almost nobody read
			// (#2712).
			if d.note != "" {
				info = append(info, fmt.Sprintf("%-*s  %s", nameW, "", d.note))
			}
		}
		mood := moodReady
		if hint := installIndexHint(dir); hint != "" {
			info = append(info, "", hint)
			// wired up with nothing to recall yet: the cat has no reason to be
			// awake until some history exists
			if strings.HasPrefix(hint, "no agent history found") {
				mood = moodAsleep
			}
		}
		printLogoMood(os.Stdout, info, mood)
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
			b := index.LastBuild
			fmt.Fprintf(os.Stderr, "index: built (%d session%s, %d message%s)\n", b.Sessions, pluralS(b.Sessions), b.Messages, pluralS(b.Messages))
		}
		return
	}
	fmt.Fprintf(os.Stderr, "installed: %d MCP, %d hooks, %d guidance files\n", mcp, hooks, guidance)
	if built {
		b := index.LastBuild
		fmt.Fprintf(os.Stderr, "index: built (%d session%s, %d message%s)\n", b.Sessions, pluralS(b.Sessions), b.Messages, pluralS(b.Messages))
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
	fmt.Fprintln(os.Stderr, "try: deja \"something you fixed weeks ago\"")
	printInstallProof(dir)
}

// printInstallProof shows the "starts full" moment right in the install: a
// few real sessions deja already indexed from this machine's history, so the
// value is visible before the first agent session ever runs.
func printInstallProof(dir string) { printMemoryProof(dir, "deja already knows this machine:") }

// printMemoryProof is that same proof under a caller's heading. `sync import`
// ends the move to a new machine and said only "imported 59000 records" —
// deja's own unit, and nothing a person can check (#929).
func printMemoryProof(dir, heading string) { printMemoryProofOf(dir, heading, nil) }

// printMemoryProofOf is the proof narrowed to the rows a caller can honestly
// claim. `sync import` says "from the machine you came from" and was handed
// the recent list, so on a batch that added nothing visible it offered this
// machine's own sessions as the evidence a transfer had landed (#988).
func printMemoryProofOf(dir, heading string, keep func(model.Session) bool) {
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
	for _, l := range lines {
		fmt.Fprintln(os.Stderr, l)
	}
	fmt.Fprintln(os.Stderr, "ask your agent about any of these — it will remember.")
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
	checks := map[string]string{
		"claude-code": sources.ClaudeConfigDir(),
		"codex":       sources.CodexHome(),
		"opencode":    filepath.Join(opencodeConfigHome(), "opencode"),
		"cursor":      sources.CursorCLIHome(),
		"gemini":      filepath.Join(sources.GeminiHome(), "settings.json"),
		"antigravity": antigravityConfigHome(),
		"copilot":     filepath.Join(homeDir(), ".copilot"),
		"grok":        sources.GrokRoot(),
		"qwen":        sources.QwenConfigDir(),
		"kimi":        sources.KimiConfigDir(),
		"cline":       sources.ClineConfigDir(),
		"hermes":      sources.HermesHome(),
		"pi":          sources.PiConfigDir(),
		"omp":         sources.OmpConfigDir(),
		"deepseek":    sources.DSHHome(),
		"openclaw":    sources.OpenClawStateDir(),
		// aider keeps no config directory: its history file is what says it
		// has been used here. The binary alone would match every machine that
		// merely has it on PATH.
		"aider": filepath.Join(homeDir(), ".aider.chat.history.md"),
		// The session store, not the config directory: deja creates the
		// latter itself, which would make every machine look like a Goose
		// machine after one install.
		"goose": sources.GooseRoot(),
		"roo":   rooFirstRoot(),
	}
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

func installTarget(target, exe string, uninstall bool) (installResult, error) {
	switch target {
	case "claude-auto":
		if err := readableStrictJSON(filepath.Join(sources.ClaudeConfigDir(), "settings.json")); err != nil {
			return installResult{}, err
		}
		return installClaudeAuto(exe, uninstall)
	case "claude-code", "claude":
		return installClaude(exe, uninstall)
	case "codex":
		return installCodex(exe, uninstall)
	case "codex-auto":
		if err := readableStrictJSON(filepath.Join(sources.CodexHome(), "hooks.json")); err != nil {
			return installResult{}, err
		}
		return installCodexAuto(exe, uninstall)
	case "cursor":
		return installCursor(exe, uninstall)
	case "cursor-auto":
		if err := readableStrictJSON(filepath.Join(sources.CursorCLIHome(), "hooks.json")); err != nil {
			return installResult{}, err
		}
		return installCursorAuto(exe, uninstall)
	case "gemini":
		return installMCPJSON(filepath.Join(sources.GeminiHome(), "settings.json"), exe, uninstall)
	case "gemini-auto":
		// MCP first, then the hooks extension. `--auto` maps gemini to this
		// target alone, so installing only the extension left the harness
		// without the tools: its own `gemini mcp list` said "No MCP servers
		// configured" on a machine that had just run `deja install --auto`.
		// grok-auto pairs them the same way.
		// Same file, same reason as qwen-auto: whichever half may refuse goes
		// first (#2745).
		if _, err := installGeminiAuto(exe, uninstall); err != nil {
			return installResult{}, err
		}
		return installMCPJSON(filepath.Join(sources.GeminiHome(), "settings.json"), exe, uninstall)
	case "antigravity":
		return installMCPJSON(filepath.Join(antigravityConfigHome(), "mcp_config.json"), exe, uninstall)
	case "antigravity-auto":
		return installAntigravityAuto(exe, uninstall)
	case "grok":
		return installGrok(exe, uninstall)
	case "grok-auto":
		probe := []string{filepath.Join(sources.GrokHome(), "user-settings.json")}
		if !uninstall {
			// Uninstall removes the hook file rather than reading it, so a
			// file it can still take is not one to refuse over.
			probe = append(probe, grokHooksPath())
		}
		if err := readableStrictJSON(probe...); err != nil {
			return installResult{}, err
		}
		if _, err := installGrok(exe, uninstall); err != nil {
			return installResult{}, err
		}
		return installGrokAuto(exe, uninstall)
	case "qwen":
		return installMCPJSON(filepath.Join(sources.QwenConfigDir(), "settings.json"), exe, uninstall)
	case "qwen-auto":
		// The hook first: it lives in the same file as the MCP entry, and a
		// refusal after the entry was written left the target reported as
		// refused with half its wiring in the file and a .bak beside it
		// (#2745, the shape #2744 was about).
		if _, err := installQwenAuto(exe, uninstall); err != nil {
			return installResult{}, err
		}
		return installMCPJSON(filepath.Join(sources.QwenConfigDir(), "settings.json"), exe, uninstall)
	case "kimi":
		return installMCPJSON(filepath.Join(sources.KimiConfigDir(), "mcp.json"), exe, uninstall)
	case "kimi-auto":
		if _, err := installMCPJSON(filepath.Join(sources.KimiConfigDir(), "mcp.json"), exe, uninstall); err != nil {
			return installResult{}, err
		}
		return installKimiAuto(exe, uninstall)
	case "zed":
		return installZedMCP(sources.ZedSettingsPath(), exe, uninstall)
	case "cline":
		return installMCPJSON(sources.ClineMCPSettingsPath(), exe, uninstall)
	case "roo":
		return installRoo(exe, uninstall)
	case "cline-auto":
		if _, err := installMCPJSON(sources.ClineMCPSettingsPath(), exe, uninstall); err != nil {
			return installResult{}, err
		}
		return installClineAuto(exe, uninstall)
	case "copilot":
		return installCopilotMCP(exe, uninstall)
	case "hermes":
		return installHermesMCP(exe, uninstall)
	case "hermes-auto":
		return installHermesAuto(exe, uninstall)
	case "pi":
		return installMCPJSON(filepath.Join(sources.PiConfigDir(), "mcp.json"), exe, uninstall)
	case "pi-auto":
		return installPiAuto(exe, uninstall)
	case "omp":
		return installMCPJSON(filepath.Join(sources.OmpConfigDir(), "mcp.json"), exe, uninstall)
	case "omp-auto":
		return installOmpAuto(exe, uninstall)
	case "deepseek":
		return installDeepSeekMCP(exe, uninstall)
	case "deepseek-auto":
		return installDeepSeekAuto(exe, uninstall)
	case "openclaw":
		return installOpenClawMCP(exe, uninstall)
	case "openclaw-auto":
		return installOpenClawAuto(exe, uninstall)
	case "opencode":
		return installOpencode(exe, uninstall)
	case "opencode-auto":
		return installOpencodeAuto(exe, uninstall)
	case "aider":
		return installAider(exe, uninstall)
	case "goose":
		return installGoose(exe, uninstall)
	case "goose-auto":
		return installGooseAuto(exe, uninstall)
	case "statusline":
		return installStatusline(exe, uninstall)
	case "sync-timer":
		return installSyncTimer(exe, uninstall)
	default:
		return installResult{}, unknownTargetError(target)
	}
}

// wroteAll folds what an -auto target did into the one result its caller
// prints. Reporting only the last write said "unchanged" about a run that had
// just rewired the MCP entry, and named the hook file while doing it (#2396).
// The first write that changed something is the answer; anything else it
// changed rides along in the note, and when nothing changed the last write
// stands, since that is the file the target is named for.
func wroteAll(rs ...installResult) installResult {
	out := rs[len(rs)-1]
	for _, r := range rs {
		if r.Path != "" && r.Action != "unchanged" {
			out = r
			break
		}
	}
	var also []string
	for _, r := range rs {
		if r.Path == "" || r.Action == "unchanged" || r.Path == out.Path {
			continue
		}
		also = append(also, fmt.Sprintf("also %s %s", r.Action, shortHome(r.Path)))
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
	mcp, err := installClaude(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	cmds, err := installClaudeCommands(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	hook, err := installClaudeHook(exe, uninstall)
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
	return wroteAll(mcp, plugin), nil
}

func installOpenClawAuto(exe string, uninstall bool) (installResult, error) {
	mcp, err := installOpenClawMCP(exe, uninstall)
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
	return wroteAll(mcp, plugin, hooks), nil
}

func installHermesAuto(exe string, uninstall bool) (installResult, error) {
	mcp, err := installHermesMCP(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	plugin, err := installHermesPlugin(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, plugin), nil
}

func installPiAuto(exe string, uninstall bool) (installResult, error) {
	mcp, err := installMCPJSON(filepath.Join(sources.PiConfigDir(), "mcp.json"), exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	ext, err := installPiExtension(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, ext), nil
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
	return wroteAll(mcp, hooks), nil
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
	if err == nil && !uninstall && !codexHasSeenItsHook() {
		fmt.Println("codex: open codex once and approve the hook (/hooks) — until then it runs nothing, `codex exec` included")
	}
	return res, err
}

func installOpencodeAuto(exe string, uninstall bool) (installResult, error) {
	mcp, err := installOpencode(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	plugin, err := installOpencodePlugin(exe, uninstall)
	if err != nil {
		return installResult{}, err
	}
	return wroteAll(mcp, plugin), nil
}

// pruneGuidanceDirs drops the directories install had to create for a guidance
// file, once that file is gone. A skill lives at <config>/skills/deja-history/
// SKILL.md; both segments are deja's, and uninstall walked away from an empty
// skills/deja-history/ nobody else put there (#840). Matching those two names
// is the bound — every other guidance path (AGENTS.md, GEMINI.md) sits directly
// in a harness directory that is not ours to delete. os.Remove on a directory
// fails unless it is empty, which is the condition wanted: a skills/ that still
// holds someone else's skill stays.
func pruneGuidanceDirs(path string) {
	dir := filepath.Dir(path)
	if filepath.Base(dir) != "deja-history" || !isRealDir(dir) {
		return
	}
	if err := os.Remove(dir); err != nil {
		return
	}
	if dir = filepath.Dir(dir); filepath.Base(dir) == "skills" && isRealDir(dir) {
		_ = os.Remove(dir)
	}
}

// isRealDir reports whether p is a directory itself rather than a link to one.
// os.Remove unlinks a symlink whatever stands behind it, so the "only if empty"
// bound above does not hold for anyone who keeps skills/ in their dotfiles and
// symlinks it into place: the link would go while the skills it points at stay.
func isRealDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

// backupOnce reports whether it created the snapshot, so an uninstall can take
// its own back out afterwards without touching one the user made.
func backupOnce(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		return false, nil
	}
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	// Configs can carry MCP credentials; the snapshot is owner-only even
	// when the live file is looser.
	return true, os.WriteFile(bak, b, 0o600)
}

// removingWiring is set for the length of an uninstall run. Thirty-seven call
// sites write configs through writeIfChanged, and on the uninstall path each
// one computes "the file without deja in it" — which, for a file that does not
// exist, is an empty config it then creates (#676). The flag is process-wide
// because the command is: one run, one direction.
var removingWiring bool

// removingTargets names the targets the current uninstall run is removing.
// The shared-skill guard asks whether any other harness still reads
// ~/.agents/skills/deja-history/SKILL.md, and answers from the wiring record —
// which still lists every harness during `uninstall --all`, because
// recordWiring runs at the end. So each target in turn found another "still
// wanting" reader and the file was kept every time (#1683). A harness being
// removed in this same run is not a reader.
var removingTargets map[string]bool

// forceGuidance is set by `deja install --force`: replace a skill deja can see
// has been edited since it wrote it.
var forceGuidance bool

// configParseError names the file a parse refusal is about. The refusal is what
// a reader is sent to act on — doctor points at `deja install <targets>` when a
// rewire failed, and the parser's own words alone left them guessing which of
// the harness configs it had opened (#2214).
func configParseError(path string, err error) error {
	return fmt.Errorf("%s: %w", path, err)
}

// mcpBlock reads the object an MCP config keeps its servers in. A missing key
// is an empty block deja fills in; a key holding something else — a list, a
// string, null — is a config deja does not understand, and building a fresh
// object over it dropped whatever was there and reported an ordinary write
// (#2399). A config that will not parse is already refused this way, and the
// wrong shape is the case where something is actually lost.
func mcpBlock(root map[string]any, key, path string) (map[string]any, bool, error) {
	v, ok := root[key]
	if !ok || v == nil {
		return nil, false, nil
	}
	m, isObject := v.(map[string]any)
	if !isObject {
		// The writers that edit a file they opened themselves name it here;
		// the one that hands bytes back to its caller leaves the naming to it.
		if path == "" {
			return nil, false, fmt.Errorf("%q is not an object deja can edit — left as it was", key)
		}
		return nil, false, fmt.Errorf("%s: %q is not an object deja can edit — left as it was", path, key)
	}
	return m, true, nil
}

// mentionsDeja reports whether a config snapshot carries deja's own wiring.
// The markers are what every generator writes: the subcommands the hooks call,
// and the name of the MCP server and extension entries.
func mentionsDeja(b []byte) bool {
	// The subcommands rather than the binary's name: a snapshot names whatever
	// path deja was installed from, which need not end in "deja" at all.
	for _, marker := range []string{
		"hook-prompt", "hook-context", "hook-tool", "hook-goose", "hook-plan",
		"hook-precompact", "hook-antigravity", "deja:", "\"deja\"", "deja-recall",
		// The harnesses that do not write the word on its own: zed names the
		// server, dsh opens a block, codex and grok put the name in a TOML
		// table header, and aider only points at deja's context file. Without
		// them their snapshots survived an uninstall carrying deja's whole
		// entry, which is the one thing this exists to prevent (#2575, #2578).
		// TestEveryInstalledConfigIsRecognisableAsDejas installs every target
		// and holds this list against what they actually write.
		zedServerID, dshBlockStart, "[mcp_servers.deja]", "aider-context.md",
	} {
		if bytes.Contains(b, []byte(marker)) {
			return true
		}
	}
	return false
}

// lfText is the text of a config normalised to LF, for the writers that splice
// blocks by counting newlines. They then work in one convention and
// writeIfChanged puts the file's own endings back (#1668).
func lfText(b []byte) string {
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// matchLineEndings writes back the line endings the file already had. A
// Windows user's configs are CRLF, and install rewrote the JSON ones LF-only
// while leaving the TOML and YAML ones half and half — deja's appended block in
// LF inside a CRLF file (#1668). install_goose.go and guidance.go already did
// this for their own two files; every other writer comes through here.
//
// Only a file whose endings are *all* CRLF is treated as a CRLF file. A mixed
// one is left alone: converting it whole would rewrite lines deja never touched,
// which is the thing this exists to avoid.
func matchLineEndings(old, next []byte) []byte {
	if len(old) == 0 || !bytes.Contains(old, []byte("\r\n")) {
		return next
	}
	if bytes.Count(old, []byte("\n")) != bytes.Count(old, []byte("\r\n")) {
		return next
	}
	// Only the newlines that are not already CRLF, so a writer that converted
	// its own output first is not given a second carriage return.
	var b bytes.Buffer
	b.Grow(len(next) + bytes.Count(next, []byte("\n")))
	for i := 0; i < len(next); i++ {
		if next[i] == '\n' && (i == 0 || next[i-1] != '\r') {
			b.WriteByte('\r')
		}
		b.WriteByte(next[i])
	}
	return b.Bytes()
}

// createdByThisRun collects the configs this process created, so the record can
// keep them and a later uninstall can take back a file that turns out to hold
// nothing but empty containers (#2583).
var createdByThisRun []string

// structurallyEmptyConfig reports that a config holds nothing but the empty
// containers deja's writers leave behind. #840 deletes a file whose content is
// gone entirely; the structured writers never produce zero bytes — an emptied
// mcp.json is `{"mcpServers":{}}` — so the same rule needs the same reading of
// "nothing left in it".
func structurallyEmptyConfig(b []byte) bool {
	trimmed := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			return -1
		}
		return r
	}, string(b))
	switch trimmed {
	case "", "{}", "[]", "null",
		`{"mcpServers":{}}`, `{"mcp":{}}`, `{"mcp":{"servers":{}}}`,
		`{"context_servers":{}}`, `{"servers":{}}`, "mcp_servers:":
		return true
	}
	return false
}

// matchFinalNewline gives a file back ending as it came in. Every writer here
// finishes with a newline, and some editors never write one, so a config
// someone owned came back changed for nothing — #2606 the other way round
// (#2619). Beside matchLineEndings because it is the same rule: what the file
// was written in is not ours to convert. A file deja creates has no ending to
// keep and gets the newline every text file wants.
func matchFinalNewline(old, next []byte) []byte {
	if len(old) == 0 || bytes.HasSuffix(old, []byte("\n")) {
		return next
	}
	next = bytes.TrimSuffix(next, []byte("\n"))
	return bytes.TrimSuffix(next, []byte("\r"))
}

// readConfig reads a config a writer is about to edit.
//
// Every writer used to read it with `old, _ := os.ReadFile`, so a file that
// exists but cannot be read was empty to all of them and deja wrote a config
// holding only deja over it. A file that is not there is still empty — that is
// the ordinary first install — but anything else is the reader's config, and
// deja cannot edit what it cannot read (#2751).
func readConfig(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return b, nil
}

func writeIfChanged(path string, old, next []byte) (string, error) {
	// The last guard for a file deja could not read: the writers go through
	// readConfig now, but this also covers the ones that write a file they
	// never read. An unreadable config used to be an empty one here, and
	// backupOnce hid it — it takes one snapshot per file and returns early
	// once a `.bak` is beside it, which is the ordinary state after any
	// earlier install (#2751).
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// Before the comparison, not after: a CRLF config converted afterwards
	// would differ from `old` on every run, so each repeat install would
	// rewrite the file and report it changed.
	next = matchLineEndings(old, next)
	next = matchFinalNewline(old, next)
	if bytes.Equal(old, next) {
		return "unchanged", nil
	}
	// Removing something must not leave more behind than it found. A file that
	// is not there has nothing in it to remove.
	if removingWiring {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return "unchanged", nil
		}
		// Nothing is left once deja's block comes out, which means deja wrote
		// the whole file: install created ~/.codex/AGENTS.md, uninstall
		// truncated it to zero bytes and left it there, plus a .bak of deja's
		// own guidance (#840). Deleting is what "remove what we added" means
		// here. Before backupOnce, so the backup of a file that was entirely
		// ours is not created either — the .bak of a config the user already
		// had still is.
		if len(next) == 0 {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return "", err
			}
			return "removed", nil
		}
		// The same rule for the structured writers, which never reach zero
		// bytes. Only for a file deja created: a config the reader already had
		// keeps its place, emptied of deja and of nothing else (#2583).
		if structurallyEmptyConfig(next) && wiringCreated(path) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return "", err
			}
			// The directory too, when deja made it and nothing else is in it.
			// os.Remove fails on a directory that is not empty, which is the
			// condition wanted (same rule as pruneGuidanceDirs).
			if dir := filepath.Dir(path); isRealDir(dir) {
				_ = os.Remove(dir)
			}
			return "removed", nil
		}
	}
	if _, err := os.Stat(path); os.IsNotExist(err) && !removingWiring {
		createdByThisRun = append(createdByThisRun, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	// Config files are very often symlinks into a dotfiles repository. Writing
	// by rename would replace the link with a regular file: the repo stops
	// tracking the config, and the next stow or chezmoi run either clobbers
	// deja's wiring or stops on a conflict. Follow the link and write where it
	// points, so the link stays a link and the change lands in the repo.
	// A dangling link has nothing to follow and keeps the old behaviour.
	if resolved, rerr := filepath.EvalSymlinks(path); rerr == nil && resolved != path {
		path = resolved
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
	}
	if _, err := backupOnce(path); err != nil {
		return "", err
	}
	// On the way out, a snapshot that itself contains deja's wiring is deja's
	// own and nothing the user asked to keep: leaving it puts a file naming a
	// binary they just removed back in their config directory. Ownership is
	// read from the content rather than from who wrote it — installing a
	// harness whose config deja edits twice takes the snapshot on the second
	// write, so the uninstall that meets it did not create it and would
	// otherwise leave it (goose). A backup with no deja in it is the user's.
	if removingWiring {
		defer func() {
			bak := path + ".bak"
			b, err := os.ReadFile(bak)
			if err != nil {
				return
			}
			// Only deja's own. A snapshot of the reader's config stays even
			// when the live file has come back to exactly it: that copy is
			// theirs, and TestUninstallLeavesNoFileOrDirItCreated has said so
			// since #840 — "the user's own config and its snapshot are not
			// ours to delete" (#2604).
			if mentionsDeja(b) {
				_ = os.Remove(bak)
			}
		}()
	}
	tmp, terr := os.CreateTemp(filepath.Dir(path), ".deja-tmp-")
	if terr != nil {
		// The scratch file is deja's business; the reader's is the config it
		// could not write. A read-only ~/.codex was reported as a permission
		// error on ~/.codex/.deja-tmp-4168817699, which cannot be looked at,
		// chmod-ed or found (#1686, the shape of #865). Rewriting the path in
		// place keeps the error a *PathError, so errors.Is still sees the
		// permission underneath and the remedy still names permissions.
		//
		// Only for a permission denial: the destination is the right thing to
		// name when the directory refuses us, and the wrong thing when the
		// failure is about the scratch file itself — "config.toml: no such
		// file or directory" would send the reader after a directory that is
		// the actual problem.
		var pe *os.PathError
		if errors.Is(terr, fs.ErrPermission) && errors.As(terr, &pe) {
			pe.Path = path
		}
		return "", terr
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(next); err != nil {
		_ = tmp.Close()
		return "", err
	}
	// Preserve the live file's mode; brand-new configs start owner-only
	// because they may carry MCP credentials.
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	if len(old) == 0 {
		return "created", nil
	}
	return "updated", nil
}

func installClaude(exe string, uninstall bool) (installResult, error) {
	path := sources.ClaudeJSONPath()
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	var note string
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if configIsJSONC(old) {
		return writeJSONCEntry(path, old, "mcpServers", exe, uninstall)
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, configParseError(path, err)
	}
	m, _, err := mcpBlock(root, "mcpServers", path)
	if err != nil {
		// On the way out there is nothing of deja's in a block it never wrote,
		// and refusing here would leave the rest of the target wired (#2399).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, err
	}
	if m == nil {
		// Adding the empty block on the way out rewrites a config that never
		// mentioned deja, and leaves a .bak of it besides (#676).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		m = map[string]any{}
		root["mcpServers"] = m
		noteBlockAdded(path, "mcpServers")
	}
	if uninstall {
		delete(m, "deja")
		removeAdoptedDejaEntries(path, "mcpServers", m)
		note = leftDejaEntriesNote(m)
		// And the block itself, when deja is what put it there: a config the
		// reader owned came back with an empty `mcpServers` they never wrote,
		// while the copy install promised sat beside it holding the real thing
		// (#2604). A block they wrote stays, empty or not.
		if len(m) == 0 && blockWasAdded(path, "mcpServers") {
			delete(root, "mcpServers")
			forgetBlockAdded(path, "mcpServers")
		}
	} else {
		key := dejaEntryKey(m)
		if key != "deja" {
			noteBlockAdded(path, "mcpServers."+key)
		}
		m[key], note = mergeDejaEntry(m[key], mcpServerEntry(exe))
		note = withOtherDejaEntries(note, m, key)
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, err
}

// claudeHookWiring is every event deja installs into Claude Code, in one place
// because two commands read it: install writes them, and doctor says which of
// them the machine actually has. They used to be a list of calls here and a
// single check over there, so a settings.json written by an older deja — three
// events where there are now five — was reported as "wired" and the features
// added since reached nobody until they happened to reinstall.
var claudeHookWiring = []struct{ Event, Sub, Matcher string }{
	{"SessionStart", "hook-context", ""},
	{"PreCompact", "hook-precompact", "manual|auto"},
	{"UserPromptSubmit", "hook-prompt", ""},
	// Recall at the moment of the action: before an edit or a command,
	// hook-tool names the file's or command's prior decision. Scoped to the
	// tools that change something so it never fires on a Read or a Glob.
	//
	// Task and Agent are the same event under two names: the parent spawning a
	// subagent. That agent gets no session start and sends no user prompt, so
	// its instructions are the only place memory can reach it, and hook-tool
	// answers this one by rewriting them rather than by speaking to the parent.
	{"PreToolUse", "hook-tool", "Bash|Edit|Write|MultiEdit|NotebookEdit|Task|Agent"},
	// The other half of the point of action: the pre-tool line speaks before a
	// command runs, this one speaks when it failed and the store knows what
	// followed that error before. Bash only — a failed edit does not carry a
	// shell error signature.
	{"PostToolUse", "hook-tool-after", "Bash"},
}

func installClaudeHook(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, configParseError(path, err)
	}
	nextRoot := root
	for _, h := range claudeHookWiring {
		nextRoot = updateClaudeHook(nextRoot, h.Event, exe+" "+h.Sub, h.Matcher, uninstall)
	}
	// In the shape the reader wrote it, like every other JSON writer: this was
	// the last one still marshalling straight, so an install that added hooks
	// also alphabetised their keys, reindented the file and expanded every
	// block they had written on one line — in the file that holds their
	// permissions and their model (#1665, the shape #2641 and #2704 gave the
	// others).
	next, err := marshalConfigLike(old, nextRoot)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func updateClaudeSessionStartHook(root map[string]any, exe string, uninstall bool) map[string]any {
	root = updateClaudeHook(root, "SessionStart", exe+" hook-context", "", uninstall)
	return root
}

// hookStatusMessage labels the hook while it runs. Codex carries it through
// to its hook run summary; Claude Code 2.1.220 parses the field but shows
// nothing for it — there the receipt in systemMessage is what the user sees,
// which was checked by rendering the actual TUI rather than by reading docs.
func hookStatusMessage(event string) string {
	switch event {
	case "SessionStart":
		return "Recalling past sessions…"
	case "UserPromptSubmit":
		return "Searching past sessions…"
	case "PreCompact":
		return "Saving this session to memory…"
	case "PreToolUse":
		return "Checking what this touches…"
	case "PostToolUse":
		return "Checking what fixed this before…"
	}
	return ""
}

// hookCommandKind says whose a hook command is. The word "deja" anywhere in the
// line is not the test: a tool living under /home/deja is somebody else's, and
// a line the reader wrote around deja's own invocation is theirs even though it
// runs ours (#2477). What decides it is the token in front of the subcommand.
type hookCommandKind int

const (
	// hookNotDejas: deja did not write this and does not run in it.
	hookNotDejas hookCommandKind = iota
	// hookDejas: the line deja writes — the binary and the subcommand, nothing
	// else. Install repoints it at the current path; uninstall takes it out.
	hookDejas
	// hookWrapsDejas: the reader's own line, which runs deja's hook as part of
	// something larger. It is already installed, so adding deja's line beside
	// it would run the hook twice at every session start; and it is not deja's
	// to rewrite or to delete.
	hookWrapsDejas
)

func hookCommandKindOf(existing any, cmd string) hookCommandKind {
	s, ok := existing.(string)
	if !ok || s == "" {
		return hookNotDejas
	}
	if s == cmd {
		return hookDejas
	}
	sub := cmd[strings.LastIndex(cmd, " ")+1:]
	for i := 0; i < len(s); {
		j := strings.Index(s[i:], " "+sub)
		if j < 0 {
			return hookNotDejas
		}
		at := i + j
		end := at + 1 + len(sub)
		if isDejaBinaryToken(lastShellToken(s[:at])) && subcommandEndsAt(s[end:]) {
			if strings.TrimSpace(s) == strings.TrimSpace(lastShellToken(s[:at])+" "+sub) {
				return hookDejas
			}
			return hookWrapsDejas
		}
		i = end
	}
	return hookNotDejas
}

// isDejaHookCommand reports whether deja's hook runs in this command at all,
// whether as the line deja wrote or inside one the reader did. Callers that go
// on to rewrite the command ask for hookDejas instead.
func isDejaHookCommand(existing any, cmd string) bool {
	return hookCommandKindOf(existing, cmd) != hookNotDejas
}

// lastShellToken is the word a command name would occupy: the last run of
// non-space characters before the subcommand.
func lastShellToken(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

// isDejaBinaryToken reports whether a token names the deja binary, quoted or
// not, under any directory, on either platform's separator.
func isDejaBinaryToken(tok string) bool {
	tok = strings.Trim(strings.TrimSpace(tok), `"'`)
	// Both separators whatever this build was compiled for, because configs
	// travel between machines, and folded because a filesystem that does not
	// care about case still runs /opt/Deja (#2713).
	if i := strings.LastIndexAny(tok, `/\`); i >= 0 {
		tok = tok[i+1:]
	}
	tok = strings.ToLower(tok)
	return tok == "deja" || tok == "deja.exe"
}

// subcommandEndsAt reports whether the subcommand really ended where it was
// found, rather than being the head of a longer word: "hook-prompt" must not
// match inside "hook-prompt-extra". A quote, a shell separator or the end of
// the line all end it.
func subcommandEndsAt(rest string) bool {
	if rest == "" {
		return true
	}
	switch rest[0] {
	case ' ', '\t', '\'', '"', ';', '&', '|', ')', '`', '\n':
		return true
	// A redirect glued to the word ends it the same way a pipe does, and no
	// subcommand contains one. Left out at first, so `deja hook-context>>/tmp/x`
	// read as somebody else's command and deja installed its hook beside it —
	// two session starts, memory injected twice (#2493).
	case '>', '<':
		return true
	}
	return false
}

func updateClaudeHook(root map[string]any, event, cmd, matcher string, uninstall bool) map[string]any {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	entries, _ := hooks[event].([]any)
	var out []any
	found := false
	for _, entryAny := range entries {
		entry, _ := entryAny.(map[string]any)
		if entry == nil {
			out = append(out, entryAny)
			continue
		}
		hs, hasHooks := entry["hooks"].([]any)
		// Self-heal: an entry whose hooks are null/absent is invalid — Claude
		// Code rejects the whole file over it. Earlier deja versions could
		// leave one behind on uninstall when the entry carried a matcher.
		if !hasHooks || len(hs) == 0 {
			continue
		}
		var kept []any
		removed := false
		for _, hAny := range hs {
			h, _ := hAny.(map[string]any)
			kind := hookNotDejas
			if h != nil && h["type"] == "command" {
				kind = hookCommandKindOf(h["command"], cmd)
			}
			if kind == hookWrapsDejas {
				// This hook is already installed, inside a line deja did not
				// write. Leaving it alone is the whole of what to do: a second
				// entry would inject memory twice, and rewriting it would throw
				// away whatever else the reader put on that line.
				found = true
				kept = append(kept, hAny)
				continue
			}
			if kind == hookDejas {
				if uninstall {
					removed = true
					continue
				}
				// An entry of ours for this event already exists. Take it over
				// rather than adding a second one: installing from a new path
				// used to leave the old entry behind, and both would fire.
				found = true
				h["command"] = cmd
				if msg := hookStatusMessage(event); msg != "" {
					h["statusMessage"] = msg
				}
			}
			kept = append(kept, hAny)
		}
		if len(kept) != len(hs) || found {
			entry["hooks"] = kept
		}
		if removed {
			if len(kept) == 0 {
				continue
			}
			entry["hooks"] = kept
		}
		out = append(out, entry)
	}
	if !uninstall && !found {
		h := map[string]any{"type": "command", "command": cmd}
		if msg := hookStatusMessage(event); msg != "" {
			h["statusMessage"] = msg
		}
		entry := map[string]any{"hooks": []any{h}}
		if matcher != "" {
			entry["matcher"] = matcher
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = out
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
	return root
}

// combinedStatusline builds a command that runs the existing statusline and
// deja's, separated by a middle dot.
func combinedStatusline(existing, exe string) string {
	return fmt.Sprintf(`sh -c 'json=$(cat); printf "%%s" "$json" | %s; printf " · "; printf "%%s" "$json" | %s statusline'`, existing, exe)
}

// installStatusline wires `deja statusline` as the Claude Code status bar.
// It refuses to replace a statusline the user already configured (many run
// ccstatusline or their own script) — printing how to combine instead.
func installStatusline(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, configParseError(path, err)
	}
	cmd := exe + " statusline"
	existing, _ := root["statusLine"].(map[string]any)
	if uninstall {
		if existing == nil || existing["command"] != cmd {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		delete(root, "statusLine")
	} else {
		if existing != nil && existing["command"] != cmd {
			// Most people already run something here. Replacing it silently
			// would be rude, and "append our output" is not actionable — so
			// hand over the line that runs both. Claude pipes session JSON to
			// the command, so it is captured once and fed to each in turn.
			prev, _ := existing["command"].(string)
			return installResult{}, fmt.Errorf("a statusline is already configured — to keep both, set statusLine.command to:\n\n  %s", combinedStatusline(prev, exe))
		}
		// refreshInterval makes Claude Code re-run the command on a timer
		// instead of only after a turn, so the first index build shows a
		// moving bar rather than a number frozen at whatever it was when the
		// user last typed.
		root["statusLine"] = map[string]any{"type": "command", "command": cmd, "refreshInterval": 1000}
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func installCodex(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.CodexHome(), "config.toml")
	cmd, args := mcpCommandArgs(exe)
	block := fmt.Sprintf("[mcp_servers.deja]\ntype = \"stdio\"\ncommand = %q\nargs = %s\n", cmd, tomlStringArray(args))
	return installTOML(path, block, uninstall)
}

// tomlStringArray renders a Go string slice as a TOML inline array, e.g.
// ["/c", "deja", "mcp"], so the Windows cmd /c shim survives the config write.
func tomlStringArray(xs []string) string {
	quoted := make([]string, len(xs))
	for i, x := range xs {
		quoted[i] = fmt.Sprintf("%q", x)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// installGrok wires the MCP server into Grok Build's config.toml.
// Same [mcp_servers.NAME] TOML shape as Codex; Grok's hook stdout is ignored
// on passive events, so MCP is the deepest integration available.
func installGrok(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.GrokHome(), "config.toml")
	cmd, args := mcpCommandArgs(exe)
	block := fmt.Sprintf("[mcp_servers.deja]\ncommand = %q\nargs = %s\n", cmd, tomlStringArray(args))
	res, err := installTOML(path, block, uninstall)
	if err != nil {
		return res, err
	}
	// The other CLI sharing this directory reads a different file entirely.
	user, uerr := installGrokUserSettings(exe, uninstall)
	if uerr != nil {
		return res, uerr
	}
	if res.Action == "unchanged" {
		if res.Note != "" {
			if user.Note != "" {
				user.Note = res.Note + "; " + user.Note
			} else {
				user.Note = res.Note
			}
		}
		return user, nil
	}
	return res, nil
}

type tomlMCPBlock struct {
	key        string
	start, end int
}

func installTOML(path, block string, uninstall bool) (installResult, error) {
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	text := lfText(old)
	blocks := tomlMCPBlocks(text)
	hasDeja := false
	for _, b := range blocks {
		if b.key == "deja" {
			hasDeja = true
			break
		}
	}
	if !uninstall && !hasDeja {
		if key := foreignTOMLDejaKey(text); key != "" {
			noteBlockAdded(path, "mcp_servers."+key)
			next := []byte(updateTOMLMCPBlock(text, key, block))
			a, err := writeIfChanged(path, old, next)
			return installResult{
				Path:   path,
				Action: a,
				Note:   fmt.Sprintf("updated %q, which already ran deja, instead of adding a second entry", key),
			}, err
		}
	}

	// The splicing below counts newlines, so it works in LF and writeIfChanged
	// puts the file's own endings back. Left as read, a CRLF file grew a blank
	// line per install: TrimRight("\n") leaves the carriage return behind.
	s := removeCodexDejaBlock(text)
	if uninstall {
		for _, key := range foreignTOMLDejaKeys(s) {
			name := "mcp_servers." + key
			if blockWasAdded(path, name) {
				s = removeTOMLMCPBlock(s, key)
				forgetBlockAdded(path, name)
			}
		}
	}
	s = strings.TrimRight(s, "\n")
	var note string
	if !uninstall {
		if s != "" {
			s += "\n\n"
		}
		s += block
	} else {
		note = leftNamedDejaEntriesNote(foreignTOMLDejaKeys(s))
		if s != "" {
			s += "\n"
		}
	}
	a, err := writeIfChanged(path, old, []byte(s))
	return installResult{Path: path, Action: a, Note: note}, err
}

func tomlMCPBlocks(s string) []tomlMCPBlock {
	lines := strings.Split(s, "\n")
	var out []tomlMCPBlock
	for i, line := range lines {
		key, ok := tomlMCPHeader(line)
		if !ok {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "[") {
				end = j
				break
			}
		}
		out = append(out, tomlMCPBlock{key: key, start: i, end: end})
	}
	return out
}

// tomlMCPHeader reads the key out of an `[mcp_servers.X]` line. Through
// tomlCode first: a header is as entitled to a trailing comment as a value
// line, and requiring the bracket to end the raw line dropped the whole block
// — install then appended the duplicate this change exists to stop.
func tomlMCPHeader(line string) (string, bool) {
	line = tomlCode(line)
	const prefix = "[mcp_servers."
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, "]") {
		return "", false
	}
	key := strings.TrimSuffix(strings.TrimPrefix(line, prefix), "]")
	if key == "" || strings.ContainsAny(key, "[]") {
		return "", false
	}
	return key, true
}

func foreignTOMLDejaKey(s string) string {
	keys := foreignTOMLDejaKeys(s)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func foreignTOMLDejaKeys(s string) []string {
	lines := strings.Split(s, "\n")
	seen := map[string]bool{}
	var keys []string
	for _, b := range tomlMCPBlocks(s) {
		if b.key == "deja" || seen[b.key] || !tomlBlockRunsDeja(lines, b) {
			continue
		}
		seen[b.key] = true
		keys = append(keys, b.key)
	}
	sort.Strings(keys)
	return keys
}

func tomlBlockRunsDeja(lines []string, block tomlMCPBlock) bool {
	for i := block.start + 1; i < block.end; i++ {
		key, value, ok := tomlLineKeyValue(lines[i])
		if !ok {
			continue
		}
		switch key {
		case "command":
			for _, value := range tomlStringValues(value) {
				if commandIsDeja(value) {
					return true
				}
			}
		case "args":
			value, i = tomlArrayValue(lines, i, block.end, value)
			for _, value := range tomlStringValues(value) {
				if commandIsDeja(value) {
					return true
				}
			}
		}
	}
	return false
}

func tomlLineKeyValue(line string) (string, string, bool) {
	line = tomlCode(line)
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(value), true
}

// tomlCode strips a line's trailing comment without misreading a `#` inside a
// quoted value — a Windows path or an arg can carry one, and cutting there
// would hand back half a string.
func tomlCode(line string) string {
	var out strings.Builder
	var quote byte
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			out.WriteByte(c)
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '#' {
			break
		}
		if c == '"' || c == '\'' {
			quote = c
		}
		out.WriteByte(c)
	}
	return strings.TrimSpace(out.String())
}

func tomlArrayValue(lines []string, start, end int, value string) (string, int) {
	depth := tomlArrayDelta(value)
	if depth <= 0 {
		return value, start
	}
	var out strings.Builder
	out.WriteString(value)
	i := start
	for i+1 < end && depth > 0 {
		i++
		line := tomlCode(lines[i])
		out.WriteByte('\n')
		out.WriteString(line)
		depth += tomlArrayDelta(line)
	}
	return out.String(), i
}

func tomlArrayDelta(s string) int {
	depth := 0
	var quote byte
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '[':
			depth++
		case ']':
			depth--
		}
	}
	return depth
}

func tomlStringValues(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		quote := s[i]
		if quote != '"' && quote != '\'' {
			continue
		}
		start := i
		escaped := false
		for i++; i < len(s); i++ {
			c := s[i]
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c != quote {
				continue
			}
			if quote == '\'' {
				out = append(out, s[start+1:i])
			} else if value, err := strconv.Unquote(s[start : i+1]); err == nil {
				out = append(out, value)
			}
			break
		}
	}
	return out
}

// updateTOMLMCPBlock rewrites the wiring inside `[mcp_servers.key]` and keeps
// every line deja does not own. The block is somebody's hand work — a
// startup timeout, an env, a comment — and the JSON side already promises the
// same through mergeDejaEntry (#2269).
func updateTOMLMCPBlock(s, key, replacement string) string {
	lines := strings.Split(s, "\n")
	var target tomlMCPBlock
	found := false
	for _, block := range tomlMCPBlocks(s) {
		if block.key == key {
			target = block
			found = true
			break
		}
	}
	if !found {
		return s
	}
	var owned []string
	replacementLines := strings.Split(strings.TrimRight(replacement, "\n"), "\n")
	for _, line := range replacementLines[1:] {
		k, _, ok := tomlLineKeyValue(line)
		if ok && tomlOwnedMCPKey(k) {
			owned = append(owned, line)
		}
	}

	body := make([]string, 0, target.end-target.start)
	inserted := false
	for i := target.start + 1; i < target.end; i++ {
		k, value, ok := tomlLineKeyValue(lines[i])
		if !ok || !tomlOwnedMCPKey(k) {
			body = append(body, lines[i])
			continue
		}
		if !inserted {
			body = append(body, owned...)
			inserted = true
		}
		if k == "args" {
			_, i = tomlArrayValue(lines, i, target.end, value)
		}
	}
	if !inserted {
		body = append(owned, body...)
	}

	out := append([]string{}, lines[:target.start+1]...)
	out = append(out, body...)
	out = append(out, lines[target.end:]...)
	return strings.Join(out, "\n")
}

func tomlOwnedMCPKey(key string) bool {
	switch key {
	case "type", "command", "args":
		return true
	}
	return false
}

func removeCodexDejaBlock(s string) string {
	return removeTOMLMCPBlock(s, "deja")
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func antigravityConfigHome() string {
	return filepath.Join(homeDir(), ".gemini", "config")
}

// dejaEntryKey keeps one MCP server when somebody wired deja by hand under a
// different name — `deja-vu` is the obvious choice. Install must update that
// entry rather than add a sibling that starts the server twice (#2269).
func dejaEntryKey(m map[string]any) string {
	if _, ok := m["deja"]; ok {
		return "deja"
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		if key != "deja" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if mcpEntryRunsDeja(m[key]) {
			return key
		}
	}
	return "deja"
}

// leftDejaEntriesNote names what uninstall deliberately leaves behind: an
// entry deja never wrote, still running deja under another name. Deleting a
// hand add is worse than leaving it, and leaving it without a word looks like
// an uninstall that missed one (#2269).
func leftDejaEntriesNote(m map[string]any) string {
	var keys []string
	for key, entry := range m {
		if key != "deja" && mcpEntryRunsDeja(entry) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return leftNamedDejaEntriesNote(keys)
}

func leftNamedDejaEntriesNote(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = fmt.Sprintf("%q", key)
	}
	if len(quoted) == 1 {
		return fmt.Sprintf("left %s, which also runs deja — deja did not write it, so uninstall does not remove it", quoted[0])
	}
	return fmt.Sprintf("left %s, which also run deja — deja did not write them, so uninstall does not remove them", naturalList(quoted))
}

func naturalList(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	case 2:
		return xs[0] + " and " + xs[1]
	default:
		return strings.Join(xs[:len(xs)-1], ", ") + ", and " + xs[len(xs)-1]
	}
}

// replacedEntryNote names the deja wiring someone had in a config before
// install overwrote it. Replacing it is install's job — a stale entry is what
// the command exists to fix — but reporting it in the same sentence deja
// prints for a config that never mentioned deja reads as nothing having been
// there (#2390). Empty when the entry is absent or already what deja writes.
func replacedEntryNote(prev, entry any) string {
	if prev == nil || sameEntry(prev, entry) {
		return ""
	}
	was := entryCommandName(prev)
	if was == "" {
		return "replaced the deja entry that was already here"
	}
	// The same command is not a replacement anyone would want told about. An
	// entry an older deja wrote differs from this one by a field or two the
	// format gained since, and saying "replaced … which ran /bin/deja" of
	// /bin/deja is noise sitting where a real takeover should stand out
	// (#2479).
	if was == entryCommandName(entry) {
		return ""
	}
	return fmt.Sprintf("replaced the deja entry that was already here, which ran %s", safeForStatusline(was, 200))
}

// entryRunsDeja reports whether an MCP entry runs the deja binary, wherever the
// config keeps it: a command string, a command list, or the args behind a
// wrapper like Windows' `cmd /c`.
func entryRunsDeja(entry map[string]any) bool {
	// The shape a client nests: doctor read it and install did not, so one
	// command called a file wired while the other wrote a second server into
	// it (#2713).
	if t, ok := entry["transport"].(map[string]any); ok && entryRunsDeja(t) {
		return true
	}
	var words []string
	switch c := entry["command"].(type) {
	case string:
		words = append(words, c)
	case []any:
		for _, v := range c {
			if s, ok := v.(string); ok {
				words = append(words, s)
			}
		}
	}
	if args, ok := entry["args"].([]any); ok {
		for _, v := range args {
			if s, ok := v.(string); ok {
				words = append(words, s)
			}
		}
	}
	for _, w := range words {
		if isDejaBinaryToken(w) {
			return true
		}
	}
	return false
}

// withOtherDejaEntries folds the note about a second deja server into whatever
// the write already had to say. Every MCP writer needs it: the file it edits is
// the one that would hold the other entry.
func withOtherDejaEntries(note string, servers map[string]any, mine string) string {
	// Everything past the entry install just took over. One of them is now
	// deja's own — adopting it is what keeps the harness from starting two
	// servers — so naming it here would report deja's own wiring as a stranger.
	other := otherDejaEntriesNote(servers, mine)
	if other == "" {
		return note
	}
	if note != "" {
		return note + "; " + other
	}
	return other
}

// otherDejaEntriesNote names entries in the same file that also run deja under
// another key.
//
// doctor already reads past the key at what an entry runs, and calls `deja-vu`
// deja's wiring; install did not, so it wrote a second server beside one it
// recognised and said only "updated". The harness then starts two on every
// session, one of them possibly naming a binary that is gone (#2712).
//
// Said, not taken: a second entry can be deliberate — another index directory,
// a pinned older binary — and removing one deja did not write is the damage
// #2583 and #2600 were about. What the reader cannot do is act on a file whose
// state nothing reports.
func otherDejaEntriesNote(servers map[string]any, mine string) string {
	var names []string
	for name, entry := range servers {
		if name == mine {
			continue
		}
		m, _ := entry.(map[string]any)
		if m != nil && entryIsDejaServer(m) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	noun, verb, it := "the entry ", "runs", "it"
	if len(names) > 1 {
		noun, verb, it = "the entries ", "run", "them"
	}
	return fmt.Sprintf("%s%s also %s deja — this harness will start %s alongside deja's own; remove what you do not want",
		noun, quotedList(names, 3), verb, it)
}

// entryIsDejaServer is entryRunsDeja for a claim about somebody else's entry.
//
// entryRunsDeja answers "may this be merged onto", where a false positive costs
// a merge onto an entry deja then overwrites anyway. This answers "should the
// reader be told to remove it", where a false positive is deja pointing at a
// config line that is none of its business: a filesystem server told to serve
// `~/code/deja` has the binary's name in its arguments and is not deja.
//
// So the name has to be the command — or, for the wrapper shapes where it
// cannot be (`cmd /c … deja mcp`), it has to be running deja's server rather
// than appearing in a path.
func entryIsDejaServer(entry map[string]any) bool {
	// The nested shape, on the same terms: this asks whether the entry is
	// deja's server, so the answer has to reach wherever the launch is written
	// (#2713).
	if t, ok := entry["transport"].(map[string]any); ok && entryIsDejaServer(t) {
		return true
	}
	switch c := entry["command"].(type) {
	case string:
		if isDejaBinaryToken(c) {
			return dejaServerArgs(entry)
		}
	case []any:
		if len(c) > 0 {
			if head, ok := c[0].(string); ok && isDejaBinaryToken(head) {
				return dejaServerArgs(entry)
			}
			for _, v := range c {
				if s, ok := v.(string); ok && isDejaBinaryToken(s) {
					return commandListRunsMCP(c)
				}
			}
		}
	}
	if args, ok := entry["args"].([]any); ok {
		for _, v := range args {
			if s, ok := v.(string); ok && isDejaBinaryToken(s) {
				return commandListRunsMCP(args)
			}
		}
	}
	return false
}

// dejaServerArgs reports whether an entry whose command is deja runs the MCP
// server rather than something else someone wired by hand.
func dejaServerArgs(entry map[string]any) bool {
	args, ok := entry["args"].([]any)
	if !ok {
		// A command with no arguments at all is deja's own shape from before
		// the subcommand was written into the entry.
		return entry["args"] == nil
	}
	return commandListRunsMCP(args)
}

// commandListRunsMCP reports whether `mcp` is one of these words.
func commandListRunsMCP(words []any) bool {
	for _, v := range words {
		if s, ok := v.(string); ok && s == "mcp" {
			return true
		}
	}
	return false
}

// mergeDejaEntry writes deja's wiring onto the entry that was already there.
// deja owns the command, the args and the type; an env pointing at a store on
// another disk, a timeout, a `disabled` the reader set — those are theirs, and
// replacing the whole entry threw them away on every reinstall (#2479).
func mergeDejaEntry(prev any, entry map[string]any) (map[string]any, string) {
	note := replacedEntryNote(prev, entry)
	old, ok := prev.(map[string]any)
	// Only onto an entry that was deja's. Keeping the fields of somebody
	// else's entry — a `url` of a remote server, an env built for another
	// program — would leave a mixed shape whose client has to guess which half
	// to believe.
	if !ok || !entryRunsDeja(old) {
		return entry, note
	}
	out := make(map[string]any, len(old)+len(entry))
	for k, v := range old {
		out[k] = v
	}
	for k, v := range entry {
		out[k] = v
	}
	// deja owns how the entry is launched, and it writes that at the top level.
	// An entry written in the nested shape kept its `transport` beside the
	// command deja had just written, and a client that prefers the nested one
	// went on launching the old binary while install reported success — the
	// mixed shape this function exists to avoid, reached through the wider gate
	// #2713 opened (#2716).
	if _, ok := out["command"]; ok {
		delete(out, "transport")
	}
	if entrySwitchedOff(out) {
		// Switching it back on silently would undo a decision deja was not
		// asked to revisit; leaving it off without a word looks like a install
		// that worked.
		if note != "" {
			note += "; "
		}
		note += "left the entry switched off, the way it was — deja will not answer until you turn it back on"
	}
	return out, note
}

// entrySwitchedOff reports whether the reader has turned this entry off.
//
// One switch, two spellings: most hosts write `disabled: true`, opencode
// writes `enabled: false`. The note was keyed to the first, so a config
// carrying the second reported a clean install for wiring that will never run
// (#2757).
func entrySwitchedOff(entry map[string]any) bool {
	if off, ok := entry["disabled"].(bool); ok && off {
		return true
	}
	if on, ok := entry["enabled"].(bool); ok && !on {
		return true
	}
	return false
}

// sameEntry compares an entry read out of a config with the one deja is about
// to write. Both sides go through the encoder first: a config's args decode as
// []any while deja builds []string, and those two are the same wiring.
func sameEntry(a, b any) bool {
	x, err := json.Marshal(a)
	if err != nil {
		return false
	}
	y, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(x, y)
}

// entryCommandName reads the command out of an MCP entry deja did not write.
// Configs spell it either as a string beside separate args or as one list, and
// anything else is a shape deja has no name for.
func entryCommandName(entry any) string {
	m, ok := entry.(map[string]any)
	if !ok {
		return ""
	}
	switch c := m["command"].(type) {
	case string:
		return c
	case []any:
		if len(c) > 0 {
			if first, ok := c[0].(string); ok {
				return first
			}
		}
	}
	return ""
}

// mcpServerEntry is the JSON mcpServers value for deja. Windows stdio MCP
// clients (Claude Code among them) spawn through cmd, so the entry uses the
// cmd /c wrapper there; elsewhere it is the executable directly.
func mcpServerEntry(exe string) map[string]any {
	command, args := mcpCommandArgs(exe)
	return map[string]any{"type": "stdio", "command": command, "args": args}
}

func mcpCommandArgs(exe string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", exe, "mcp"}
	}
	return exe, []string{"mcp"}
}

// installCursor wires the MCP server into Cursor's global config
// (~/.cursor/mcp.json). Gemini CLI and Antigravity use the identical

// mcpServers shape in their own files.
func installCursor(exe string, uninstall bool) (installResult, error) {
	return installMCPJSON(filepath.Join(sources.CursorCLIHome(), "mcp.json"), exe, uninstall)
}

// installCopilotMCP wires deja into GitHub Copilot CLI's MCP registry
// (~/.copilot/mcp-config.json). Copilot's schema differs from the common
// mcpServers shape: entries carry a type and an enabled-tools list.
func installCopilotMCP(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.Home(), ".copilot", "mcp-config.json")
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	var note string
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, configParseError(path, err)
	}
	m, _, err := mcpBlock(root, "mcpServers", path)
	if err != nil {
		// On the way out there is nothing of deja's in a block it never wrote,
		// and refusing here would leave the rest of the target wired (#2399).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, err
	}
	if m == nil {
		// Adding the empty block on the way out rewrites a config that never
		// mentioned deja, and leaves a .bak of it besides (#676).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		m = map[string]any{}
		root["mcpServers"] = m
		noteBlockAdded(path, "mcpServers")
	}
	if uninstall {
		delete(m, "deja")
		removeAdoptedDejaEntries(path, "mcpServers", m)
		note = leftDejaEntriesNote(m)
		// And the block, when deja is what put it there (#2604).
		if len(m) == 0 && blockWasAdded(path, "mcpServers") {
			delete(root, "mcpServers")
			forgetBlockAdded(path, "mcpServers")
		}
	} else {
		command, args := mcpCommandArgs(exe)
		key := dejaEntryKey(m)
		if key != "deja" {
			noteBlockAdded(path, "mcpServers."+key)
		}
		m[key], note = mergeDejaEntry(m[key], map[string]any{"type": "local", "command": command, "args": args, "tools": []string{"*"}})
		note = withOtherDejaEntries(note, m, key)
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, err
}

// installOpenClawMCP wires deja into openclaw.json — OpenClaw keeps MCP
// servers under mcp.servers, not the common mcpServers root.
func installOpenClawMCP(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	var note string
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, configParseError(path, err)
	}
	mcp, _, err := mcpBlock(root, "mcp", path)
	if err != nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, err
	}
	if mcp == nil {
		mcp = map[string]any{}
		root["mcp"] = mcp
	}
	servers, _, err := mcpBlock(mcp, "servers", path)
	if err != nil {
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, err
	}
	if servers == nil {
		servers = map[string]any{}
		mcp["servers"] = servers
		noteBlockAdded(path, "mcp.servers")
	}
	if uninstall {
		delete(servers, "deja")
		removeAdoptedDejaEntries(path, "mcp.servers", servers)
		note = leftDejaEntriesNote(servers)
		if len(servers) == 0 && blockWasAdded(path, "mcp.servers") {
			delete(mcp, "servers")
			if len(mcp) == 0 {
				delete(root, "mcp")
			}
			forgetBlockAdded(path, "mcp.servers")
		}
	} else {
		command, args := mcpCommandArgs(exe)
		key := dejaEntryKey(servers)
		if key != "deja" {
			noteBlockAdded(path, "mcp.servers."+key)
		}
		servers[key], note = mergeDejaEntry(servers[key], map[string]any{"command": command, "args": args})
		note = withOtherDejaEntries(note, servers, key)
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, err
}

// firstDirThatTakesAWrite is the directory a write would actually land in: the
// config's own directory once it exists, and otherwise the nearest parent that
// does, since install creates what is missing under it. os.Stat rather than a
// Lstat: the write follows a symlinked directory, so the question has to
// follow it too.
func firstDirThatTakesAWrite(dir string) string {
	for {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// mcpConfigReadable asks only whether the config can be read and parsed —
// deliberately not whether its block is a shape deja edits. On the way out,
// a block deja never wrote is left alone and reported unchanged (#2399), so
// naming it as a host deja gave up on would be wrong.
func mcpConfigReadable(path string) error {
	old, err := readConfig(path)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(old)) == 0 {
		return nil
	}
	_, err = parseConfigRoot(path, old)
	return err
}

// parseConfigRoot decodes a config the writers edit, comments and all. It
// returns a nil root only for an empty file; `null` is an error, since it
// decodes into a nil map without one and the writer then assigns into it.
func parseConfigRoot(path string, old []byte) (map[string]any, error) {
	text := string(old)
	if configIsJSONC(old) {
		text = stripJSONComments(text)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(text), &root); err != nil {
		return nil, configParseError(path, err)
	}
	if root == nil {
		return nil, configParseError(path, errors.New("the file holds null, not an object"))
	}
	return root, nil
}

// mcpEntryWritable asks whether installMCPJSON could write its entry into this
// config, without writing anything.
//
// It has to accept exactly what the writer accepts: a comment is not a broken
// file (#1664), so a JSONC config passes here and is edited as text. What it
// catches is the config the writer would refuse — one that does not parse, or
// whose block is something other than an object.
func mcpEntryWritable(path, blockKey string) error {
	old, err := readConfig(path)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(old)) == 0 {
		return nil
	}
	jsonc := configIsJSONC(old)
	root, err := parseConfigRoot(path, old)
	if err != nil {
		return err
	}
	if jsonc {
		// The text writer inserts rather than replaces, so a key holding
		// anything but an object would end up in the file twice with the
		// reader's value winning — writeJSONCEntry refuses those, and so must
		// this (#2740). It also needs a top-level object to insert into.
		if v, present := root[blockKey]; present {
			if _, isObject := v.(map[string]any); !isObject {
				return fmt.Errorf("%s: %q is not an object deja can edit — left as it was", path, blockKey)
			}
		}
		if zedTopLevelOpen(string(old)) < 0 {
			return fmt.Errorf("%s: does not look like a settings object; add %q by hand", path, blockKey)
		}
	}
	_, _, err = mcpBlock(root, blockKey, path)
	return err
}

func installMCPJSON(path, exe string, uninstall bool) (installResult, error) {
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var root map[string]any
	var note string
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if configIsJSONC(old) {
		// A comment is not a broken file, and refusing the target over one is
		// how somebody who annotated their config could not install deja at
		// all (#1664).
		return writeJSONCEntry(path, old, "mcpServers", exe, uninstall)
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, configParseError(path, err)
	}
	m, _, err := mcpBlock(root, "mcpServers", path)
	if err != nil {
		// On the way out there is nothing of deja's in a block it never wrote,
		// and refusing here would leave the rest of the target wired (#2399).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		return installResult{}, err
	}
	if m == nil {
		// Adding the empty block on the way out rewrites a config that never
		// mentioned deja, and leaves a .bak of it besides (#676).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		m = map[string]any{}
		root["mcpServers"] = m
		noteBlockAdded(path, "mcpServers")
	}
	if uninstall {
		delete(m, "deja")
		removeAdoptedDejaEntries(path, "mcpServers", m)
		note = leftDejaEntriesNote(m)
		// And the block, when deja is what put it there (#2604).
		if len(m) == 0 && blockWasAdded(path, "mcpServers") {
			delete(root, "mcpServers")
			forgetBlockAdded(path, "mcpServers")
		}
	} else {
		command, args := mcpCommandArgs(exe)
		key := dejaEntryKey(m)
		if key != "deja" {
			noteBlockAdded(path, "mcpServers."+key)
		}
		m[key], note = mergeDejaEntry(m[key], map[string]any{"command": command, "args": args})
		note = withOtherDejaEntries(note, m, key)
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, err
}

func installOpencode(exe string, uninstall bool) (installResult, error) {
	dir := filepath.Join(opencodeConfigHome(), "opencode")
	path := filepath.Join(dir, "opencode.json")
	if _, err := os.Stat(path); err != nil {
		if _, e := os.Stat(filepath.Join(dir, "opencode.jsonc")); e == nil {
			path = filepath.Join(dir, "opencode.jsonc")
		}
	}
	old, err := readConfig(path)
	if err != nil {
		return installResult{}, err
	}
	var next []byte
	var note string
	if strings.HasSuffix(path, ".jsonc") {
		next, note, err = updateOpencodeJSONC(old, exe, uninstall)
	} else {
		next, note, err = updateOpencodeJSON(old, path, exe, uninstall)
	}
	if err != nil {
		return installResult{}, configParseError(path, err)
	}
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a, Note: note}, err
}

func updateOpencodeJSON(old []byte, path, exe string, uninstall bool) ([]byte, string, error) {
	var root map[string]any
	var note string
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return nil, "", err
	}
	m, _, err := mcpBlock(root, "mcp", "")
	if err != nil {
		if uninstall {
			return old, "", nil
		}
		return nil, "", err
	}
	if m == nil {
		m = map[string]any{}
		root["mcp"] = m
	}
	if uninstall {
		delete(m, "deja")
		removeAdoptedDejaEntries(path, "mcp", m)
		note = leftDejaEntriesNote(m)
	} else {
		key := dejaEntryKey(m)
		if key != "deja" {
			noteBlockAdded(path, "mcp."+key)
		}
		m[key], note = mergeDejaEntry(m[key], map[string]any{"type": "local", "command": []string{exe, "mcp"}})
		note = withOtherDejaEntries(note, m, key)
	}
	next, err := marshalConfigLike(old, root)
	if err != nil {
		return nil, "", err
	}
	return append(next, '\n'), note, nil
}

// dropJSONCEntry takes the deja entry out of an "mcp" block written as lines,
// and hands back what is left beside what it removed. An entry spelled across
// lines carries `"deja"` on its first one only; dropping just that line left
// the rest of it in the block and the config stopped parsing (#2394), so the
// entry is bounded by counting braces the way the block itself is.
func dropJSONCEntry(lines []string) (body, dropped []string, wasLast bool) {
	// On the code a parser reads, not the raw line. A comment that only names
	// deja — a parked entry someone commented out, a note saying who wrote the
	// block — is not an entry to replace, and dropping its first line leaves a
	// /* … */ closing on its own, which costs the reader every server in the
	// file (#2473). The same reading keeps a brace inside a comment out of the
	// depth count. jsoncLastCodeLine already reads the block this way to place
	// its comma.
	inBlock := false
	for i := 0; i < len(lines); i++ {
		code, next, _ := jsoncCodeOf(lines[i], inBlock)
		inBlock = next
		if !strings.Contains(code, `"deja"`) {
			body = append(body, lines[i])
			if code != "" {
				wasLast = false
			}
			continue
		}
		dropped = append(dropped, lines[i])
		wasLast = true
		depth := jsoncBraceDelta(code)
		for depth > 0 && i+1 < len(lines) {
			i++
			c, nb, _ := jsoncCodeOf(lines[i], inBlock)
			inBlock = nb
			dropped = append(dropped, lines[i])
			depth += jsoncBraceDelta(c)
		}
	}
	return body, dropped, wasLast
}

// jsoncFirstCodeLine finds the first line of a .jsonc block that a parser
// would read as code, so our entry goes under any comment the reader wrote
// above their first one rather than between the comment and what it annotates.
// It returns -1 when the block holds no code at all.
func jsoncFirstCodeLine(body []string) int {
	inBlock := false
	for i, line := range body {
		c, nextBlock, _ := jsoncCodeOf(line, inBlock)
		inBlock = nextBlock
		if c != "" {
			return i
		}
	}
	return -1
}

// jsoncLastCodeLine finds the last line of a .jsonc block that a parser would
// read as code, and where that code ends on it. It returns -1 when the block
// holds nothing but comments and blank lines.
//
// The comma that joins deja's entry to the previous one has to land at the end
// of the code, not the end of the line. Appended after a trailing // comment it
// is stripped with the comment and the two entries lose their separator;
// appended inside a /* … */ block it either vanishes with the block or is left
// behind as a comma of its own (#1695).
func jsoncLastCodeLine(body []string) (idx, end int, code string) {
	idx = -1
	inBlock := false
	for i, line := range body {
		c, nextBlock, e := jsoncCodeOf(line, inBlock)
		if c != "" {
			idx, end, code = i, e, c
		}
		inBlock = nextBlock
	}
	return idx, end, code
}

// jsoncCodeOf returns the code a parser reads on one line, whether the line
// leaves a /* … */ block open, and the offset where that code ends.
func jsoncCodeOf(line string, inBlock bool) (code string, stillInBlock bool, end int) {
	var b strings.Builder
	inString, escaped := false, false
	end = 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inBlock {
			if c == '*' && i+1 < len(line) && line[i+1] == '/' {
				inBlock, i = false, i+1
			}
			continue
		}
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && c == '/' && i+1 < len(line) && line[i+1] == '/':
			return strings.TrimSpace(b.String()), false, end
		case !inString && c == '/' && i+1 < len(line) && line[i+1] == '*':
			inBlock, i = true, i+1
			continue
		}
		b.WriteByte(c)
		if c != ' ' && c != '\t' {
			end = i + 1
		}
	}
	return strings.TrimSpace(b.String()), inBlock, end
}

// jsoncBraceDelta counts the braces of one line's code that a parser reads as
// structure. Comments are already out — jsoncCodeOf strips them, and keeps
// string contents because the `"deja"` match and the comma offset both need
// them — so what is left to skip here is quoted text. A command path holding a
// brace is ordinary (`${VENDOR}` left literal, `/opt/{a,b}/bin`, a Windows GUID
// directory), and counting one as structure moved the end of the "mcp" block:
// deja's entry landed outside it, or inside it without a comma, or the drop
// loop swallowed the server underneath (#2475).
func jsoncBraceDelta(code string) int {
	depth := 0
	inString, escaped := false, false
	for i := 0; i < len(code); i++ {
		switch c := code[i]; {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
		}
	}
	return depth
}

func updateOpencodeJSONC(old []byte, exe string, uninstall bool) ([]byte, string, error) {
	line := fmt.Sprintf(`    "deja": {"type":"local","command":[%q,"mcp"]}`, exe)
	s := string(old)
	if strings.TrimSpace(s) == "" {
		if uninstall {
			return []byte("{}\n"), "", nil
		}
		return []byte("{\n  \"mcp\": {\n" + line + "\n  }\n}\n"), "", nil
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	start, end := -1, -1
	inBlock := false
	for i, l := range lines {
		code, next, _ := jsoncCodeOf(l, inBlock)
		inBlock = next
		if strings.Contains(code, `"mcp"`) && strings.Contains(code, "{") {
			start = i
			depth := jsoncBraceDelta(code)
			block := inBlock
			for j := i + 1; j < len(lines); j++ {
				c, nb, _ := jsoncCodeOf(lines[j], block)
				block = nb
				depth += jsoncBraceDelta(c)
				if depth <= 0 {
					end = j
					break
				}
			}
			break
		}
	}
	if start >= 0 && end > start {
		// The lines this drops are the entry that was already here. Naming what
		// it ran is the same sentence the .json writer prints (#2390); without
		// it, what install told you depended on which of the two names the
		// config had (#2392).
		body, dropped, wasLast := dropJSONCEntry(lines[start+1 : end])
		// Our entry used to go last, so the comma joining it to the block sat
		// on the line above — the reader's. Taking ours out leaves that comma
		// with nothing after it, which a strict reader of the file rejects, so
		// a comma we put there once has to come back out with the entry it was
		// written for (#2617). Only where ours ended the block: a comma
		// anywhere else is still separating two things.
		if wasLast {
			if i, code, trim := jsoncLastCodeLine(body); i >= 0 && strings.HasSuffix(trim, ",") {
				body[i] = body[i][:code-1] + body[i][code:]
			}
		}
		note := ""
		if !uninstall {
			note = replacedJSONCLineNote(dropped, line)
		}
		if !uninstall {
			// Ours goes first, carrying its own comma. Written last it needed
			// one on the line above — the reader's — and uninstall has no way
			// to tell that comma from one they wrote, so it stayed behind in a
			// file that was not ours to change (#2617). A comma of ours leaves
			// with our line, and the placement rules that a comma on someone
			// else's line needed — not on an opening brace, not after a
			// trailing comment, not inside a block comment (#1695) — stop
			// applying at all.
			entry := line
			at := len(body)
			if i := jsoncFirstCodeLine(body); i >= 0 {
				entry += ","
				at = i
			}
			body = append(body[:at:at], append([]string{entry}, body[at:]...)...)
		}
		out := append([]string{}, lines[:start+1]...)
		out = append(out, body...)
		out = append(out, lines[end:]...)
		return []byte(strings.Join(out, "\n") + "\n"), note, nil
	}
	if uninstall {
		return []byte(strings.Join(lines, "\n") + "\n"), "", nil
	}
	// An "mcp" key exists but brace-counting could not bound it — its opening
	// brace is on a later line, or the braces are unbalanced. Adding a fresh
	// block here would leave the file with two "mcp" keys, which drops the
	// user's other servers. Refuse rather than corrupt a config that cannot be
	// rebuilt; the caller surfaces this so the user can wire deja by hand.
	if start >= 0 || opencodeHasMCPKey(lines) {
		return nil, "", fmt.Errorf("opencode config has an \"mcp\" block deja could not edit without risking it — add the deja server by hand")
	}
	insert := len(lines) - 1
	comma := ""
	for i := insert - 1; i >= 0; i-- {
		trim := strings.TrimSpace(lines[i])
		if trim != "" && !strings.HasPrefix(trim, "//") && !strings.HasSuffix(trim, ",") && trim != "{" {
			lines[i] += ","
			break
		}
	}
	mcp := []string{comma + `  "mcp": {`, line, "  }"}
	out := append([]string{}, lines[:insert]...)
	out = append(out, mcp...)
	out = append(out, lines[insert:]...)
	return []byte(strings.Join(out, "\n") + "\n"), "", nil
}

// replacedJSONCLineNote names the deja entry a line-editing write dropped.
// The lines are text rather than a decoded entry, so the comparison is the
// text deja would have written: same line, nothing replaced. Empty when the
// entry was absent or already deja's own.
func replacedJSONCLineNote(dropped []string, line string) string {
	if len(dropped) == 0 {
		return ""
	}
	if len(dropped) == 1 && strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(dropped[0]), ",")) ==
		strings.TrimSpace(line) {
		return ""
	}
	was := jsoncEntryCommand(strings.Join(dropped, " "))
	if was == "" {
		return "replaced the deja entry that was already here"
	}
	return fmt.Sprintf("replaced the deja entry that was already here, which ran %s", safeForStatusline(was, 200))
}

// jsoncEntryCommand pulls the command out of an entry deja did not write. It
// reads the text rather than the JSON: the block may carry comments and
// trailing commas, which is why this writer exists at all.
func jsoncEntryCommand(text string) string {
	i := strings.Index(text, `"command"`)
	if i < 0 {
		return ""
	}
	rest := text[i+len(`"command"`):]
	if j := strings.IndexAny(rest, `"`); j >= 0 {
		rest = rest[j+1:]
		if k := strings.IndexByte(rest, '"'); k > 0 {
			return rest[:k]
		}
	}
	return ""
}

// opencodeHasMCPKey reports whether any line declares an "mcp" key, so the
// brace-counting editor can tell "there is a block I failed to bound" from
// "there is no block, add one".
func opencodeHasMCPKey(lines []string) bool {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, `"mcp"`) && strings.Contains(t, ":") {
			return true
		}
	}
	return false
}

// autoTargetFor maps a detected harness to the deepest integration it has: the
// -auto target where one exists, the plain one otherwise (the IDE extensions,
// where an MCP server is as far as it goes).
//
// This was a hand-written switch, and the two harnesses added after it was
// written fell through to the default — so `deja install --auto` wired their
// MCP server and quietly left auto-recall off on a machine that has it.
func autoTargetFor(detected string) string {
	// Detection reports Claude as "claude-code" while its target is "claude".
	base := detected
	if base == "claude-code" {
		base = "claude"
	}
	for _, name := range installTargetNames() {
		if name == base+"-auto" {
			return name
		}
	}
	return detected
}

// withAutoTargets pairs each target with its -auto sibling where one exists,
// keeping the original order so the report reads harness by harness.
func withAutoTargets(targets []string) []string {
	known := map[string]bool{}
	for _, name := range installTargetNames() {
		known[name] = true
	}
	// Detection reports Claude as "claude-code" while its hook target is
	// "claude-auto"; without this the slash command and hooks survive a
	// full uninstall.
	autoName := map[string]string{"claude-code": "claude-auto"}
	out := make([]string, 0, len(targets)*2)
	seen := map[string]bool{}
	for _, t := range targets {
		base := strings.TrimSuffix(t, "-auto")
		if alias, ok := autoName[base]; ok {
			for _, candidate := range []string{base, alias} {
				if !seen[candidate] {
					seen[candidate] = true
					out = append(out, candidate)
				}
			}
			continue
		}
		for _, candidate := range []string{base, base + "-auto"} {
			if !known[candidate] || seen[candidate] {
				continue
			}
			seen[candidate] = true
			out = append(out, candidate)
		}
	}
	return out
}

// unknownTargetError names what would have worked. There are two dozen targets
// and the difference between them is a few characters, so a bare "unknown
// target" leaves someone who typed `claud` guessing — while `deja completion`
// two commands away lists its three valid values.
func unknownTargetError(target string) error {
	names := installTargetNames()
	if near := nearestTarget(target, names); near != "" {
		return fmt.Errorf("unknown target %q — did you mean %q? (`deja install --all` wires every agent it finds)", target, near)
	}
	return fmt.Errorf("unknown target %q — try one of: %s, or --all / --auto", target, strings.Join(names, ", "))
}

// nearestTarget finds a target within one edit of what was typed, which covers
// the ways a name is actually got wrong: a dropped letter, a doubled one, a
// transposition.
func nearestTarget(typed string, names []string) string {
	typed = strings.ToLower(strings.TrimSpace(typed))
	if typed == "" {
		return ""
	}
	// A truncated name is the commonest miss — `claud` for claude-code — and it
	// is far from the full string by edit distance, so check prefixes first.
	for _, n := range names {
		if strings.HasPrefix(n, typed) {
			return n
		}
	}
	best, bestDist := "", 3
	for _, n := range names {
		if d := editDistance(typed, n); d < bestDist {
			best, bestDist = n, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// installTargetNames is the one list of what `deja install` accepts. Shell
// completion and doctor's coverage test both read it, so a harness added to
// installTarget without appearing here is caught rather than quietly missing
// from both.
func installTargetNames() []string {
	names := []string{
		"claude-code", "claude-auto",
		"codex", "codex-auto",
		"opencode", "opencode-auto",
		"cursor", "cursor-auto",
		"gemini", "gemini-auto",
		"antigravity", "antigravity-auto",
		"qwen", "qwen-auto",
		"kimi", "kimi-auto",
		"hermes", "hermes-auto",
		"pi", "pi-auto",
		"omp", "omp-auto",
		"deepseek", "deepseek-auto",
		"openclaw", "openclaw-auto",
		"cline", "cline-auto",
		"goose", "goose-auto",
		"grok", "grok-auto", "copilot", "roo", "aider",
		// Zed's agent takes MCP servers and nothing else: no CLI to hand a
		// prompt to, so there is no -auto pair to install.
		"zed",
		"statusline",
	}
	// Not a harness, and deliberately not "-auto": that suffix means a
	// harness's auto-recall hook, and this is the timer that keeps this
	// machine's memory in step with the others. It belongs in this list
	// because `deja install --all` is where someone setting up a second
	// machine looks — but only where deja can actually schedule anything.
	// Offering a target that always fails is worse than not offering it.
	if syncTimerSchedulable(runtime.GOOS) {
		names = append(names, "sync-timer")
	}
	return names
}

// syncTimerSchedulable reports whether this platform has a service manager the
// timer knows how to write for. Taking the platform as an argument keeps the
// answer checkable from any machine.
func syncTimerSchedulable(goos string) bool {
	return goos == "darwin" || goos == "linux"
}

// removeAdoptedDejaEntries removes only renamed entries an install claimed;
// hand wiring without that ownership record must survive uninstall (#2648).
func removeAdoptedDejaEntries(path, container string, m map[string]any) {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !mcpEntryRunsDeja(m[key]) {
			continue
		}
		name := container + "." + key
		if blockWasAdded(path, name) {
			delete(m, key)
			forgetBlockAdded(path, name)
		}
	}
}

// removeTOMLMCPBlock removes one named MCP table without disturbing its
// siblings; reading the header as TOML code keeps adopted commented blocks
// removable on uninstall (#2648).
func removeTOMLMCPBlock(s, key string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		found, ok := tomlMCPHeader(lines[i])
		if !ok || !tomlBlockOwnedBy(found, key) {
			out = append(out, lines[i])
			continue
		}
		// To the next header, whatever it is. A sub-table of this same server
		// opens one of its own, and the loop above takes it on the next turn
		// because it belongs to the key being removed — which is the whole of
		// the fix for #2716, and why there is no second skip here.
		i++
		for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(tomlCode(lines[i])), "[") {
			i++
		}
		i--
	}
	return strings.Join(out, "\n")
}

// tomlBlockOwnedBy reports whether a header names the block being removed or
// one of its sub-tables.
//
// TOML lets a server carry sub-tables, and `[mcp_servers.deja.env]` opens with
// `[` like any other header — so removing the block by walking to the next one
// stopped there and left the sub-table behind, naming a server that had just
// gone (#2716). Reading the sub-table as part of the block is enough: the walk
// stops at its header and the caller's next turn removes it in the same way.
//
// The dot is what decides. `deja.env` belongs to `deja`; `deja-vu` does not,
// and a bare prefix test would take a hand-wired server with it.

func tomlBlockOwnedBy(found, key string) bool {
	return found == key || strings.HasPrefix(found, key+".")
}
