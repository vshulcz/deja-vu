package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/hookengine"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// `deja hook-tool-after` is the fix pair delivered at the failure.
//
// deja has known which command followed an error before for a long time, and
// `fix` exposes it as a tool. Measured over eighteen opportunities across a
// flat tool list, a dispatcher and a dispatcher without its steering clause,
// an agent chose that tool zero times: it does not occur to a model to ask
// whether this error has been solved here before. It is not a wording problem.
//
// So stop asking anything to choose it. A command fails, deja already has the
// pair, and the pair arrives in the same turn. Measured six runs each, with
// nothing in the prompt mentioning deja: unaided, the agent carried the
// remembered command 0 of 6 times and twice proposed rewriting vendored source
// to make the symbol go away; with the pair injected at the failure, 6 of 6.
// (#1174, #1298)
//
// Same discipline as the pre-tool hook: one line, a hard cap, deduped per agent
// session, and silence unless the store actually holds a pair.
const (
	// toolAfterMaxBytes caps the payload. Two short lines: what failed before,
	// and the command that followed it.
	toolAfterMaxBytes = 420
	// toolAfterOutputScan caps how much of a failing command's output is read
	// for a signature. An error names itself at the top or the bottom; the
	// middle of a 10 MB build log is not worth the scan inside an action the
	// user is waiting on.
	toolAfterOutputScan = 8000
)

type toolAfterInput struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	// TRAE IDE's name for the same field (#4731).
	LLMToolName string `json:"llm_tool_name"`
	ToolInput   struct {
		Command          string          `json:"command"`
		CommandLine      string          `json:"CommandLine"`
		CmdLine          string          `json:"command_line"`
		Cmd              string          `json:"cmd"`
		Script           string          `json:"script"`
		Args             any             `json:"args"`
		CWD              string          `json:"cwd"`
		CwdCamel         string          `json:"CWD"`
		Workdir          string          `json:"workdir"`
		WorkingDirectory string          `json:"working_directory"`
		WorkingDir       string          `json:"workingDir"`
		ToolArgs         json.RawMessage `json:"toolArgs"`
		ToolArgsSnake    json.RawMessage `json:"tool_args"`
	} `json:"tool_input"`
	// Harnesses disagree about where a command's output lives, and about
	// whether the exit code is reported at all, so every shape is read and the
	// decision rests on the text rather than on a status field.
	ToolResponse json.RawMessage `json:"tool_response"`
	// Cursor calls it tool_output, and sends a JSON document inside a JSON
	// string: {"output":"…","exitCode":0} (measured on cursor-agent
	// 2026.09.02). Reading only tool_response left the fix pair with nothing.
	ToolOutput json.RawMessage `json:"tool_output"`
	// Qwen Code has a separate event for a failed tool, and its payload carries
	// the command's output under `error` rather than tool_response — measured
	// on qwen-code 0.20.0. Without this the hook fires at the failure and finds
	// nothing to look up.
	Error json.RawMessage `json:"error"`
	// Claw Code PostToolUseFailure puts the failure string in tool_error.
	ToolError json.RawMessage `json:"tool_error"`
	// Cursor's postToolUseFailure names the failure here (2026.09.02).
	ErrorMessage   json.RawMessage `json:"error_message"`
	SessionID      string          `json:"session_id"`
	ConversationID string          `json:"conversation_id"`
	CWD            string          `json:"cwd"`
	WorkspaceRoots []string        `json:"workspace_roots"`
	// Grok spells the rest in camelCase and puts the result under
	// toolResult (#4499). See hook_grok.go.
	grokEnvelope
	ToolResult    json.RawMessage `json:"toolResult"`
	ToolArgs      json.RawMessage `json:"toolArgs"`
	ToolArgsSnake json.RawMessage `json:"tool_args"`
}

func (i *toolAfterInput) adopt() {
	i.SessionID, _, i.WorkspaceRoots = i.adoptCommon(i.SessionID, i.ConversationID, "", i.WorkspaceRoots)
	if i.CWD == "" && len(i.WorkspaceRoots) > 0 {
		i.CWD = i.WorkspaceRoots[0]
	}
	i.ToolName = adoptGrok(adoptGrok(i.ToolName, i.grokEnvelope.ToolName), i.LLMToolName)

	cmd := firstNonEmpty(
		i.ToolInput.Command,
		i.grokEnvelope.ToolInput.Command,
		i.ToolInput.CommandLine,
		i.ToolInput.CmdLine,
		i.ToolInput.Cmd,
		i.ToolInput.Script,
	)
	toolCWD := firstNonEmpty(
		i.ToolInput.CWD,
		i.ToolInput.CwdCamel,
		i.ToolInput.Workdir,
		i.ToolInput.WorkingDirectory,
		i.ToolInput.WorkingDir,
	)
	for _, rawArgs := range []json.RawMessage{i.ToolInput.ToolArgs, i.ToolInput.ToolArgsSnake, i.ToolArgs, i.ToolArgsSnake} {
		if args := copilotToolArgs(rawArgs); args != nil {
			if cmd == "" {
				cmd = mapString(args, "command", "CommandLine", "command_line", "cmd", "script")
			}
			if toolCWD == "" {
				toolCWD = mapString(args, "cwd", "CWD", "workdir", "working_directory", "workingDir")
			}
			if i.ToolInput.Args == nil && args["args"] != nil {
				i.ToolInput.Args = args["args"]
			}
		}
	}
	if strings.TrimSpace(toolCWD) != "" {
		i.CWD = strings.TrimSpace(toolCWD)
	}
	if cmd != "" && i.ToolInput.Args != nil {
		cmd += sources.CommandArgs(i.ToolInput.Args)
	}
	i.ToolInput.Command = cmd
}

func runHookToolAfter(dir string, stdin io.Reader, stdout io.Writer) error {
	return runHookToolAfterMode(dir, stdin, stdout, false)
}

// plain=true prints the block itself rather than the PostToolUse envelope, for
// a host that puts it somewhere of its own. cline's message builder appends it
// to the failing tool result the model is about to read.
func runHookToolAfterMode(dir string, stdin io.Reader, stdout io.Writer, plain bool) error {
	var input toolAfterInput
	raw := readHookPayload(stdin, hookStdinWait)
	_ = json.NewDecoder(bytes.NewReader(raw)).Decode(&input)
	input.adopt()
	adoptCopilotHost(raw)
	adoptCursorHost(raw)
	// The kill switch, before anything is read. It reached the session-start
	// hook and nothing else, so a machine with recall off still had text drawn
	// from its own indexed sessions injected here (#2701).
	if recallIsOff() {
		return nil
	}

	if !planIndexReady(dir) {
		// Ask, do not build. #777 gave the per-prompt and session-start hooks
		// this: an index in a format this build cannot read answers nothing,
		// which reads as a user with no history, and nothing else asks for the
		// rebuild that fixes it. A spawned subagent reaches only these hooks —
		// install.go says so where it wires Task and Agent — so without this a
		// fleet works against a stale index until its parent types something
		// (#2567). requestWarmup writes a sentinel and detaches a child; the
		// action pays neither the read nor the wait.
		// Keep running index-free repo-memory and AST/symbol recovery on cold stores.
		requestWarmup(dir)
	}
	// A payload the decoder could not read has no tool name either, and the
	// gate below would drop it before the salvage a few lines down. Read the
	// name straight out of the raw bytes instead — a payload that names another
	// tool is still not ours, cut or not.
	name := input.ToolName
	truncated := name == "" && len(bytes.TrimSpace(raw)) > 0
	if truncated {
		name, _ = jsonStringAfter(after(string(raw), `"tool_name"`))
		if name == "" {
			name, _ = jsonStringAfter(after(string(raw), `"toolName"`))
		}
		if name == "" {
			name, _ = jsonStringAfter(after(string(raw), `"llm_tool_name"`))
		}
	}
	if !isCommandTool(name) {
		return nil
	}
	response := input.ToolResponse
	if len(bytes.TrimSpace(response)) == 0 {
		response = input.ToolOutput
	}
	if len(bytes.TrimSpace(response)) == 0 {
		response = input.Error
	}
	if len(bytes.TrimSpace(response)) == 0 {
		response = input.ToolResult
	}
	if len(bytes.TrimSpace(response)) == 0 {
		response = input.ToolError
	}
	if len(bytes.TrimSpace(response)) == 0 {
		response = input.ErrorMessage
	}
	out := toolResponseText(response)
	if out == "" {
		// readHookPayload stops at 1 MiB, so a verbose build cuts the JSON
		// mid-string and the decode yields nothing at all — not a truncated
		// payload, an empty one. The error is usually the first line of that
		// output, and this hook exists for exactly the failing `make` or
		// `pytest -v` whose log runs past a megabyte (#1716). What arrived is
		// still worth reading: pull the output back out of the cut JSON rather
		// than throwing away a megabyte that begins with the error.
		out = sources.UnwrapMCPCommandHeader(sources.UnwrapShellReport(salvageFromPayload(string(raw))))
	}
	// Out of the raw bytes for a cut payload, the way the tool name is: the
	// line keeps to the project it names, and the harness sends cwd and session
	// ahead of the output.
	cwd := input.CWD
	sessionID := input.SessionID
	cmdStr := input.ToolInput.Command
	if truncated {
		head := string(raw[:min(len(raw), 4096)])
		if cwd == "" {
			for _, key := range []string{`"cwd"`, `"workspaceRoot"`} {
				if v, _ := jsonStringAfter(after(head, key)); v != "" {
					cwd = v
					break
				}
			}
		}
		if sessionID == "" {
			for _, key := range []string{`"session_id"`, `"sessionId"`, `"conversation_id"`, `"conversationId"`} {
				if v, _ := jsonStringAfter(after(head, key)); v != "" {
					sessionID = v
					break
				}
			}
		}
		if cmdStr == "" {
			for _, key := range []string{`"command"`, `"cmd"`} {
				if v, _ := jsonStringAfter(after(head, key)); v != "" {
					cmdStr = v
					break
				}
			}
		}
	}
	resolvedCWD := hookCWD(cwd)
	streak := coordRecordAfterCommand(dir, resolvedCWD, cmdStr, out, raw, sessionID)
	if out == "" {
		return nil
	}
	line := fixPairLineForStreak(dir, resolvedCWD, cmdStr, out, streak)
	if line == "" {
		return nil
	}
	token := "fix:" + shortHash(line)
	if dir != "" && sessionID != "" && !hookengine.NewLedger(dir).ClaimOnce(sessionID, token) {
		return nil
	}
	payload := frameRecall(truncateToolLine(line, toolAfterMaxBytes))
	// The receiving session too: this is the repair delivered at the failure,
	// the surface with the best evidence behind it, and it was the one that
	// could not be followed (#1494 gave the per-prompt hook the same field).
	// The text too, for the reason the pre-tool hook keeps it: a fix pair
	// delivered at a failure is the one injection worth reading back.
	usage.RecordDigestInto(dir, usage.KindTool, payload, sessionID, 1, 0, nil)
	if plain {
		fmt.Fprint(stdout, payload)
		return nil
	}
	// Copilot CLI reads only the flat shape. Measured on 1.0.91: a command
	// that exits non-zero is still a successful tool call there, so it is
	// postToolUse that fires, and its additionalContext is appended to the
	// result the model reads next, in the same turn.
	if copilotHookOutput {
		emitCopilotContext(stdout, payload)
		return nil
	}
	if mcodeHookOutput {
		emitMcodeContext(stdout, replyEventName(input.HookEventName, "PostToolUse", "PostToolUse", "PostToolUseFailure"), payload)
		return nil
	}
	var resp sessionStartHookResponse
	// The event it was sent. Claude Code fires PostToolUseFailure for a command
	// that exited non-zero and drops a reply naming any other event (#4488);
	// qwen takes either. Everything else gets the name it always had.
	resp.HookSpecificOutput.HookEventName = replyEventName(input.HookEventName, "PostToolUse", "PostToolUse", "PostToolUseFailure")
	resp.HookSpecificOutput.AdditionalContext = payload
	if notesHaveReader() {
		resp.SystemMessage = safeNote("deja: " + line)
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil //nolint:nilerr // a reply that cannot marshal means no reply
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

// isExternalMCPToolWithSuffix reports whether name is a non-deja MCP tool ending in one of suffixes.
func isExternalMCPToolWithSuffix(name string, suffixes ...string) bool {
	return hookengine.IsExternalMCPToolWithSuffix(name, suffixes...)
}

// isCommandTool reports whether this tool runs a command.
func isCommandTool(name string) bool {
	return hookengine.IsCommandTool(name)
}

// toolResponseText pulls whatever the harness called the output. Claude Code
// sends an object with stdout and stderr, codex a string, and others a mix; a
// bare string response is the whole output.
//
// What comes back is unwrapped of the labelled report gemini and qwen put
// around a command's output — gemini under llmContent, qwen's failure payload
// as a plain string in `error`. With the frame in place the error reads as
// `Output: ./main.go:9:2: …` and hashes to a signature no session recorded.
// Once, as the index does: a second pass took a leading "Output: " off the
// command's own output too.
func toolResponseText(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		// A harness that puts a JSON document inside the string — cursor sends
		// {"output":"…","exitCode":0} that way — would otherwise hand the
		// signature scan a line of escaped JSON rather than the error.
		if inner := strings.TrimSpace(s); strings.HasPrefix(inner, "{") {
			if text := toolResponseText(json.RawMessage(inner)); text != "" {
				return text
			}
		}
		return clampOutput(sources.UnwrapMCPCommandHeader(sources.UnwrapShellReport(s)))
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	var b strings.Builder
	appendPart := func(v string) {
		if strings.TrimSpace(v) == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(v)
	}
	// stderr first: the signature is far more often there, and the scan is
	// capped, so a long stdout must not push it out of reach.
	// llmContent is gemini's: the text it puts in front of the model, which for
	// a shell tool is the command's own output inside a wrapper of its own.
	// output_for_prompt is grok's: its `output` is the raw bytes as a number
	// array, and this is the text the model reads (#4499).
	// textResultForLlm is Copilot CLI's: the output as its model gets it.
	// message is Kimi's: a failed tool's `error` is {code, message, ...}.
	for _, key := range []string{"stderr", "error", "output", "stdout", "content", "result", "llmContent", "output_for_prompt", "textResultForLlm", "message"} {
		if v, ok := obj[key].(string); ok {
			appendPart(v)
			continue
		}
		if key == "content" || key == "llmContent" {
			appendPart(mcpContentArrayText(obj[key]))
		}
	}
	return clampOutput(sources.UnwrapMCPCommandHeader(sources.UnwrapShellReport(b.String())))
}

// mcpContentArrayText extracts text segments from an MCP CallToolResult content
// array such as `[{"type":"text","text":"..."}]`.
func mcpContentArrayText(v any) string {
	items, ok := v.([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ != "" && typ != "text" {
			continue
		}
		text, _ := m["text"].(string)
		if strings.TrimSpace(text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(text)
	}
	return b.String()
}

// after returns what follows key where it is used as one, or "" when the key is
// not there. Scoping the salvage this way keeps it from mining the command in
// tool_input, which can hold any of the keys below.
//
// Where it is used as one, because the first occurrence is inside the command
// whenever the command mentions it — and this path exists for payloads the
// decoder could not read, which is where an unescaped `"tool_response"` in a
// command shows up. A key is followed by a colon; a mention is not (#2051).
func after(s, key string) string { return afterKey(s, key, false) }

// salvageFromPayload is the whole salvage: scope to the tool's response, pull a
// value out of it, and bound what comes back. Qwen Code sends a failed
// command's report as a top-level error with no tool_response, so without one
// the last error key is read (#4314).
func salvageFromPayload(raw string) string {
	if scope := afterLast(raw, `"tool_response"`); scope != "" {
		return clampOutput(salvageToolOutput(scope))
	}
	for _, key := range []string{`"error"`, `"tool_error"`} {
		if v, _ := jsonStringAfter(afterLast(raw, key)); strings.TrimSpace(v) != "" {
			return clampOutput(v)
		}
	}
	return ""
}

// afterLast is after, from the last place the key is used as one. A tool's
// response is written after the input that produced it, so where a command
// quotes a whole payload of its own — a shape this path sees, since a command
// with unescaped quotes in it is why the decoder failed — the real key is the
// later one (#2051).
func afterLast(s, key string) string { return afterKey(s, key, true) }

// afterKey is both: what follows the first or the last place key is used as
// one. A key is followed by a colon; a mention of it in a command is not.
func afterKey(s, key string, last bool) string {
	out := ""
	for i := 0; ; {
		j := strings.Index(s[i:], key)
		if j < 0 {
			return out
		}
		at := i + j + len(key)
		if rest := strings.TrimLeft(s[at:], " \t\r\n"); strings.HasPrefix(rest, ":") {
			out = s[at:]
			if !last {
				return out
			}
		}
		i = at
	}
}

// salvageToolOutput pulls the tool's output out of a payload the decoder could
// not read. Scanning the raw bytes does not work: the JSON prefix is glued to
// the first line of the output, and a line holding a quote is dropped as source
// rather than error — correctly, for real tool output. So find where the value
// starts, unescape what follows, and hand back that.
func salvageToolOutput(raw string) string {
	// `after` finds each key where it is a key, which is what keeps a mention
	// of "stderr" inside another value from standing in for the field: read at
	// the mention, the scan gave up on stderr and answered with stdout, the
	// half of the payload without the error in it (#2051).
	// llmContent and returnDisplay are where Gemini CLI puts a command's
	// output (#4314).
	for _, key := range []string{`"stderr"`, `"error"`, `"tool_error"`, `"output"`, `"stdout"`, `"content"`, `"result"`, `"llmContent"`, `"returnDisplay"`} {
		// Every occurrence, not the first: a mention of the key that is not a
		// key — `grep "stderr" build.log` — made this give up on the key
		// entirely and skip the real one further along (#2051).
		for i := 0; ; {
			j := strings.Index(raw[i:], key)
			if j < 0 {
				break
			}
			at := i + j + len(key)
			if v, ok := jsonStringAfter(raw[at:]); ok && strings.TrimSpace(v) != "" {
				return v
			}
			i = at
		}
	}
	return ""
}

// jsonStringAfter reads the string value that follows a key: the colon, any
// whitespace a pretty-printed payload puts there, then the quoted value. The
// value ends at the first unescaped quote, or at the end of what arrived when
// the payload was cut mid-string — which is the case this exists for.
func jsonStringAfter(s string) (string, bool) {
	s = strings.TrimLeft(s, " \t\r\n")
	if !strings.HasPrefix(s, ":") {
		return "", false
	}
	s = strings.TrimLeft(s[1:], " \t\r\n")
	if !strings.HasPrefix(s, `"`) {
		return "", false
	}
	s = s[1:]
	if end := unescapedQuote(s); end >= 0 {
		s = s[:end]
	}
	s = unescapeJSONString(s)
	return s, true
}

// unescapeJSONString undoes the escapes a tool log actually carries, in one
// left-to-right pass so an escaped backslash cannot be re-read as an escape.
// A cut value can end on a lone backslash; that one is dropped.
func unescapeJSONString(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			if s[i] != '\\' {
				b.WriteByte(s[i])
			}
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '"', '\\', '/':
			b.WriteByte(s[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// unescapedQuote returns the index of the first quote not preceded by an odd
// number of backslashes, or -1.
func unescapedQuote(s string) int {
	for i := range len(s) {
		if s[i] != '"' {
			continue
		}
		back := 0
		for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
			back++
		}
		if back%2 == 0 {
			return i
		}
	}
	return -1
}

func clampOutput(s string) string {
	if len(s) <= toolAfterOutputScan {
		return s
	}
	// Both ends, because a stack trace names itself at the top and a build log
	// at the bottom.
	half := toolAfterOutputScan / 2
	return s[:half] + "\n" + s[len(s)-half:]
}

// fixPairLine is the one line the failure gets: the error this machine saw
// before and the command that followed it without the error coming back.
func fixPairLine(dir, cwd, output string) string {
	return fixPairLineFor(dir, cwd, "", output)
}

// fixPairLineFor is fixPairLine knowing the command that failed, "" when the
// host does not say (index.FixesForCommand). Direct callers outside live
// command streaks treat the failure as confirmed past the first-strike gate.
func fixPairLineFor(dir, cwd, command, output string) string {
	return fixPairLineForStreak(dir, cwd, command, output, 2)
}

func fixPairLineForStreak(dir, cwd, command, output string, streak int) string {
	pol := policy.Load()
	names := sync.OnceValue(func() []string { return digest.ProjectNameCandidates(cwd) })
	here := func(project string) bool { return project != "" && namesInclude(names(), project) }
	ready := planIndexReady(dir)
	// Restrict auto-injected fix pairs strictly to the current project:
	// unscoped calls (cwd == "" or $HOME) and cross-project pairs stay silent
	// in auto-hooks while remaining available to explicit `deja fix` queries.
	allowHere := func(project string) bool {
		return cwd != "" && here(project) && pol.Allows(policy.ActivationAuto, project)
	}
	var pairs []index.FixPair
	if ready && cwd != "" {
		pairs = index.FixesForCommand(dir, command, output, 4, allowHere)
	}
	for _, p := range pairs {
		if p.Candidate || orientSkipFixPairForHook(p) {
			continue
		}
		if streak < 2 && !fixPairAllowedOnFirstStrike(p, output) {
			continue
		}
		if line := fixLineIn(p, frictionCount(dir, p, pol), cwd); line != "" {
			return line
		}
	}
	if line := repoMemoryLineForFailedOutput(cwd, command, output, pol); line != "" {
		return line
	}
	if line := compilerSymbolSignatureLine(cwd, output, pol); line != "" {
		return line
	}
	if line := failingTestSourceLine(cwd, output, pol); line != "" {
		return line
	}
	if !ready || cwd == "" {
		return ""
	}
	for _, probe := range nearErrorProbesFromOutput(output) {
		near := nearestRecordedError(dir, probe, allowHere)
		if near == "" {
			continue
		}
		for _, p := range index.FixesForCommand(dir, command, near, 4, allowHere) {
			if p.Candidate || orientSkipFixPairForHook(p) {
				continue
			}
			if streak < 2 && !fixPairAllowedOnFirstStrike(p, output) {
				continue
			}
			if line := fixLineIn(p, frictionCount(dir, p, pol), cwd); line != "" {
				return line
			}
		}
	}
	return ""
}

func fixPairAllowedOnFirstStrike(p index.FixPair, output string) bool {
	return p.Repaired || p.Substitute || isDetEnvFailureOutput(output)
}

func isDetEnvFailureOutput(output string) bool {
	if output == "" {
		return false
	}
	if index.IsShellDiagnostic(output) {
		return true
	}
	low := strings.ToLower(output)
	for _, needle := range []string{
		"command not found",
		"no such file or directory",
		"executable file not found",
		"no module named",
		"modulenotfounderror",
		"cannot find module",
		"unknown flag",
		"flag provided but not defined",
		"unknown shorthand",
		"unrecognized arguments",
		"usage:",
		"permission denied",
		"eacces",
		"unauthorized",
		"api key",
	} {
		if strings.Contains(low, needle) {
			return true
		}
	}
	if strings.Contains(low, "token") &&
		(strings.Contains(low, "expired") || strings.Contains(low, "invalid") || strings.Contains(low, "missing")) {
		return true
	}
	return false
}

// failingTestSourceLine extracts the exact failing source line from a test file
// cited in test failure output across Go, Python, TS/JS, and Rust so the agent
// sees the failing assertion immediately without a Read round-trip.
func failingTestSourceLine(cwd, output string, pol policy.Policy) string {
	if cwd == "" || output == "" || pol.SourceHintsDisabled() || policy.SourceHintsOff() || !pol.Allows(policy.ActivationAuto, "") {
		return ""
	}
	if !strings.Contains(output, "FAIL") && !strings.Contains(output, "AssertionError") && !strings.Contains(output, "panicked at ") && !strings.Contains(output, "Traceback (most recent call last):") {
		return ""
	}
	targets := extractFailingTestTargets(output)
	if len(targets) == 0 {
		return ""
	}
	scanRoot := safeScanRoot(cwd)
	if scanRoot == "" {
		return ""
	}
	tree := orientRepoTree(scanRoot)
	for _, target := range targets {
		if line := resolveFailingTargetSnippet(scanRoot, tree, target); line != "" {
			return line
		}
	}
	return ""
}

type failingTestTarget struct {
	filePart string
	lineNo   int
	testName string
}

func extractFailingTestTargets(output string) []failingTestTarget {
	var targets []failingTestTarget
	seen := map[string]bool{}
	addTarget := func(filePart string, lineNo int, testName string) {
		key := fmt.Sprintf("%s:%d", filePart, lineNo)
		if !seen[key] {
			seen[key] = true
			targets = append(targets, failingTestTarget{filePart: filePart, lineNo: lineNo, testName: testName})
		}
	}
	if strings.Contains(output, "panicked at ") {
		if filePart, lineNo, testName := parseRustPanicTarget(output); filePart != "" && lineNo > 0 {
			addTarget(filePart, lineNo, testName)
		}
	}
	for tok := range strings.FieldsSeq(output) {
		tok = strings.Trim(tok, "\"'`(),;!?[]{}<>❯")
		if tok == "at" || tok == "" {
			continue
		}
		colon1 := strings.IndexByte(tok, ':')
		if colon1 <= 0 {
			continue
		}
		filePart := tok[:colon1]
		if !isFailingTestFileToken(filePart) {
			continue
		}
		rest := tok[colon1+1:]
		if colon2 := strings.IndexByte(rest, ':'); colon2 >= 0 {
			rest = rest[:colon2]
		}
		lineNo, err := strconv.Atoi(rest)
		if err == nil && lineNo > 0 {
			addTarget(filePart, lineNo, "")
		}
	}
	if len(targets) == 0 && strings.Contains(output, "Traceback (most recent call last):") {
		var pyFrames []failingTestTarget
		for line := range strings.SplitSeq(output, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, `File "`) {
				continue
			}
			rest := strings.TrimPrefix(trimmed, `File "`)
			qIdx := strings.IndexByte(rest, '"')
			if qIdx <= 0 {
				continue
			}
			filePart := rest[:qIdx]
			slashPath := strings.ToLower(strings.ReplaceAll(filePart, `\`, "/"))
			if strings.HasPrefix(filePart, "<") || strings.Contains(slashPath, "site-packages") ||
				strings.Contains(slashPath, "/venv/") || strings.Contains(slashPath, "/.venv/") ||
				strings.Contains(slashPath, "/lib/python") {
				continue
			}
			afterQuote := rest[qIdx+1:]
			_, afterLine, ok := strings.Cut(afterQuote, ", line ")
			if !ok {
				continue
			}
			numStr := strings.TrimSpace(afterLine)
			if comma := strings.IndexAny(numStr, ", "); comma >= 0 {
				numStr = numStr[:comma]
			}
			if lineNo, err := strconv.Atoi(numStr); err == nil && lineNo > 0 {
				pyFrames = append(pyFrames, failingTestTarget{filePart: filePart, lineNo: lineNo})
			}
		}
		for _, f := range slices.Backward(pyFrames) {
			targets = append(targets, f)
		}
	}
	return targets
}

func parseRustPanicTarget(output string) (string, int, string) {
	for line := range strings.SplitSeq(output, "\n") {
		idx := strings.Index(line, "panicked at ")
		if idx < 0 {
			continue
		}
		testName := ""
		if tStart := strings.Index(line, "thread '"); tStart >= 0 && tStart < idx {
			tRest := line[tStart+len("thread '"):]
			if tName, _, ok := strings.Cut(tRest, "'"); ok {
				testName = tName
				if colonColon := strings.LastIndex(testName, "::"); colonColon >= 0 {
					testName = testName[colonColon+2:]
				}
			}
		}
		rest := strings.TrimSpace(line[idx+len("panicked at "):])
		rest = strings.Trim(rest, "\"'`(),;!?[]{}<>")
		colon1 := strings.IndexByte(rest, ':')
		if colon1 <= 0 {
			continue
		}
		filePart := rest[:colon1]
		if !strings.HasSuffix(strings.ToLower(filePart), ".rs") {
			continue
		}
		numPart := rest[colon1+1:]
		if colon2 := strings.IndexByte(numPart, ':'); colon2 >= 0 {
			numPart = numPart[:colon2]
		}
		lineNo, err := strconv.Atoi(numPart)
		if err == nil && lineNo > 0 {
			return filePart, lineNo, testName
		}
	}
	return "", 0, ""
}

func isFailingTestFileToken(filePart string) bool {
	lowFile := strings.ToLower(filePart)
	if strings.HasSuffix(lowFile, "_test.go") || strings.HasSuffix(lowFile, "_test.rs") {
		return true
	}
	if strings.HasSuffix(lowFile, ".py") && (strings.Contains(lowFile, "test_") || strings.Contains(lowFile, "_test.py")) {
		return true
	}
	if (strings.Contains(lowFile, ".test.") || strings.Contains(lowFile, ".spec.")) &&
		(strings.HasSuffix(lowFile, ".ts") || strings.HasSuffix(lowFile, ".tsx") || strings.HasSuffix(lowFile, ".js") || strings.HasSuffix(lowFile, ".jsx")) {
		return true
	}
	return false
}

func relativizeToScanRoot(scanRoot, diskPath, fallback string) string {
	relIfUnder := func(root, target string) string {
		r, err := filepath.Rel(root, target)
		if err != nil {
			return ""
		}
		slashR := filepath.ToSlash(r)
		if slashR == ".." || strings.HasPrefix(slashR, "../") {
			return ""
		}
		return slashR
	}
	if r := relIfUnder(scanRoot, diskPath); r != "" {
		return r
	}
	canonRoot, err1 := filepath.EvalSymlinks(scanRoot)
	canonDisk, err2 := filepath.EvalSymlinks(diskPath)
	if err1 == nil && err2 == nil {
		if r := relIfUnder(canonRoot, canonDisk); r != "" {
			return r
		}
	}
	return fallback
}

func resolveSourceTargetPath(scanRoot string, tree repoFileTree, filePart string) (diskPath, rel string) {
	rawTrim := strings.TrimSpace(filePart)
	rel = filepath.ToSlash(rawTrim)
	for strings.HasPrefix(rel, "./") {
		rel = rel[2:]
	}
	if resolved := tree.resolveRel(rel); resolved != "" {
		rel = resolved
	}
	diskPath = rel
	if !filepath.IsAbs(diskPath) {
		diskPath = filepath.Join(scanRoot, filepath.FromSlash(diskPath))
	}
	if !fileThere(diskPath) && !strings.ContainsAny(rawTrim, `/\`) {
		diskPath, rel = locateFailingFileInScanRoot(scanRoot, rawTrim, diskPath, rel)
	}
	if filepath.IsAbs(diskPath) || filepath.IsAbs(rel) {
		rel = relativizeToScanRoot(scanRoot, diskPath, rel)
	}
	rel = filepath.ToSlash(rel)
	return diskPath, rel
}

func resolveFailingTargetSnippet(scanRoot string, tree repoFileTree, target failingTestTarget) string {
	diskPath, rel := resolveSourceTargetPath(scanRoot, tree, target.filePart)
	raw, err := os.ReadFile(diskPath)
	if err != nil || len(raw) > 512<<10 {
		return ""
	}
	lines := strings.Split(string(raw), "\n")
	if target.lineNo > len(lines) {
		return ""
	}
	srcLine := strings.TrimSpace(lines[target.lineNo-1])
	if srcLine == "" {
		return ""
	}
	curTest := target.testName
	if curTest == "" {
		for i := target.lineNo - 1; i >= 0; i-- {
			trimmed := strings.TrimSpace(lines[i])
			if h := testHeaderName(trimmed); h != "" {
				curTest = h
				break
			}
		}
	}
	var base string
	if curTest != "" {
		base = fmt.Sprintf("failing test line — %s:%d (%s): %s",
			search.SafePath(rel), target.lineNo, search.SafeLine(curTest),
			clipDecision(search.SafeText(srcLine), 140))
	} else {
		base = fmt.Sprintf("failing test line — %s:%d: %s",
			search.SafePath(rel), target.lineNo, clipDecision(search.SafeText(srcLine), 140))
	}
	if sut := findFailingTestSUTTarget(scanRoot, lines, target.lineNo); sut != "" {
		return base + " | target: " + sut
	}
	return base
}

func findFailingTestSUTTarget(scanRoot string, lines []string, targetLineNo int) string {
	rel, declLine, sig := resolveCallFromSourceWindow(scanRoot, lines, targetLineNo, true)
	if rel == "" || sig == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d: %s",
		search.SafePath(rel), declLine, clipDecision(search.SafeText(sig), 110))
}

func locateFailingFileInScanRoot(scanRoot, filePart, defaultDisk, defaultRel string) (string, string) {
	if policy.SourceHintsOff() {
		return defaultDisk, defaultRel
	}
	diskPath := defaultDisk
	rel := defaultRel
	_ = filepath.WalkDir(scanRoot, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // skip unreadable paths
		}
		if d.IsDir() {
			if p != scanRoot && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(d.Name(), filePart) {
			diskPath = p
			if r, rErr := filepath.Rel(scanRoot, p); rErr == nil && !strings.HasPrefix(r, "..") {
				rel = filepath.ToSlash(r)
			}
			return filepath.SkipAll
		}
		return nil
	})
	return diskPath, rel
}

// compilerSymbolSignatureLine inspects compiler or test failure output for
// symbol-mismatch errors (undefined symbol, wrong argument count/types) and
// resolves the symbol's live declaration signature in the repository.
func compilerSymbolSignatureLine(cwd, output string, pol policy.Policy) string {
	if cwd == "" || output == "" || pol.SourceHintsDisabled() || policy.SourceHintsOff() || !pol.Allows(policy.ActivationAuto, "") {
		return ""
	}
	if line := attributeErrorSignatureLine(cwd, output); line != "" {
		return line
	}
	markers := []string{
		"not enough arguments in call to ",
		"too many arguments in call to ",
		"in argument to ",
		" variables but ",
		" variable but ",
		" return values of ",
		"undefined: ",
		"has no field or method ",
		"cannot import name '",
		"NameError: name '",
		"Cannot find name '",
		"has no exported member '",
		"cannot find function `",
		"cannot find value `",
		"cannot find struct `",
		"TypeError: ",
	}
	var fallbackSym, fallbackMod string
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, m := range markers {
			_, after, ok := strings.Cut(trimmed, m)
			if !ok {
				continue
			}
			cand := strings.TrimSpace(after)
			if idx := strings.IndexAny(cand, " \t'\"`();,"); idx > 0 {
				cand = cand[:idx]
			}
			cand = strings.TrimRight(cand, ":")
			var qual string
			if sep := strings.LastIndex(cand, "::"); sep > 0 && sep+2 < len(cand) {
				qual = cleanRustTypeIdent(cand[:sep])
				cand = cand[sep+2:]
			} else if dot := strings.LastIndexByte(cand, '.'); dot >= 0 && dot+1 < len(cand) {
				qual = lastDotIdent(cand[:dot])
				cand = cand[dot+1:]
			}
			sym := leadingIdent(cand)
			if len(sym) < 3 || (qual == "" && len(sym) < 4) {
				continue
			}
			if qual != "" {
				if rel, lineNo, sig := findQualifiedMemberSignatureInRepo(cwd, qual, sym); rel != "" && sig != "" {
					return formatSymbolSignatureLine(rel, lineNo, sig)
				}
			}
			if len(sym) >= 4 {
				if rel, lineNo, sig := findSymbolDeclarationInRepo(cwd, sym); rel != "" && sig != "" {
					return formatSymbolSignatureLine(rel, lineNo, sig)
				}
			}
			if qual != "" && len(sym) >= 5 && !isCommonTrivialMethod(sym) {
				if rel, lineNo, sig := findTSMethodByNameInRepo(cwd, sym, true); rel != "" && sig != "" {
					return formatSymbolSignatureLine(rel, lineNo, sig)
				}
			}
			if fallbackSym == "" && len(sym) >= 4 && isNearMissMarker(m) {
				fallbackSym = sym
				fallbackMod = extractModuleHintFromErrorLine(trimmed)
			}
		}
	}
	if fallbackSym == "" {
		if line := callErrorSourceSignatureLine(cwd, output); line != "" {
			return line
		}
	}
	if fallbackSym != "" {
		return nearMissSymbolSignatureLine(cwd, fallbackSym, fallbackMod)
	}
	return ""
}

func formatSymbolSignatureLine(rel string, lineNo int, sig string) string {
	return fmt.Sprintf("symbol signature in repo — %s:%d: %s",
		search.SafePath(rel), lineNo, clipDecision(search.SafeText(sig), 160))
}

func lastDotIdent(prefix string) string {
	if dot := strings.LastIndexByte(prefix, '.'); dot >= 0 && dot+1 < len(prefix) {
		prefix = prefix[dot+1:]
	}
	return leadingIdent(strings.TrimSpace(prefix))
}

func findQualifiedMemberSignatureInRepo(cwd, qualifier, member string) (string, int, string) {
	if policy.SourceHintsOff() || cwd == "" || len(qualifier) < 2 || len(member) < 2 {
		return "", 0, ""
	}
	for _, isMod := range []bool{false, true} {
		_, _, members := collectTypeMembersInRepo(cwd, qualifier, isMod)
		for _, m := range members {
			if m.name == member && m.sig != "" {
				return m.rel, m.lineNo, m.sig
			}
		}
	}
	return "", 0, ""
}

func findTSMethodByNameInRepo(cwd, member string, excludeTestFiles bool) (string, int, string) {
	if policy.SourceHintsOff() {
		return "", 0, ""
	}
	scanRoot := safeScanRoot(cwd)
	if scanRoot == "" || len(member) < 4 {
		return "", 0, ""
	}
	type tsScanFile struct {
		abs string
		rel string
	}
	var files []tsScanFile
	_ = filepath.WalkDir(scanRoot, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // skip unreadable paths during symbol lookup
		}
		if d.IsDir() {
			name := d.Name()
			if p != scanRoot && (strings.HasPrefix(name, ".") || isSkippedScanDir(name) || name == "target") {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= 300 {
			return filepath.SkipAll
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		switch ext {
		case ".ts", ".tsx", ".js", ".jsx", ".mjs":
		default:
			return nil
		}
		rel, rErr := filepath.Rel(scanRoot, p)
		if rErr != nil || strings.HasPrefix(rel, "..") {
			return nil //nolint:nilerr // ignore paths outside scanRoot
		}
		rel = filepath.ToSlash(rel)
		if excludeTestFiles && isTestFilePath(rel) {
			return nil
		}
		files = append(files, tsScanFile{abs: p, rel: rel})
		return nil
	})
	for _, f := range files {
		raw, err := os.ReadFile(f.abs)
		if err != nil || len(raw) > 256<<10 {
			continue
		}
		content := string(raw)
		if !strings.Contains(content, member) {
			continue
		}
		_, _, ms := parseTSTypeMembers(f.rel, "*", content)
		for _, m := range ms {
			if m.name == member && m.sig != "" {
				return m.rel, m.lineNo, m.sig
			}
		}
	}
	return "", 0, ""
}

type callCandidate struct {
	qualifier string
	name      string
}

func resolveCallFromSourceWindow(scanRoot string, lines []string, lineNo int, excludeTestFiles bool) (string, int, string) {
	if scanRoot == "" || lineNo < 1 || lineNo > len(lines) {
		return "", 0, ""
	}
	low := max(0, lineNo-6)
	for idx := lineNo - 1; idx >= low; idx-- {
		trimmed := strings.TrimSpace(lines[idx])
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if idx < lineNo-1 && (testHeaderName(trimmed) != "" || startsWithDeclKeyword(trimmed)) {
			break
		}
		for _, cand := range extractCallCandidates(lines[idx]) {
			if rel, declLine, sig := resolveCallCandidateInRepo(scanRoot, cand, excludeTestFiles); rel != "" && sig != "" {
				return rel, declLine, sig
			}
		}
	}
	return "", 0, ""
}

func resolveCallCandidateInRepo(cwd string, cand callCandidate, excludeTestFiles bool) (string, int, string) {
	if cand.qualifier != "" {
		if rel, lineNo, sig := findQualifiedMemberSignatureInRepo(cwd, cand.qualifier, cand.name); rel != "" && sig != "" {
			if !excludeTestFiles || !isTestFilePath(rel) {
				return rel, lineNo, sig
			}
		}
	}
	if len(cand.name) >= 4 {
		if rel, lineNo, sig := findSymbolDeclarationInRepo(cwd, cand.name); rel != "" && sig != "" {
			if !excludeTestFiles || !isTestFilePath(rel) {
				return rel, lineNo, sig
			}
		}
	}
	if cand.qualifier != "" && len(cand.name) >= 5 && !isCommonTrivialMethod(cand.name) {
		if rel, lineNo, sig := findTSMethodByNameInRepo(cwd, cand.name, excludeTestFiles); rel != "" && sig != "" {
			return rel, lineNo, sig
		}
	}
	return "", 0, ""
}

func isIdentContByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func isIdentStartByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func readIdentBackward(s string, end int) (string, int) {
	j := end
	for j > 0 && isIdentContByte(s[j-1]) {
		j--
	}
	if j == end || !isIdentStartByte(s[j]) {
		return "", end
	}
	return s[j:end], j
}

func isSkippedCallName(qual, name string) bool {
	switch strings.ToLower(qual) {
	case "t", "b", "assert", "require", "pytest", "expect", "fmt", "strings", "strconv", "errors", "os", "filepath", "json", "console", "math":
		return true
	}
	switch name {
	case "if", "for", "while", "switch", "match", "catch", "return", "sizeof", "typeof",
		"len", "cap", "make", "new", "append", "copy", "delete", "close", "panic", "recover",
		"print", "println", "printf", "format", "assert", "assert_eq", "assert_ne",
		"expect", "toEqual", "toBe", "toThrow", "isinstance", "range", "list", "dict", "set", "str", "int", "float", "bool", "super", "Super":
		return true
	}
	return false
}

func isCommonTrivialMethod(name string) bool {
	switch strings.ToLower(name) {
	case "apply", "reset", "close", "clear", "parse", "write", "fetch", "build", "render", "value", "clone", "start", "check":
		return true
	default:
		return false
	}
}

func extractCallCandidates(line string) []callCandidate {
	var out []callCandidate
	seen := map[string]bool{}
	var quote byte
	escaped := false
	for i := range len(line) {
		c := line[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' && quote != '`' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '/' && i+1 < len(line) && line[i+1] == '/' {
			break
		}
		if c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			break
		}
		if c != '(' {
			continue
		}
		end := i
		for end > 0 && (line[end-1] == ' ' || line[end-1] == '\t') {
			end--
		}
		if end > 0 && line[end-1] == '>' {
			if lt := strings.LastIndexByte(line[:end-1], '<'); lt > 0 && end-lt <= 64 && !strings.ContainsAny(line[lt:end], ";{}") {
				end = lt
				if end >= 2 && line[end-2] == ':' && line[end-1] == ':' {
					end -= 2
				}
			}
		}
		name, nameStart := readIdentBackward(line, end)
		if len(name) < 3 {
			continue
		}
		var qual string
		if nameStart >= 2 && line[nameStart-2] == ':' && line[nameStart-1] == ':' {
			qual, _ = readIdentBackward(line, nameStart-2)
		} else if nameStart >= 1 && line[nameStart-1] == '.' {
			qual, _ = readIdentBackward(line, nameStart-1)
		}
		if qual == "this" || qual == "self" || qual == "cls" {
			qual = ""
		}
		if isSkippedCallName(qual, name) {
			continue
		}
		key := qual + "." + name
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, callCandidate{qualifier: qual, name: name})
	}
	return out
}

func looksLikeCallError(output string) bool {
	if strings.Contains(output, "error TS2554") || strings.Contains(output, "error TS2555") ||
		strings.Contains(output, "error TS2345") || strings.Contains(output, "error TS2349") ||
		strings.Contains(output, "error TS2769") ||
		strings.Contains(output, "error[E0061]") || strings.Contains(output, "error[E0308]") ||
		strings.Contains(output, "TypeError:") ||
		strings.Contains(output, "not enough arguments") || strings.Contains(output, "too many arguments") ||
		strings.Contains(output, "too few arguments") || strings.Contains(output, "Missing positional argument") ||
		strings.Contains(output, "Unexpected keyword argument") || strings.Contains(output, "[call-arg]") ||
		strings.Contains(output, "this function takes ") || strings.Contains(output, "this method takes ") ||
		strings.Contains(output, "mismatched types") || strings.Contains(output, "in argument to ") ||
		strings.Contains(output, "positional argument") {
		return true
	}
	return strings.Contains(output, "Expected ") && strings.Contains(output, "argument")
}

func extractCallSiteTargets(output string) []failingTestTarget {
	var targets []failingTestTarget
	seen := map[string]bool{}
	add := func(filePart string, lineNo int) {
		if filePart == "" || lineNo <= 0 || !isCodeExtForSymbolScan(strings.ToLower(filepath.Ext(filePart))) {
			return
		}
		key := fmt.Sprintf("%s:%d", filePart, lineNo)
		if !seen[key] {
			seen[key] = true
			targets = append(targets, failingTestTarget{filePart: filePart, lineNo: lineNo})
		}
	}
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(trimmed, `File "`); ok {
			if qIdx := strings.IndexByte(rest, '"'); qIdx > 0 {
				filePart := rest[:qIdx]
				if !strings.HasPrefix(filePart, "<") && !strings.Contains(filePart, "site-packages") &&
					!strings.Contains(filePart, "/venv/") && !strings.Contains(filePart, "/.venv/") &&
					!strings.Contains(filePart, "/lib/python") {
					if _, afterLine, ok := strings.Cut(rest[qIdx+1:], ", line "); ok {
						numStr := strings.TrimSpace(afterLine)
						if comma := strings.IndexAny(numStr, ", "); comma >= 0 {
							numStr = numStr[:comma]
						}
						if n, err := strconv.Atoi(numStr); err == nil {
							add(filePart, n)
						}
					}
				}
			}
		}
		for tok := range strings.FieldsSeq(trimmed) {
			tok = strings.TrimRight(strings.Trim(tok, "\"'`[],;!?{}<>❯"), ":")
			if tok == "" {
				continue
			}
			if strings.HasSuffix(tok, ")") {
				if lp := strings.LastIndexByte(tok, '('); lp > 0 {
					filePart := tok[:lp]
					inside := tok[lp+1 : len(tok)-1]
					if comma := strings.IndexByte(inside, ','); comma >= 0 {
						inside = inside[:comma]
					}
					if n, err := strconv.Atoi(strings.TrimSpace(inside)); err == nil {
						add(filePart, n)
						continue
					}
				}
			}
			if colon1 := strings.IndexByte(tok, ':'); colon1 > 0 {
				filePart := tok[:colon1]
				rest := tok[colon1+1:]
				if colon2 := strings.IndexByte(rest, ':'); colon2 >= 0 {
					rest = rest[:colon2]
				}
				if n, err := strconv.Atoi(rest); err == nil {
					add(filePart, n)
				}
			}
		}
	}
	return targets
}

func callErrorSourceSignatureLine(cwd, output string) string {
	if policy.SourceHintsOff() || !looksLikeCallError(output) {
		return ""
	}
	targets := extractCallSiteTargets(output)
	if len(targets) == 0 {
		return ""
	}
	scanRoot := safeScanRoot(cwd)
	if scanRoot == "" {
		return ""
	}
	tree := orientRepoTree(scanRoot)
	for _, target := range targets {
		diskPath, _ := resolveSourceTargetPath(scanRoot, tree, target.filePart)
		raw, err := os.ReadFile(diskPath)
		if err != nil || len(raw) > 512<<10 {
			continue
		}
		lines := strings.Split(string(raw), "\n")
		if rel, declLine, sig := resolveCallFromSourceWindow(scanRoot, lines, target.lineNo, true); rel != "" && sig != "" {
			return formatSymbolSignatureLine(rel, declLine, sig)
		}
	}
	return ""
}

func isNearMissMarker(marker string) bool {
	switch marker {
	case "undefined: ",
		"has no field or method ",
		"cannot import name '",
		"NameError: name '",
		"Cannot find name '",
		"has no exported member '",
		"cannot find function `",
		"cannot find value `",
		"cannot find struct `":
		return true
	default:
		return false
	}
}

func extractModuleHintFromErrorLine(line string) string {
	for _, pat := range []string{" from '", " from \"", "Module '\"", "Module \"'"} {
		_, after, ok := strings.Cut(line, pat)
		if !ok {
			continue
		}
		if end := strings.IndexAny(after, "'\""); end > 0 {
			mod := strings.TrimSpace(after[:end])
			mod = strings.TrimPrefix(mod, "./")
			mod = strings.TrimPrefix(mod, "../")
			mod = strings.TrimPrefix(mod, "@/")
			return strings.ReplaceAll(mod, ".", "/")
		}
	}
	return ""
}

func attributeErrorSignatureLine(cwd, output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		typeName, attr, isModule, ok := parseAttributeErrorLine(trimmed)
		if !ok {
			continue
		}
		if hint := lookupTypeMemberHint(cwd, typeName, attr, isModule); hint != "" {
			return hint
		}
	}
	return ""
}

func parseAttributeErrorLine(line string) (typeName, attr string, isModule, ok bool) {
	if _, after, found := strings.Cut(line, "AttributeError: module '"); found {
		if mod, rest, foundAttr := strings.Cut(after, "' has no attribute '"); foundAttr {
			name, _, _ := strings.Cut(rest, "'")
			attr = leadingIdent(strings.TrimSpace(name))
			if mod != "" && len(attr) >= 2 {
				return strings.TrimSpace(mod), attr, true, true
			}
		}
	}
	if _, after, found := strings.Cut(line, "AttributeError: '"); found {
		if typ, rest, foundAttr := strings.Cut(after, "' object has no attribute '"); foundAttr {
			name, _, _ := strings.Cut(rest, "'")
			if dot := strings.LastIndexByte(typ, '.'); dot >= 0 && dot+1 < len(typ) {
				typ = typ[dot+1:]
			}
			typeName = leadingIdent(strings.TrimSpace(typ))
			attr = leadingIdent(strings.TrimSpace(name))
			if len(typeName) >= 2 && len(attr) >= 2 {
				return typeName, attr, false, true
			}
		}
	}
	if _, after, found := strings.Cut(line, "Property '"); found {
		if prop, rest, foundType := strings.Cut(after, "' does not exist on type '"); foundType {
			typ, _, _ := strings.Cut(rest, "'")
			typeName = cleanTSTypeIdent(typ)
			attr = leadingIdent(strings.TrimSpace(prop))
			if len(typeName) >= 2 && len(attr) >= 2 {
				return typeName, attr, false, true
			}
		}
	}
	if strings.Contains(line, "Object literal may only specify known properties") {
		if idx := strings.Index(line, "' does not exist in type '"); idx > 0 {
			before := line[:idx]
			if q := strings.LastIndexByte(before, '\''); q >= 0 && q+1 < len(before) {
				prop := before[q+1:]
				rest := line[idx+len("' does not exist in type '"):]
				typ, _, _ := strings.Cut(rest, "'")
				typeName = cleanTSTypeIdent(typ)
				attr = leadingIdent(strings.TrimSpace(prop))
				if len(typeName) >= 2 && len(attr) >= 2 {
					return typeName, attr, false, true
				}
			}
		}
	}
	if _, after, found := strings.Cut(line, "no method named `"); found {
		if method, rest, foundFor := strings.Cut(after, "` found for "); foundFor {
			if _, inTick, hasTick := strings.Cut(rest, "`"); hasTick {
				typ, _, _ := strings.Cut(inTick, "`")
				typeName = cleanRustTypeIdent(typ)
				attr = leadingIdent(strings.TrimSpace(method))
				if len(typeName) >= 2 && len(attr) >= 2 {
					return typeName, attr, false, true
				}
			}
		}
	}
	if _, after, found := strings.Cut(line, "struct `"); found {
		if typPart, rest, hasField := strings.Cut(after, "` has no field named `"); hasField {
			fieldName, _, _ := strings.Cut(rest, "`")
			typeName = cleanRustTypeIdent(typPart)
			attr = leadingIdent(strings.TrimSpace(fieldName))
			if len(typeName) >= 2 && len(attr) >= 2 {
				return typeName, attr, false, true
			}
		}
	}
	if _, after, found := strings.Cut(line, "no field `"); found {
		if fieldPart, rest, hasType := strings.Cut(after, "` on type `"); hasType {
			typPart, _, _ := strings.Cut(rest, "`")
			typeName = cleanRustTypeIdent(typPart)
			attr = leadingIdent(strings.TrimSpace(fieldPart))
			if len(typeName) >= 2 && len(attr) >= 2 {
				return typeName, attr, false, true
			}
		}
	}
	if before, after, found := strings.Cut(line, " has no field or method "); found {
		attr = leadingIdent(strings.TrimSpace(after))
		if idx := strings.LastIndex(before, "type "); idx >= 0 {
			typeName = cleanGoTypeIdent(before[idx+len("type "):])
			if len(typeName) >= 2 && len(attr) >= 2 {
				return typeName, attr, false, true
			}
		}
	}
	if _, after, found := strings.Cut(line, "unknown field "); found {
		if fieldPart, rest, hasType := strings.Cut(after, " in struct literal of type "); hasType {
			attr = leadingIdent(strings.Trim(strings.TrimSpace(fieldPart), "'\"`"))
			typeName = cleanGoTypeIdent(rest)
			if len(typeName) >= 2 && len(attr) >= 2 {
				return typeName, attr, false, true
			}
		}
	}
	return "", "", false, false
}

func cleanTSTypeIdent(typ string) string {
	typ = strings.TrimSpace(typ)
	typ = strings.TrimPrefix(typ, "typeof ")
	typ = strings.TrimPrefix(typ, "readonly ")
	if idx := strings.IndexAny(typ, "<|["); idx > 0 {
		typ = typ[:idx]
	}
	return leadingIdent(strings.TrimSpace(typ))
}

func cleanRustTypeIdent(typ string) string {
	typ = strings.TrimLeft(strings.TrimSpace(typ), "&")
	typ = strings.TrimPrefix(typ, "mut ")
	if idx := strings.IndexAny(typ, "< \t"); idx > 0 {
		typ = typ[:idx]
	}
	if sep := strings.LastIndex(typ, "::"); sep >= 0 && sep+2 < len(typ) {
		typ = typ[sep+2:]
	}
	return leadingIdent(strings.TrimSpace(typ))
}

func cleanGoTypeIdent(typ string) string {
	typ = strings.TrimLeft(strings.TrimSpace(typ), "*&")
	if end := strings.IndexAny(typ, " \t()[{,;:"); end > 0 {
		typ = typ[:end]
	}
	if dot := strings.LastIndexByte(typ, '.'); dot >= 0 && dot+1 < len(typ) {
		typ = typ[dot+1:]
	}
	return leadingIdent(typ)
}

type repoMemberDecl struct {
	name   string
	rel    string
	lineNo int
	sig    string
}

func lookupTypeMemberHint(cwd, typeName, missingAttr string, isModule bool) string {
	defRel, defLine, members := collectTypeMembersInRepo(cwd, typeName, isModule)
	if len(members) == 0 {
		return ""
	}
	bestIdx := -1
	bestScore := 1 << 20
	for i, m := range members {
		score, ok := nearMissScore(missingAttr, m.name)
		if !ok {
			continue
		}
		if score < bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	var names []string
	seen := map[string]bool{}
	if bestIdx >= 0 {
		names = append(names, members[bestIdx].name)
		seen[members[bestIdx].name] = true
	}
	for _, passDunder := range []bool{false, true} {
		for _, m := range members {
			if seen[m.name] {
				continue
			}
			isDunder := strings.HasPrefix(m.name, "__") && strings.HasSuffix(m.name, "__")
			if isDunder != passDunder {
				continue
			}
			seen[m.name] = true
			names = append(names, m.name)
			if len(names) >= 5 {
				break
			}
		}
		if len(names) >= 5 {
			break
		}
	}
	if bestIdx >= 0 {
		best := members[bestIdx]
		return fmt.Sprintf("attribute hint for %s — %s.%s (%s:%d: %s); members: %s",
			search.SafeLine(typeName), search.SafeLine(typeName), search.SafeLine(best.name),
			search.SafePath(best.rel), best.lineNo, clipDecision(search.SafeText(best.sig), 120),
			search.SafeLine(strings.Join(names, ", ")))
	}
	if defRel == "" {
		defRel = members[0].rel
		defLine = members[0].lineNo
	}
	return fmt.Sprintf("attributes on %s (%s:%d) — members: %s (e.g. %s)",
		search.SafeLine(typeName), search.SafePath(defRel), defLine,
		search.SafeLine(strings.Join(names, ", ")), clipDecision(search.SafeText(members[0].sig), 100))
}

func collectTypeMembersInRepo(cwd, typeName string, isModule bool) (defRel string, defLine int, members []repoMemberDecl) {
	if policy.SourceHintsOff() {
		return "", 0, nil
	}
	scanRoot := safeScanRoot(cwd)
	if scanRoot == "" {
		return "", 0, nil
	}
	modPath := strings.ReplaceAll(typeName, ".", "/")
	filesScanned := 0
	var paths []string
	_ = filepath.WalkDir(scanRoot, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // skip unreadable paths during symbol lookup
		}
		if d.IsDir() {
			name := d.Name()
			if p != scanRoot && (strings.HasPrefix(name, ".") || isSkippedScanDir(name) || name == "__pycache__" || name == ".venv" || name == "target") {
				return filepath.SkipDir
			}
			return nil
		}
		if filesScanned >= 300 {
			return filepath.SkipAll
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if !isCodeExtForSymbolScan(ext) {
			return nil
		}
		rel, rErr := filepath.Rel(scanRoot, p)
		if rErr != nil || strings.HasPrefix(rel, "..") {
			return nil //nolint:nilerr // ignore paths outside scanRoot
		}
		rel = filepath.ToSlash(rel)
		if isTestFilePath(rel) {
			return nil
		}
		if isModule && ext != ".py" {
			return nil
		}
		filesScanned++
		paths = append(paths, rel)
		return nil
	})
	for _, rel := range paths {
		ext := strings.ToLower(filepath.Ext(rel))
		if isModule && !matchesPythonModuleRel(rel, modPath) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(scanRoot, filepath.FromSlash(rel)))
		if err != nil || len(raw) > 256<<10 {
			continue
		}
		if isModule {
			dr, dl, ms := parsePythonModuleMembers(rel, string(raw))
			if len(ms) > 0 {
				return dr, dl, ms
			}
			continue
		}
		switch ext {
		case ".py":
			dr, dl, ms := parsePythonClassMembers(rel, typeName, string(raw))
			if dr != "" && defRel == "" {
				defRel, defLine = dr, dl
			}
			members = append(members, ms...)
		case ".go":
			dr, dl, ms := parseGoTypeMembers(rel, typeName, string(raw))
			if dr != "" && defRel == "" {
				defRel, defLine = dr, dl
			}
			members = append(members, ms...)
		case ".ts", ".tsx", ".js", ".jsx", ".mjs":
			dr, dl, ms := parseTSTypeMembers(rel, typeName, string(raw))
			if dr != "" && defRel == "" {
				defRel, defLine = dr, dl
			}
			members = append(members, ms...)
		case ".rs":
			dr, dl, ms := parseRustTypeMembers(rel, typeName, string(raw))
			if dr != "" && defRel == "" {
				defRel, defLine = dr, dl
			}
			members = append(members, ms...)
		}
	}
	return defRel, defLine, members
}

func isCodeExtForSymbolScan(ext string) bool {
	switch ext {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".rs":
		return true
	default:
		return false
	}
}

func matchesPythonModuleRel(rel, modPath string) bool {
	noExt := strings.TrimSuffix(rel, ".py")
	if noExt == modPath || strings.HasSuffix(noExt, "/"+modPath) {
		return true
	}
	if noExt == modPath+"/__init__" || strings.HasSuffix(noExt, "/"+modPath+"/__init__") {
		return true
	}
	if idx := strings.LastIndexByte(modPath, '/'); idx >= 0 {
		base := modPath[idx+1:]
		return filepath.Base(noExt) == base
	}
	return false
}

func parsePythonModuleMembers(rel, content string) (string, int, []repoMemberDecl) {
	var members []repoMemberDecl
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		lineNo := i + 1
		if line == "" || line[0] == ' ' || line[0] == '\t' || strings.HasPrefix(line, "#") {
			continue
		}
		collapsed, endIdx := collapseMemberLineWindow(lines, i)
		if name, sig := parseDeclarationLine(collapsed); name != "" {
			members = append(members, repoMemberDecl{name: name, rel: rel, lineNo: lineNo, sig: cleanMemberSig(sig)})
			i = endIdx
			continue
		}
		if id, rest, ok := strings.Cut(collapsed, "="); ok {
			name := leadingIdent(strings.TrimSpace(id))
			if name != "" && name == strings.TrimSpace(id) && !strings.HasPrefix(rest, "=") {
				members = append(members, repoMemberDecl{name: name, rel: rel, lineNo: lineNo, sig: cleanMemberSig(collapsed)})
				i = endIdx
			}
		}
	}
	return rel, 1, members
}

func parsePythonClassMembers(rel, typeName, content string) (defRel string, defLine int, members []repoMemberDecl) {
	inClass := false
	baseIndent := 0
	memberIndent := -1
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := leadingWhitespaceCount(line)
		if inClass && indent <= baseIndent {
			inClass = false
			memberIndent = -1
		}
		if !inClass {
			if matchDeclKeywordTarget(trimmed, "class", typeName) {
				inClass = true
				baseIndent = indent
				defRel = rel
				defLine = lineNo
			}
			continue
		}
		if memberIndent < 0 {
			memberIndent = indent
		}
		if indent != memberIndent {
			continue
		}
		collapsed, endIdx := collapseMemberLineWindow(lines, i)
		if name, sig := parseDeclarationLine(collapsed); name != "" {
			members = append(members, repoMemberDecl{name: name, rel: rel, lineNo: lineNo, sig: cleanMemberSig(sig)})
			i = endIdx
			continue
		}
		if left, ok := cutClassFieldAssign(collapsed); ok {
			members = append(members, repoMemberDecl{name: left, rel: rel, lineNo: lineNo, sig: cleanMemberSig(collapsed)})
		}
		if endIdx > i {
			i = endIdx
		}
	}
	return defRel, defLine, members
}

func cutClassFieldAssign(trimmed string) (string, bool) {
	if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async ") || strings.HasPrefix(trimmed, "@") {
		return "", false
	}
	for _, sep := range []string{":", "="} {
		if left, _, ok := strings.Cut(trimmed, sep); ok {
			cand := strings.TrimSpace(left)
			id := leadingIdent(cand)
			if id != "" && id == cand {
				return id, true
			}
		}
	}
	return "", false
}

func parseGoTypeMembers(rel, typeName, content string) (defRel string, defLine int, members []repoMemberDecl) {
	inStruct := false
	depth := 0
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if !inStruct && matchDeclKeywordTarget(trimmed, "type", typeName) && strings.Contains(trimmed, "struct") {
			defRel = rel
			defLine = lineNo
			if strings.Contains(trimmed, "{") && !strings.Contains(trimmed, "}") {
				inStruct = true
				depth = braceDelta(trimmed)
			}
			continue
		}
		if inStruct {
			if depth == 1 && !strings.HasPrefix(trimmed, "}") {
				field := leadingIdent(trimmed)
				if field != "" {
					members = append(members, repoMemberDecl{name: field, rel: rel, lineNo: lineNo, sig: trimmed})
				}
			}
			depth += braceDelta(trimmed)
			if depth <= 0 {
				inStruct = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "func (") || strings.HasPrefix(trimmed, "func(") {
			collapsed, endIdx := collapseMemberLineWindow(lines, i)
			if method := parseGoReceiverMethod(collapsed, typeName); method != "" {
				members = append(members, repoMemberDecl{name: method, rel: rel, lineNo: lineNo, sig: cleanMemberSig(collapsed)})
			}
			if endIdx > i {
				i = endIdx
			}
		}
	}
	return defRel, defLine, members
}

func parseGoReceiverMethod(trimmed, typeName string) string {
	if !strings.HasPrefix(trimmed, "func (") && !strings.HasPrefix(trimmed, "func(") {
		return ""
	}
	_, afterOpen, ok := strings.Cut(trimmed, "(")
	if !ok {
		return ""
	}
	recv, afterClose, ok := strings.Cut(afterOpen, ")")
	if !ok {
		return ""
	}
	fields := strings.Fields(recv)
	if len(fields) == 0 {
		return ""
	}
	recvType := strings.TrimLeft(fields[len(fields)-1], "*")
	if idx := strings.IndexByte(recvType, '['); idx > 0 {
		recvType = recvType[:idx]
	}
	if recvType != typeName {
		return ""
	}
	return leadingIdent(strings.TrimSpace(afterClose))
}

func parseTSTypeMembers(rel, typeName, content string) (defRel string, defLine int, members []repoMemberDecl) {
	inBlock := false
	depth := 0
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if !inBlock {
			clean := trimmed
			clean = strings.TrimPrefix(clean, "export ")
			clean = strings.TrimPrefix(clean, "default ")
			clean = strings.TrimPrefix(clean, "abstract ")
			clean = strings.TrimPrefix(clean, "declare ")
			if matchDeclKeywordTarget(clean, "class", typeName) || matchDeclKeywordTarget(clean, "interface", typeName) || matchDeclKeywordTarget(clean, "type", typeName) {
				defRel = rel
				defLine = lineNo
				if strings.Contains(trimmed, "{") {
					d := braceDelta(trimmed)
					if d > 0 {
						inBlock = true
						depth = d
					}
				}
			}
			continue
		}
		endIdx := i
		if depth == 1 && !strings.HasPrefix(trimmed, "}") {
			var collapsed string
			collapsed, endIdx = collapseMemberLineWindow(lines, i)
			if member := parseTSMemberName(collapsed); member != "" {
				members = append(members, repoMemberDecl{name: member, rel: rel, lineNo: lineNo, sig: cleanMemberSig(collapsed)})
			}
		}
		for k := i; k <= endIdx; k++ {
			depth += braceDelta(lines[k])
		}
		i = endIdx
		if depth <= 0 {
			inBlock = false
		}
	}
	return defRel, defLine, members
}

func parseTSMemberName(trimmed string) string {
	s := trimmed
	for {
		moved := false
		for _, mod := range []string{"public ", "private ", "protected ", "readonly ", "static ", "async ", "override ", "get ", "set "} {
			if strings.HasPrefix(s, mod) {
				s = strings.TrimSpace(s[len(mod):])
				moved = true
			}
		}
		if !moved {
			break
		}
	}
	id := leadingIdent(s)
	switch id {
	case "", "constructor", "if", "for", "while", "switch", "return", "throw", "try", "catch":
		return ""
	}
	rest := strings.TrimSpace(s[len(id):])
	if strings.HasPrefix(rest, "(") || strings.HasPrefix(rest, "<") || strings.HasPrefix(rest, ": ") || strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "?:") || strings.HasPrefix(rest, "?(") || strings.HasPrefix(rest, "?<") || strings.HasPrefix(rest, "!") || strings.HasPrefix(rest, "=") {
		return id
	}
	return ""
}

func parseRustTypeMembers(rel, typeName, content string) (defRel string, defLine int, members []repoMemberDecl) {
	inBlock := false
	isImpl := false
	depth := 0
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#[") {
			continue
		}
		if !inBlock {
			clean := strings.TrimPrefix(trimmed, "pub ")
			clean = strings.TrimSpace(strings.TrimPrefix(clean, "pub(crate) "))
			matched := false
			if matchDeclKeywordTarget(clean, "struct", typeName) || matchDeclKeywordTarget(clean, "enum", typeName) {
				matched = true
				isImpl = false
				if defRel == "" {
					defRel = rel
					defLine = lineNo
				}
			} else if matchRustImplTarget(clean, typeName) {
				matched = true
				isImpl = true
				if defRel == "" {
					defRel = rel
					defLine = lineNo
				}
			}
			if matched && strings.Contains(trimmed, "{") {
				d := braceDelta(trimmed)
				if d > 0 {
					inBlock = true
					depth = d
				}
			}
			continue
		}
		endIdx := i
		if depth == 1 && !strings.HasPrefix(trimmed, "}") {
			var collapsed string
			collapsed, endIdx = collapseMemberLineWindow(lines, i)
			if isImpl {
				if name, sig := parseDeclarationLine(collapsed); name != "" {
					members = append(members, repoMemberDecl{name: name, rel: rel, lineNo: lineNo, sig: cleanMemberSig(sig)})
				}
			} else {
				cleanField := strings.TrimPrefix(collapsed, "pub ")
				cleanField = strings.TrimSpace(strings.TrimPrefix(cleanField, "pub(crate) "))
				if field := leadingIdent(cleanField); field != "" && strings.Contains(cleanField, ":") {
					members = append(members, repoMemberDecl{name: field, rel: rel, lineNo: lineNo, sig: cleanMemberSig(collapsed)})
				}
			}
		}
		for k := i; k <= endIdx; k++ {
			depth += braceDelta(lines[k])
		}
		i = endIdx
		if depth <= 0 {
			inBlock = false
		}
	}
	return defRel, defLine, members
}

func matchRustImplTarget(trimmed, typeName string) bool {
	if !strings.HasPrefix(trimmed, "impl") {
		return false
	}
	rest := trimmed[len("impl"):]
	if rest == "" || (rest[0] != ' ' && rest[0] != '\t' && rest[0] != '<') {
		return false
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "<") {
		if _, after, ok := strings.Cut(rest, ">"); ok {
			rest = strings.TrimSpace(after)
		}
	}
	if before, _, ok := strings.Cut(rest, "{"); ok {
		rest = before
	}
	if before, _, ok := strings.Cut(rest, " where "); ok {
		rest = before
	}
	rest = strings.TrimSpace(rest)
	if _, after, ok := strings.Cut(rest, " for "); ok {
		rest = strings.TrimSpace(after)
	}
	if sep := strings.LastIndex(rest, "::"); sep >= 0 && sep+2 < len(rest) {
		rest = rest[sep+2:]
	}
	if idx := strings.IndexByte(rest, '<'); idx > 0 {
		rest = rest[:idx]
	}
	return leadingIdent(rest) == typeName
}

func matchDeclKeywordTarget(trimmed, kw, typeName string) bool {
	if !strings.HasPrefix(trimmed, kw) {
		return false
	}
	rest := trimmed[len(kw):]
	if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return false
	}
	rest = strings.TrimSpace(rest)
	if typeName == "*" {
		return leadingIdent(rest) != ""
	}
	if !strings.HasPrefix(rest, typeName) {
		return false
	}
	tail := rest[len(typeName):]
	if tail == "" {
		return true
	}
	switch tail[0] {
	case ' ', '\t', '(', ':', '<', '{', '[', '=':
		return true
	default:
		return false
	}
}

func leadingWhitespaceCount(line string) int {
	n := 0
	for i := range len(line) {
		if line[i] != ' ' && line[i] != '\t' {
			break
		}
		n++
	}
	return n
}

func braceDelta(s string) int {
	d := 0
	for i := range len(s) {
		switch s[i] {
		case '{':
			d++
		case '}':
			d--
		}
	}
	return d
}

func nearMissSymbolSignatureLine(cwd, missingSym, modHint string) string {
	if policy.SourceHintsOff() {
		return ""
	}
	scanRoot := safeScanRoot(cwd)
	if scanRoot == "" || len(missingSym) < 4 {
		return ""
	}
	var paths []string
	_ = filepath.WalkDir(scanRoot, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // skip unreadable paths during near-miss lookup
		}
		if d.IsDir() {
			name := d.Name()
			if p != scanRoot && (strings.HasPrefix(name, ".") || isSkippedScanDir(name) || name == "__pycache__" || name == ".venv" || name == "target") {
				return filepath.SkipDir
			}
			return nil
		}
		if len(paths) >= 300 {
			return filepath.SkipAll
		}
		if !isCodeExtForSymbolScan(strings.ToLower(filepath.Ext(d.Name()))) {
			return nil
		}
		rel, rErr := filepath.Rel(scanRoot, p)
		if rErr != nil || strings.HasPrefix(rel, "..") {
			return nil //nolint:nilerr // ignore paths outside scanRoot
		}
		rel = filepath.ToSlash(rel)
		if isTestFilePath(rel) {
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	var best repoMemberDecl
	bestScore := 1 << 20
	for _, rel := range paths {
		raw, rErr := os.ReadFile(filepath.Join(scanRoot, filepath.FromSlash(rel)))
		if rErr != nil || len(raw) > 256<<10 {
			continue
		}
		modBonus := 0
		if modHint != "" && strings.Contains(strings.TrimSuffix(rel, filepath.Ext(rel)), modHint) {
			modBonus = 2
		}
		lines := strings.Split(string(raw), "\n")
		for i := 0; i < len(lines); i++ {
			startLine := i + 1
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
				continue
			}
			collapsed, endIdx := collapseDeclarationWindow(lines, i)
			name, sig := parseDeclarationLine(collapsed)
			if endIdx > i {
				i = endIdx
			}
			if len(name) < 4 || sig == "" {
				continue
			}
			score, ok := nearMissScore(missingSym, name)
			if !ok {
				continue
			}
			rank := score - modBonus
			if rank < bestScore {
				bestScore = rank
				best = repoMemberDecl{name: name, rel: rel, lineNo: startLine, sig: clipDeclSignature(sig)}
			}
		}
	}
	if best.name == "" {
		return ""
	}
	return fmt.Sprintf("symbol near-match in repo — %s:%d: %s",
		search.SafePath(best.rel), best.lineNo, clipDecision(search.SafeText(best.sig), 160))
}

func nearMissScore(missing, cand string) (int, bool) {
	lowM := strings.ToLower(missing)
	lowC := strings.ToLower(cand)
	if lowM == lowC {
		return 0, true
	}
	dist := levenshteinDistance(lowM, lowC)
	pref := commonPrefixLength(lowM, lowC)
	suf := commonSuffixLength(lowM, lowC)
	maxLen := max(len(lowM), len(lowC))
	minLen := min(len(lowM), len(lowC))
	ok := dist <= 2 ||
		(maxLen >= 6 && dist <= 3 && (pref >= 3 || suf >= 4)) ||
		(minLen >= 5 && float64(minLen)/float64(maxLen) >= 0.6 && (strings.Contains(lowC, lowM) || strings.Contains(lowM, lowC))) ||
		(hasSharedIdentifierWord(missing, cand) && dist <= maxLen/2)
	if !ok {
		return 0, false
	}
	score := dist*4 - pref*2 - suf
	if hasSharedIdentifierWord(missing, cand) {
		score -= 2
	}
	return score, true
}

func hasSharedIdentifierWord(a, b string) bool {
	wordsA := splitIdentifierWords(a)
	wordsB := splitIdentifierWords(b)
	for _, wa := range wordsA {
		if len(wa) < 3 {
			continue
		}
		if slices.Contains(wordsB, wa) {
			return true
		}
	}
	return false
}

// repoMemoryLineForFailedOutput inspects a failed command and its output for
// repository file references and surfaces any matching .deja/memory.jsonl
// guardrail (rejected, gotcha, or file-bound decision) that explains the trap.
func repoMemoryLineForFailedOutput(cwd, command, output string, pol policy.Policy) string {
	if cwd == "" || !pol.Allows(policy.ActivationAuto, "") || !promptCarriesErrorOrTest(output) {
		return ""
	}
	root := sources.RepoRoot(cwd)
	if root == "" {
		return ""
	}
	entries, _, err := readRepoMemoryAndNotes(root)
	if err != nil || len(entries) == 0 {
		return ""
	}
	tree := orientRepoTree(root)
	combined := command + "\n" + output
	seen := map[string]bool{}
	var citedFiles []string
	for tok := range strings.FieldsSeq(combined) {
		tok = strings.Trim(tok, "\"'`(),;!?[]{}<>")
		if idx := strings.IndexByte(tok, ':'); idx > 0 {
			tok = tok[:idx]
		}
		if tok == "" || !looksLikePromptFileRef(tok) {
			continue
		}
		rel := tok
		if resolved := tree.resolveRel(tok); resolved != "" {
			rel = resolved
		}
		if !seen[rel] {
			seen[rel] = true
			citedFiles = append(citedFiles, rel)
		}
	}
	if len(citedFiles) == 0 {
		return ""
	}
	for _, e := range slices.Backward(entries) {
		for _, cf := range citedFiles {
			if repoEntryHasFile(root, cf, e.Files) || repoEntryMentionsFile(root, cf, e.Text) {
				files := strings.Join(e.Files, ", ")
				if files == "" {
					files = cf
				}
				kind := e.Kind
				if kind == "" {
					kind = "note"
				}
				return fmt.Sprintf("in .deja/memory.jsonl (%s): [%s] %s",
					search.SafePath(files), search.SafeText(kind),
					clipDecision(search.SafeText(e.Text), toolHookDecisionBudget))
			}
		}
	}
	return ""
}

func orientSkipFixPairForHook(p index.FixPair) bool {
	cmd, ok := withoutFailedExit(strings.TrimPrefix(strings.TrimSpace(search.SafeText(p.Command)), "$ "))
	if ok && cmd != "" {
		if orientDestructiveRemedy(cmd) || !orientTestRemedyMatchesFail(p.Error, cmd) {
			return true
		}
		return false
	}
	edit := strings.TrimSpace(search.SafeLine(p.Edit))
	if edit != "" && p.Symbol == "" && p.Why == "" {
		if orientTautologicalEdit(p.Error, edit) {
			return true
		}
	}
	return false
}

func nearErrorProbesFromOutput(output string) []string {
	var probes []string
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if len(p) >= 12 && !seen[p] {
			seen[p] = true
			probes = append(probes, p)
		}
	}
	for raw := range strings.SplitSeq(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "--- FAIL:") {
			probe := line
			if idx := strings.LastIndex(probe, " ("); idx > len("--- FAIL:") {
				probe = strings.TrimSpace(probe[:idx])
			}
			add(probe)
			continue
		}
		low := strings.ToLower(line)
		if strings.Contains(low, "does not contain main module") {
			add("does not contain main module")
			continue
		}
		for _, prefix := range []string{"level=error", "error:", "fatal:", "panic:"} {
			if strings.HasPrefix(low, prefix) {
				add(line)
				break
			}
		}
		if msg := stripFileLineColDiagnostic(line); msg != "" {
			add(msg)
		}
	}
	return probes
}

func stripFileLineColDiagnostic(line string) string {
	idx := strings.Index(line, ": ")
	if idx <= 0 {
		return ""
	}
	loc, msg := line[:idx], strings.TrimSpace(line[idx+2:])
	if len(msg) < 12 || strings.ContainsAny(loc, " \t") || !strings.ContainsAny(loc, "./\\") {
		return ""
	}
	parts := strings.Split(loc, ":")
	if len(parts) < 2 {
		return ""
	}
	if _, err := strconv.Atoi(parts[len(parts)-1]); err != nil {
		return ""
	}
	return msg
}

// frictionCount is how many sessions hit this error, scoped the way the line
// that reports it is scoped: the pair names a project, so the count is that
// project's, and a machine-wide pair counts machine-wide. Same trust rule as
// the pair itself.
func frictionCount(dir string, p index.FixPair, pol policy.Policy) int {
	project := strings.TrimSpace(p.Project)
	return index.FrictionSessions(dir, p.Sig, func(proj string) bool {
		if !pol.Allows(policy.ActivationAuto, proj) {
			return false
		}
		return project == "" || proj == project || index.ProjectInScope(proj, project) || index.ProjectInScope(project, proj)
	})
}

func fixLine(p index.FixPair, sessions int) string {
	return fixLineIn(p, sessions, howCwd())
}

func fixLineIn(p index.FixPair, sessions int, cwd string) string {
	// Some harnesses store the command with the prompt they printed it after;
	// the line reads as an instruction, so it should not start with a shell
	// prompt the reader would have to mentally strip.
	cmd, ok := withoutFailedExit(strings.TrimPrefix(strings.TrimSpace(search.SafeText(p.Command)), "$ "))
	// An edit remedy (#2163) has no command: what fixed a failing test was a
	// change to a file, and the useful half is which file. `deja fix` said so;
	// the hook, the surface that fires at the failure, said nothing (#3259).
	edit := strings.TrimSpace(search.SafeLine(p.Edit))
	// codex and opencode record the exit status on the command; a remedy that
	// exited non-zero is not one, and handing it over as "what followed it"
	// tells an agent to run something that already failed here.
	if (!ok || cmd == "") && edit == "" {
		return ""
	}
	when := ""
	if !p.When.IsZero() {
		when = " (" + p.When.Local().Format("2006-01-02") + ")"
	}
	// "here" read as this project, and the lookup is machine-wide on purpose —
	// an error signature is closer to an environment fact than to project
	// history, which is what the command line and the environment block say out
	// loud. The pair carries its project, so the line can name whose command it
	// is offering rather than implying it is this checkout's (#2363).
	where := "on this machine"
	if project := strings.TrimSpace(search.SafeLine(p.Project)); project != "" {
		where = "in " + project
	}
	// How often, not just that it happened: "came up before" reads as a
	// coincidence where "came up in 12 sessions" reads as a property of this
	// machine. friction, the environment block and the plan finding all lead
	// with the count; this line, the one that arrives at the moment of the
	// failure, said only "before" (#2491).
	how := ""
	if sessions > 1 {
		how = " in " + toolSessionCount(sessions)
	}
	// A single sighting is said as one. Two sessions doing the same thing after
	// the same error is evidence it worked; one session doing something is what
	// one session did, and an agent handed it at the moment it is stuck has to
	// be told which of the two it is holding.
	if edit != "" && (!ok || cmd == "") {
		if cwd == "" {
			cwd = howCwd()
		}
		if cwd != "" {
			edit = search.SafePath(orientPath(edit, cwd))
		}
		detail := ""
		if p.Symbol != "" {
			detail += " (" + search.SafeLine(p.Symbol) + ")"
		}
		if p.Why != "" {
			detail += " — " + truncateToolLine(search.SafeLine(p.Why), 130)
		}
		// Not something to run, so not offered as one: the file, in the words
		// `deja fix` uses for the same pair.
		if p.Candidate {
			return "this error came up" + how + " " + where + " before" + when +
				" — this file was changed after it, and nothing confirms that worked: " + edit + detail
		}
		return "this error came up" + how + " " + where + " before" + when + " — changed next: " + edit + detail
	}
	if p.Candidate {
		return "this error came up" + how + " " + where + " before" + when +
			" — this was run after it, and nothing confirms it worked: " + orientCommand(cmd)
	}
	// A repaired remedy is not "what followed it", it is the command the reader
	// has just run, corrected — and this is the one surface where they already
	// know what they ran, so the line names the relationship and not the command
	// twice. Of the 360 pairs served on a real store, 107 name nothing their
	// error names for exactly this reason.
	if p.Failed != "" {
		return "this error came up" + how + " " + where + " before" + when + " — the same command worked as: " + orientCommand(cmd)
	}
	return "this error came up" + how + " " + where + " before" + when + " — what followed it: " + orientCommand(cmd)
}

// withoutFailedExit drops the recorded exit status from a command, and reports
// false when that status says the command failed.
//
// Only a suffix of that exact shape counts. Looking for the marker anywhere cut
// `echo "→ exit 0"` down to `echo "` and called it a failure, because the code
// it read was `0"` — a command that mentions deja's own marker is still just a
// command (#2048).
func withoutFailedExit(cmd string) (string, bool) {
	rest, code, recorded := index.CommandExitOutcome(cmd)
	if !recorded {
		return cmd, true
	}
	return rest, code == 0
}
