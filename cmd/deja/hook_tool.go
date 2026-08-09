package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
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
// default answer. It gates on the tool: a command line is worth saying only for
// the tool that runs a command, and a file's history only before the file is
// changed — Read, Glob and NotebookRead carry a file_path too, and a hook wired
// with a wide matcher would otherwise fire on every one of them.
func toolHookLine(dir string, input toolHookInput) string {
	switch input.ToolName {
	case "Bash":
		if cmd := strings.TrimSpace(input.ToolInput.Command); cmd != "" {
			return commandHookLine(dir, cmd)
		}
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		if path := strings.TrimSpace(input.ToolInput.FilePath); path != "" {
			return fileHookLine(dir, path)
		}
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
	// FileSessions matches on the file's basename, so without scoping "main.go"
	// or "README.md" collects every project's file of that name — the line then
	// claims a history this file does not have and points `deja blame` at a
	// pile of other repos. Count only sessions in the project being worked in,
	// unless the stored path is the exact one (which cannot collide).
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	projects := digest.ProjectNameCandidates(cwd)
	// A hook that fires unasked is the auto activation, so a session the trust
	// policy withholds must not even be counted here.
	pol := policy.Load()
	var sessions int
	var last time.Time
	for _, meta := range index.FileSessions(dir, path) {
		if !pol.Allows(policy.ActivationAuto, meta.Project) {
			continue
		}
		if !fileMetaInScope(meta, path, projects) {
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

// fileMetaInScope keeps a session only if it worked on this exact path (an
// absolute path cannot collide across projects) or if it belongs to the
// project being worked in now. It is what stops a same-named file in an
// unrelated repo from being counted.
func fileMetaInScope(meta index.SessionMeta, path string, projects []string) bool {
	for _, t := range meta.Touched {
		if t == path {
			return true
		}
	}
	proj := strings.TrimPrefix(meta.Project, "imported:")
	for _, cand := range projects {
		if cand == "" {
			continue
		}
		if proj == cand || strings.HasSuffix(proj, "/"+cand) || strings.HasSuffix(cand, "/"+proj) {
			return true
		}
	}
	return false
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
