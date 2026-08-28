package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/search"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/prompt"
)

// planHookBudget is the complete framed payload budget. Findings receive the
// remainder after the existing untrusted-history frame takes its share.
const planHookBudget = 2048

const (
	planFindingLimit = 4
	planStepLimit    = 3
	planTermLimit    = 4
)

type planHookInput struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Plan         string `json:"plan"`
		PlanFilePath string `json:"planFilePath"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
}

type planFinding struct {
	line     string
	sessions []index.SessionMeta
}

var planIndexReady = func(dir string) bool {
	return index.HasManifest(dir) &&
		index.IsCurrentVersion(dir) &&
		!index.Damaged(dir)
}

var planFrictionLookup = index.PlanFrictionMatches

// runHookPlan is the ExitPlanMode PreToolUse hook. Silence is the miss
// contract, including malformed input and an unavailable index.
func runHookPlan(dir string, stdin io.Reader, stdout io.Writer) error {
	var input planHookInput
	_ = json.NewDecoder(bytes.NewReader(readHookPayload(stdin, hookStdinWait))).Decode(&input)
	// The matcher should guarantee ExitPlanMode, but a hook wired with a
	// wider matcher must not answer for tools it was never designed to read.
	if input.ToolName != "ExitPlanMode" {
		return nil
	}

	lines := planFindings(dir, input.ToolInput.Plan, input.SessionID)
	if len(lines) == 0 {
		return nil
	}

	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "PreToolUse"
	resp.HookSpecificOutput.AdditionalContext = frameRecall(strings.Join(lines, "\n"))
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

// planFindings is shared verbatim by hook-plan and check. A non-empty session
// id enables the same cross-hook advisory dedupe used by hook-prompt.
func planFindings(dir, plan, sessionID string) []string {
	steps := planSearchSteps(plan)
	if len(steps) == 0 {
		return nil
	}

	pol := policy.Load()
	seen := alreadyInjected(dir, sessionID)
	keep := func(meta index.SessionMeta) bool {
		if !pol.Allows(policy.ActivationAuto, meta.Project) {
			return false
		}
		if sessionID != "" && (meta.ID == sessionID || seen[meta.ID]) {
			return false
		}
		return true
	}

	// This path never asks for a rebuild. A missing, stale, or damaged
	// snapshot is a silent miss so plan submission cannot block on indexing.
	if !planIndexReady(dir) {
		return nil
	}

	matches := planFrictionLookup(dir, steps, keep, planFindingLimit)
	if len(matches) == 0 {
		return nil
	}

	var findings []planFinding
	emittedWalls := map[string]bool{}
	for _, match := range matches {
		wallSessions := filterPlanMetas(match.Wall.Sessions, keep, nil)
		if len(wallSessions) < index.FrictionMinSessions {
			continue
		}
		wallIDs := make(map[string]bool, len(wallSessions))
		for _, meta := range wallSessions {
			wallIDs[planMetaKey(meta)] = true
		}
		// The wall recurrence is the finding on its own (see PlanCooccurrence
		// in internal/index/plan.go). A command is only ever a bonus clause,
		// so a command whose evidence sessions did not survive the policy
		// filter is dropped rather than taking the whole finding with it.
		match.Wall.Sessions = wallSessions
		commandSessions := filterPlanMetas(match.CommandSessions, keep, wallIDs)
		if len(commandSessions) > 0 && strings.TrimSpace(match.Command) != "" {
			match.CommandSessions = commandSessions
		} else {
			match.Command = ""
			match.CommandSessions = nil
		}
		wallKey := strings.ToLower(strings.TrimSpace(match.Wall.Text))
		if wallKey == "" || emittedWalls[wallKey] {
			continue
		}
		emittedWalls[wallKey] = true

		line := formatPlanFinding(match)
		if line == "" {
			continue
		}
		// What this machine ran after that error, when the census found no
		// command of its own. The plan hook speaks before the agent walks into
		// the wall, which is the best moment deja has, and it named the wall
		// and stopped — while `deja fix` answered the same error with the way
		// past it. Same source, same activation the walls above are filtered
		// by, and only a confirmed pair (#2458).
		if match.Command == "" {
			if fix := planRemedy(dir, match.Wall.Text); fix != "" {
				line += fmt.Sprintf("; what followed it: %s", strconv.Quote(neutralPlanEvidence(fix)))
			}
		}
		findings = append(findings, planFinding{
			line:     line,
			sessions: wallSessions,
		})
	}

	findings = budgetPlanFindings(findings, planHookBudget-recallFrameOverhead)
	if len(findings) == 0 {
		return nil
	}
	if sessionID != "" {
		rememberPlanFindings(dir, sessionID, findings)
	}

	lines := make([]string, 0, len(findings))
	for _, finding := range findings {
		lines = append(lines, finding.line)
	}
	return lines
}

func filterPlanMetas(metas []index.SessionMeta, keep func(index.SessionMeta) bool, members map[string]bool) []index.SessionMeta {
	out := make([]index.SessionMeta, 0, len(metas))
	seen := map[string]bool{}
	for _, meta := range metas {
		key := planMetaKey(meta)
		if seen[key] || !keep(meta) {
			continue
		}
		if members != nil && !members[key] {
			continue
		}
		seen[key] = true
		out = append(out, meta)
	}
	return out
}

func planMetaKey(meta index.SessionMeta) string {
	return meta.Harness + ":" + meta.ID
}

func rememberPlanFindings(dir, sessionID string, findings []planFinding) {
	seen := map[string]bool{}
	var sessions []model.Session
	for _, finding := range findings {
		for _, meta := range finding.sessions {
			if meta.ID == "" || seen[meta.ID] {
				continue
			}
			seen[meta.ID] = true
			sessions = append(sessions, model.Session{ID: meta.ID})
		}
	}
	rememberInjected(dir, sessionID, sessions)
}

// extractPlanSteps prefers explicit Markdown steps. Prose lines are considered
// only when the plan contains no numbered or bulleted items.
func extractPlanSteps(plan string) []string {
	lines := strings.Split(strings.ReplaceAll(plan, "\r\n", "\n"), "\n")
	var listed []string
	for _, line := range lines {
		if step, ok := stripPlanMarker(line); ok && step != "" {
			listed = append(listed, step)
		}
	}
	if len(listed) > 0 {
		return listed
	}

	var fallback []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		if line != "" {
			fallback = append(fallback, line)
		}
	}
	return fallback
}

func stripPlanMarker(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if len(s) >= 2 {
		if (s[0] == '-' || s[0] == '*' || s[0] == '+') && unicode.IsSpace(rune(s[1])) {
			return stripPlanCheckbox(strings.TrimSpace(s[1:])), true
		}
	}

	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) || (s[i] != '.' && s[i] != ')') {
		return "", false
	}
	i++
	if i >= len(s) || !unicode.IsSpace(rune(s[i])) {
		return "", false
	}
	return stripPlanCheckbox(strings.TrimSpace(s[i:])), true
}

func stripPlanCheckbox(s string) string {
	if len(s) >= 3 && s[0] == '[' && s[2] == ']' {
		switch s[1] {
		case ' ', 'x', 'X':
			return strings.TrimSpace(s[3:])
		}
	}
	return s
}

// planSearchSteps returns at most the first three steps carrying enough raw
// terms to be worth a lookup; whether a term actually counts toward that
// lookup — rare enough in this corpus, or identifier-shaped and exempt from
// rarity entirely — is the index layer's call (planIndexedSteps), made
// against the same 1-or-2 need computed here. Each step is independently
// capped so one long paragraph cannot dominate candidate selection. The need
// is two terms, same as auto-recall, except a step naming an identifier
// (hasIdentifierTerm) needs only one — hook-prompt already trusts a lone
// identifier hit, and a plan step deserves the same trust.
func planSearchSteps(plan string) [][]string {
	var out [][]string
	for _, step := range extractPlanSteps(plan) {
		terms := prompt.Terms(step)
		if len(terms) > planTermLimit {
			terms = terms[:planTermLimit]
		}
		need := 2
		if search.HasIdentifierTerm(terms) {
			need = 1
		}
		if len(terms) < need {
			continue
		}
		out = append(out, terms)
		if len(out) == planStepLimit {
			break
		}
	}
	return out
}

// planRemedy is the command recorded after a wall, for the finding above. Only
// a confirmed pair — a candidate is a guess, and a guess handed to an agent
// about to act is worse than silence.
func planRemedy(dir, wall string) string {
	pol := policy.Load()
	fixes := index.FixesFor(dir, wall, 1, func(project string) bool {
		return pol.Allows(policy.ActivationAuto, project)
	})
	if len(fixes) == 0 || fixes[0].Candidate {
		return ""
	}
	cmd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(fixes[0].Command), "$ "))
	if cmd == "" || len(cmd) > planCommandMax {
		return ""
	}
	return cmd
}

// planCommandMax bounds the remedy clause: the finding rides in a block that
// is budgeted, and a command longer than this is a script rather than a step.
const planCommandMax = 120

// formatPlanFinding reports the wall recurrence — the fact a census
// verified (internal/index/plan.go's PlanCooccurrence doc) — and, only when
// one was actually found, appends the command clause as a bonus, not a
// requirement.
func formatPlanFinding(match index.PlanCooccurrence) string {
	if len(match.Wall.Sessions) < index.FrictionMinSessions ||
		strings.TrimSpace(match.Wall.Text) == "" {
		return ""
	}

	since := ""
	if oldest := oldestPlanSession(match.Wall.Sessions); !oldest.IsZero() {
		since = " since " + oldest.Format("2006-01-02")
	}
	wall := strconv.Quote(neutralPlanEvidence(match.Wall.Text))
	line := fmt.Sprintf("wall hit in %d sessions%s: %s", len(match.Wall.Sessions), since, wall)

	if len(match.CommandSessions) == 0 || strings.TrimSpace(match.Command) == "" {
		return line
	}
	command := strconv.Quote(neutralPlanEvidence(match.Command))
	return fmt.Sprintf(
		"%s; past sessions also ran %s (%d sessions)",
		line, command, len(match.CommandSessions),
	)
}

func oldestPlanSession(metas []index.SessionMeta) time.Time {
	var oldest time.Time
	for _, meta := range metas {
		if meta.Updated.IsZero() {
			continue
		}
		if oldest.IsZero() || meta.Updated.Before(oldest) {
			oldest = meta.Updated
		}
	}
	return oldest
}

var forbiddenPlanClaims = []*regexp.Regexp{
	regexp.MustCompile(`(?i)conflicts with`),
	regexp.MustCompile(`(?i)contradicts`),
	regexp.MustCompile(`(?i)already tried`),
	regexp.MustCompile(`(?i)was abandoned`),
}

// neutralPlanEvidence keeps recalled text as quoted evidence without allowing
// its own wording to turn a co-occurrence report into a judgment.
func neutralPlanEvidence(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	text = neutralizeFrameMarkers(text)
	for _, pattern := range forbiddenPlanClaims {
		text = pattern.ReplaceAllString(text, "[historical wording omitted]")
	}
	return text
}

func budgetPlanFindings(findings []planFinding, budget int) []planFinding {
	if budget <= 0 {
		return nil
	}
	out := make([]planFinding, 0, len(findings))
	used := 0
	for _, finding := range findings {
		line := strings.TrimSpace(finding.line)
		if line == "" {
			continue
		}
		separator := 0
		if len(out) > 0 {
			separator = 1
		}
		available := budget - used - separator
		if available <= 0 {
			break
		}
		if len(line) > available {
			line = truncatePlanBytes(line, available)
			if line == "" {
				break
			}
			finding.line = line
			out = append(out, finding)
			break
		}
		finding.line = line
		out = append(out, finding)
		used += separator + len(line)
	}
	return out
}

func truncatePlanBytes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	// The character, not what a mis-decoded copy of it turns into: this held
	// the Shift_JIS reading of "…" and the plan hook appended that to every
	// truncated finding an agent was shown (#1319).
	const ellipsis = "…"
	if limit <= len(ellipsis) {
		return ""
	}
	end := limit - len(ellipsis)
	for end > 0 && !utf8.ValidString(text[:end]) {
		end--
	}
	if end == 0 {
		return ""
	}
	return strings.TrimSpace(text[:end]) + ellipsis
}
