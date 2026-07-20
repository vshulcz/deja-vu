package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
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
func runHandoff(args []string, stdout io.Writer) error {
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
	if target == "" {
		return fmt.Errorf("handoff needs --to <agent>: %s", strings.Join(handoffTargets(), ", "))
	}
	argv, ok := handoffCommand(target, "")
	if !ok {
		return fmt.Errorf("don't know how to hand off to %q; targets: %s", target, strings.Join(handoffTargets(), ", "))
	}
	s, err := handoffSource(prefix)
	if err != nil {
		return err
	}
	digest := handoffDigest(s, handoffBudget)
	if !doExec {
		printSanitized(stdout, digest)
		fmt.Fprintf(os.Stderr, "\nhand it off:\n  %s \"$(deja handoff --to %s%s)\"\nor run it now: deja handoff --to %s%s --exec\n",
			strings.Join(argv, " "), target, prefixArg(prefix), target, prefixArg(prefix))
		return nil
	}
	argv, _ = handoffCommand(target, digest)
	if _, err := exec.LookPath(argv[0]); err != nil {
		return fmt.Errorf("handoff: %s is not installed (looked for %q in PATH)", target, argv[0])
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func prefixArg(prefix string) string {
	if prefix == "" {
		return ""
	}
	return " " + prefix
}

// handoffSource resolves the session being handed off: an explicit id-prefix,
// or the newest indexed session for the project in the current directory.
func handoffSource(prefix string) (model.Session, error) {
	if err := index.Ensure(index.DefaultDir(), "", false, os.Stderr); err != nil {
		return model.Session{}, err
	}
	if prefix != "" {
		s, ok, err := findByPrefix(prefix)
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
	for _, name := range projectNameCandidates(cwd) {
		ss, err := index.RecentProject(index.DefaultDir(), name, 1)
		if err != nil || len(ss) == 0 {
			continue
		}
		if ss[0].Updated.After(newest.Updated) {
			newest = ss[0]
		}
	}
	if newest.ID == "" {
		return model.Session{}, fmt.Errorf("no indexed sessions for this project — pass a session id-prefix (see `deja last`)")
	}
	return newest, nil
}

func projectNameCandidates(cwd string) []string {
	names := []string{sources.ClaudeProjectName(cwd)}
	if base := filepath.Base(cwd); base != "" {
		if two := filepath.Join(filepath.Base(filepath.Dir(cwd)), base); two != names[0] {
			names = append(names, two)
		}
		if base != names[0] {
			names = append(names, base)
		}
	}
	return names
}

// agentArtifactMarkers flag transcript entries that are tool output or
// harness plumbing recorded under a user/assistant role — noise that would
// bury the actual problem statement in a handoff.
var agentArtifactMarkers = []string{
	"<system-reminder>",
	"</teammate-message>",
	"<task-notification>",
	"<command-name>",
	"Bash completed with no output",
	"Shell cwd was reset",
	"tool_use_error",
	"no need to Read it back)",
	"Called the Read tool with",
	"[Request interrupted by user]",
}

func isAgentArtifact(text string) bool {
	for _, m := range agentArtifactMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	trimmed := strings.TrimSpace(text)
	// ls dumps recorded under a user role.
	if strings.HasPrefix(trimmed, "total ") && strings.Contains(trimmed, "rwx") {
		return true
	}
	// Tool echoes: file writes, diffs, command transcripts.
	for _, p := range []string{"File created successfully at:", "The file ", "diff --git ", "$ "} {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	// Long dumps with almost no prose: measure letters vs symbols/digits in
	// the first few hundred bytes — listings and tables sit far below prose.
	if len(trimmed) > 400 {
		letters, others := 0, 0
		for _, r := range trimmed[:400] {
			switch {
			case r == ' ':
			case ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || r >= 0x400: // latin + cyrillic
				letters++
			default:
				others++
			}
		}
		if others > letters {
			return true
		}
	}
	return false
}

// handoffClean drops agent artifacts so the digest carries conversation, not
// tool output replayed under a user role.
func handoffClean(s model.Session) model.Session {
	out := s
	out.Messages = nil
	for _, m := range s.Messages {
		if isAgentArtifact(m.Text) {
			continue
		}
		out.Messages = append(out.Messages, m)
	}
	return out
}

// handoffDigest is the package the target agent starts from: framing header,
// the user's problem statements, key conclusions, and the tail of the
// conversation — the "where it stopped" part a plain summary loses.
func handoffDigest(s model.Session, budget int) string {
	s = handoffClean(s)
	var b strings.Builder
	date := "unknown"
	if !s.Updated.IsZero() {
		date = s.Updated.Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "You are picking up work handed off from a %s session (project %s, %s). ", s.Harness, s.Project, date)
	b.WriteString("Below is the packaged context: the problem, key conclusions so far, and where it stopped. Continue from there instead of re-deriving what is already done.\n\n")
	body := shareDigest(s, budget*3/4)
	// Drop the share header line; the framing above replaces it.
	if i := strings.Index(body, "\n"); i > 0 && strings.HasPrefix(body, "# deja share:") {
		body = strings.TrimSpace(body[i:])
	}
	b.WriteString(body)
	if tail := handoffTail(s, budget-b.Len()); tail != "" {
		b.WriteString("\n\n## Where it stopped\n\n")
		b.WriteString(tail)
	}
	return strings.TrimSpace(b.String()) + "\n"
}

// handoffTail returns the last few substantive exchanges verbatim so the
// target agent sees the live state, not just conclusions.
func handoffTail(s model.Session, budget int) string {
	if budget <= 0 {
		return ""
	}
	var picked []model.Message
	for i := len(s.Messages) - 1; i >= 0 && len(picked) < 4; i-- {
		m := s.Messages[i]
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if noisyShareMessage(m.Text) || shareMessageText(m.Text) == "" {
			continue
		}
		picked = append(picked, m)
	}
	var b strings.Builder
	for i := len(picked) - 1; i >= 0; i-- {
		m := picked[i]
		chunk := fmt.Sprintf("**%s:** %s\n\n", m.Role, shareMessageText(m.Text))
		if b.Len()+len(chunk) > budget {
			chunk = utf8SafeCut(chunk, budget-b.Len())
		}
		b.WriteString(chunk)
		if b.Len() >= budget {
			break
		}
	}
	return strings.TrimSpace(b.String())
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
		return []string{"opencode", "run", prompt}, true
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
	default:
		return nil, false
	}
}

func handoffTargets() []string {
	return []string{"claude", "codex", "opencode", "gemini", "qwen", "aider", "pi", "grok"}
}
