package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A rule someone gives one agent never reaches the others. On the machine this
// was measured on, seven rules had been given again in at least two sessions and
// two different harnesses, and the opencode rules file — the harness with the
// most sessions in the index — held none of what the Claude Code one did
// (#4086). So the reader keeps their rules in one file, and deja copies it as a
// marked block into each agent's own global rules file, the way it already
// shares AGENTS.md with its reader for guidance.

const (
	rulesStart = "<!-- deja rules:start -->"
	rulesEnd   = "<!-- deja rules:end -->"
	// Every agent carries the block in every session, whether or not the
	// question has anything to do with it, so the file is kept to what a person
	// would write as standing rules rather than a manual.
	rulesMaxBytes = 8 << 10
)

// rulesHarnesses are the harnesses whose global rules file deja knows, in the
// order `deja rules` lists them.
var rulesHarnesses = []string{"claude-code", "codex", "opencode", "gemini", "qwen", "kimi", "grok", "goose"}

// rulesPath is the file a harness reads its global rules from, or "" when deja
// does not know one. Only paths something already showed reach the model: the
// four in retiredGuidancePaths carried deja's guidance before it moved to
// skills, grok's is the one `grok inspect` lists for Grok Build, goose's is the
// one of six a stub endpoint saw arrive (gooseHintsPath), and Claude Code's is
// its documented user memory file. Hermes, pi, cursor, zed, cline, copilot and
// vscode are left out rather than guessed: a block in a file the agent never
// reads looks like a rule that was delivered.
func rulesPath(harness string) string {
	switch harness {
	case "claude-code":
		return filepath.Join(sources.ClaudeConfigDir(), "CLAUDE.md")
	case "codex":
		return filepath.Join(sources.CodexHome(), "AGENTS.md")
	case "opencode":
		return filepath.Join(opencodeConfigHome(), "opencode", "AGENTS.md")
	case "gemini":
		return filepath.Join(sources.GeminiHome(), "GEMINI.md")
	case "qwen":
		return filepath.Join(sources.QwenConfigDir(), "QWEN.md")
	case "kimi":
		return filepath.Join(sources.KimiConfigDir(), "AGENTS.md")
	case "grok":
		return filepath.Join(sources.GrokHome(), "AGENTS.md")
	case "goose":
		return gooseHintsPath()
	}
	return ""
}

func rulesSourcePath() string {
	return filepath.Join(xdgConfigHome(), "deja", "rules.md")
}

// readRules returns the rules as they go into the block: without a byte order
// mark, with \n line endings and without trailing blank lines. An absent file
// and an empty one are the same answer — nothing to copy — and exists says
// which it was only so the status can say how to create it.
func readRules() (body string, exists bool, err error) {
	b, err := os.ReadFile(rulesSourcePath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if len(b) > rulesMaxBytes {
		return "", true, fmt.Errorf("%s is %d bytes; the limit is %d, because every agent carries these rules in every session",
			shortHome(rulesSourcePath()), len(b), rulesMaxBytes)
	}
	b = bytes.TrimPrefix(b, utf8BOM)
	body = strings.ReplaceAll(string(b), "\r\n", "\n")
	body = strings.TrimRight(body, " \t\n")
	// A marker inside the rules would end up inside the block, and the next
	// sync would cut the reader's file at it.
	for _, m := range []string{rulesStart, rulesEnd, guidanceStart, guidanceEnd, gooseRecallStart, gooseRecallEnd} {
		if strings.Contains(body, m) {
			return "", true, fmt.Errorf("%s contains %q, one of deja's own block markers — take it out", shortHome(rulesSourcePath()), m)
		}
	}
	return body, true, nil
}

func rulesBlock(body string) string {
	if body == "" {
		return ""
	}
	return rulesStart + "\n" + body + "\n" + rulesEnd + "\n"
}

// rulesTargets splits the harnesses deja is installed for into the ones with a
// known rules file and the ones without. Installed means named in the wiring
// record; a machine with no record — deja wired by hand, or by a version before
// the record — falls back to the harnesses whose config directory exists.
func rulesTargets() (known, unknown []string) {
	installed := map[string]bool{}
	for _, t := range readWiringState().Targets {
		h := guidanceHarness(t)
		// statusline, sync-timer and the like are not agents and have no
		// rules to read.
		if rulesPath(h) != "" || guidancePath(h) != "" {
			installed[h] = true
		}
	}
	if len(installed) == 0 {
		for _, h := range rulesHarnesses {
			if dirExists(filepath.Dir(rulesPath(h))) {
				installed[h] = true
			}
		}
	}
	for _, h := range rulesHarnesses {
		// The directory has to be there: a file created in a directory the
		// agent never made is one nothing reads.
		if installed[h] && dirExists(filepath.Dir(rulesPath(h))) {
			known = append(known, h)
		}
		delete(installed, h)
	}
	for h := range installed {
		unknown = append(unknown, h)
	}
	sort.Strings(unknown)
	return known, unknown
}

// rulesState compares a harness's file with the rules: "in sync", "stale" (a
// block with other text, or one left after the rules were emptied), "missing"
// (rules to copy and no block), "none" (no rules and no block), or
// "unbounded" when a marker has lost its pair and only the reader can say where
// the block ends.
func rulesState(path, body string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	doc := string(bytes.TrimPrefix(b, utf8BOM))
	if checkMarkerPairs(doc, rulesStart, rulesEnd, "rules") != nil {
		return "unbounded", nil
	}
	start, end := markerLines(doc, rulesStart, rulesEnd)
	if start < 0 || end < 0 {
		if body == "" {
			return "none", nil
		}
		return "missing", nil
	}
	if body == "" {
		return "stale", nil
	}
	inner := strings.ReplaceAll(doc[start:end], "\r\n", "\n")
	if inner == rulesBlock(body) {
		return "in sync", nil
	}
	return "stale", nil
}

func runRules(dir string, args []string) error {
	if len(args) > 0 && args[0] == "candidates" {
		return runRulesCandidates(dir, os.Stdout, args[1:])
	}
	return runRulesTo(os.Stdout, args)
}

func runRulesTo(w io.Writer, args []string) error {
	sync := false
	for _, a := range args {
		switch a {
		case "sync":
			sync = true
		case "status":
		case "candidates":
			return fmt.Errorf("rules: candidates goes first and takes no other subcommand — `deja rules candidates [--json] [--limit n] [--since 90d]`")
		default:
			return unknownFlag("rules", a, []string{"sync", "status", "candidates"})
		}
	}
	body, exists, err := readRules()
	if err != nil {
		return err
	}
	known, unknown := rulesTargets()
	if sync {
		return syncRules(w, body, known)
	}
	switch {
	case !exists:
		fmt.Fprintf(w, "rules: %s is not there — write your standing rules in it, then `deja rules sync` copies them to every agent below\n", shortHome(rulesSourcePath()))
	case body == "":
		fmt.Fprintf(w, "rules: %s is empty — `deja rules sync` removes the copies below\n", shortHome(rulesSourcePath()))
	default:
		fmt.Fprintf(w, "rules: %s, %d lines, %d bytes\n", shortHome(rulesSourcePath()), strings.Count(body, "\n")+1, len(body))
	}
	behind := false
	for _, h := range known {
		path := rulesPath(h)
		state, err := rulesState(path, body)
		if err != nil {
			return err
		}
		if state == "missing" || state == "stale" {
			behind = true
		}
		if state == "unbounded" {
			state = "marker without its pair — fix by hand"
		}
		fmt.Fprintf(w, "  %-12s %-9s %s\n", h, state, shortHome(path))
	}
	for _, h := range unknown {
		fmt.Fprintf(w, "  %-12s no global rules file known\n", h)
	}
	if len(known) == 0 && len(unknown) == 0 {
		fmt.Fprintln(w, "  no agents found — `deja install` first")
	}
	if behind {
		fmt.Fprintln(w, "run `deja rules sync` to bring them in line")
	}
	return nil
}

// syncRules writes the block into every known file, or takes it out when there
// are no rules. Every file is attempted: one the reader has to fix by hand is
// named at the end rather than stopping the rest.
func syncRules(w io.Writer, body string, known []string) error {
	release, _, _ := holdInstallLock(true)
	defer release()
	var refused []string
	for _, h := range known {
		path := rulesPath(h)
		action, err := writeRulesBlock(path, body)
		if err != nil {
			refused = append(refused, err.Error())
			continue
		}
		fmt.Fprintf(w, "%s: rules %s %s\n", h, action, shortHome(path))
	}
	if len(refused) > 0 {
		return fmt.Errorf("left as they are:\n  %s", strings.Join(refused, "\n  "))
	}
	return nil
}

// writeRulesBlock puts body into path as deja's rules block, or removes the
// block when body is empty. A file that held nothing but the block goes with
// it: it was deja's to begin with, and an empty CLAUDE.md is litter.
func writeRulesBlock(path, body string) (string, error) {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if len(old) == 0 && body == "" {
		return "unchanged", nil
	}
	next, err := replaceMarkedBlock(string(old), rulesStart, rulesEnd, "rules", rulesBlock(body))
	if err != nil {
		return "", markerErrorFor(path, err)
	}
	if body == "" && strings.TrimSpace(next) == "" {
		if err := os.Remove(path); err != nil {
			return "", err
		}
		return "removed", nil
	}
	return writeIfChanged(path, old, []byte(next))
}

// dropRulesBlock is uninstall's half: the harness is leaving, so the copy of
// the rules goes with it, the same way its guidance does.
func dropRulesBlock(target string) (path, action string, err error) {
	h := guidanceHarness(target)
	path = rulesPath(h)
	if path == "" {
		return "", "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return path, "unchanged", nil
		}
		return path, "", err
	}
	if start, end := markerLines(string(b), rulesStart, rulesEnd); start < 0 || end < 0 {
		return path, "unchanged", nil
	}
	action, err = writeRulesBlock(path, "")
	return path, action, err
}

// rulesDoctorNote is the one line doctor prints when rules exist and some
// agent's copy is behind them. Silence otherwise: most machines have no rules
// file, and a line about a feature nobody uses is noise on every run.
func rulesDoctorNote() string {
	body, _, err := readRules()
	if err != nil {
		return err.Error()
	}
	if body == "" {
		return ""
	}
	known, _ := rulesTargets()
	var behind []string
	for _, h := range known {
		if state, err := rulesState(rulesPath(h), body); err == nil && state != "in sync" {
			behind = append(behind, h)
		}
	}
	if len(behind) == 0 {
		return ""
	}
	return fmt.Sprintf("%s behind %s — `deja rules sync`", strings.Join(behind, ", "), shortHome(rulesSourcePath()))
}
