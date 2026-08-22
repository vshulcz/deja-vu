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
	_ = json.NewDecoder(bytes.NewReader(readHookPayload(stdin, hookStdinWait))).Decode(&input)
	adoptHookCWD(input.CWD)
	if !planIndexReady(dir) {
		return nil
	}
	if !isCommandTool(input.ToolName) {
		return nil
	}
	out := toolResponseText(input.ToolResponse)
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
	return "deja: this error came up here before" + when + " — what followed it: " + cmd
}

// withoutFailedExit drops the recorded exit status from a command, and reports
// false when that status says the command failed.
func withoutFailedExit(cmd string) (string, bool) {
	i := strings.LastIndex(cmd, "→ exit ")
	if i < 0 {
		return cmd, true
	}
	code := strings.TrimSpace(cmd[i+len("→ exit "):])
	return strings.TrimSpace(cmd[:i]), code == "0"
}
