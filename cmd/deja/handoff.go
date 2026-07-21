package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
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
			if i+1 >= len(args) {
				return fmt.Errorf("handoff: --to needs an agent name")
			}
			target = args[i+1]
			i++
		case "--exec":
			doExec = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("handoff: unknown flag %s", args[i])
			}
			prefix = args[i]
		}
	}
	pasteOnly := target == "" || target == "antigravity"
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
	// Source receipt: the user must always see WHAT is being handed off —
	// wrong-project or stale handoffs should be obvious before they land.
	age := "unknown age"
	if !s.Updated.IsZero() {
		age = humanAge(time.Since(s.Updated))
	}
	fmt.Fprintf(os.Stderr, "deja: handing off %s · %s · %s · %s\n", s.Harness, s.Project, digest.Short(s.ID), age)
	if !s.Updated.IsZero() && time.Since(s.Updated) > 7*24*time.Hour {
		fmt.Fprintf(os.Stderr, "deja: note — this session is %s old; if you meant newer work, pass an id-prefix (see `deja last`)\n", age)
	}
	digest := digest.Handoff(s, handoffBudget)
	usage.RecordDigest(dir, usage.KindHandoff, digest, 1, rawSize([]model.Session{s}))
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
	for _, name := range digest.ProjectNameCandidates(cwd) {
		ss, err := index.RecentProject(dir, name, 1)
		if err != nil || len(ss) == 0 {
			continue
		}
		distinct[ss[0].ID] = true
		if ss[0].Updated.After(newest.Updated) {
			newest = ss[0]
		}
	}
	if newest.ID == "" {
		return model.Session{}, fmt.Errorf("no indexed sessions for this project — pass a session id-prefix (see `deja last`)")
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
	case "grok":
		return []string{"grok", prompt}, true
	case "cursor":
		return []string{"cursor-agent", prompt}, true
	case "copilot":
		return []string{"copilot", "-p", prompt}, true
	default:
		return nil, false
	}
}

func handoffTargets() []string {
	return []string{"claude", "codex", "opencode", "cursor", "copilot", "gemini", "qwen", "aider", "pi", "grok"}
}
