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
	SessionID    string          `json:"session_id"`
	CWD          string          `json:"cwd"`
}

func runHookToolAfter(dir string, stdin io.Reader, stdout io.Writer) error {
	var input toolAfterInput
	raw := readHookPayload(stdin, hookStdinWait)
	_ = json.NewDecoder(bytes.NewReader(raw)).Decode(&input)
	if !planIndexReady(dir) {
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
	out := toolResponseText(input.ToolResponse)
	if out == "" {
		// readHookPayload stops at 1 MiB, so a verbose build cuts the JSON
		// mid-string and the decode yields nothing at all — not a truncated
		// payload, an empty one. The error is usually the first line of that
		// output, and this hook exists for exactly the failing `make` or
		// `pytest -v` whose log runs past a megabyte (#1716). What arrived is
		// still worth reading: pull the output back out of the cut JSON rather
		// than throwing away a megabyte that begins with the error.
		out = clampOutput(salvageToolOutput(after(string(raw), `"tool_response"`)))
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
	case "Bash", "bash", "shell", "run_command", "execute_command", "terminal":
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
		return clampOutput(s)
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	var b strings.Builder
	// stderr first: the signature is far more often there, and the scan is
	// capped, so a long stdout must not push it out of reach.
	for _, key := range []string{"stderr", "error", "output", "stdout", "content", "result"} {
		if v, ok := obj[key].(string); ok && strings.TrimSpace(v) != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(v)
		}
	}
	return clampOutput(b.String())
}

// after returns what follows key, or "" when the key is not there. Scoping the
// salvage this way keeps it from mining the command in tool_input, which can
// hold any of the keys below.
func after(s, key string) string {
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	return s[i+len(key):]
}

// salvageToolOutput pulls the tool's output out of a payload the decoder could
// not read. Scanning the raw bytes does not work: the JSON prefix is glued to
// the first line of the output, and a line holding a quote is dropped as source
// rather than error — correctly, for real tool output. So find where the value
// starts, unescape what follows, and hand back that.
func salvageToolOutput(raw string) string {
	for _, key := range []string{`"stderr"`, `"error"`, `"output"`, `"stdout"`, `"content"`, `"result"`} {
		i := strings.Index(raw, key)
		if i < 0 {
			continue
		}
		v, ok := jsonStringAfter(raw[i+len(key):])
		if !ok {
			continue
		}
		if strings.TrimSpace(v) != "" {
			return v
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
		if line := fixLine(p); line != "" {
			return line
		}
	}
	return ""
}

func fixLine(p index.FixPair) string {
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
	// A single sighting is said as one. Two sessions doing the same thing after
	// the same error is evidence it worked; one session doing something is what
	// one session did, and an agent handed it at the moment it is stuck has to
	// be told which of the two it is holding.
	if p.Candidate {
		return "deja: this error came up here before" + when +
			" — one session ran this after it, and nothing confirms it worked: " + cmd
	}
	return "deja: this error came up here before" + when + " — what followed it: " + cmd
}

// exitMarker is the shape a source appends when it knows what a command
// returned: two spaces, the marker, the digits, end of string
// (internal/sources/codex.go:259, internal/sources/opencode.go:202).
const exitMarker = "  → exit "

// withoutFailedExit drops the recorded exit status from a command, and reports
// false when that status says the command failed.
//
// Only a suffix of that exact shape counts. Looking for the marker anywhere cut
// `echo "→ exit 0"` down to `echo "` and called it a failure, because the code
// it read was `0"` — a command that mentions deja's own marker is still just a
// command (#2048).
func withoutFailedExit(cmd string) (string, bool) {
	i := strings.LastIndex(cmd, exitMarker)
	if i < 0 {
		return cmd, true
	}
	code := cmd[i+len(exitMarker):]
	if code == "" {
		return cmd, true
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return cmd, true
		}
	}
	return strings.TrimSpace(cmd[:i]), code == "0"
}
