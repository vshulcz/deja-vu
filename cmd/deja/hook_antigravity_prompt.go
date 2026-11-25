package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/hookengine"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// Antigravity has no per-prompt event. PreInvocation is the only place a hook
// can speak, and it fires before every model call — several times inside one
// user turn — so deja answered on the first invocation and then stayed silent
// for the rest of the conversation. Everything after the opening question got
// nothing, which is most of a session.
//
// The question is in the transcript the payload names: antigravity writes each
// turn as a step, and a user turn is a USER_INPUT step wrapping the text in
// <USER_REQUEST>. Reading the newest one turns PreInvocation into the
// per-prompt channel the harness does not offer, and the prompt hook's own
// dedupe keeps it to one answer per question rather than one per model call.
const (
	// antigravityTranscriptTail bounds the read. This runs before every model
	// call, and the newest turn is at the end of the file — a conversation's
	// transcript grows all day and no part of this needs its beginning.
	antigravityTranscriptTail = 512 << 10
	userRequestOpen           = "<USER_REQUEST>"
	userRequestClose          = "</USER_REQUEST>"
)

// transcriptStep is as much of an antigravity step as this hook reads.
type transcriptStep struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	ToolCalls any    `json:"tool_calls"`
}

// latestEditCallsFromSteps are the files and edit snippets the newest planner
// row of this turn edits. By the PreInvocation that follows it the edit has
// run, so the line arrives with its result, the way it does on opencode and
// Grok: PreToolUse's reason never reaches the model here (hook_antigravity.go).
func latestEditCallsFromSteps(steps []transcriptStep) []sources.EditCall {
	for _, step := range steps {
		if step.Type == "USER_INPUT" {
			return nil
		}
		if calls, ok := step.ToolCalls.([]any); ok && len(calls) > 0 {
			return sources.AntigravityEditCalls(calls)
		}
	}
	return nil
}

// latestCommandCallsFromSteps returns shell commands invoked by the newest
// planner row of the current turn before any prior USER_INPUT boundary.
func latestCommandCallsFromSteps(steps []transcriptStep) []string {
	for _, step := range steps {
		if step.Type == "USER_INPUT" {
			return nil
		}
		if calls, ok := step.ToolCalls.([]any); ok && len(calls) > 0 {
			return sources.AntigravityCommandCalls(calls)
		}
	}
	return nil
}

// latestPlanFromSteps returns the plan text from the newest planner row in the
// current turn before any prior USER_INPUT boundary.
func latestPlanFromSteps(steps []transcriptStep, workspace string) string {
	for _, step := range steps {
		if step.Type == "USER_INPUT" {
			return ""
		}
		calls, ok := step.ToolCalls.([]any)
		if !ok || len(calls) == 0 {
			continue
		}
		for _, rawCall := range calls {
			call, _ := rawCall.(map[string]any)
			name := mapString(call, "name", "tool_name", "toolName")
			if !isPlanTool(name) {
				continue
			}
			args, _ := call["args"].(map[string]any)
			return resolvePlanText(
				hookCWD(workspace),
				mapString(args, "plan", "Plan", "content", "summary"),
				mapString(args, "planFilePath", "plan_file_path", "file_path", "filePath", "path"),
			)
		}
		return ""
	}
	return ""
}

// antigravityPlanBlock evaluates plan findings for the newest plan step in
// steps so Antigravity receives coupled-file and guardrail warnings on its next invocation.
func antigravityPlanBlock(dir string, steps []transcriptStep, convID, workspace string) string {
	plan := latestPlanFromSteps(steps, workspace)
	if plan == "" {
		return ""
	}
	lines := planFindingsIn(dir, hookCWD(workspace), plan, convID)
	if len(lines) == 0 {
		return ""
	}
	framed := frameRecall(strings.Join(lines, "\n"))
	usage.RecordDigestInto(dir, usage.KindTool, framed, convID, len(lines), int64(len(plan)), nil)
	return framed
}

// antigravityProjectWorkspace narrows a home-directory or empty workspace to
// the git repository the recent transcript steps actually work in, so sessions
// launched from ~ still scope per-prompt recall and .deja/memory.jsonl to the
// active checkout.
func antigravityProjectWorkspace(workspace string, steps []transcriptStep) string {
	if workspace != "" && len(digest.ProjectNameCandidates(workspace)) > 0 {
		return workspace
	}
	for _, step := range steps {
		calls, ok := step.ToolCalls.([]any)
		if !ok {
			continue
		}
		for _, rawCall := range calls {
			call, _ := rawCall.(map[string]any)
			args, _ := call["args"].(map[string]any)
			if args == nil {
				continue
			}
			for _, p := range antigravityCallPaths(args) {
				if repo := antigravityRepoRootFromPath(workspace, p); repo != "" {
					return repo
				}
			}
		}
	}
	return workspace
}

func selectAntigravityWorkspaces(paths []string, convFallback string, steps []transcriptStep) (string, string) {
	seen := make(map[string]bool, len(paths))
	cands := make([]string, 0, len(paths))
	for _, raw := range paths {
		ws := strings.TrimSpace(raw)
		if ws != "" && !seen[ws] {
			seen[ws] = true
			cands = append(cands, ws)
		}
	}
	if len(cands) == 0 && strings.TrimSpace(convFallback) != "" {
		cands = append(cands, strings.TrimSpace(convFallback))
	}
	if len(cands) == 0 {
		return "", ""
	}
	hasExistingProject := false
	for _, ws := range cands {
		if fi, err := os.Stat(ws); err == nil && fi.IsDir() && len(digest.ProjectNameCandidates(ws)) > 0 {
			hasExistingProject = true
			break
		}
	}
	for _, st := range slices.Backward(steps) {
		calls, ok := st.ToolCalls.([]any)
		if !ok {
			continue
		}
		for _, rawCall := range calls {
			call, _ := rawCall.(map[string]any)
			args, _ := call["args"].(map[string]any)
			if args == nil {
				continue
			}
			for _, p := range antigravityCallPaths(args) {
				for _, ws := range cands {
					if repo := antigravityRepoRootFromPath(ws, p); repo != "" {
						return repo, repo
					}
				}
				if !hasExistingProject {
					if repo := antigravityRepoRootFromPath("", p); repo != "" {
						return repo, repo
					}
				}
			}
		}
	}
	for _, ws := range cands {
		if fi, err := os.Stat(ws); err == nil && fi.IsDir() && len(digest.ProjectNameCandidates(ws)) > 0 && sources.RepoRoot(ws) != "" {
			return ws, ws
		}
	}
	for _, ws := range cands {
		if fi, err := os.Stat(ws); err == nil && fi.IsDir() && len(digest.ProjectNameCandidates(ws)) > 0 {
			return ws, ws
		}
	}
	for _, ws := range cands {
		if fi, err := os.Stat(ws); err == nil && fi.IsDir() {
			return ws, antigravityProjectWorkspace(ws, steps)
		}
	}
	return cands[0], antigravityProjectWorkspace(cands[0], steps)
}

func antigravityCallPaths(args map[string]any) []string {
	var out []string
	for _, k := range []string{"TargetFile", "AbsolutePath", "Path", "cwd", "Cwd", "where"} {
		if s, ok := args[k].(string); ok && s != "" {
			var decoded string
			if strings.HasPrefix(s, `"`) && json.Unmarshal([]byte(s), &decoded) == nil {
				s = decoded
			}
			out = append(out, s)
		}
	}
	if list, ok := args["paths"].([]any); ok {
		for _, item := range list {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func antigravityRepoRootFromPath(workspace, raw string) string {
	p := strings.TrimSpace(strings.TrimPrefix(raw, "file://"))
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	if sources.IsAgentScratchPath(p) {
		return ""
	}
	if workspace != "" {
		cleanW := filepath.Clean(workspace)
		cleanP := filepath.Clean(p)
		if cleanP != cleanW && !strings.HasPrefix(cleanP, cleanW+string(filepath.Separator)) {
			return ""
		}
	}
	probe := p
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		probe = filepath.Dir(p)
	}
	repo := sources.RepoRoot(probe)
	if repo != "" && len(digest.ProjectNameCandidates(repo)) > 0 {
		return repo
	}
	return ""
}

// editFileLines is what the pre-tool hook would say before an edit of each
// file, through the same hook every other harness runs, so its dedupe and
// budget hold on hosts whose pre-tool answer never reaches the model
// (Antigravity, goose) and the line rides a later hook.
func editFileLines(dir string, paths []string, conversationID, workspace string) string {
	calls := make([]sources.EditCall, 0, len(paths))
	for _, p := range paths {
		calls = append(calls, sources.EditCall{FilePath: p})
	}
	return editFileCallLines(dir, calls, conversationID, workspace)
}

func runAntigravityHookTool(dir string, body map[string]any) string {
	payload, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	var out bytes.Buffer
	if runHookTool(dir, bytes.NewReader(payload), &out) != nil {
		return ""
	}
	var resp sessionStartHookResponse
	if json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp) != nil {
		return ""
	}
	return resp.HookSpecificOutput.AdditionalContext
}

func editFileCallLines(dir string, calls []sources.EditCall, conversationID, workspace string) string {
	var lines []string
	seen := make(map[string]bool, len(calls))
	for _, c := range calls {
		p := c.FilePath
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		toolInput := map[string]string{"file_path": p}
		if c.OldString != "" {
			toolInput["old_string"] = c.OldString
		}
		if c.NewString != "" {
			toolInput["new_string"] = c.NewString
		}
		toolName := "Edit"
		if c.ToolName != "" && !sources.IsCompactionEditTool(c.ToolName) {
			toolName = "read_file"
		}
		if line := runAntigravityHookTool(dir, map[string]any{
			"hook_event_name": "PostToolUse",
			"tool_name":       toolName,
			"tool_input":      toolInput,
			"session_id":      conversationID,
			"cwd":             workspace,
		}); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n\n")
}

func commandCallLines(dir string, cmds []string, conversationID, workspace string) string {
	var lines []string
	seen := make(map[string]bool, len(cmds))
	for _, cmd := range cmds {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" || seen[cmd] {
			continue
		}
		seen[cmd] = true
		if line := runAntigravityHookTool(dir, map[string]any{
			"hook_event_name": "PostToolUse",
			"tool_name":       "Bash",
			"tool_input":      map[string]string{"command": cmd},
			"session_id":      conversationID,
			"cwd":             workspace,
		}); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n\n")
}

// transcriptTailSteps decodes the end of a transcript, newest step first. A
// partial first line from the tail cut simply fails to decode, which is the
// right answer for it.
func transcriptTailSteps(path string) []transcriptStep {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	size := fi.Size()
	offset := int64(0)
	if size > antigravityTranscriptTail {
		offset = size - antigravityTranscriptTail
	}
	buf := make([]byte, size-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return nil
	}
	lines := strings.Split(string(buf), "\n")
	out := make([]transcriptStep, 0, len(lines))
	for _, line := range slices.Backward(lines) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var step transcriptStep
		if json.Unmarshal([]byte(line), &step) != nil {
			continue
		}
		out = append(out, step)
	}
	return out
}

func latestUserRequestFromSteps(steps []transcriptStep) string {
	for _, step := range steps {
		if step.Type != "USER_INPUT" {
			continue
		}
		if q := userRequestText(step.Content); q != "" {
			return q
		}
	}
	return ""
}

// userRequestText pulls the person's words out of the step antigravity writes,
// which wraps them in a tag and appends metadata blocks of its own.
func userRequestText(content string) string {
	_, after, ok := strings.Cut(content, userRequestOpen)
	if !ok {
		return strings.TrimSpace(content)
	}
	rest := after
	if end := strings.Index(rest, userRequestClose); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// antigravityPromptBlock is what the per-prompt path would inject for this
// question, or "" for the silence that is its usual answer. It drives the
// ordinary prompt hook rather than reimplementing the ranking: the budget, the
// dedupe and the kill switch are the ones every other harness gets.
func antigravityPromptBlock(dir, question, conversationID, workspace string) string {
	return antigravityPromptBlockSkipping(dir, question, conversationID, workspace, nil)
}

func antigravityPromptBlockSkipping(dir, question, conversationID, workspace string, skipIDs []string) string {
	return antigravityPromptBlockFrom(dir, question, conversationID, workspace, "", skipIDs)
}

func antigravityPromptBlockFrom(dir, question, conversationID, workspace, transcriptPath string, skipIDs []string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return ""
	}
	if dir != "" && strings.TrimSpace(conversationID) != "" {
		qKey := "q:" + shortHash(question)
		dKey := antigravityDigestKey(conversationID)
		if !hookengine.NewLedger(dir).ClaimOnce(dKey, qKey) {
			return ""
		}
	}
	body := map[string]any{
		"prompt":     question,
		"session_id": conversationID,
		"cwd":        workspace,
	}
	if strings.TrimSpace(transcriptPath) != "" {
		body["transcript_path"] = transcriptPath
	}
	if len(skipIDs) > 0 {
		body["skip_ids"] = skipIDs
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	var out bytes.Buffer
	if err := runHookPromptMode(dir, bytes.NewReader(payload), &out, false); err != nil {
		return ""
	}
	var resp sessionStartHookResponse
	if json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp) != nil {
		return ""
	}
	return resp.HookSpecificOutput.AdditionalContext
}

// digestAlreadyInjected reports whether this conversation has had the project
// digest. Antigravity's invocation counter restarts every turn, so without a
// record of its own the digest would go in again on every turn of a long
// conversation.
func digestAlreadyInjected(dir, conversationID string) bool {
	if strings.TrimSpace(conversationID) == "" {
		return false
	}
	return alreadyInjected(dir, antigravityDigestKey(conversationID))[antigravityDigestMarker]
}

func rememberDigestInjected(dir, conversationID string) {
	if strings.TrimSpace(conversationID) == "" {
		return
	}
	rememberInjectedIDs(dir, antigravityDigestKey(conversationID), antigravityDigestMarker)
}

func antigravityDigestKey(conversationID string) string {
	return hookengine.AgyDigestKey(conversationID)
}

// antigravityDigestMarker is what the ledger row says: this conversation has
// been handed the digest.
const antigravityDigestMarker = "agy-digest"

// workspaceFromConversation is where this conversation was started. The CLI
// leaves workspacePaths empty unless it was given --add-dir (measured on
// antigravity-cli 1.1.13: an ordinary `agy -p` inside a project sends []), and
// the hook's own working directory is the plugin folder — so without this the
// digest is scoped to a directory nobody works in and recalls the wrong
// project's history, or none.
//
// Antigravity keeps the answer beside the transcript: a cache mapping each
// directory to the conversation it last opened there. The payload names the
// conversation, so the map is read backwards.
func workspaceFromConversation(transcriptPath, conversationID string) string {
	if strings.TrimSpace(transcriptPath) == "" || strings.TrimSpace(conversationID) == "" {
		return ""
	}
	// <product root>/brain/<conversation>/.system_generated/logs/transcript.jsonl
	root := transcriptPath
	for range 5 {
		root = filepath.Dir(root)
	}
	b, err := os.ReadFile(filepath.Join(root, "cache", "last_conversations.json"))
	if err != nil {
		return ""
	}
	var byDir map[string]string
	if json.Unmarshal(b, &byDir) != nil {
		return ""
	}
	// The longest match, so a conversation opened in a subdirectory is filed
	// under that subdirectory rather than under its parent.
	best := ""
	for dir, conv := range byDir {
		if conv != conversationID {
			continue
		}
		if len(dir) > len(best) {
			best = dir
		}
	}
	return best
}

func stripAntigravityToolHeader(body string) string {
	lines := strings.Split(body, "\n")
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "Created At:") || strings.HasPrefix(trimmed, "Completed At:") {
			i++
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			if closeIdx := strings.Index(trimmed, "]"); closeIdx > 0 && strings.Contains(trimmed[:closeIdx], "| exit=") {
				rest := strings.TrimSpace(trimmed[closeIdx+1:])
				if rest == "" {
					i++
					continue
				}
				lines[i] = rest
			}
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines[i:], "\n"))
}

func latestToolFailureFromSteps(steps []transcriptStep) string {
	for _, step := range steps {
		if step.Type == "USER_INPUT" {
			return ""
		}
		if step.Type != "GENERIC" {
			continue
		}
		body := step.Content
		if !isAntigravityToolFailure(body) {
			return ""
		}
		for _, marker := range []string{commandOutputMarker, toolOutputMarker} {
			if _, after, ok := strings.Cut(body, marker); ok {
				return strings.TrimSpace(after)
			}
		}
		return stripAntigravityToolHeader(body)
	}
	return ""
}

func isAntigravityToolFailure(body string) bool {
	for _, prefix := range []string{exitedWithCode, commandExitedWithCode} {
		if strings.Contains(body, prefix) {
			return !strings.Contains(body, prefix+" 0.") && !strings.Contains(body, prefix+" 0\n")
		}
	}
	if strings.Contains(body, "| exit=") && !strings.Contains(body, "| exit=0") {
		return true
	}
	return false
}

func latestToolStepSucceededFromSteps(steps []transcriptStep) bool {
	for _, step := range steps {
		if step.Type == "USER_INPUT" {
			return false
		}
		if step.Type != "GENERIC" {
			continue
		}
		body := step.Content
		if isAntigravityToolFailure(body) {
			return false
		}
		return strings.Contains(body, exitedWithCode+" 0.") ||
			strings.Contains(body, exitedWithCode+" 0\n") ||
			strings.Contains(body, commandExitedWithCode+" 0.") ||
			strings.Contains(body, commandExitedWithCode+" 0\n") ||
			strings.Contains(body, "| exit=0")
	}
	return false
}

const (
	exitedWithCode        = "The command exited with code"
	commandExitedWithCode = "Command exited with code"
	commandOutputMarker   = "Command output:"
	toolOutputMarker      = "Output:"
)

// antigravityFixPair is the repair this machine ran after the same failure, or
// "" when the store holds none. It drives the ordinary post-tool hook, so the
// budget, the dedupe and the "waiting for a second sighting" rule are the ones
// every other harness gets.
func antigravityFixPair(dir, output, conversationID, workspace string) string {
	if strings.TrimSpace(output) == "" {
		return ""
	}
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": ""},
		"tool_response":   map[string]string{"output": output},
		"session_id":      conversationID,
		"cwd":             workspace,
	})
	if err != nil {
		return ""
	}
	var out bytes.Buffer
	if err := runHookToolAfter(dir, bytes.NewReader(payload), &out); err != nil {
		return ""
	}
	var resp sessionStartHookResponse
	if json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp) != nil {
		return ""
	}
	return resp.HookSpecificOutput.AdditionalContext
}

// newestCheckpointFromSteps is when this conversation was last compacted, or ""
// if it has not been. Antigravity has no compaction event — the five it offers
// are PreToolUse, PostToolUse, PreInvocation, PostInvocation and Stop — but it
// truncates the conversation and writes a CHECKPOINT step saying so, and the
// hook is handed the transcript on every call.
func newestCheckpointFromSteps(steps []transcriptStep) string {
	for _, step := range steps {
		if step.Type == checkpointStepType {
			return step.CreatedAt
		}
	}
	return ""
}

const checkpointStepType = "CHECKPOINT"

// forgetOnCheckpoint clears what this conversation was shown once it has been
// compacted, so the memory it just lost can be served again. Without it the
// blocks are gone from the context while the record of having sent them
// outlives them, which is the one state where recall is silent about exactly
// what the agent no longer has.
//
// The marker is kept under a key of its own, so forgetting the conversation's
// rows does not forget that this compaction was handled.
func forgetOnCheckpoint(dir, conversationID, marker string) bool {
	return hookengine.NewLedger(dir).ForgetCheckpoint(conversationID, marker)
}
