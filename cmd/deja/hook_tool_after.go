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
		Command       string          `json:"command"`
		CommandLine   string          `json:"CommandLine"`
		CmdLine       string          `json:"command_line"`
		Cmd           string          `json:"cmd"`
		Script        string          `json:"script"`
		Args          any             `json:"args"`
		ToolArgs      json.RawMessage `json:"toolArgs"`
		ToolArgsSnake json.RawMessage `json:"tool_args"`
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
	// Cursor's postToolUseFailure names the failure here (2026.09.02).
	ErrorMessage json.RawMessage `json:"error_message"`
	SessionID    string          `json:"session_id"`
	CWD          string          `json:"cwd"`
	// Grok spells the rest in camelCase and puts the result under
	// toolResult (#4499). See hook_grok.go.
	grokEnvelope
	ToolResult    json.RawMessage `json:"toolResult"`
	ToolArgs      json.RawMessage `json:"toolArgs"`
	ToolArgsSnake json.RawMessage `json:"tool_args"`
}

func (i *toolAfterInput) adopt() {
	i.ToolName = adoptGrok(adoptGrok(i.ToolName, i.grokEnvelope.ToolName), i.LLMToolName)
	i.SessionID = adoptGrok(i.SessionID, i.grokEnvelope.SessionID)

	cmd := firstNonEmpty(
		i.ToolInput.Command,
		i.ToolInput.CommandLine,
		i.ToolInput.CmdLine,
		i.ToolInput.Cmd,
		i.ToolInput.Script,
	)
	for _, rawArgs := range []json.RawMessage{i.ToolInput.ToolArgs, i.ToolInput.ToolArgsSnake, i.ToolArgs, i.ToolArgsSnake} {
		if args := copilotToolArgs(rawArgs); args != nil {
			if cmd == "" {
				cmd = mapString(args, "command", "CommandLine", "command_line", "cmd", "script")
			}
			if i.ToolInput.Args == nil && args["args"] != nil {
				i.ToolInput.Args = args["args"]
			}
		}
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
		requestWarmup(dir)
		return nil
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
		out = sources.UnwrapShellReport(salvageFromPayload(string(raw)))
	}
	if out == "" {
		return nil
	}
	// Out of the raw bytes for a cut payload, the way the tool name is: the
	// line keeps to the project it names, and the harness sends cwd ahead of
	// the output.
	cwd := input.CWD
	if cwd == "" && truncated {
		cwd, _ = jsonStringAfter(after(string(raw), `"cwd"`))
	}
	line := fixPairLineFor(dir, hookCWD(cwd), input.ToolInput.Command, out)
	if line == "" {
		return nil
	}
	token := "fix:" + shortHash(line)
	if alreadyInjected(dir, input.SessionID)[token] {
		return nil
	}
	rememberInjectedIDs(dir, input.SessionID, token)
	payload := frameRecall(truncateToolLine(line, toolAfterMaxBytes))
	// The receiving session too: this is the repair delivered at the failure,
	// the surface with the best evidence behind it, and it was the one that
	// could not be followed (#1494 gave the per-prompt hook the same field).
	// The text too, for the reason the pre-tool hook keeps it: a fix pair
	// delivered at a failure is the one injection worth reading back.
	usage.RecordDigestInto(dir, usage.KindTool, payload, input.SessionID, 1, 0, nil)
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

// isCommandTool reports whether this tool runs a command. File edits fail in
// ways a shell error signature does not describe, and the pre-tool hook already
// speaks for them.
func isCommandTool(name string) bool {
	switch name {
	case "Bash", "bash", "shell", "Shell", "run_command", "execute_command", "terminal",
		// Claude Code's shell on Windows (#4489), and the pi-coding-agent's
		// under Senpi and Kimchi (#4523).
		"PowerShell", "powershell",
		"run_shell_command", "run_terminal_command", "run_commands",
		// Command Code's payload carries the internal name, not the SHELL its
		// matcher sees (#4371); that matcher is a case-insensitive regex, so it
		// fires on powershell too (#4540).
		"shell_command",
		// Kiro's shell tool.
		"execute_bash",
		// VS Code Copilot Chat's terminal tool; its result ends with
		// "Command exited with code N" when the command failed.
		"run_in_terminal",
		// Devin's shell tool.
		"exec":
		return true
	}
	return false
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
		return clampOutput(sources.UnwrapShellReport(s))
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	var b strings.Builder
	// stderr first: the signature is far more often there, and the scan is
	// capped, so a long stdout must not push it out of reach.
	// llmContent is gemini's: the text it puts in front of the model, which for
	// a shell tool is the command's own output inside a wrapper of its own.
	// output_for_prompt is grok's: its `output` is the raw bytes as a number
	// array, and this is the text the model reads (#4499).
	// textResultForLlm is Copilot CLI's: the output as its model gets it.
	// message is Kimi's: a failed tool's `error` is {code, message, ...}.
	for _, key := range []string{"stderr", "error", "output", "stdout", "content", "result", "llmContent", "output_for_prompt", "textResultForLlm", "message"} {
		v, ok := obj[key].(string)
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(v)
	}
	return clampOutput(sources.UnwrapShellReport(b.String()))
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
	v, _ := jsonStringAfter(afterLast(raw, `"error"`))
	return clampOutput(v)
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
	for _, key := range []string{`"stderr"`, `"error"`, `"output"`, `"stdout"`, `"content"`, `"result"`, `"llmContent"`, `"returnDisplay"`} {
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
// host does not say (index.FixesForCommand).
func fixPairLineFor(dir, cwd, command, output string) string {
	pol := policy.Load()
	// A pair that names another project is what that project ran next, and
	// that is almost never about this failure: over 396 hints on one machine,
	// 2 of 67 from another project addressed the error and 59 had nothing to
	// do with it, against 109 of 324 from the project itself. A pair with no
	// project is a machine fact and stays.
	// The project's names are asked for once, and only if a pair needs them:
	// each ask runs `git worktree list`, and the filter runs per candidate.
	names := sync.OnceValue(func() []string { return digest.ProjectNameCandidates(cwd) })
	here := func(project string) bool { return project != "" && namesInclude(names(), project) }
	others := func(project string) bool {
		return cwd != "" && project != "" && !here(project)
	}
	// A few, not one: the newest pair for an error can be a remedy that failed,
	// and silence is the wrong answer when the one behind it worked.
	allowHere := func(project string) bool {
		return pol.Allows(policy.ActivationAuto, project) && !others(project)
	}
	pairs := index.FixesForCommand(dir, command, output, 4, allowHere)
	for _, p := range pairs {
		if p.Candidate || orientSkipFixPairForHook(p) {
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
	for _, p := range pairs {
		if !p.Candidate || orientSkipFixPairForHook(p) {
			continue
		}
		if line := fixLineIn(p, frictionCount(dir, p, pol), cwd); line != "" {
			return line
		}
	}
	for _, probe := range nearErrorProbesFromOutput(output) {
		near := nearestRecordedError(dir, probe, allowHere)
		if near == "" {
			continue
		}
		for _, p := range index.FixesForCommand(dir, command, near, 4, allowHere) {
			if orientSkipFixPairForHook(p) {
				continue
			}
			if line := fixLineIn(p, frictionCount(dir, p, pol), cwd); line != "" {
				return line
			}
		}
	}
	// Except a program this machine does not have, or a command line the
	// shell itself refused (#5986). `timeout` missing or zsh refusing an
	// unquoted `--include=*.py` in one checkout is the same in all of them,
	// and the command that worked was learned wherever the agent happened to be.
	isShellErr := index.IsShellDiagnostic(output)
	elsewhere := index.FixesForCommand(dir, command, output, 4, func(project string) bool {
		return pol.Allows(policy.ActivationAuto, project) && project != "" && !here(project)
	})
	for _, p := range elsewhere {
		if (!p.MachineFact() && !isShellErr) || orientSkipFixPairForHook(p) {
			continue
		}
		if line := fixLineIn(p, frictionCount(dir, p, pol), cwd); line != "" {
			return line
		}
	}
	return ""
}

// failingTestSourceLine extracts the exact failing source line from a test file
// cited in test failure output across Go, Python, TS/JS, and Rust so the agent
// sees the failing assertion immediately without a Read round-trip.
func failingTestSourceLine(cwd, output string, pol policy.Policy) string {
	if cwd == "" || output == "" || !pol.Allows(policy.ActivationAuto, "") {
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
			if strings.HasPrefix(filePart, "<") || strings.Contains(filePart, "site-packages") ||
				strings.Contains(filePart, "/venv/") || strings.Contains(filePart, "/.venv/") ||
				strings.Contains(filePart, "/lib/python") {
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

func resolveFailingTargetSnippet(scanRoot string, tree repoFileTree, target failingTestTarget) string {
	rel := target.filePart
	if resolved := tree.resolveRel(target.filePart); resolved != "" {
		rel = resolved
	}
	diskPath := rel
	if !filepath.IsAbs(diskPath) {
		diskPath = filepath.Join(scanRoot, diskPath)
	}
	if !fileThere(diskPath) && !strings.Contains(target.filePart, "/") {
		diskPath, rel = locateFailingFileInScanRoot(scanRoot, target.filePart, diskPath, rel)
	}
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
	if curTest != "" {
		return fmt.Sprintf("failing test line — %s:%d (%s): %s",
			search.SafePath(rel), target.lineNo, search.SafeLine(curTest),
			clipDecision(search.SafeText(srcLine), 140))
	}
	return fmt.Sprintf("failing test line — %s:%d: %s",
		search.SafePath(rel), target.lineNo, clipDecision(search.SafeText(srcLine), 140))
}

func locateFailingFileInScanRoot(scanRoot, filePart, defaultDisk, defaultRel string) (string, string) {
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
	if cwd == "" || output == "" || !pol.Allows(policy.ActivationAuto, "") {
		return ""
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
		"TypeError: ",
	}
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, m := range markers {
			_, after, ok := strings.Cut(trimmed, m)
			if !ok {
				continue
			}
			cand := strings.TrimSpace(after)
			if idx := strings.IndexAny(cand, " \t'\"():;,"); idx > 0 {
				cand = cand[:idx]
			}
			if dot := strings.LastIndexByte(cand, '.'); dot >= 0 && dot+1 < len(cand) {
				cand = cand[dot+1:]
			}
			sym := leadingIdent(cand)
			if len(sym) < 4 {
				continue
			}
			if rel, lineNo, sig := findSymbolDeclarationInRepo(cwd, sym); rel != "" && sig != "" {
				return fmt.Sprintf("symbol signature in repo — %s:%d: %s",
					search.SafePath(rel), lineNo, clipDecision(search.SafeText(sig), 160))
			}
		}
	}
	return ""
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
