package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// maxPlanFileBytes bounds disk reads when a plan hook names a plan file path.
const maxPlanFileBytes = 64 << 10

// planHookBudget is the complete framed payload budget. Findings receive the
// remainder after the existing untrusted-history frame takes its share.
const planHookBudget = 2048

const (
	planFindingLimit = 4
	planStepLimit    = 3
	planTermLimit    = 4
)

type planToolArgs struct {
	Plan              string          `json:"plan"`
	PlanUpper         string          `json:"Plan"`
	Content           string          `json:"content"`
	Summary           string          `json:"summary"`
	PlanFilePath      string          `json:"planFilePath"`
	PlanFilePathSnake string          `json:"plan_file_path"`
	FilePath          string          `json:"file_path"`
	FilePathCamel     string          `json:"filePath"`
	Path              string          `json:"path"`
	NestedToolArgs    json.RawMessage `json:"toolArgs"`
	NestedToolArgsSnk json.RawMessage `json:"tool_args"`
}

func (a planToolArgs) planText() string {
	return firstNonEmpty(a.Plan, a.PlanUpper, a.Content, a.Summary)
}

func (a planToolArgs) planPath() string {
	return firstNonEmpty(a.PlanFilePath, a.PlanFilePathSnake, a.FilePath, a.FilePathCamel, a.Path)
}

type planHookInput struct {
	HookEventName       string          `json:"hook_event_name"`
	HookEventNameCamel  string          `json:"hookEventName"`
	ToolName            string          `json:"tool_name"`
	ToolNameCamel       string          `json:"toolName"`
	LLMToolName         string          `json:"llm_tool_name"`
	ToolInput           planToolArgs    `json:"tool_input"`
	ToolInputCamel      planToolArgs    `json:"toolInput"`
	ToolArgs            json.RawMessage `json:"toolArgs"`
	ToolArgsSnake       json.RawMessage `json:"tool_args"`
	SessionID           string          `json:"session_id"`
	SessionIDCamel      string          `json:"sessionId"`
	ConversationID      string          `json:"conversation_id"`
	ConversationIDCamel string          `json:"conversationId"`
	TranscriptPath      string          `json:"transcript_path"`
	TranscriptPathCamel string          `json:"transcriptPath"`
	CWD                 string          `json:"cwd"`
	WorkspaceRoot       string          `json:"workspaceRoot"`
	WorkspaceRoots      []string        `json:"workspace_roots"`
}

func decodePlanToolArgs(raw json.RawMessage, dst *planToolArgs) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return
	}
	if raw[0] == '"' {
		var nested string
		if json.Unmarshal(raw, &nested) != nil {
			return
		}
		nested = strings.TrimSpace(nested)
		if nested == "" {
			return
		}
		raw = []byte(nested)
	}
	_ = json.Unmarshal(raw, dst)
}

func (in *planHookInput) adopt() {
	in.HookEventName = adoptGrok(in.HookEventName, in.HookEventNameCamel)
	in.ToolName = adoptGrok(adoptGrok(in.ToolName, in.ToolNameCamel), in.LLMToolName)
	in.SessionID = firstNonEmpty(in.SessionID, in.SessionIDCamel, in.ConversationID, in.ConversationIDCamel)
	in.TranscriptPath = adoptGrok(in.TranscriptPath, in.TranscriptPathCamel)
	in.WorkspaceRoots = adoptGrokRoots(in.WorkspaceRoots, in.WorkspaceRoot)
	if strings.TrimSpace(in.CWD) == "" {
		in.CWD = hookProjectPath(in.CWD, in.WorkspaceRoots)
	}

	var argsRoot, argsSnake, nestedCamel, nestedSnake, camelNestedCamel, camelNestedSnake planToolArgs
	decodePlanToolArgs(in.ToolArgs, &argsRoot)
	decodePlanToolArgs(in.ToolArgsSnake, &argsSnake)
	decodePlanToolArgs(in.ToolInput.NestedToolArgs, &nestedCamel)
	decodePlanToolArgs(in.ToolInput.NestedToolArgsSnk, &nestedSnake)
	decodePlanToolArgs(in.ToolInputCamel.NestedToolArgs, &camelNestedCamel)
	decodePlanToolArgs(in.ToolInputCamel.NestedToolArgsSnk, &camelNestedSnake)

	in.ToolInput.Plan = firstNonEmpty(
		in.ToolInput.planText(),
		in.ToolInputCamel.planText(),
		argsRoot.planText(),
		argsSnake.planText(),
		nestedCamel.planText(),
		nestedSnake.planText(),
		camelNestedCamel.planText(),
		camelNestedSnake.planText(),
	)
	in.ToolInput.PlanFilePath = firstNonEmpty(
		in.ToolInput.planPath(),
		in.ToolInputCamel.planPath(),
		argsRoot.planPath(),
		argsSnake.planPath(),
		nestedCamel.planPath(),
		nestedSnake.planPath(),
		camelNestedCamel.planPath(),
		camelNestedSnake.planPath(),
	)
}

func isPlanTool(name string) bool {
	low := strings.ToLower(strings.TrimSpace(name))
	switch low {
	case "exitplanmode", "exit_plan_mode", "plan_exit", "submit_plan", "submitplan", "finalize_plan":
		return true
	}
	return isExternalMCPToolWithSuffix(low, "_exit_plan_mode", "_plan_exit", "_submit_plan", "_finalize_plan")
}

func resolvePlanText(cwd, inlinePlan, planFilePath string) string {
	if strings.TrimSpace(inlinePlan) != "" {
		return inlinePlan
	}
	p := cleanHookFilePath(planFilePath)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) && cwd != "" {
		p = filepath.Join(cwd, p)
	}
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() <= 0 || fi.Size() > maxPlanFileBytes {
		return ""
	}
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	fi2, err := f.Stat()
	if err != nil || !fi2.Mode().IsRegular() || fi2.Size() <= 0 || fi2.Size() > maxPlanFileBytes {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPlanFileBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxPlanFileBytes {
		return ""
	}
	return string(data)
}

type planFinding struct {
	line     string
	sessions []index.SessionMeta
	token    string
}

// indexDamaged is a variable for the same reason indexFormatDirection is: a
// test can put `check` in front of a damaged store without shipping a way to
// break one (#2682).
// indexDamaged is the reason read as a bool, so a test that stubs one cannot
// put the two into a state the store itself can never be in.
var indexDamaged = func(dir string) bool { return indexDamageReason(dir) != "" }

// indexDamageReason is the same seam for the sentence doctor prints: which
// file is not what the manifest says, rather than a summary of every way a
// store can break (#2695).
var indexDamageReason = index.DamageReason

var planIndexReady = func(dir string) bool {
	return index.HasManifest(dir) &&
		index.IsCurrentVersion(dir) &&
		!index.HookDamaged(dir)
}

var planFrictionLookup = index.PlanFrictionMatches

// runHookPlan is the ExitPlanMode PreToolUse hook. Silence is the miss
// contract, including malformed input and an unavailable index.
func runHookPlan(dir string, stdin io.Reader, stdout io.Writer) error {
	return runHookPlanMode(dir, stdin, stdout, hookToolClaude)
}

func runHookPlanMode(dir string, stdin io.Reader, stdout io.Writer, shape hookToolShape) error {
	raw := readHookPayload(stdin, hookStdinWait)
	return emitHookPlanFromTool(dir, raw, stdout, shape)
}

func emitHookPlanFromTool(dir string, raw []byte, stdout io.Writer, shape hookToolShape) error {
	var input planHookInput
	_ = json.NewDecoder(bytes.NewReader(raw)).Decode(&input)
	input.adopt()

	// The kill switch, before anything is read. It reached the session-start
	// hook and nothing else, so a machine with recall off still had text drawn
	// from its own indexed sessions injected here (#2701).
	if recallIsOff() {
		return nil
	}

	// Only plan submission needs a check; everything else returns quietly so an
	// unrelated PreToolUse invocation never pays a delay or receives noise.
	if !isPlanTool(input.ToolName) {
		return nil
	}

	cwd := hookCWD(input.CWD)
	planText := resolvePlanText(cwd, input.ToolInput.Plan, input.ToolInput.PlanFilePath)
	lines := planFindingsIn(dir, cwd, planText, input.SessionID)
	if len(lines) == 0 {
		return nil
	}

	framed := frameRecall(strings.Join(lines, "\n"))
	usage.RecordDigestInto(dir, usage.KindTool, framed, input.SessionID, len(lines), int64(len(planText)), nil)
	event := firstNonEmpty(input.HookEventName, "PreToolUse")
	switch shape {
	case hookToolPlain:
		fmt.Fprintln(stdout, framed)
		return nil
	case hookToolCrush:
		b, err := json.Marshal(struct {
			Context string `json:"context"`
		}{Context: framed})
		if err != nil {
			return nil //nolint:nilerr // a reply that cannot marshal means no reply
		}
		fmt.Fprintln(stdout, string(b))
		return nil
	case hookToolCopilot:
		emitCopilotContext(stdout, framed)
		return nil
	case hookToolMcode:
		emitMcodeContext(stdout, event, framed)
		return nil
	case hookToolClaude:
	}
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = event
	resp.HookSpecificOutput.AdditionalContext = framed
	b, err := json.Marshal(resp)
	if err != nil {
		return nil //nolint:nilerr // a reply that cannot marshal means no reply
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

// planFindings is shared verbatim by hook-plan and check. A non-empty session
// id enables the same cross-hook advisory dedupe used by hook-prompt.
func planFindings(dir, plan, sessionID string) []string {
	return planFindingsIn(dir, "", plan, sessionID)
}

func planFindingsIn(dir, cwd, plan, sessionID string) []string {
	steps := planSearchSteps(plan)
	if len(steps) == 0 {
		return nil
	}

	pol := policy.Load()
	seen := alreadyInjected(dir, sessionID)
	var projects []string
	if cwd != "" {
		projects = digest.ProjectNameCandidates(cwd)
	}
	scopeToProject := false
	if len(projects) > 0 {
		if metas, err := index.AllMeta(dir); err == nil && len(metas) > 0 {
			for _, m := range metas {
				if planMetaInProject(m, projects) {
					scopeToProject = true
					break
				}
			}
		}
	}
	var extraFindings []planFinding
	if cwd != "" {
		extraFindings = append(extraFindings, planRepoMemoryFindings(cwd, plan, pol, seen)...)
	}
	keep := func(meta index.SessionMeta) bool {
		if sources.HarnessExcluded(meta.Harness) || pol.Ignored(meta.Path, meta.Project) {
			return false
		}
		if !pol.Allows(policy.ActivationAuto, meta.Project) {
			return false
		}
		if scopeToProject && !planMetaInProject(meta, projects) {
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
		// Ask, do not build. #777 gave the per-prompt and session-start hooks
		// this: an index in a format this build cannot read answers nothing,
		// which reads as a user with no history, and nothing else asks for the
		// rebuild that fixes it. A spawned subagent reaches only these hooks —
		// install.go says so where it wires Task and Agent — so without this a
		// fleet works against a stale index until its parent types something
		// (#2567). requestWarmup writes a sentinel and detaches a child; the
		// action pays neither the read nor the wait.
		requestWarmup(dir)
		if len(extraFindings) == 0 {
			return nil
		}
		findings := budgetPlanFindings(extraFindings, planHookBudget-recallFrameOverhead)
		if sessionID != "" {
			rememberPlanFindings(dir, sessionID, findings)
		}
		lines := make([]string, 0, len(findings))
		for _, f := range findings {
			lines = append(lines, f.line)
		}
		return lines
	}

	if cwd != "" {
		extraFindings = append(extraFindings, planFileGuardrailFindings(dir, cwd, plan, pol, projects, sessionID, extraFindings, seen)...)
		extraFindings = append(extraFindings, planCoupledFileFindings(dir, cwd, plan, pol, projects, sessionID, extraFindings, seen)...)
	}

	matches := planFrictionLookup(dir, steps, keep, planFindingLimit)
	if len(matches) == 0 && len(extraFindings) == 0 {
		return nil
	}

	findings := append([]planFinding{}, extraFindings...)
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
		// What this machine ran after that error. The plan hook speaks before
		// the agent walks into the wall, which is the best moment deja has,
		// and it named the wall and stopped — while `deja fix` answered the
		// same error with the way past it. Same source, same activation the
		// walls above are filtered by, and only a confirmed pair (#2458).
		//
		// Beside the census command, not instead of it. The two are different
		// claims — one is what the plan's own step matched in those sessions,
		// which is usually the command about to be run again; the other is
		// what followed the error. Withholding the second whenever the first
		// was found meant the closer the plan came to repeating the failure,
		// the less deja said (#2485).
		if fix := planRemedy(dir, match.Wall.Text); fix != "" &&
			!strings.EqualFold(strings.TrimSpace(fix), strings.TrimSpace(strings.TrimPrefix(match.Command, "$ "))) {
			line += "; what followed it: " + strconv.Quote(neutralPlanEvidence(fix))
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

func planRepoMemoryFindings(cwd, plan string, pol policy.Policy, seen map[string]bool) []planFinding {
	root := sources.RepoRoot(cwd)
	if root == "" || !pol.Allows(policy.ActivationAuto, sources.RepoMemoryProject(root)) {
		return nil
	}
	entries, _, err := readRepoMemoryAndNotes(root)
	if err != nil || len(entries) == 0 {
		return nil
	}
	var out []planFinding
	for _, e := range entries {
		matched := commandTouchesRepoEntryFiles(plan, e.Files)
		if !matched {
			for _, f := range e.Files {
				if repoEntryMentionsFile(root, f, plan) {
					matched = true
					break
				}
			}
		}
		if !matched {
			matched = repoEntryMentionsCommand(plan, e.Text)
		}
		if !matched {
			continue
		}
		scope := ""
		if len(e.Files) > 0 {
			scope = " (" + strings.Join(e.Files, ", ") + ")"
		}
		line := fmt.Sprintf("in %s%s: [%s] %s", repoFileShown, scope, e.Kind, neutralPlanEvidence(clipDecision(e.Text, 240)))
		tok := "plan:rm:" + shortHash(line)
		if seen[tok] {
			continue
		}
		out = append(out, planFinding{line: line, token: tok})
		if len(out) >= 2 {
			break
		}
	}
	return out
}

func planRepoRoot(cwd string) string {
	if root := sources.RepoRoot(cwd); root != "" {
		return root
	}
	return cwd
}

func planMetaInProject(meta index.SessionMeta, projects []string) bool {
	if len(projects) == 0 {
		return true
	}
	proj := meta.BareProject()
	for _, cand := range projects {
		if cand == "" {
			continue
		}
		if index.ProjectInScope(meta.Project, cand) {
			return true
		}
		if proj != "" && strings.HasSuffix(cand, "/"+proj) {
			return true
		}
	}
	return false
}

func planReferencedRepoFiles(cwd, plan string) []string {
	root := planRepoRoot(cwd)
	if root == "" || strings.TrimSpace(plan) == "" {
		return nil
	}
	tree := orientRepoTree(root)
	if len(tree.rel) == 0 {
		return nil
	}
	cands := make([]string, 0, len(tree.rel))
	for rel := range tree.rel {
		cands = append(cands, rel)
	}
	sort.Strings(cands)
	var out []string
	for _, rel := range cands {
		if commandTouchesRepoEntryFiles(plan, []string{rel}) {
			out = append(out, rel)
			if len(out) >= 12 {
				break
			}
		}
	}
	return out
}

func planScopedMetasForFile(dir, root, fullPath string, pol policy.Policy, projects []string, sessionID string) []index.SessionMeta {
	var inScope []index.SessionMeta
	for _, meta := range index.FileSessions(dir, fullPath) {
		if sources.HarnessExcluded(meta.Harness) || pol.Ignored(meta.Path, meta.Project) {
			continue
		}
		if !pol.Allows(policy.ActivationAuto, meta.Project) {
			continue
		}
		if len(projects) > 0 && !planMetaInProject(meta, projects) {
			continue
		}
		if !fileMetaMatchesRepoSubpath(meta, root, fullPath) {
			continue
		}
		if sessionID != "" && meta.ID == sessionID {
			continue
		}
		inScope = append(inScope, meta)
	}
	return inScope
}

func planFindingAlreadyCovered(existing []planFinding, a, b string) bool {
	aLow, bLow := strings.ToLower(a), strings.ToLower(b)
	aBase, bBase := strings.ToLower(baseName(a)), strings.ToLower(baseName(b))
	for _, ex := range existing {
		low := strings.ToLower(ex.line)
		hasA := strings.Contains(low, aLow) || (aBase != "" && strings.Contains(low, aBase))
		hasB := strings.Contains(low, bLow) || (bBase != "" && strings.Contains(low, bBase))
		if hasA && hasB {
			return true
		}
	}
	return false
}

// planCoupledPairToken keys coupled-file dedupe on the sorted basename pair so
// direct file-session and project orient lookups share one session token.
func planCoupledPairToken(a, b string) string {
	norm := func(p string) string {
		if base := strings.ToLower(strings.TrimSpace(baseName(p))); base != "" {
			return base
		}
		return strings.ToLower(filepath.ToSlash(strings.TrimSpace(p)))
	}
	x, y := norm(a), norm(b)
	if x > y {
		x, y = y, x
	}
	return "plan:co:" + shortHash(x+"\x00"+y)
}

func planFileGuardrailFindings(
	dir, cwd, plan string,
	pol policy.Policy,
	projects []string,
	sessionID string,
	existing []planFinding,
	seen map[string]bool,
) []planFinding {
	root := planRepoRoot(cwd)
	refs := planReferencedRepoFiles(root, plan)
	if len(refs) == 0 {
		return nil
	}
	selfSkip := map[string]bool{}
	if sessionID != "" {
		selfSkip[sessionID] = true
	}
	var out []planFinding
	for _, rel := range refs {
		fullPath := filepath.Join(root, rel)
		inScope := planScopedMetasForFile(dir, root, fullPath, pol, projects, sessionID)
		clause := turn0FileGuardrail(dir, root, fullPath, inScope, selfSkip)
		if clause == "" || strings.HasPrefix(clause, repoDecisionLabel) {
			continue
		}
		detail := strings.TrimSpace(strings.TrimPrefix(clause, " — "))
		if detail == "" {
			continue
		}
		line := fmt.Sprintf("%s: %s", search.SafePath(rel), neutralPlanEvidence(detail))
		tok := "plan:fg:" + shortHash(line)
		if seen[tok] || planFindingAlreadyCovered(existing, rel, detail) || planFindingAlreadyCovered(out, rel, detail) {
			continue
		}
		out = append(out, planFinding{line: line, token: tok})
		if len(out) >= 2 {
			break
		}
	}
	return out
}

func planCoupledFileFindings(
	dir, cwd, plan string,
	pol policy.Policy,
	projects []string,
	sessionID string,
	existing []planFinding,
	seen map[string]bool,
) []planFinding {
	root := planRepoRoot(cwd)
	refs := planReferencedRepoFiles(root, plan)
	covered := append([]planFinding{}, existing...)
	var out []planFinding
	for _, rel := range refs {
		fullPath := filepath.Join(root, rel)
		inScope := planScopedMetasForFile(dir, root, fullPath, pol, projects, sessionID)
		comp, coCount, total := coChangeCompanionIn(root, fullPath, inScope)
		if comp == "" {
			continue
		}
		if commandTouchesRepoEntryFiles(plan, []string{comp}) || slices.Contains(refs, comp) {
			continue
		}
		pairText := fmt.Sprintf("%s ↔ %s (%d/%d sessions)", rel, comp, coCount, total)
		if len(orientFilterCoupledByRepoMemory(root, []string{pairText})) == 0 {
			continue
		}
		if planFindingAlreadyCovered(covered, rel, comp) {
			continue
		}
		line := fmt.Sprintf("coupled files: %s is usually edited alongside %s (%d/%d sessions), which is not named in this plan", search.SafePath(rel), comp, coCount, total)
		tok := planCoupledPairToken(rel, comp)
		if seen[tok] {
			covered = append(covered, planFinding{line: line})
			continue
		}
		f := planFinding{line: line, token: tok}
		covered = append(covered, f)
		out = append(out, f)
		if len(out) >= 2 {
			return out
		}
	}

	names := digest.ProjectNameCandidates(cwd)
	if len(names) == 0 {
		return out
	}
	pairs := orientCoChangePairs(dir, names, cwd, orientFiles(dir, names, cwd), policy.ActivationAuto)
	for _, pair := range pairs {
		parts := strings.SplitN(pair, " (", 2)
		if len(parts) != 2 {
			continue
		}
		files := strings.SplitN(parts[0], " ↔ ", 2)
		if len(files) != 2 {
			continue
		}
		fileA := strings.TrimSpace(files[0])
		fileB := strings.TrimSpace(files[1])
		inA := commandTouchesRepoEntryFiles(plan, []string{fileA})
		inB := commandTouchesRepoEntryFiles(plan, []string{fileB})
		if inA == inB {
			continue
		}
		named, missing := fileA, fileB
		if inB {
			named, missing = fileB, fileA
		}
		if planFindingAlreadyCovered(covered, named, missing) {
			continue
		}
		stats := strings.TrimSuffix(parts[1], ")")
		line := fmt.Sprintf("coupled files: %s is usually edited alongside %s (%s), which is not named in this plan", named, missing, stats)
		tok := planCoupledPairToken(named, missing)
		if seen[tok] {
			continue
		}
		f := planFinding{line: line, token: tok}
		covered = append(covered, f)
		out = append(out, f)
		if len(out) >= 2 {
			break
		}
	}
	return out
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
	return meta.Key()
}

func rememberPlanFindings(dir, sessionID string, findings []planFinding) {
	seenSessions := map[string]bool{}
	var sessions []model.Session
	seenTokens := map[string]bool{}
	var tokens []string
	for _, finding := range findings {
		for _, meta := range finding.sessions {
			if meta.ID == "" || seenSessions[meta.ID] {
				continue
			}
			seenSessions[meta.ID] = true
			sessions = append(sessions, model.Session{ID: meta.ID})
		}
		if finding.token != "" && !seenTokens[finding.token] {
			seenTokens[finding.token] = true
			tokens = append(tokens, finding.token)
		}
	}
	if len(sessions) > 0 {
		rememberInjected(dir, sessionID, sessions)
	}
	if len(tokens) > 0 {
		rememberInjectedIDs(dir, sessionID, tokens...)
	}
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
		terms := prompt.CommandTerms(step)
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
	fixes := index.FixesFor(dir, wall, 5, func(project string) bool {
		return pol.Allows(policy.ActivationAuto, project)
	})
	for _, f := range fixes {
		if f.Candidate || f.Repaired {
			continue
		}
		cmd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(f.Command), "$ "))
		if cmd != "" && len(cmd) <= planCommandMax {
			return cmd
		}
	}
	return ""
}

// planCommandMax bounds the remedy clause: the finding rides in a block that
// is budgeted, and a command longer than this is a script rather than a step.
const planCommandMax = 120

func planSessionCount(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

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
		since = " since " + oldest.Local().Format("2006-01-02")
	}
	wall := strconv.Quote(neutralPlanEvidence(match.Wall.Text))
	line := fmt.Sprintf("wall hit in %s%s: %s", planSessionCount(len(match.Wall.Sessions)), since, wall)

	if len(match.CommandSessions) == 0 || strings.TrimSpace(match.Command) == "" {
		return line
	}
	command := strconv.Quote(neutralPlanEvidence(match.Command))
	return fmt.Sprintf(
		"%s; past sessions also ran %s (%s)",
		line, command, planSessionCount(len(match.CommandSessions)),
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
