package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
)

// installLineItem is one row of the install banner.
type installLineItem struct{ target, action, path, note, aside string }

// installRun is what one install or uninstall run gathers across its targets.
type installRun struct {
	exe       string
	uninstall bool
	guidance  bool
	banner    bool
	saidNotes map[string]bool
	// The real paths, not the display ones: an uninstall reports the snapshots
	// it left beside the configs it handled, and ~ does not stat (#2676).
	touchedPaths  []string
	done          []installLineItem
	guidanceCount int
	mcpCount      int
	hookCount     int
	// One target that refuses to be written used to end the run, so an
	// uninstall stopped halfway: the hooks were gone, one guidance file was
	// left behind, and the reader was handed a syscall for a path they did not
	// choose. Everything else is still done, and what could not be is named at
	// the end (#902).
	refused     []string
	refusedErrs []error
	// How many targets were actually written. A target nobody has heard of used
	// to still leave the CLI skill behind: `deja install frobnicate` refused the
	// target, exited 1, and wrote `~/.agents/skills/deja-search/SKILL.md` on the
	// way (#3583).
	written int
	// changed counts targets whose wiring this run actually wrote or removed,
	// for the line that says what happens next.
	changed int
}

// note records a target that could not be done.
func (ir *installRun) note(t string, err error) {
	ir.refused = append(ir.refused, fmt.Sprintf("%s: %v", t, err))
	ir.refusedErrs = append(ir.refusedErrs, err)
}

// parseInstallArgs splits the install flags from the targets.
func parseInstallArgs(args []string) (guidance, noIndex bool, targetArgs []string) {
	guidance = true
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
	return guidance, noIndex, targetArgs
}

// resolveInstallTargets turns the named targets, --all or --auto into the
// list the run walks. done is true when there is nothing to do and the run
// ends without an error.
func resolveInstallTargets(verb string, targetArgs []string, uninstall bool) (targets []string, done bool, err error) {
	// A flag deja does not know is named plainly by every other command; here
	// it fell into the target list, and the refusal then said the target was
	// missing while printing the target it had been given (#1078). At one
	// argument it fell through anyway and `deja install --nosuch` was reported
	// as an unknown target, with thirty-eight harness names for a remedy
	// (#1680). No target begins with a dash, so the shape alone settles it.
	for _, a := range targetArgs {
		if strings.HasPrefix(a, "-") && a != "--all" && a != "--auto" {
			return nil, false, fmt.Errorf("%s: unknown flag %q — it takes a target plus --no-guidance, --no-index or --force", verb, a)
		}
	}
	if len(targetArgs) == 0 {
		// The first command a new machine runs, so a bare word they have to go
		// look up is the worst possible answer. Every other command in this
		// position prints the shape it wants (#830), and this one can do
		// better still: name the agents actually present here.
		if found := existingTargets(); len(found) > 0 {
			sort.Strings(found)
			return nil, false, fmt.Errorf("%s needs a target — found here: %s (or --all, --auto)", verb, strings.Join(found, ", "))
		}
		return nil, false, fmt.Errorf("%s needs a target — no agent config found here; `deja help install` lists every target deja knows", verb)
	}
	// Several names at once, because doctor asks for exactly that: the stale
	// wiring row prints `deja install ` and the targets it recorded, and on any
	// machine with two of them that command answered "install needs a target"
	// — the remedy for a binary that moved was a line that could not run
	// (#3686).
	if len(targetArgs) > 1 {
		for _, a := range targetArgs {
			if a == "--all" || a == "--auto" {
				return nil, false, fmt.Errorf("%s: %s takes no other target beside it", verb, a)
			}
		}
	}
	targets = append([]string(nil), targetArgs...)
	if targetArgs[0] == "--auto" {
		targets = nil
		for _, t := range existingTargets() {
			targets = append(targets, autoTargetFor(t))
		}
		if len(targets) == 0 {
			noAgentConfigHere(uninstall)
			return nil, true, nil
		}
	}
	if targetArgs[0] == "--all" {
		targets = existingTargets()
		if len(targets) == 0 {
			noAgentConfigHere(uninstall)
			return nil, true, nil
		}
		if !uninstall {
			targets = withWiredAutoTargets(targets)
		}
	}
	// Uninstalling has to reach further than installing: --all wires MCP, but
	// --auto may have written hooks and plugins too, and leaving those behind
	// means every agent keeps shelling out to a binary the user just removed.
	if uninstall {
		targets = withAutoTargets(targets)
		// The status bar is not a harness, so it is in no detected list and
		// `uninstall --all` walked past it: the entry stayed, running the
		// binary the reader was in the middle of removing, on every refresh
		// (#3684). Only when it is deja's — installStatusline leaves anyone
		// else's alone, and adding the target where there is nothing of ours
		// would print a line about it to everybody.
		if targetArgs[0] == "--all" && statuslineIsDejas() && !slices.Contains(targets, "statusline") {
			targets = append(targets, "statusline")
		}
		// A name it does not know drops out of that expansion and leaves
		// nothing to do, so `deja uninstall claude-cod` printed not one word
		// and exited 0 — while `deja install claude-cod` names the near miss
		// (#2273). Someone removing deja by a half-remembered name was told it
		// worked, with the wiring still in place.
		if len(targets) == 0 {
			return nil, false, unknownTargetError(targetArgs[0])
		}
	}
	return targets, false, nil
}

// target installs or uninstalls one target, its guidance and command file,
// and prints or queues its line.
func (ir *installRun) target(t string) {
	uninstall, banner := ir.uninstall, ir.banner
	guidanceMoved := false
	r, err := installTarget(t, ir.exe, uninstall)
	// A client saved a config under the edit: edited again from its
	// version rather than renamed over it (#4561).
	for i := 0; i < configRaceRetries && errors.Is(err, errConfigChanged); i++ {
		r, err = installTarget(t, ir.exe, uninstall)
	}
	if err != nil {
		ir.note(t, err)
		return
	}
	// The copy of the reader's rules leaves with the harness, whether or not
	// --no-guidance was given: the markers are deja's, and an uninstall is
	// the promise that nothing of deja's is left in the agent's files (#4086).
	if uninstall {
		if p, a, err := dropRulesBlock(t); err != nil {
			ir.note(t, err)
		} else if p != "" && a != "unchanged" && !banner {
			fmt.Printf("%s: rules %s %s\n", t, a, shortHome(p))
		}
	}
	if ir.guidance {
		moved, ok := ir.targetGuidance(t)
		if !ok {
			return
		}
		guidanceMoved = moved
	}
	// A client that keeps deja off by its own switch gets the same line
	// doctor prints, unless the writer already said the entry is off
	// (#4468, #4469). Once a run: gemini and gemini-auto read one file.
	if !uninstall && !strings.Contains(r.Note, "switched off") {
		for _, off := range installClientOffNotes(t) {
			if ir.saidNotes[off] {
				continue
			}
			ir.saidNotes[off] = true
			if r.Note != "" {
				r.Note += "; "
			}
			r.Note += off
		}
	}
	// On a line of its own: it is about the agent's files, not this write.
	aside := ""
	if !uninstall {
		if n := claudeCleanupNote(t); n != "" && !ir.saidNotes[n] {
			ir.saidNotes[n] = true
			aside = n
		}
	}
	ir.written++
	if r.Action != "unchanged" || guidanceMoved {
		ir.changed++
	}
	ir.touchedPaths = append(ir.touchedPaths, r.touched()...)
	if banner {
		ir.done = append(ir.done, installLineItem{t, r.Action, shortHome(r.Path), r.Note, aside})
	} else {
		printInstallTargetLine(t, r, aside, uninstall, guidanceMoved)
	}
	if !uninstall && t != "statusline" {
		ir.mcpCount++
	}
	if !uninstall && strings.HasSuffix(t, "-auto") {
		ir.hookCount++
	}
}

// targetGuidance writes or removes one target's guidance and command file.
// moved says the guidance file changed; ok is false when the target refused.
func (ir *installRun) targetGuidance(t string) (moved, ok bool) {
	uninstall, banner := ir.uninstall, ir.banner
	gr, err := guidanceResult(t, uninstall)
	if err != nil {
		ir.note(t, err)
		return false, false
	}
	// Two targets can share one guidance harness — `gemini` and
	// `gemini-auto` do — and a note about a file is about the file,
	// not about the target that noticed it.
	if gr.Note != "" && ir.saidNotes[gr.Note] {
		gr.Note = ""
	} else if gr.Note != "" {
		ir.saidNotes[gr.Note] = true
	}
	// Removing what was never there is not news: the line would say
	// "unchanged" about a file deja has nothing in.
	quietMiss := uninstall && gr.Action == "unchanged" && gr.Note == ""
	if gr.Path != "" && !banner && !quietMiss {
		fmt.Println(guidanceOutput(t, gr))
	}
	if gr.Path != "" && gr.Action != "unchanged" {
		moved = true
	}
	if gr.Path != "" && uninstall {
		pruneGuidanceDirs(gr.Path)
	}
	if gr.Path != "" && !uninstall {
		ir.guidanceCount++
	}
	// The command rides with guidance rather than with the wiring: both
	// are text telling the agent deja exists, and --no-guidance is the
	// switch for someone who wants the plumbing without either.
	cr, err := installCommandFile(guidanceHarness(t), ir.exe, uninstall)
	if err != nil {
		ir.note(t, err)
		return moved, false
	}
	if cr.Path != "" && !banner {
		fmt.Printf("%s: command %s %s\n", t, cr.Action, shortHome(cr.Path))
	}
	if cr.Path != "" && uninstall {
		pruneGuidanceDirs(cr.Path)
	}
	return moved, true
}

// printInstallTargetLine prints one target's line outside the banner.
func printInstallTargetLine(t string, r installResult, aside string, uninstall, guidanceMoved bool) {
	switch {
	case uninstall && r.Action == "unchanged" && r.Note == "" && !guidanceMoved:
		// Nothing of deja's was in this agent's files.
		fmt.Printf("%s: not installed\n", t)
	case r.Path == "":
		fmt.Printf("%s: %s\n", t, r.Action)
	default:
		fmt.Printf("%s: %s %s\n", t, r.Action, shortHome(r.Path))
	}
	// Under the action rather than in place of it, the way guidance
	// prints its own note: the action is still what happened (#2218).
	for _, n := range []string{r.Note, aside} {
		if n != "" {
			fmt.Printf("%s%s\n", strings.Repeat(" ", len(t)+2), n)
		}
	}
}

// cliSkill writes or removes the CLI skill once the targets are done.
func (ir *installRun) cliSkill(targets []string) {
	var err error
	switch {
	case !ir.uninstall && ir.written > 0:
		var action string
		action, err = writeCLISkill()
		// Every -auto target wrote this file and not one of them said so:
		// nineteen unreported writes on the screen whose job is saying what
		// was touched (#3254). Named once, here, rather than in each
		// target's line.
		if err == nil && action != "" {
			ir.done = append(ir.done, installLineItem{target: "skill", action: action, path: shortHome(cliSkillPath())})
			ir.touchedPaths = append(ir.touchedPaths, cliSkillPath())
			if !ir.banner {
				fmt.Printf("skill: %s %s\n", action, shortHome(cliSkillPath()))
			}
		}
	case !cliSkillStillWanted(targets):
		err = removeCLISkill()
	}
	if err != nil {
		ir.note(cliSkillName, err)
	}
}

// refusal is the run's summary error, or nil when every target was done.
func (ir *installRun) refusal() error {
	if len(ir.refused) == 0 {
		return nil
	}
	verb := "install"
	if ir.uninstall {
		verb = "uninstall"
	}
	refusal := fmt.Errorf("%s finished what it could; %d target%s refused: %s — %s",
		verb, len(ir.refused), pluralS(len(ir.refused)), strings.Join(ir.refused, "; "), refusalRemedy(ir.refusedErrs))
	// Nothing was done and every refusal is a name deja does not know:
	// "finished what it could" and "fix what it reports" describe a run
	// that never started. The name and the near miss are the whole answer.
	if ir.written == 0 && allUnknownTargets(ir.refusedErrs) {
		msgs := make([]string, len(ir.refusedErrs))
		for i, e := range ir.refusedErrs {
			msgs[i] = e.Error()
		}
		refusal = fmt.Errorf("%s: %s", verb, strings.Join(msgs, "; "))
	}
	return refusal
}

// printInstallBanner prints the logo and the table of what the run wired.
func printInstallBanner(dir string, done []installLineItem, all bool) {
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
		for _, n := range []string{d.note, d.aside} {
			if n != "" {
				info = append(info, fmt.Sprintf("%-*s  %s", nameW, "", n))
			}
		}
	}
	// --all writes the MCP entries and nothing else, which is the flag's
	// job and not what its name suggests: someone repairing a machine
	// reaches for "all", gets the servers rewired, and the hooks — the
	// half that carries recall without being asked — are left exactly as
	// they were (#3422).
	if all {
		info = append(info, "", "hooks are not in this list — `deja install --auto` writes those too")
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
