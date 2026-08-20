package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
)

type installResult struct{ Path, Action string }

func runInstall(dir string, args []string, uninstall bool) error {
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
	// missing while printing the target it had been given (#1078).
	if len(targetArgs) > 1 {
		for _, a := range targetArgs {
			if strings.HasPrefix(a, "--") && a != "--all" && a != "--auto" {
				return fmt.Errorf("%s: unknown flag %q — it takes a target plus --no-guidance, --no-index or --force", verb, a)
			}
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
			switch t {
			case "claude-code":
				targets = append(targets, "claude-auto")
			case "codex":
				targets = append(targets, "codex-auto")
			case "opencode":
				targets = append(targets, "opencode-auto")
			case "gemini":
				targets = append(targets, "gemini-auto")
			case "qwen":
				targets = append(targets, "qwen-auto")
			case "kimi":
				targets = append(targets, "kimi-auto")
			case "cursor":
				targets = append(targets, "cursor-auto")
			case "pi":
				targets = append(targets, "pi-auto")
			case "hermes":
				targets = append(targets, "hermes-auto")
			case "openclaw":
				targets = append(targets, "openclaw-auto")
			case "antigravity":
				targets = append(targets, "antigravity-auto")
			case "cline":
				targets = append(targets, "cline-auto")
			case "goose":
				targets = append(targets, "goose-auto")
			case "grok":
				targets = append(targets, "grok-auto")
			default:
				// The IDE extensions: the MCP server is the deepest integration
				// those harnesses support.
				targets = append(targets, t)
			}
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
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	// Remembered so a later upgrade can refresh exactly these and nothing
	// else: the generators change, the files on disk do not.
	defer recordWiring(targets, uninstall)
	banner := !uninstall && (targetArgs[0] == "--auto" || targetArgs[0] == "--all") && logoWanted(os.Stdout)
	type lineItem struct{ target, action, path string }
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
	note := func(t string, err error) {
		refused = append(refused, fmt.Sprintf("%s: %v", t, err))
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
		if banner {
			done = append(done, lineItem{t, r.Action, shortHome(r.Path)})
		} else {
			if r.Path == "" {
				fmt.Printf("%s: %s\n", t, r.Action)
			} else {
				fmt.Printf("%s: %s %s\n", t, r.Action, r.Path)
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
	if len(refused) > 0 {
		verb := "install"
		if uninstall {
			verb = "uninstall"
		}
		return fmt.Errorf("%s finished what it could; %d target%s refused: %s — check those paths' permissions and run it again",
			verb, len(refused), pluralS(len(refused)), strings.Join(refused, "; "))
	}
	// Every install builds, not only --auto and --all. Installing is the one
	// moment a person has already accepted a wait — they just ran an installer
	// — and spending the build here is what keeps the first real use, usually
	// the first agent turn, instant.
	if !uninstall && !noIndex {
		installIndexWarmup(dir, mcpCount, hookCount, guidanceCount,
			targetArgs[0] == "--auto" || targetArgs[0] == "--all")
	}
	if banner {
		info := append(brandInfo(), "")
		nameW := 0
		for _, d := range done {
			if len(d.target) > nameW {
				nameW = len(d.target)
			}
		}
		for _, d := range done {
			info = append(info, fmt.Sprintf("%-*s  %s%-9s%s %s", nameW, d.target, logoBold, d.action, logoReset, logoDim+d.path+logoReset))
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
	return nil
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
		return installClaudeAuto(exe, uninstall)
	case "claude-code", "claude":
		return installClaude(exe, uninstall)
	case "codex":
		return installCodex(exe, uninstall)
	case "codex-auto":
		return installCodexAuto(exe, uninstall)
	case "cursor":
		return installCursor(exe, uninstall)
	case "cursor-auto":
		return installCursorAuto(exe, uninstall)
	case "gemini":
		return installMCPJSON(filepath.Join(sources.GeminiHome(), "settings.json"), exe, uninstall)
	case "gemini-auto":
		// MCP first, then the hooks extension. `--auto` maps gemini to this
		// target alone, so installing only the extension left the harness
		// without the tools: its own `gemini mcp list` said "No MCP servers
		// configured" on a machine that had just run `deja install --auto`.
		// grok-auto pairs them the same way.
		if _, err := installMCPJSON(filepath.Join(sources.GeminiHome(), "settings.json"), exe, uninstall); err != nil {
			return installResult{}, err
		}
		return installGeminiAuto(exe, uninstall)
	case "antigravity":
		return installMCPJSON(filepath.Join(antigravityConfigHome(), "mcp_config.json"), exe, uninstall)
	case "antigravity-auto":
		return installAntigravityAuto(exe, uninstall)
	case "grok":
		return installGrok(exe, uninstall)
	case "grok-auto":
		if _, err := installGrok(exe, uninstall); err != nil {
			return installResult{}, err
		}
		return installGrokAuto(exe, uninstall)
	case "qwen":
		return installMCPJSON(filepath.Join(sources.QwenConfigDir(), "settings.json"), exe, uninstall)
	case "qwen-auto":
		if _, err := installMCPJSON(filepath.Join(sources.QwenConfigDir(), "settings.json"), exe, uninstall); err != nil {
			return installResult{}, err
		}
		return installQwenAuto(exe, uninstall)
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

func installClaudeAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installClaude(exe, uninstall); err != nil {
		return installResult{}, err
	}
	if _, err := installClaudeCommands(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installClaudeHook(exe, uninstall)
}

func installAntigravityAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installMCPJSON(filepath.Join(antigravityConfigHome(), "mcp_config.json"), exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installAntigravityPlugin(exe, uninstall)
}

func installOpenClawAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installOpenClawMCP(exe, uninstall); err != nil {
		return installResult{}, err
	}
	// The plugin covers the prompt; the bootstrap hook covers the session.
	if _, err := installOpenClawPlugin(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installOpenClawHooks(exe, uninstall)
}

func installHermesAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installHermesMCP(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installHermesPlugin(exe, uninstall)
}

func installPiAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installMCPJSON(filepath.Join(sources.PiConfigDir(), "mcp.json"), exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installPiExtension(exe, uninstall)
}

func installCursorAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installCursor(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installCursorHooks(exe, uninstall)
}

func installCodexAuto(exe string, uninstall bool) (installResult, error) {
	if _, err := installCodex(exe, uninstall); err != nil {
		return installResult{}, err
	}
	res, err := installCodexHooks(exe, uninstall)
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
	if _, err := installOpencode(exe, uninstall); err != nil {
		return installResult{}, err
	}
	return installOpencodePlugin(exe, uninstall)
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

// forceGuidance is set by `deja install --force`: replace a skill deja can see
// has been edited since it wrote it.
var forceGuidance bool

// mentionsDeja reports whether a config snapshot carries deja's own wiring.
// The markers are what every generator writes: the subcommands the hooks call,
// and the name of the MCP server and extension entries.
func mentionsDeja(b []byte) bool {
	// The subcommands rather than the binary's name: a snapshot names whatever
	// path deja was installed from, which need not end in "deja" at all.
	for _, marker := range []string{
		"hook-prompt", "hook-context", "hook-tool", "hook-goose", "hook-plan",
		"hook-precompact", "hook-antigravity", "deja:", "\"deja\"", "deja-recall",
	} {
		if bytes.Contains(b, []byte(marker)) {
			return true
		}
	}
	return false
}

func writeIfChanged(path string, old, next []byte) (string, error) {
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
			if b, err := os.ReadFile(bak); err == nil && mentionsDeja(b) {
				_ = os.Remove(bak)
			}
		}()
	}
	tmp, terr := os.CreateTemp(filepath.Dir(path), ".deja-tmp-")
	if terr != nil {
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
	old, _ := os.ReadFile(path)
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, err
	}
	m, _ := root["mcpServers"].(map[string]any)
	if m == nil {
		// Adding the empty block on the way out rewrites a config that never
		// mentioned deja, and leaves a .bak of it besides (#676).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		m = map[string]any{}
		root["mcpServers"] = m
	}
	if uninstall {
		delete(m, "deja")
	} else {
		m["deja"] = mcpServerEntry(exe)
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func installClaudeHook(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	old, _ := os.ReadFile(path)
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, err
	}
	nextRoot := updateClaudeSessionStartHook(root, exe, uninstall)
	nextRoot = updateClaudeHook(nextRoot, "PreCompact", exe+" hook-precompact", "manual|auto", uninstall)
	nextRoot = updateClaudeHook(nextRoot, "UserPromptSubmit", exe+" hook-prompt", "", uninstall)
	// Recall at the moment of the action: before an edit or a command, hook-tool
	// names the file's or command's prior decision. Scoped to the tools that
	// change something so it never fires on a Read or a Glob.
	nextRoot = updateClaudeHook(nextRoot, "PreToolUse", exe+" hook-tool", "Bash|Edit|Write|MultiEdit|NotebookEdit", uninstall)
	next, err := json.MarshalIndent(nextRoot, "", "  ")
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
	}
	return ""
}

// isDejaHookCommand reports whether an existing hook entry is one of ours for
// the same subcommand. Matching on the trailing subcommand rather than the
// whole string means moving the binary replaces the old entry instead of
// leaving a duplicate that fires alongside the new one.
func isDejaHookCommand(existing any, cmd string) bool {
	s, ok := existing.(string)
	if !ok {
		return false
	}
	if s == cmd {
		return true
	}
	sub := cmd[strings.LastIndex(cmd, " ")+1:]
	return strings.HasSuffix(s, " "+sub) && strings.Contains(s, "deja")
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
			if h != nil && h["type"] == "command" && isDejaHookCommand(h["command"], cmd) {
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
	old, _ := os.ReadFile(path)
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, err
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
	next, err := json.MarshalIndent(root, "", "  ")
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
		return user, nil
	}
	return res, nil
}

func installTOML(path, block string, uninstall bool) (installResult, error) {
	old, _ := os.ReadFile(path)
	s := removeCodexDejaBlock(string(old))
	s = strings.TrimRight(s, "\n")
	if !uninstall {
		if s != "" {
			s += "\n\n"
		}
		s += block
	} else if s != "" {
		s += "\n"
	}
	a, err := writeIfChanged(path, old, []byte(s))
	return installResult{Path: path, Action: a}, err
}

func removeCodexDejaBlock(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "[mcp_servers.deja]" {
			out = append(out, lines[i])
			continue
		}
		i++
		for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
			i++
		}
		i--
	}
	return strings.Join(out, "\n")
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func antigravityConfigHome() string {
	return filepath.Join(homeDir(), ".gemini", "config")
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
	old, _ := os.ReadFile(path)
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, err
	}
	m, _ := root["mcpServers"].(map[string]any)
	if m == nil {
		// Adding the empty block on the way out rewrites a config that never
		// mentioned deja, and leaves a .bak of it besides (#676).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		m = map[string]any{}
		root["mcpServers"] = m
	}
	if uninstall {
		delete(m, "deja")
	} else {
		command, args := mcpCommandArgs(exe)
		m["deja"] = map[string]any{"type": "local", "command": command, "args": args, "tools": []string{"*"}}
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

// installOpenClawMCP wires deja into openclaw.json — OpenClaw keeps MCP
// servers under mcp.servers, not the common mcpServers root.
func installOpenClawMCP(exe string, uninstall bool) (installResult, error) {
	path := filepath.Join(sources.OpenClawStateDir(), "openclaw.json")
	old, _ := os.ReadFile(path)
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, err
	}
	mcp, _ := root["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
		root["mcp"] = mcp
	}
	servers, _ := mcp["servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		mcp["servers"] = servers
	}
	if uninstall {
		delete(servers, "deja")
	} else {
		command, args := mcpCommandArgs(exe)
		servers["deja"] = map[string]any{"command": command, "args": args}
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func installMCPJSON(path, exe string, uninstall bool) (installResult, error) {
	old, _ := os.ReadFile(path)
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return installResult{}, err
	}
	m, _ := root["mcpServers"].(map[string]any)
	if m == nil {
		// Adding the empty block on the way out rewrites a config that never
		// mentioned deja, and leaves a .bak of it besides (#676).
		if uninstall {
			return installResult{Path: path, Action: "unchanged"}, nil
		}
		m = map[string]any{}
		root["mcpServers"] = m
	}
	if uninstall {
		delete(m, "deja")
	} else {
		command, args := mcpCommandArgs(exe)
		m["deja"] = map[string]any{"command": command, "args": args}
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return installResult{}, err
	}
	next = append(next, '\n')
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func installOpencode(exe string, uninstall bool) (installResult, error) {
	dir := filepath.Join(opencodeConfigHome(), "opencode")
	path := filepath.Join(dir, "opencode.json")
	if _, err := os.Stat(path); err != nil {
		if _, e := os.Stat(filepath.Join(dir, "opencode.jsonc")); e == nil {
			path = filepath.Join(dir, "opencode.jsonc")
		}
	}
	old, _ := os.ReadFile(path)
	var next []byte
	var err error
	if strings.HasSuffix(path, ".jsonc") {
		next, err = updateOpencodeJSONC(old, exe, uninstall)
		if err != nil {
			return installResult{}, err
		}
	} else {
		next, err = updateOpencodeJSON(old, exe, uninstall)
		if err != nil {
			return installResult{}, err
		}
	}
	a, err := writeIfChanged(path, old, next)
	return installResult{Path: path, Action: a}, err
}

func updateOpencodeJSON(old []byte, exe string, uninstall bool) ([]byte, error) {
	var root map[string]any
	if len(bytes.TrimSpace(old)) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(old, &root); err != nil {
		return nil, err
	}
	m, _ := root["mcp"].(map[string]any)
	if m == nil {
		m = map[string]any{}
		root["mcp"] = m
	}
	if uninstall {
		delete(m, "deja")
	} else {
		m["deja"] = map[string]any{"type": "local", "command": []string{exe, "mcp"}}
	}
	next, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(next, '\n'), nil
}

func updateOpencodeJSONC(old []byte, exe string, uninstall bool) ([]byte, error) {
	line := fmt.Sprintf(`    "deja": {"type":"local","command":[%q,"mcp"]}`, exe)
	s := string(old)
	if strings.TrimSpace(s) == "" {
		if uninstall {
			return []byte("{}\n"), nil
		}
		return []byte("{\n  \"mcp\": {\n" + line + "\n  }\n}\n"), nil
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	start, end := -1, -1
	for i, l := range lines {
		if strings.Contains(l, `"mcp"`) && strings.Contains(l, "{") {
			start = i
			depth := strings.Count(l, "{") - strings.Count(l, "}")
			for j := i + 1; j < len(lines); j++ {
				depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
				if depth <= 0 {
					end = j
					break
				}
			}
			break
		}
	}
	if start >= 0 && end > start {
		var body []string
		for _, l := range lines[start+1 : end] {
			if !strings.Contains(l, `"deja"`) {
				body = append(body, l)
			}
		}
		if !uninstall {
			for i := len(body) - 1; i >= 0; i-- {
				trim := strings.TrimSpace(body[i])
				if trim != "" && !strings.HasPrefix(trim, "//") && !strings.HasSuffix(trim, ",") {
					body[i] += ","
					break
				}
			}
			body = append(body, line)
		}
		out := append([]string{}, lines[:start+1]...)
		out = append(out, body...)
		out = append(out, lines[end:]...)
		return []byte(strings.Join(out, "\n") + "\n"), nil
	}
	if uninstall {
		return []byte(strings.Join(lines, "\n") + "\n"), nil
	}
	// An "mcp" key exists but brace-counting could not bound it — its opening
	// brace is on a later line, or the braces are unbalanced. Adding a fresh
	// block here would leave the file with two "mcp" keys, which drops the
	// user's other servers. Refuse rather than corrupt a config that cannot be
	// rebuilt; the caller surfaces this so the user can wire deja by hand.
	if start >= 0 || opencodeHasMCPKey(lines) {
		return nil, fmt.Errorf("opencode config has an \"mcp\" block deja could not edit without risking it — add the deja server by hand")
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
	return []byte(strings.Join(out, "\n") + "\n"), nil
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
		"omp",
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
