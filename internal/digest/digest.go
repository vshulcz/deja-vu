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

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
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
	fmt.Fprintf(&b, "# deja share: %s\n\n", oneLine(s.ID))
	fmt.Fprintf(&b, "- Project: %s\n", oneLine(s.Project))
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
			cut := b.Len()+len(chunk) > budget
			if cut {
				chunk = cutMarked(chunk, budget-b.Len())
			}
			b.WriteString(chunk)
			// A marker says the passage before it was cut. Anything written
			// after it reads as continuing text and makes the marker a lie, and
			// trimming can leave enough room for another message to fit.
			if cut {
				break
			}
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
	s = strings.TrimSpace(redact.SafeForDisplay(s))
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
	letters, total, wordish, spaceless := 0, 0, 0, 0
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
		if cjkfold.IsCJK(r) {
			spaceless++
		}
	}
	if total == 0 {
		return false
	}
	// enough real words, and letters are a real share of the characters.
	// Chinese, Japanese and Korean prose is written without spaces, so Fields
	// sees one long token and the word count reads a paragraph of it as a
	// pasted dump: every assistant answer over 80 bytes in those scripts was
	// dropped from digests, and a session written in them recalled nothing
	// (#1340). Their characters are the words — but a share of the line, not a
	// handful of them, or a JSON blob with Chinese values reads as a sentence.
	if spaceless >= 8 && spaceless*100/total >= 50 {
		return true
	}
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

// cutMarker is how this package says a passage was cut. It ends the block, so
// it carries the paragraph break with it.
const cutMarker = "…\n\n"

// cutMarked trims a passage to n bytes and says that it was cut. A block handed
// to a person or an agent that simply stops reads as a finished thought, and the
// end of a message is where a session says it changed its mind — the same defect
// #1336 fixed for conclusions. The marker is paid for out of the budget rather
// than added to it.
func cutMarked(s string, n int) string {
	if n <= len(cutMarker) {
		return ""
	}
	t := strings.TrimRight(UTF8SafeCut(s, n-len(cutMarker)), " \t\n")
	if t == "" {
		return ""
	}
	if strings.HasSuffix(t, "…") {
		return t + "\n\n"
	}
	return t + cutMarker
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
		add(filepath.Base(filepath.Dir(cwd)) + "/" + base)
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
			add(filepath.Base(filepath.Dir(root)) + "/" + base)
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
	fmt.Fprintf(&b, "You are picking up work handed off from a %s session (project %s, %s). ", s.Harness, oneLine(s.Project), date)
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
	// The head, not the joined form: this id is meant to be typed back as
	// `deja show <id>`, and that matches on a prefix. Measured on an id with a
	// break in it — `deja show x1abcdef` finds the session, `deja show
	// "x1abcdef fake"` matches nothing.
	short := idSelector(Short(s.ID))
	fmt.Fprintf(&b, "\n\nThis is a compact slice of session %s. If anything you need is missing — an exact error, a file, a decision — search the full history with `deja \"<term>\"` or `deja show %s`, or call the deja MCP tools recall / recall_context if available.\n", short, short)
	return strings.TrimSpace(b.String()) + "\n"
}

// oneLine is a session field made safe for a line of a document deja writes.
//
// Project and id are text deja did not author — a directory name, a
// harness-assigned id — and both land in structured headers here: a break in
// either splits a markdown header in two, and the stray half then reads as
// part of the digest. The handoff framing shows it plainly: it strips the
// share header by cutting at the first newline, so an id containing one left
// the rest of that id standing at the top of what another agent is handed.
func oneLine(s string) string {
	return strings.Join(strings.Fields(redact.SafeForDisplay(s)), " ")
}

// idSelector is an id cut to something the reader can type back. oneLine
// keeps every word so the header still names the session in full; here only
// the leading run is useful, because a prefix is what the lookup takes.
func idSelector(s string) string {
	if f := strings.Fields(redact.SafeForDisplay(s)); len(f) > 0 {
		return f[0]
	}
	return ""
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
		cut := b.Len()+len(chunk) > budget
		if cut {
			chunk = cutMarked(chunk, budget-b.Len())
		}
		b.WriteString(chunk)
		// Same reason as in Share: nothing follows a marked cut.
		if cut || b.Len() >= budget {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// Short keeps an id readable in a line without destroying the thing it names.
//
// A flat cut printed "deja-2026-08" for a note whose id is
// "deja-2026-08-01-ops/db" — an id no session has, in an error message telling
// the reader which session was refused (#741). Ids are meaningful at both ends,
// so the middle goes; #707 made the same change for search result lines.
func Short(s string) string {
	const width = 20
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	head := width/2 - 1
	tail := width - head - 1
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// decisionMarkers spot conclusion-bearing assistant messages in tool-heavy
// sessions, where 95% of the transcript is status chatter around a few
// sentences that actually explain what happened and why.
var decisionMarkers = []string{
	"root cause", "because", "the fix", "fixed", "decided", "instead of",
	"turned out", "the problem was", "solution", "so the answer", "conclusion",
	"works now", "passes now", "merged", "released", "chose", "won't work",
	// Half the sessions on a real store are in Russian, and a list of English
	// phrases reads every line of them as a passing mention. These are the
	// same shapes: what was concluded, what turned out, what got fixed.
	"решили", "в итоге", "оказалось", "причина", "выяснилось", "вывод",
	"выбрали", "остановились", "исправил", "починил", "заработало",
	"не будем", "убрали", "переделали", "смержил", "выкатили",
	// Mirrors of entries already in the English half: "works now", "passes
	// now", "fixed". Measured over forty thousand assistant lines from a real
	// store, Russian lines were marked 3.1% of the time against 4.3% for
	// English, and these three phrases account for most of the gap.
	"заработал", "готово", "зелёный",
}

// CarriesDecision reports whether a line reads as something concluded rather
// than something mentioned in passing. The markers are the same ones the
// session-start digest uses to find what a session decided; per-prompt recall
// had no use for them and picked its line by where the query's words fell.
func CarriesDecision(text string) bool {
	return CarriesDecisionExcept(text, nil)
}

// CarriesDecisionExcept is the same, ignoring markers the asker used. A marker
// that is also a word of the question makes the check circular: "в итоге" is
// both, so a line matched on that phrase then counted as a conclusion because
// of it. Measured on the benchmark, the question "по чему у нас в итоге
// шардирование" promoted a session about something else entirely.
func CarriesDecisionExcept(text string, asked []string) bool {
	low := strings.ToLower(text)
	for _, d := range decisionMarkers {
		if !strings.Contains(low, d) {
			continue
		}
		skip := false
		for _, a := range asked {
			if a != "" && strings.Contains(d, strings.ToLower(a)) {
				skip = true
				break
			}
		}
		if !skip {
			return true
		}
	}
	return false
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

// Conclusions is the short "what this session concluded" line-set recall shows
// under its best hit. A recall answer used to be excerpts alone — the passages
// where the query words appeared — so an agent had to open the session to learn
// what came of it. These are the assistant's decision-carrying sentences, the
// same ones `share` puts under "Key assistant conclusions", trimmed to one or
// two lines each so the whole block costs a few hundred bytes.
func Conclusions(s model.Session, budget int, max int) []string {
	if budget <= 0 || max <= 0 {
		return nil
	}
	var assistants []model.Message
	for _, m := range s.Messages {
		if m.Role != "assistant" || noisyMessage(m.Text) || IsAgentArtifact(m.Text) {
			continue
		}
		assistants = append(assistants, m)
	}
	if len(assistants) == 0 {
		return nil
	}
	picked := dedupeStatus(selectConclusions(assistants))
	// Newest first: the last thing concluded outranks the first thing tried.
	var out []string
	spent := 0
	for i := len(picked) - 1; i >= 0 && len(out) < max; i-- {
		line := firstSentences(MessageText(picked[i].Text), 2)
		if line == "" {
			continue
		}
		if spent+len(line) > budget {
			// Whole sentences or nothing. Cutting here left a conclusion that
			// reads as a finished thought while the part that fell off was the
			// end of it — "we decided to cap retries at three after weighing the
			// options" arrived without the "and then reverted that" it ended on,
			// which is the opposite of what the session concluded (#1336).
			if line = firstSentences(line, 1); spent+len(line) > budget {
				break
			}
		}
		out = append(out, line)
		spent += len(line)
		if spent >= budget {
			break
		}
	}
	return out
}

// isCJKSentenceEnd is the full stop, exclamation and question mark Chinese and
// Japanese write. They are separate characters from the ASCII ones, so a
// message in either script contained no sentence end as far as this file was
// concerned (#1319).
//
// The fullwidth full stop U+FF0E is deliberately absent: it is also how a
// decimal point is written in fullwidth digits, and the space rule that keeps
// "v1.2" together cannot help here — these scripts put no space after a stop,
// so the rule has to be dropped for them. The enumeration comma and the
// fullwidth semicolon are absent for the plainer reason that neither ends a
// sentence.
func isCJKSentenceEnd(r rune) bool {
	switch r {
	case '\u3002', '\uff01', '\uff1f':
		return true
	}
	return false
}

// firstSentences keeps the opening n sentences of a message: a conclusion
// states itself up front and then explains, and recall pays for every byte.
func firstSentences(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return ""
	}
	count := 0
	for i, r := range s {
		switch {
		case r == '.' || r == '!' || r == '?':
			// "v1.2" and "e.g." are not sentence ends: require a space after.
			if i+1 < len(s) && s[i+1] != ' ' {
				continue
			}
		case isCJKSentenceEnd(r):
			// No space rule here: these scripts put none after the stop, so
			// requiring one would find no sentence at all.
		default:
			continue
		}
		count++
		if count == n {
			return strings.TrimSpace(s[:i+utf8.RuneLen(r)])
		}
	}
	const cap = 240
	if len(s) > cap {
		return strings.TrimSpace(UTF8SafeCut(s, cap)) + "…"
	}
	return s
}
