// Command commandgate asks which gate keeps the per-action hook from saying
// what a command settled last time.
//
// #3001 measured 48 probes on a real store: 13 pointers, 35 silences and not
// one decision. The mechanism works on a seeded stand, so the question is which
// condition real sessions fail. commandDecisionLine walks the ranked sessions
// and needs all of: the project's policy to allow the session, the session to
// be usable, the session to have actually run the command, and the digest to
// yield a conclusion from its tail. This counts where the walk stops, per
// command, over the store's own most-run commands.
//
//	go run ./scripts/commandgate [-n 40] [-dir ~/.cache/deja/index.db]
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The hook's own numbers, so this harness asks the question the hook asks.
const (
	decisionScan   = 40
	decisionBudget = 400
	msgTail        = 12
)

type stop struct {
	name  string
	count int
}

func main() {
	n := flag.Int("n", 40, "how many of the store's most-run commands to ask about")
	tail := flag.Int("tail", msgTail, "how many of a session's last messages the decision is drawn from")
	dir := flag.String("dir", index.DefaultDir(), "index directory to read")
	flag.Parse()

	cmds := index.ReadCommands(*dir)
	if len(cmds) == 0 {
		fmt.Fprintln(os.Stderr, "no commands in this store")
		os.Exit(1)
	}
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Runs > cmds[j].Runs })
	// The hook says nothing about an inspection command by design — status,
	// diff, ls — so counting them here would answer a question nobody asked.
	// What is left is the commands that do something.
	kept := cmds[:0]
	skipped := 0
	for _, c := range cmds {
		if index.InspectionCommand(c.Command) {
			skipped++
			continue
		}
		kept = append(kept, c)
	}
	cmds = kept
	if len(cmds) > *n {
		cmds = cmds[:*n]
	}

	pol := policy.Load()
	states := sources.PromotedLifecycles()
	counts := map[string]int{}
	var lines []string
	pairs := 0
	for _, use := range cmds {
		// One row per project the command was run in: the hook asks from the
		// directory the action is in, so a command with history in three
		// projects is three different questions.
		for _, project := range projectsOf(use) {
			where, detail := walk(*dir, use, project, pol, states, *tail)
			counts[where]++
			pairs++
			lines = append(lines, fmt.Sprintf("  %-9s %-40s %-18s %s", where, short(use.Command), project, detail))
		}
	}

	order := []stop{
		{name: "decision"}, {name: "tail"}, {name: "ran"},
		{name: "policy"}, {name: "ranked"}, {name: "terms"},
	}
	fmt.Printf("%d commands that do something in %d projects (%d inspection commands skipped), tail %d, %s\n",
		len(cmds), pairs, skipped, *tail, *dir)
	for i := range order {
		order[i].count = counts[order[i].name]
	}
	for _, s := range order {
		fmt.Printf("  %-9s %3d  %s\n", s.name, s.count, meaning(s.name))
	}
	fmt.Println()
	for _, line := range lines {
		fmt.Println(line)
	}
}

// walk repeats commandDecisionLine's conditions and reports the first one that
// held everything back, with a number for the step it got to.
func walk(dir string, use index.CommandUse, project string, pol policy.Policy, states map[string]sources.Lifecycle, tail int) (string, string) {
	cmd := use.Command
	terms := prompt.Terms(normalized(cmd))
	if len(terms) == 0 {
		return "terms", "the command reduces to no searchable word"
	}
	cwd := project
	ranked, _, _, _, err := index.ProjectRelevant(dir, digest.ProjectNameCandidates(cwd), terms, decisionScan)
	if err != nil || len(ranked) == 0 {
		return "ranked", fmt.Sprintf("no session ranked for %v", terms)
	}
	allowed, ran := 0, 0
	for _, s := range ranked {
		if !pol.Allows(policy.ActivationAuto, s.Project) || !usable(s, states) {
			continue
		}
		allowed++
		whole, ok, ferr := index.FindByIdentity(dir, s.Harness, s.ID)
		if ferr != nil || !ok || !index.SessionRanCommand(whole, cmd) {
			continue
		}
		ran++
		last := s
		if tail > 0 && len(last.Messages) > tail {
			last.Messages = last.Messages[len(last.Messages)-tail:]
		}
		if cs := digest.Conclusions(last, decisionBudget, 1); len(cs) > 0 {
			return "decision", fmt.Sprintf("ranked %d, allowed %d, ran %d", len(ranked), allowed, ran)
		}
	}
	switch {
	case allowed == 0:
		return "policy", fmt.Sprintf("ranked %d, none of them usable", len(ranked))
	case ran == 0:
		return "ran", fmt.Sprintf("ranked %d, allowed %d, none of them ran it", len(ranked), allowed)
	default:
		return "tail", fmt.Sprintf("ranked %d, allowed %d, ran %d, no conclusion in the tail", len(ranked), allowed, ran)
	}
}

// usable mirrors decisionUsable: a session that gave up, or one whose note has
// been rejected, superseded or gone stale, is not one to repeat.
func usable(s model.Session, states map[string]sources.Lifecycle) bool {
	if s.GaveUp {
		return false
	}
	state := s.Lifecycle
	if lc, ok := states[s.Harness+":"+s.ID]; ok && lc.State != "" {
		state = lc.State
	}
	switch state {
	case "rejected", "superseded", "stale":
		return false
	}
	return true
}

func normalized(cmd string) string {
	fields := strings.Fields(cmd)
	var out []string
	for _, f := range fields {
		if strings.HasPrefix(f, "-") {
			continue
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

// projectsOf lists the projects a command has been run in, in a stable order so
// two runs of this harness ask the same questions.
func projectsOf(use index.CommandUse) []string {
	names := make([]string, 0, len(use.ByProject))
	for p := range use.ByProject {
		names = append(names, p)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return []string{""}
	}
	return names
}

func short(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 50 {
		return s[:49] + "…"
	}
	return s
}

func meaning(name string) string {
	switch name {
	case "decision":
		return "the hook has a line to say"
	case "tail":
		return "a session ran it, and its last turns settle nothing"
	case "ran":
		return "sessions ranked for the words, none of them ran the command"
	case "policy":
		return "every ranked session is out of scope or unusable"
	case "ranked":
		return "nothing ranked for the command's words"
	case "terms":
		return "the command has no word to search by"
	}
	return ""
}
