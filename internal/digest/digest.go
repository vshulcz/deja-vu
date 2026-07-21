// Package digest builds the shareable/handoff text slices of a session:
// noise-filtered problem statements, conclusions, and the live tail.
package digest

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const ShareBudget = 6 * 1024

func Share(s model.Session, budget int) string {
	if budget <= 0 {
		budget = ShareBudget
	}
	var b strings.Builder
	date := "unknown"
	if !s.Updated.IsZero() {
		date = s.Updated.Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "# deja share: %s\n\n", s.ID)
	fmt.Fprintf(&b, "- Project: %s\n", s.Project)
	fmt.Fprintf(&b, "- Harness: %s\n", s.Harness)
	fmt.Fprintf(&b, "- Date: %s\n\n", date)
	appendSection := func(title string, messages []model.Message) {
		if len(messages) == 0 || b.Len() >= budget {
			return
		}
		fmt.Fprintf(&b, "## %s\n\n", title)
		for _, m := range messages {
			if b.Len() >= budget {
				break
			}
			text := MessageText(m.Text)
			if text == "" {
				continue
			}
			chunk := fmt.Sprintf("%s\n\n", text)
			if b.Len()+len(chunk) > budget {
				chunk = UTF8SafeCut(chunk, budget-b.Len())
			}
			b.WriteString(chunk)
		}
	}
	var users, assistants []model.Message
	for _, m := range s.Messages {
		if noisyMessage(m.Text) || IsAgentArtifact(m.Text) {
			continue
		}
		switch m.Role {
		case "user":
			users = append(users, m)
		case "assistant":
			assistants = append(assistants, m)
		}
	}
	appendSection("User problem statement(s)", dedupeStatus(users))
	appendSection("Key assistant conclusions / code blocks", dedupeStatus(selectConclusions(assistants)))
	return strings.TrimSpace(b.String()) + "\n"
}

func MessageText(s string) string {
	s = strings.TrimSpace(stripANSI(s))
	if s == "" {
		return ""
	}
	if strings.Contains(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	var keep []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || noisyMessage(line) || noiseLine(line) || !looksLikeProse(line) {
			continue
		}
		keep = append(keep, line)
		if len(keep) >= 16 {
			break
		}
	}
	return strings.Join(strings.Fields(strings.Join(keep, " ")), " ")
}

var (
	shareLineNumRE = regexp.MustCompile(`^\s*\d{1,6}\s`)            // "1 diff --git", numbered dumps
	shareGrepRE    = regexp.MustCompile(`^\S+\.[a-z]{1,5}:\d+[:)]`) // path/file.go:18: grep output
	shareShellRE   = regexp.MustCompile(`^\((eval|\w*sh)\):\d*:?`)  // zsh/bash error prefixes
	shareDigitsRE  = regexp.MustCompile(`^[\d\s.,%-]+$`)            // bare number sequences
)

func looksLikeProse(line string) bool {
	// Short lines are kept: dumps are long. The prose gate exists to drop
	// pasted JSON/CLI walls, not three-word problem statements.
	if len(line) < 80 {
		return true
	}
	letters, total, wordish := 0, 0, 0
	for _, f := range strings.Fields(line) {
		hasLetter := false
		for _, r := range f {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x80 {
				hasLetter = true
			}
		}
		if hasLetter && len(f) >= 2 {
			wordish++
		}
	}
	for _, r := range line {
		total++
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x80 {
			letters++
		}
	}
	if total == 0 {
		return false
	}
	// enough real words, and letters are a real share of the characters
	return wordish >= 4 && letters*100/total >= 45
}

func noiseLine(line string) bool {
	return shareLineNumRE.MatchString(line) || shareGrepRE.MatchString(line) ||
		shareShellRE.MatchString(line) || shareDigitsRE.MatchString(line) ||
		looksLikeListingDump(line)
}

// shareStopwords: a line of 8+ tokens with none of these is a path listing or
// ls dump, not a sentence anyone wrote.
var shareStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "to": true,
	"of": true, "in": true, "on": true, "and": true, "or": true, "it": true,
	"we": true, "i": true, "you": true, "not": true, "with": true, "for": true,
	"и": true, "в": true, "на": true, "не": true, "что": true, "как": true,
	"это": true, "у": true, "с": true, "по": true, "а": true, "но": true,
}

func looksLikeListingDump(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 8 {
		return false
	}
	slashes := 0
	for _, f := range fields {
		if strings.ContainsRune(f, '/') {
			slashes++
		}
		if shareStopwords[strings.ToLower(strings.Trim(f, ".,!?:;"))] {
			return false
		}
	}
	return true
}

func noisyMessage(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return true
	}
	for _, p := range []string{"<local-command", "<command-", "<task-notification", "<teammate-message", "<bash-", "Caveat:", "<system-reminder"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	if strings.Contains(t, "tool_use") || strings.Contains(t, "tool_result") {
		return true
	}
	return looksLikeDataDump(t)
}

// looksLikeDataDump flags pasted JSON, CLI output, or blobs with very long
// unbroken tokens — content that would make a shared digest unreadable.
func looksLikeDataDump(t string) bool {
	if len(t) > 400 {
		if (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && strings.Contains(t, "\":\"") {
			return true
		}
	}
	longestRun := 0
	run := 0
	for _, r := range t {
		if r == ' ' || r == '\n' || r == '\t' {
			run = 0
			continue
		}
		run++
		if run > longestRun {
			longestRun = run
		}
	}
	return longestRun > 200
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	inCSI := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inCSI {
			if c >= '@' && c <= '~' {
				inCSI = false
			}
			continue
		}
		if inEsc {
			inEsc = false
			if c == '[' {
				inCSI = true
			}
			continue
		}
		if c == 0x1b {
			inEsc = true
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func UTF8SafeCut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func ProjectNameCandidates(cwd string) []string {
	names := []string{sources.ClaudeProjectName(cwd)}
	add := func(name string) {
		for _, n := range names {
			if n == name {
				return
			}
		}
		names = append(names, name)
	}
	if base := filepath.Base(cwd); base != "" {
		add(filepath.Join(filepath.Base(filepath.Dir(cwd)), base))
		add(base)
	}
	// The same repo appears under every worktree's path; sessions recorded in
	// one worktree belong to the project, not to that checkout. Each worktree
	// root contributes its name forms so recall sees one project. Two
	// different repos that merely share a basename stay separate everywhere
	// the full encoded path matches first.
	for _, root := range gitWorktreeRoots(cwd) {
		add(sources.ClaudeProjectName(root))
		if base := filepath.Base(root); base != "" {
			add(filepath.Join(filepath.Base(filepath.Dir(root)), base))
			add(base)
		}
	}
	return names
}

// gitWorktreeRoots lists the repo's worktree roots (including the main one)
// when cwd is inside a git repository. Best effort with a hard timeout: no
// git, no repo, or a slow disk simply yields nothing.
func gitWorktreeRoots(cwd string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}
	var roots []string
	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok && strings.TrimSpace(p) != "" {
			roots = append(roots, strings.TrimSpace(p))
		}
	}
	if len(roots) < 2 {
		return nil // a single worktree adds nothing beyond cwd's own names
	}
	return roots
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
	"Comments on artifact URI:",
	"idle_notification",
	`{"type":`,
}

func IsAgentArtifact(text string) bool {
	for _, m := range agentArtifactMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	trimmed := strings.TrimSpace(text)
	// Harness preambles injected as user turns: <environment_context>,
	// <user_instructions> and similar XML-wrapped plumbing.
	if strings.HasPrefix(trimmed, "<") && strings.Contains(trimmed, "</") {
		return true
	}
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

// cleanSession drops agent artifacts and exact repeats so the digest carries
// conversation, not tool output replayed under a user role.
func cleanSession(s model.Session) model.Session {
	out := s
	out.Messages = nil
	seen := map[string]bool{}
	for _, m := range s.Messages {
		if IsAgentArtifact(m.Text) {
			continue
		}
		key := m.Role + "\x00" + strings.TrimSpace(m.Text)
		if seen[key] {
			continue
		}
		seen[key] = true
		out.Messages = append(out.Messages, m)
	}
	return out
}

// Handoff is the package the target agent starts from: framing header,
// the user's problem statements, key conclusions, and the tail of the
// conversation — the "where it stopped" part a plain summary loses.
func Handoff(s model.Session, budget int) string {
	s = cleanSession(s)
	var b strings.Builder
	date := "unknown"
	if !s.Updated.IsZero() {
		date = s.Updated.Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "You are picking up work handed off from a %s session (project %s, %s). ", s.Harness, s.Project, date)
	b.WriteString("Below is the packaged context: the problem, key conclusions so far, and where it stopped. Continue from there instead of re-deriving what is already done.\n\n")
	body := Share(s, budget*3/4)
	// Drop the share header line; the framing above replaces it.
	if i := strings.Index(body, "\n"); i > 0 && strings.HasPrefix(body, "# deja share:") {
		body = strings.TrimSpace(body[i:])
	}
	b.WriteString(body)
	if tail := tailSection(s, budget-b.Len()); tail != "" {
		b.WriteString("\n\n## Where it stopped\n\n")
		b.WriteString(tail)
	}
	// The digest is a lossy slice by construction. Tell the receiving agent it
	// can pull deeper instead of being stuck with the summary: push+pull, not
	// one-shot push.
	fmt.Fprintf(&b, "\n\nThis is a compact slice of session %s. If anything you need is missing — an exact error, a file, a decision — search the full history with `deja \"<term>\"` or `deja show %s`, or call the deja MCP tools recall / recall_context if available.\n", Short(s.ID), Short(s.ID))
	return strings.TrimSpace(b.String()) + "\n"
}

// tailSection returns the last few substantive exchanges verbatim so the
// target agent sees the live state, not just conclusions.
func tailSection(s model.Session, budget int) string {
	if budget <= 0 {
		return ""
	}
	var picked []model.Message
	for i := len(s.Messages) - 1; i >= 0 && len(picked) < 4; i-- {
		m := s.Messages[i]
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if noisyMessage(m.Text) || MessageText(m.Text) == "" {
			continue
		}
		picked = append(picked, m)
	}
	var b strings.Builder
	for i := len(picked) - 1; i >= 0; i-- {
		m := picked[i]
		chunk := fmt.Sprintf("**%s:** %s\n\n", m.Role, MessageText(m.Text))
		if b.Len()+len(chunk) > budget {
			chunk = UTF8SafeCut(chunk, budget-b.Len())
		}
		b.WriteString(chunk)
		if b.Len() >= budget {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func Short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// decisionMarkers spot conclusion-bearing assistant messages in tool-heavy
// sessions, where 95% of the transcript is status chatter around a few
// sentences that actually explain what happened and why.
var decisionMarkers = []string{
	"root cause", "because", "the fix", "fixed", "decided", "instead of",
	"turned out", "the problem was", "solution", "so the answer", "conclusion",
	"works now", "passes now", "merged", "released", "chose", "won't work",
}

// selectConclusions keeps assistant messages that carry a decision marker,
// plus the final message (the outcome), in transcript order. Conversational
// sessions where nothing matches keep everything — the filter only kicks in
// when it has something better to offer.
func selectConclusions(ms []model.Message) []model.Message {
	if len(ms) <= 2 {
		return ms
	}
	var keep []model.Message
	for i, m := range ms {
		low := strings.ToLower(m.Text)
		marked := false
		for _, d := range decisionMarkers {
			if strings.Contains(low, d) {
				marked = true
				break
			}
		}
		if marked || strings.Contains(m.Text, "```") || i == len(ms)-1 {
			keep = append(keep, m)
		}
	}
	if len(keep) < 2 {
		return ms
	}
	return keep
}

// dedupeStatus drops messages that repeat an earlier message's opening —
// agent loops emit the same status line dozens of times and each survives
// the noise filters individually.
func dedupeStatus(ms []model.Message) []model.Message {
	seen := map[string]bool{}
	var out []model.Message
	for _, m := range ms {
		key := strings.ToLower(strings.Join(strings.Fields(m.Text), " "))
		if r := []rune(key); len(r) > 60 {
			key = string(r[:60])
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	return out
}
