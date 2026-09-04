package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
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
	ToolInput     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	// Harnesses disagree about where a command's output lives, and about
	// whether the exit code is reported at all, so every shape is read and the
	// decision rests on the text rather than on a status field.
	ToolResponse json.RawMessage `json:"tool_response"`
	// Cursor calls it tool_output, and sends a JSON document inside a JSON
	// string: {"output":"…","exitCode":0} (measured on cursor-agent
	// 2026.09.02). Reading only tool_response left the fix pair with nothing.
	ToolOutput json.RawMessage `json:"tool_output"`
	SessionID  string          `json:"session_id"`
	CWD        string          `json:"cwd"`
}

func runHookToolAfter(dir string, stdin io.Reader, stdout io.Writer) error {
	var input toolAfterInput
	raw := readHookPayload(stdin, hookStdinWait)
	_ = json.NewDecoder(bytes.NewReader(raw)).Decode(&input)
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
	}
	if !isCommandTool(name) {
		return nil
	}
	response := input.ToolResponse
	if len(bytes.TrimSpace(response)) == 0 {
		response = input.ToolOutput
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
		out = salvageFromPayload(string(raw))
	}
	if out == "" {
		return nil
	}
	line := fixPairLine(dir, out)
	if line == "" {
		return nil
	}
	token := "fix:" + shortHash(line)
	if alreadyInjected(dir, input.SessionID)[token] {
		return nil
	}
	rememberInjectedIDs(dir, input.SessionID, token)
	payload := frameRecall(truncateToolLine(line, toolAfterMaxBytes))
	usage.RecordResult(dir, usage.KindTool, len(payload), 1, false)
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "PostToolUse"
	resp.HookSpecificOutput.AdditionalContext = payload
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

// isCommandTool reports whether this tool runs a command. File edits fail in
// ways a shell error signature does not describe, and the pre-tool hook already
// speaks for them.
func isCommandTool(name string) bool {
	switch name {
	case "Bash", "bash", "shell", "Shell", "run_command", "execute_command", "terminal", "run_shell_command":
		return true
	}
	return false
}

// toolResponseText pulls whatever the harness called the output. Claude Code
// sends an object with stdout and stderr, codex a string, and others a mix; a
// bare string response is the whole output.
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
		return clampOutput(s)
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
	for _, key := range []string{"stderr", "error", "output", "stdout", "content", "result", "llmContent"} {
		v, ok := obj[key].(string)
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		if key == "llmContent" {
			v = unwrapGeminiShellOutput(v)
		}
		if strings.TrimSpace(v) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(v)
	}
	return clampOutput(b.String())
}

// unwrapGeminiShellOutput strips the frame gemini and qwen put around a
// command's output before handing it to the model. Gemini fences it in
// <untrusted_context> with an "Output:" marker; qwen writes a labelled report —
// Command, Directory, Output, Error, Exit Code, Signal, PGID. The marker is
// what matters: with it in front, the first line of a build failure stops
// looking like an error, and the fix pair went silent on a failure it answers
// the moment the marker is gone (gemini-cli 0.55.1, qwen-code 0.20.0).
func unwrapGeminiShellOutput(s string) string {
	if !strings.Contains(s, "Output:") {
		return s
	}
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t == "<untrusted_context>" || t == "</untrusted_context>" || framingLabel(t) {
			continue
		}
		// The label introduces the payload on its first line only; what follows
		// is the command's own output, untouched.
		for _, label := range []string{"Output: ", "Error: "} {
			if strings.HasPrefix(line, label) {
				line = line[len(label):]
				break
			}
		}
		if strings.TrimSpace(line) == "(none)" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// framingLabel reports whether a line is part of the shell report rather than
// the command's output.
func framingLabel(t string) bool {
	for _, label := range []string{"Command: ", "Directory: ", "Exit Code: ", "Signal: ", "Process Group PGID:"} {
		if strings.HasPrefix(t, label) {
			return true
		}
	}
	return false
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
// value out of it, and bound what comes back.
func salvageFromPayload(raw string) string {
	return clampOutput(salvageToolOutput(afterLast(raw, `"tool_response"`)))
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
	for _, key := range []string{`"stderr"`, `"error"`, `"output"`, `"stdout"`, `"content"`, `"result"`} {
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
	for i := 0; i < len(s); i++ {
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
func fixPairLine(dir, output string) string {
	pol := policy.Load()
	// A few, not one: the newest pair for an error can be a remedy that failed,
	// and silence is the wrong answer when the one behind it worked.
	pairs := index.FixesFor(dir, output, 4, func(project string) bool {
		return pol.Allows(policy.ActivationAuto, project)
	})
	for _, p := range pairs {
		if line := fixLine(p, frictionCount(dir, p, pol)); line != "" {
			return line
		}
	}
	return ""
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
		return project == "" || proj == project
	})
}

func fixLine(p index.FixPair, sessions int) string {
	// Some harnesses store the command with the prompt they printed it after;
	// the line reads as an instruction, so it should not start with a shell
	// prompt the reader would have to mentally strip.
	cmd, ok := withoutFailedExit(strings.TrimPrefix(strings.TrimSpace(search.SafeText(p.Command)), "$ "))
	// codex and opencode record the exit status on the command; a remedy that
	// exited non-zero is not one, and handing it over as "what followed it"
	// tells an agent to run something that already failed here.
	if !ok || cmd == "" {
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
	if p.Candidate {
		return "deja: this error came up" + how + " " + where + " before" + when +
			" — one session ran this after it, and nothing confirms it worked: " + cmd
	}
	return "deja: this error came up" + how + " " + where + " before" + when + " — what followed it: " + cmd
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
