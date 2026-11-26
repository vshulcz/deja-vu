package hookengine

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	antigravityUserRequestOpen  = "<USER_REQUEST>"
	antigravityUserRequestClose = "</USER_REQUEST>"
)

// GrokEnvelope holds camelCase fields emitted by Grok Build across hook
// events so common callers can adopt them without duplicating alias logic.
type GrokEnvelope struct {
	SessionID           string   `json:"sessionId"`
	ConversationIDCamel string   `json:"conversationId"`
	WorkspaceRoot       string   `json:"workspaceRoot"`
	WorkspacePaths      []string `json:"workspacePaths"`
	TranscriptPath      string   `json:"transcriptPath"`
	ToolName            string   `json:"toolName"`
	ToolInput           struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
		Args     any    `json:"args"`
	} `json:"toolInput"`
}

// AdoptCommon fills empty snake_case hook fields from camelCase or alternate
// harness spellings.
func (e GrokEnvelope) AdoptCommon(sessionID, conversationID, transcriptPath string, roots []string) (string, string, []string) {
	sid := AdoptValue(AdoptValue(AdoptValue(sessionID, e.SessionID), conversationID), e.ConversationIDCamel)
	tp := AdoptValue(transcriptPath, e.TranscriptPath)
	ws := AdoptRoots(roots, e.WorkspaceRoot)
	if len(ws) == 0 && len(e.WorkspacePaths) > 0 {
		for _, wp := range e.WorkspacePaths {
			if trimmed := strings.TrimSpace(wp); trimmed != "" {
				ws = append(ws, trimmed)
			}
		}
	}
	return sid, tp, ws
}

// AdoptValue returns common when non-empty, otherwise fallback.
func AdoptValue(common, fallback string) string {
	if common != "" {
		return common
	}
	return fallback
}

// AdoptRoots returns common when non-empty, or a single-element slice holding
// fallback when fallback is non-empty.
func AdoptRoots(common []string, fallback string) []string {
	if len(common) > 0 || fallback == "" {
		return common
	}
	return []string{fallback}
}

// ProjectPath returns the effective project directory for a hook payload.
// When cwd is unset, or points at the user's home directory while a git
// repository appears in roots (as Antigravity Stop/SessionEnd sends), that
// repository root wins over non-repository entries.
func ProjectPath(cwd string, roots []string) string {
	trimmedCWD := strings.TrimSpace(cwd)
	repoInRoots := func() string {
		for _, r := range roots {
			if s := strings.TrimSpace(r); s != "" && sources.RepoRoot(s) != "" {
				return s
			}
		}
		return ""
	}
	if trimmedCWD != "" {
		if sources.RepoRoot(trimmedCWD) != "" || len(roots) == 0 {
			return trimmedCWD
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" && filepath.Clean(trimmedCWD) == filepath.Clean(home) {
			if r := repoInRoots(); r != "" {
				return r
			}
		}
		return trimmedCWD
	}
	if r := repoInRoots(); r != "" {
		return r
	}
	for _, r := range roots {
		if s := strings.TrimSpace(r); s != "" {
			return s
		}
	}
	return ""
}

// FirstNonEmpty returns the first argument that contains non-whitespace text.
func FirstNonEmpty(items ...string) string {
	for _, it := range items {
		if strings.TrimSpace(it) != "" {
			return it
		}
	}
	return ""
}

// MapString retrieves the first non-empty string value mapped to any key in m.
func MapString(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// CopilotToolArgs reads toolArgs either as a JSON object or as a JSON-encoded
// string holding an object.
func CopilotToolArgs(raw json.RawMessage) map[string]any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = json.RawMessage(s)
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// DecodePromptText decodes a hook prompt sent either as a plain string or as a
// content-part array and strips injected recall/hook context blocks.
func DecodePromptText(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return StripInjectedContext(s)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(p.Text)
	}
	return StripInjectedContext(sb.String())
}

// PromptEvent holds normalized user-prompt hook metadata and prompt text.
type PromptEvent struct {
	Prompt          string
	SessionID       string
	ConversationID  string
	ParentSessionID string
	TranscriptPath  string
	CWD             string
	WorkspaceRoots  []string
	HookEventName   string
	Harness         string
	Messages        json.RawMessage
	SkipIDs         []string
	Grok            GrokEnvelope
}

type rawPromptEvent struct {
	Prompt          json.RawMessage `json:"prompt"`
	SessionID       string          `json:"session_id"`
	ConversationID  string          `json:"conversation_id"`
	ParentSessionID string          `json:"parent_session_id"`
	TranscriptPath  string          `json:"transcript_path"`
	CWD             string          `json:"cwd"`
	WorkspaceRoots  []string        `json:"workspace_roots"`
	HookEventName   string          `json:"hook_event_name"`
	Harness         string          `json:"harness"`
	Messages        json.RawMessage `json:"messages"`
	SkipIDs         []string        `json:"skip_ids"`
	GrokEnvelope
}

// NormalizePromptEvent parses and normalizes a user-prompt hook payload.
func NormalizePromptEvent(payload []byte) (PromptEvent, bool) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return PromptEvent{}, false
	}
	var raw rawPromptEvent
	if err := json.Unmarshal(payload, &raw); err != nil {
		return PromptEvent{}, false
	}
	sid, tp, ws := raw.AdoptCommon(raw.SessionID, raw.ConversationID, raw.TranscriptPath, raw.WorkspaceRoots)
	return PromptEvent{
		Prompt:          DecodePromptText(raw.Prompt),
		SessionID:       sid,
		ConversationID:  raw.ConversationID,
		ParentSessionID: raw.ParentSessionID,
		TranscriptPath:  tp,
		CWD:             raw.CWD,
		WorkspaceRoots:  ws,
		HookEventName:   raw.HookEventName,
		Harness:         raw.Harness,
		Messages:        raw.Messages,
		SkipIDs:         raw.SkipIDs,
		Grok:            raw.GrokEnvelope,
	}, true
}

// ToolEdit represents one search-and-replace entry inside a multi-edit call.
type ToolEdit struct {
	OldString   string `json:"old_string"`
	NewString   string `json:"new_string"`
	OldStr      string `json:"old_str"`
	NewStr      string `json:"new_str"`
	OldText     string `json:"oldText"`
	NewText     string `json:"newText"`
	Target      string `json:"Target"`
	Replacement string `json:"Replacement"`
}

// ToolInput captures tool parameters across supported agent harness dialects.
type ToolInput struct {
	Command            string          `json:"command"`
	CommandLine        string          `json:"CommandLine"`
	CommandLineSnake   string          `json:"command_line"`
	Cmd                string          `json:"cmd"`
	Script             string          `json:"script"`
	FilePath           string          `json:"file_path"`
	AbsolutePath       string          `json:"AbsolutePath"`
	AbsolutePathSnk    string          `json:"absolute_path"`
	Paths              []string        `json:"paths"`
	PathsUpper         []string        `json:"Paths"`
	NotebookPath       string          `json:"notebook_path"`
	Path               string          `json:"path"`
	TargetFile         string          `json:"target_file"`
	Args               any             `json:"args"`
	CWD                string          `json:"cwd"`
	CwdCamel           string          `json:"CWD"`
	Workdir            string          `json:"workdir"`
	WorkingDirectory   string          `json:"working_directory"`
	WorkingDir         string          `json:"workingDir"`
	FilePathCamel      string          `json:"filePath"`
	PatchText          string          `json:"patchText"`
	Patch              string          `json:"patch"`
	NestedToolArgs     json.RawMessage `json:"toolArgs"`
	NestedToolArgsSnk  json.RawMessage `json:"tool_args"`
	OldString          string          `json:"old_string"`
	NewString          string          `json:"new_string"`
	OldStr             string          `json:"oldStr"`
	NewStr             string          `json:"newStr"`
	OldText            string          `json:"oldText"`
	NewText            string          `json:"newText"`
	OldTextSnake       string          `json:"old_text"`
	NewTextSnake       string          `json:"new_text"`
	OldStringCamel     string          `json:"oldString"`
	NewStringCamel     string          `json:"newString"`
	Target             string          `json:"Target"`
	Replacement        string          `json:"Replacement"`
	TargetLower        string          `json:"target"`
	ReplacementLower   string          `json:"replacement"`
	TargetContent      string          `json:"TargetContent"`
	ReplacementContent string          `json:"ReplacementContent"`
	Content            string          `json:"content"`
	ContentUpper       string          `json:"Content"`
	FileText           string          `json:"file_text"`
	NewSource          string          `json:"new_source"`
	Edits              []ToolEdit      `json:"edits"`
}

// ToolEvent holds the raw and normalized fields of a pre-tool hook payload.
type ToolEvent struct {
	HookEventName  string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	LLMToolName    string          `json:"llm_tool_name"`
	ToolInput      ToolInput       `json:"tool_input"`
	ToolArgs       json.RawMessage `json:"toolArgs"`
	ToolArgsSnake  json.RawMessage `json:"tool_args"`
	SessionID      string          `json:"session_id"`
	ConversationID string          `json:"conversation_id"`
	TranscriptPath string          `json:"transcript_path"`
	Harness        string          `json:"harness"`
	ToolUseID      string          `json:"tool_use_id"`
	CWD            string          `json:"cwd"`
	WorkspaceRoots []string        `json:"workspace_roots"`
	GrokEnvelope
}

// NormalizeToolEvent decodes payload into a ToolEvent and runs Normalize.
func NormalizeToolEvent(payload []byte) (ToolEvent, bool) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return ToolEvent{}, false
	}
	var ev ToolEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return ToolEvent{}, false
	}
	ev.Normalize()
	return ev, true
}

// Normalize resolves cross-harness aliases, Copilot nested args, file URIs,
// read selectors, and edit strings into canonical fields on ev.
func (ev *ToolEvent) Normalize() {
	ev.SessionID, ev.TranscriptPath, ev.WorkspaceRoots = ev.AdoptCommon(
		ev.SessionID, ev.ConversationID, ev.TranscriptPath, ev.WorkspaceRoots,
	)
	ev.ToolName = AdoptValue(AdoptValue(ev.ToolName, ev.GrokEnvelope.ToolName), ev.LLMToolName)
	ev.ToolInput.Command = AdoptValue(ev.ToolInput.Command, ev.GrokEnvelope.ToolInput.Command)
	ev.ToolInput.Command = FirstNonEmpty(
		ev.ToolInput.Command,
		ev.ToolInput.CommandLine,
		ev.ToolInput.CommandLineSnake,
		ev.ToolInput.Cmd,
		ev.ToolInput.Script,
	)
	ev.ToolInput.FilePath = AdoptValue(ev.ToolInput.FilePath, ev.GrokEnvelope.ToolInput.FilePath)
	ev.ToolInput.FilePath = FirstNonEmpty(
		ev.ToolInput.FilePath,
		ev.ToolInput.NotebookPath,
		ev.ToolInput.Path,
		ev.ToolInput.FilePathCamel,
		ev.ToolInput.TargetFile,
		ev.ToolInput.AbsolutePath,
		ev.ToolInput.AbsolutePathSnk,
	)
	if ev.ToolInput.FilePath == "" {
		ev.ToolInput.FilePath = FirstNonScratchPath(ev.ToolInput.Paths...)
	}
	if ev.ToolInput.FilePath == "" {
		ev.ToolInput.FilePath = FirstNonScratchPath(ev.ToolInput.PathsUpper...)
	}
	toolCWD := FirstNonEmpty(
		ev.ToolInput.CWD,
		ev.ToolInput.CwdCamel,
		ev.ToolInput.Workdir,
		ev.ToolInput.WorkingDirectory,
		ev.ToolInput.WorkingDir,
	)
	for _, rawArgs := range []json.RawMessage{
		ev.ToolArgs, ev.ToolArgsSnake, ev.ToolInput.NestedToolArgs, ev.ToolInput.NestedToolArgsSnk,
	} {
		args := CopilotToolArgs(rawArgs)
		if args == nil {
			continue
		}
		if ev.ToolInput.Command == "" {
			ev.ToolInput.Command = MapString(args, "command", "CommandLine", "command_line", "cmd", "script")
		}
		if ev.ToolInput.FilePath == "" {
			ev.ToolInput.FilePath = MapString(args, "path", "file_path", "filePath", "target_file", "AbsolutePath", "absolute_path")
		}
		if ev.ToolInput.FilePath == "" {
			ev.ToolInput.FilePath = FirstNonScratchSliceFromAny(args["paths"])
		}
		if ev.ToolInput.FilePath == "" {
			ev.ToolInput.FilePath = FirstNonScratchSliceFromAny(args["Paths"])
		}
		if toolCWD == "" {
			toolCWD = MapString(args, "cwd", "CWD", "workdir", "working_directory", "workingDir")
		}
	}
	if strings.TrimSpace(toolCWD) != "" {
		ev.CWD = strings.TrimSpace(toolCWD)
	}
	ev.ToolInput.FilePath = CleanFilePath(ev.ToolInput.FilePath)
	if ev.ToolInput.Args == nil {
		ev.ToolInput.Args = ev.GrokEnvelope.ToolInput.Args
	}
	if IsCommandTool(ev.ToolName) && strings.TrimSpace(ev.ToolInput.Command) != "" {
		ev.ToolInput.Command += sources.CommandArgs(ev.ToolInput.Args)
	}
	if IsReadOnlyFileTool(ev.ToolName) && ev.ToolInput.FilePath != "" {
		ev.ToolInput.FilePath = StripReadPathSelectors(ProjectPath(ev.CWD, ev.WorkspaceRoots), ev.ToolInput.FilePath)
	}
	ev.normalizeEditStrings()
}

func extractToolArgsEdits(args map[string]any) (string, string) {
	rawEdits, ok := args["edits"].([]any)
	if !ok {
		return "", ""
	}
	olds := make([]string, 0, len(rawEdits))
	news := make([]string, 0, len(rawEdits))
	for _, rawEd := range rawEdits {
		edMap, ok := rawEd.(map[string]any)
		if !ok {
			continue
		}
		if o := MapString(edMap, "old_string", "oldStr", "oldString", "oldText", "old_text", "Target", "target"); o != "" {
			olds = append(olds, o)
		}
		if n := MapString(edMap, "new_string", "newStr", "newString", "newText", "new_text", "Replacement", "replacement"); n != "" {
			news = append(news, n)
		}
	}
	return strings.Join(olds, "\n"), strings.Join(news, "\n")
}

func (ev *ToolEvent) normalizeEditStrings() {
	editOlds := make([]string, 0, len(ev.ToolInput.Edits))
	editNews := make([]string, 0, len(ev.ToolInput.Edits))
	for _, ed := range ev.ToolInput.Edits {
		if o := FirstNonEmpty(ed.OldString, ed.OldStr, ed.OldText, ed.Target); o != "" {
			editOlds = append(editOlds, o)
		}
		if n := FirstNonEmpty(ed.NewString, ed.NewStr, ed.NewText, ed.Replacement); n != "" {
			editNews = append(editNews, n)
		}
	}
	joinedOldEdits := strings.Join(editOlds, "\n")
	joinedNewEdits := strings.Join(editNews, "\n")

	var toolArgsOld, toolArgsNew string
	for _, rawArgs := range []json.RawMessage{
		ev.ToolArgs, ev.ToolArgsSnake, ev.ToolInput.NestedToolArgs, ev.ToolInput.NestedToolArgsSnk,
	} {
		args := CopilotToolArgs(rawArgs)
		if args == nil {
			continue
		}
		if toolArgsOld == "" {
			toolArgsOld = MapString(args, "old_string", "oldStr", "oldString", "oldText", "old_text", "Target", "target", "TargetContent")
		}
		if toolArgsNew == "" {
			toolArgsNew = MapString(args, "new_string", "newStr", "newString", "newText", "new_text", "Replacement", "replacement", "ReplacementContent", "content", "Content", "file_text", "new_source")
		}
		aOld, aNew := extractToolArgsEdits(args)
		if toolArgsOld == "" {
			toolArgsOld = aOld
		}
		if toolArgsNew == "" {
			toolArgsNew = aNew
		}
	}

	ev.ToolInput.OldString = FirstNonEmpty(
		ev.ToolInput.OldString,
		ev.ToolInput.OldStr,
		ev.ToolInput.OldStringCamel,
		ev.ToolInput.OldText,
		ev.ToolInput.OldTextSnake,
		ev.ToolInput.Target,
		ev.ToolInput.TargetLower,
		ev.ToolInput.TargetContent,
		joinedOldEdits,
		toolArgsOld,
	)
	ev.ToolInput.NewString = FirstNonEmpty(
		ev.ToolInput.NewString,
		ev.ToolInput.NewStr,
		ev.ToolInput.NewStringCamel,
		ev.ToolInput.NewText,
		ev.ToolInput.NewTextSnake,
		ev.ToolInput.Replacement,
		ev.ToolInput.ReplacementLower,
		ev.ToolInput.ReplacementContent,
		joinedNewEdits,
		toolArgsNew,
		ev.ToolInput.Content,
		ev.ToolInput.ContentUpper,
		ev.ToolInput.FileText,
		ev.ToolInput.NewSource,
	)
}

var ompReadSelectorRe = regexp.MustCompile(`(?i):(?:raw|-?[0-9]+(?:[-+][0-9]*)?(?:,[0-9]+(?:[-+][0-9]*)?)*)$`)

// StripReadPathSelectors peels omp-style line or :raw selectors off p unless
// the literal path exists on disk.
func StripReadPathSelectors(cwd, p string) string {
	if p == "" || !strings.Contains(p, ":") {
		return p
	}
	check := p
	if !filepath.IsAbs(check) && cwd != "" {
		check = filepath.Join(cwd, check)
	}
	if _, err := os.Stat(check); err == nil {
		return p
	}
	for ompReadSelectorRe.MatchString(p) {
		next := ompReadSelectorRe.ReplaceAllString(p, "")
		if next == "" || next == p {
			break
		}
		p = next
	}
	return p
}

// CleanFilePath strips surrounding quotes and file:// prefixes.
func CleanFilePath(p string) string {
	p = strings.TrimSpace(p)
	if len(p) >= 2 && ((p[0] == '"' && p[len(p)-1] == '"') || (p[0] == '\'' && p[len(p)-1] == '\'')) {
		p = strings.TrimSpace(p[1 : len(p)-1])
	}
	if after, ok := strings.CutPrefix(p, "file://"); ok {
		p = after
		if dec, err := url.PathUnescape(p); err == nil {
			p = dec
		}
	}
	return strings.TrimSpace(p)
}

// FirstNonScratchPath returns the first cleaned path not pointing at an agent
// scratch directory.
func FirstNonScratchPath(paths ...string) string {
	for _, raw := range paths {
		p := CleanFilePath(raw)
		if p == "" {
			continue
		}
		if sources.IsAgentScratchPath(filepath.ToSlash(p)) {
			continue
		}
		return p
	}
	return ""
}

// FirstNonScratchSliceFromAny extracts the first non-scratch path from a
// []string, []any, or JSON string array.
func FirstNonScratchSliceFromAny(v any) string {
	switch s := v.(type) {
	case []string:
		return FirstNonScratchPath(s...)
	case []any:
		cands := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				cands = append(cands, str)
			}
		}
		return FirstNonScratchPath(cands...)
	case string:
		if trimmed := strings.TrimSpace(s); strings.HasPrefix(trimmed, "[") {
			var cands []string
			if json.Unmarshal([]byte(trimmed), &cands) == nil {
				return FirstNonScratchPath(cands...)
			}
		}
	}
	return ""
}

// IsDejaMCPToolCall reports whether name refers to one of deja's own MCP tools.
func IsDejaMCPToolCall(name string) bool {
	return strings.HasPrefix(name, "mcp__deja__") ||
		strings.HasPrefix(name, "mcp_deja_") ||
		strings.HasPrefix(name, "deja__")
}

// IsExternalMCPToolWithSuffix reports whether name is a non-deja MCP tool whose
// lowercase name ends with one of suffixes.
func IsExternalMCPToolWithSuffix(name string, suffixes ...string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasPrefix(lower, "mcp_") && !strings.HasPrefix(lower, "mcp__") {
		return false
	}
	if IsDejaMCPToolCall(lower) {
		return false
	}
	for _, suf := range suffixes {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	return false
}

// IsExternalMCPCommandTool reports whether name is a non-deja MCP command tool.
func IsExternalMCPCommandTool(name string) bool {
	return IsExternalMCPToolWithSuffix(name, "_cmd", "_exec", "_shell", "_command", "_run", "_run_command", "_execute", "_bash", "_terminal")
}

// IsExternalMCPEditTool reports whether name is a non-deja MCP file-edit tool.
func IsExternalMCPEditTool(name string) bool {
	return IsExternalMCPToolWithSuffix(name, "_write", "_edit", "_create_file", "_write_file", "_edit_file", "_replace", "_str_replace", "_patch", "_apply_patch", "_modify_file", "_update_file")
}

// IsExternalMCPSearchReplaceTool reports whether name is a non-deja MCP
// search-and-replace edit tool whose arguments contain old/new strings.
func IsExternalMCPSearchReplaceTool(name string) bool {
	return IsExternalMCPToolWithSuffix(name, "_edit", "_edit_file", "_replace", "_str_replace", "_modify_file", "_update_file")
}

// IsExternalMCPReadTool reports whether name is a non-deja MCP file-read tool.
func IsExternalMCPReadTool(name string) bool {
	return IsExternalMCPToolWithSuffix(name, "_read", "_read_file", "_view_file", "_open", "_open_file", "_cat", "_get_file")
}

// IsCommandTool reports whether name invokes a shell or command runner tool.
func IsCommandTool(name string) bool {
	switch name {
	case "Bash", "bash", "shell", "Shell", "run_command", "execute_command", "terminal",
		"PowerShell", "powershell",
		"run_shell_command", "run_terminal_command", "run_terminal_cmd", "run_commands", "command_execution",
		"shell_command",
		"execute_bash",
		"run_in_terminal",
		"exec":
		return true
	}
	return IsExternalMCPCommandTool(name)
}

// IsReadOnlyFileTool reports whether name is a file-read tool.
func IsReadOnlyFileTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "read", "read_file", "Read", "view_file", "read_many_files":
		return true
	}
	return IsExternalMCPReadTool(toolName)
}

// AntigravityEvent holds the PreInvocation payload sent by Antigravity CLI,
// including agy 1.5.10 lastUserInput and cwd fields.
type AntigravityEvent struct {
	InvocationNum  int      `json:"invocationNum"`
	WorkspacePaths []string `json:"workspacePaths"`
	TranscriptPath string   `json:"transcriptPath"`
	ConversationID string   `json:"conversationId"`
	LastUserInput  string   `json:"lastUserInput"`
	CWD            string   `json:"cwd"`
}

// NormalizeAntigravityEvent parses and normalizes an Antigravity PreInvocation
// hook payload. Malformed or empty payloads return ok=false so callers emit {}.
func NormalizeAntigravityEvent(payload []byte) (AntigravityEvent, bool) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return AntigravityEvent{}, false
	}
	var ev AntigravityEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return AntigravityEvent{}, false
	}
	ev.TranscriptPath = strings.TrimSpace(ev.TranscriptPath)
	ev.ConversationID = strings.TrimSpace(ev.ConversationID)
	ev.CWD = strings.TrimSpace(ev.CWD)
	ev.LastUserInput = CleanAntigravityUserInput(ev.LastUserInput)
	cleaned := make([]string, 0, len(ev.WorkspacePaths))
	for _, wp := range ev.WorkspacePaths {
		if s := strings.TrimSpace(wp); s != "" {
			cleaned = append(cleaned, s)
		}
	}
	ev.WorkspacePaths = cleaned
	return ev, true
}

// CleanAntigravityUserInput strips injected hook blocks and <USER_REQUEST>
// framing from an Antigravity user message string.
func CleanAntigravityUserInput(content string) string {
	cleaned := StripInjectedContext(content)
	if _, after, ok := strings.Cut(cleaned, antigravityUserRequestOpen); ok {
		rest := after
		if end := strings.Index(rest, antigravityUserRequestClose); end >= 0 {
			rest = rest[:end]
		}
		return StripInjectedContext(rest)
	}
	return cleaned
}

// EffectiveWorkspace selects the primary workspace path, preferring explicit
// workspacePaths, then conversation-derived fallback, then payload cwd.
func (e AntigravityEvent) EffectiveWorkspace(convFallback string) string {
	if len(e.WorkspacePaths) > 0 {
		return e.WorkspacePaths[0]
	}
	if fb := strings.TrimSpace(convFallback); fb != "" {
		return fb
	}
	return e.CWD
}

// PreferredPrompt returns lastUserInput when non-empty, falling back to the
// latest transcript prompt otherwise.
func (e AntigravityEvent) PreferredPrompt(transcriptFallback string) string {
	if e.LastUserInput != "" {
		return e.LastUserInput
	}
	return strings.TrimSpace(transcriptFallback)
}
