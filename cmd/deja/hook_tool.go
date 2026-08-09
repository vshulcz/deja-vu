package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// `deja hook-tool` is recall at the moment of the action rather than at the
// moment of the sentence.
//
// Every other injection point deja has fires on text a person wrote. But an
// agent spends its time running commands and editing files, and that is where
// the machine's own history is both cheapest to match — an exact command, an
// exact path, no lexical guessing — and most likely to save work. On a real
// 1165-session store, 335 commands were run in three or more separate sessions
// and 487 files were touched in five or more.
//
// The cost side is the reason this is deliberately thin. A prompt hook is paid
// once per message; this is paid once per action, and there are an order of
// magnitude more of those. So: a hard cap of one short line, no digest, no
// snippets, and silence unless the history is a pattern rather than a
// coincidence.

const (
	// toolHookMaxBytes is the whole payload. The line is a pointer, not an
	// answer: an agent that wants the story calls recall.
	toolHookMaxBytes = 300
	// toolHookMinFileSessions is when a file's history stops being noise. A
	// file two sessions touched is ordinary work; five is a place with a past.
	toolHookMinFileSessions = 5
)

type toolHookInput struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
}

func runHookTool(dir string, stdin io.Reader, stdout io.Writer) error {
	var input toolHookInput
	_ = json.NewDecoder(bytes.NewReader(readHookPayload(stdin, hookStdinWait))).Decode(&input)
	adoptHookCWD(input.CWD)
	// Never build or repair from here. This runs inside an action the user is
	// waiting on, and a miss costs nothing while a rebuild costs seconds.
	if !planIndexReady(dir) {
		return nil
	}
	line := toolHookLine(dir, input)
	if line == "" {
		return nil
	}
	if len(line) > toolHookMaxBytes {
		line = line[:toolHookMaxBytes]
	}
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "PreToolUse"
	resp.HookSpecificOutput.AdditionalContext = frameRecall(line)
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

// toolHookLine is what the agent is told, or "" for the silence that is the
// default answer.
func toolHookLine(dir string, input toolHookInput) string {
	if cmd := strings.TrimSpace(input.ToolInput.Command); cmd != "" {
		return commandHookLine(dir, cmd)
	}
	if path := strings.TrimSpace(input.ToolInput.FilePath); path != "" {
		return fileHookLine(dir, path)
	}
	return ""
}

func commandHookLine(dir, cmd string) string {
	use, ok := index.CommandHistory(dir, cmd)
	if !ok {
		return ""
	}
	when := ""
	if !use.Last.IsZero() {
		when = ", last " + use.Last.Local().Format("2006-01-02")
	}
	return fmt.Sprintf("This machine has run that command in %s%s.",
		toolSessionCount(use.Sessions), when)
}

func fileHookLine(dir, path string) string {
	// A hook that fires unasked is the auto activation, so a session the trust
	// policy withholds must not even be counted here.
	pol := policy.Load()
	var sessions int
	var last time.Time
	for _, meta := range index.FileSessions(dir, path) {
		if !pol.Allows(policy.ActivationAuto, meta.Project) {
			continue
		}
		sessions++
		if meta.Updated.After(last) {
			last = meta.Updated
		}
	}
	if sessions < toolHookMinFileSessions {
		return ""
	}
	when := ""
	if !last.IsZero() {
		when = ", last " + last.Local().Format("2006-01-02")
	}
	return fmt.Sprintf("%s has been worked on in %s%s — `deja blame %s` has the history.",
		search.SafeText(baseName(path)), toolSessionCount(sessions), when, search.SafeText(baseName(path)))
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 && i+1 < len(p) {
		return p[i+1:]
	}
	return p
}

// toolSessionCount words a count for a line read inside an action.
func toolSessionCount(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}
