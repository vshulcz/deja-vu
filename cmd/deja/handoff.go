package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

const handoffBudget = 6 * 1024

// runHandoff packages the live context of a session — the problem, what was
// concluded, where it stopped — and continues it in a different agent.
// Default output is the digest itself so it composes into any CLI:
//
//	codex "$(deja handoff --to codex)"
//
// --exec launches the target agent directly with the digest as its first
// prompt. The source is the newest session for the current project unless an
// id-prefix picks one explicitly.
func runHandoff(dir string, args []string, stdout io.Writer) error {
	target := ""
	prefix := ""
	doExec := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to":
			// Empty as well as absent: an empty value fell through the
			// `target != ""` gate below and printed the paste-only handoff, so
			// a scripted `--to "$AGENT"` with the variable unset read like a
			// handoff to an agent (#1647).
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				// Named targets, like the refusal below: the reader who typed
				// an empty --to needs the same list as the one who typed a
				// wrong name.
				return fmt.Errorf("handoff: --to needs an agent name: %s", strings.Join(handoffTargets(), ", "))
			}
			target = args[i+1]
			i++
		case "--exec":
			doExec = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("handoff: unknown flag %q", args[i])
			}
			// The last one used to win, so a stray word replaced the id and
			// the refusal named it as the session that was missing (#2251).
			if prefix != "" {
				return fmt.Errorf("handoff takes one id-prefix — got %q and %q", prefix, args[i])
			}
			prefix = args[i]
		}
	}
	if canon, ok := handoffAlias[target]; ok {
		target = canon
	}
	pasteOnly := target == "" || handoffPasteOnly[target]
	if !pasteOnly {
		if _, ok := handoffCommand(target, ""); !ok {
			return fmt.Errorf("don't know how to hand off to %q; targets: %s (or omit --to and paste the digest anywhere)", target, strings.Join(handoffTargets(), ", "))
		}
	}
	if pasteOnly && doExec {
		if target == "" {
			return fmt.Errorf("handoff --exec needs --to <agent>: %s", strings.Join(handoffTargets(), ", "))
		}
		return fmt.Errorf("%s has no CLI prompt entry — run `deja handoff --to %s` and paste the digest into a new chat", target, target)
	}
	s, err := handoffSource(dir, prefix)
	if err != nil {
		return err
	}
	if err := denyPolicyHidden(prefix, s, os.Stderr); err != nil {
		return err
	}
	// The exclude list is a privacy control that only runs at ingest, so an
	// index built before the pattern still holds the session. share refuses it
	// for that reason (#1307) and promote followed (#2278); handing the same
	// session to another agent is the same move with a longer extract (#2280).
	if sources.ExcludedProject(s.Project) {
		return fmt.Errorf("%s is in a project your exclude list covers — `deja index --rebuild` drops it from the index, or remove the pattern to hand it off", prefix)
	}
	// Source receipt: the user must always see WHAT is being handed off —
	// wrong-project or stale handoffs should be obvious before they land.
	age := "unknown age"
	if !s.Updated.IsZero() {
		age = humanAge(time.Since(s.Updated))
	}
	fmt.Fprintf(os.Stderr, "deja: handing off %s · %s · %s · %s\n", s.Harness, s.Project, digest.Short(s.ID), age)
	if !s.Updated.IsZero() && time.Since(s.Updated) > 7*24*time.Hour {
		// humanAge already ends in "old"; appending the word again printed
		// "this session is 11d old old" (#743).
		fmt.Fprintf(os.Stderr, "deja: note — this session is %s; if you meant newer work, pass an id-prefix (see `deja last`)\n", age)
	}
	digest := digest.Handoff(s, handoffBudget)
	usage.RecordDigestPolicyFrom(dir, usage.KindHandoff, digest, "", 1, rawSize([]model.Session{s}), projectsOf(s), "")
	if !doExec {
		printSanitized(stdout, digest)
		if pasteOnly {
			fmt.Fprintf(os.Stderr, "\npaste this into a new chat, or hand off directly: deja handoff --to <%s> [--exec]\n", strings.Join(handoffTargets(), "|"))
		} else {
			argv, _ := handoffCommand(target, "")
			head := make([]string, 0, len(argv))
			for _, a := range argv {
				if a != "" {
					head = append(head, a)
				}
			}
			fmt.Fprintf(os.Stderr, "\nhand it off:\n  %s \"$(deja handoff --to %s%s)\"\nor run it now: deja handoff --to %s%s --exec\n",
				strings.Join(head, " "), target, prefixArg(prefix), target, prefixArg(prefix))
		}
		return nil
	}
	// A prompt can never start with "-" today, but keep the invariant explicit
	// so a future digest change cannot turn the prompt into a flag.
	if strings.HasPrefix(digest, "-") {
		digest = " " + digest
	}
	argv, _ := handoffCommand(target, digest)
	if _, err := exec.LookPath(argv[0]); err != nil {
		return fmt.Errorf("handoff: %s is not installed (looked for %q in PATH)", target, argv[0])
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func humanAge(d time.Duration) string {
	switch {
	case d < 0:
		// A timestamp ahead of the clock. Transcripts carry whatever time the
		// machine that wrote them had, and a store synced from a machine whose
		// clock runs fast brings that here — measured as "-576000m old" on a
		// session dated a year ahead, since every negative duration fell into
		// the minutes branch. The receipt exists for the reader to sanity-check
		// what is being handed off, so it says what it knows rather than a
		// number that cannot be true.
		return "unknown age"
	case d < time.Hour:
		return fmt.Sprintf("%dm old", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh old", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd old", int(d.Hours()/24))
	}
}

func prefixArg(prefix string) string {
	if prefix == "" {
		return ""
	}
	return " " + prefix
}

// handoffSource resolves the session being handed off: an explicit id-prefix,
// or the newest indexed session for the project in the current directory.
func handoffSource(dir, prefix string) (model.Session, error) {
	if err := index.Ensure(dir, "", false, os.Stderr); err != nil {
		return model.Session{}, err
	}
	if prefix != "" {
		s, ok, err := findByPrefix(dir, prefix)
		noteAmbiguousPrefix(dir, prefix, "handing off")
		if err != nil {
			return model.Session{}, err
		}
		if !ok {
			return model.Session{}, fmt.Errorf("no session matches %q", prefix)
		}
		return s, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return model.Session{}, err
	}
	var newest model.Session
	distinct := map[string]bool{}
	// By session, not by pass: a directory yields several project-name
	// candidates and the same session answers to more than one of them, so
	// counting per pass told the reader the rule hides more than the project
	// holds (#981).
	hidden := map[string]bool{}
	// Newest of the withheld ones, so the pick can say it is not the latest
	// work in this project rather than handing over older work in silence.
	var hiddenNewest time.Time
	for _, name := range digest.ProjectNameCandidates(cwd) {
		ss, err := index.RecentInProject(dir, name, 3)
		if err != nil || len(ss) == 0 {
			continue
		}
		// Nobody named this session — deja is choosing it, which is what a
		// listing does and what a listing filters (#937). Without this the
		// pick landed on a session the rule keeps out of recall and packaged
		// its content for another agent (#953).
		// Synced work is offered by recall under the looser rule, but handing
		// it to another agent on a directory-name match is a different act:
		// from a directory named api, the newest match was a teammate's
		// clients/acme/api (#2347).
		var own []model.Session
		for _, s := range ss {
			if index.ProjectInScopeStrict(s.Project, name) {
				own = append(own, s)
			}
		}
		ss = own
		kept, _ := policyFilterSessionsCounted(policy.ActivationSearch, ss)
		keptIDs := map[string]bool{}
		for _, k := range kept {
			keptIDs[k.ID] = true
		}
		for _, s := range ss {
			if !keptIDs[s.ID] {
				hidden[s.ID] = true
				if s.Updated.After(hiddenNewest) {
					hiddenNewest = s.Updated
				}
			}
		}
		if len(kept) == 0 {
			continue
		}
		distinct[kept[0].ID] = true
		if kept[0].Updated.After(newest.Updated) {
			newest = kept[0]
		}
	}
	if newest.ID == "" {
		if len(hidden) > 0 {
			return model.Session{}, errors.New(strings.TrimPrefix(policyHiddenNote(policy.ActivationSearch, len(hidden)), "deja: "))
		}
		return model.Session{}, idPrefixNeeded(dir, "handoff needs a session to package", "no indexed sessions for this project — pass a session id-prefix (see `deja last`)")
	}
	// The pick is deja's, and a rule that removed newer work from the choice
	// changes what the pick means: handing over a month-old session while
	// today's is withheld looked exactly like a project nobody has touched
	// since (#1013).
	if hiddenNewest.After(newest.Updated) {
		fmt.Fprintf(os.Stderr, "deja: newer work in this project is withheld by the trust policy (%s: %s) — this is the newest session it allows\n",
			policy.ActivationSearch, policy.Load().Describe(policy.ActivationSearch))
	}
	if len(distinct) > 1 {
		fmt.Fprintf(os.Stderr, "deja: %d different sessions match this directory's project names — picked the newest; pass an id-prefix to choose (see `deja last`)\n", len(distinct))
	}
	return newest, nil
}

// handoffCommand maps a target agent to the argv that opens it with an
// initial prompt. Prompt is passed as a single argv element — no shell.
func handoffCommand(target, prompt string) ([]string, bool) {
	switch target {
	case "claude":
		return []string{"claude", prompt}, true
	case "codex":
		return []string{"codex", prompt}, true
	case "opencode":
		return []string{"opencode", "--prompt", prompt}, true
	case "gemini":
		return []string{"gemini", "-i", prompt}, true
	case "qwen":
		return []string{"qwen", "-i", prompt}, true
	case "aider":
		return []string{"aider", "--message", prompt}, true
	case "pi":
		return []string{"pi", prompt}, true
	case "omp":
		return []string{"omp", prompt}, true
	case "grok":
		return []string{"grok", prompt}, true
	case "cursor":
		return []string{"cursor-agent", prompt}, true
	case "copilot":
		return []string{"copilot", "-p", prompt}, true
	case "cline":
		// The CLI runs the extension headlessly and takes the prompt as its
		// argument; verified by running one.
		return []string{"cline", prompt}, true
	case "goose":
		return []string{"goose", "run", "-t", prompt}, true
	case "kimi":
		return []string{"kimi", "-p", prompt}, true
	case "antigravity":
		// Antigravity's CLI is `agy`; -i seeds a prompt into an interactive session.
		return []string{"agy", "-i", prompt}, true
	default:
		return nil, false
	}
}

// handoffAlias lets a target be spelled the way its own CLI is invoked.
var handoffAlias = map[string]string{"agy": "antigravity"}

// handoffPasteOnly mirrors the registry's `handoff: paste` entries; the
// capability drift test keeps the two in sync.
// Zed is paste-only for the same reason Roo is: the agent lives in the editor,
// so there is no CLI invocation to hand a prompt to.
var handoffPasteOnly = map[string]bool{"openclaw": true, "hermes": true, "roo": true, "zed": true, "deepseek": true, "prime": true}

func handoffTargets() []string {
	return []string{"claude", "codex", "opencode", "cursor", "copilot", "gemini", "qwen", "antigravity", "aider", "pi", "omp", "grok", "cline", "goose", "kimi"}
}
