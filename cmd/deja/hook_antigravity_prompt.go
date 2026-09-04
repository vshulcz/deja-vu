package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	antigravityTranscriptTail = 128 << 10
	userRequestOpen           = "<USER_REQUEST>"
	userRequestClose          = "</USER_REQUEST>"
)

// transcriptStep is as much of an antigravity step as this hook reads.
type transcriptStep struct {
	Type    string `json:"type"`
	Content string `json:"content"`
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
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
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

// latestUserRequest returns the newest user turn in an antigravity transcript,
// or "" when the file holds none it can read.
func latestUserRequest(path string) string {
	for _, step := range transcriptTailSteps(path) {
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
	open := strings.Index(content, userRequestOpen)
	if open < 0 {
		return strings.TrimSpace(content)
	}
	rest := content[open+len(userRequestOpen):]
	if close := strings.Index(rest, userRequestClose); close >= 0 {
		rest = rest[:close]
	}
	return strings.TrimSpace(rest)
}

// antigravityPromptBlock is what the per-prompt path would inject for this
// question, or "" for the silence that is its usual answer. It drives the
// ordinary prompt hook rather than reimplementing the ranking: the budget, the
// dedupe and the kill switch are the ones every other harness gets.
func antigravityPromptBlock(dir, question, conversationID, workspace string) string {
	if strings.TrimSpace(question) == "" {
		return ""
	}
	payload, err := json.Marshal(map[string]string{
		"prompt":     question,
		"session_id": conversationID,
		"cwd":        workspace,
	})
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
	return alreadyInjected(dir, antigravityDigestKey(conversationID))[antigravityDigestToken]
}

func rememberDigestInjected(dir, conversationID string) {
	if strings.TrimSpace(conversationID) == "" {
		return
	}
	rememberInjectedIDs(dir, antigravityDigestKey(conversationID), antigravityDigestToken)
}

func antigravityDigestKey(conversationID string) string {
	return "agy:" + conversationID
}

// antigravityDigestToken is what the ledger row says: this conversation has
// been handed the digest.
const antigravityDigestToken = "agy-digest"

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

// latestToolFailure is the output of the newest tool step that ended badly, or
// "" when the last thing the agent did was not a failure. PostToolUse gets the
// error but its contract allows no answer at all (checked on antigravity-cli
// 1.1.13: it must return an empty object), so the moment a command fails is
// reachable only from the invocation that follows it.
//
// Only the newest tool step is read: a failure two steps back has already been
// answered or worked around, and injecting it again would be talking about
// something the agent has moved on from.
func latestToolFailure(path string) string {
	for _, step := range transcriptTailSteps(path) {
		if step.Type != "GENERIC" {
			continue
		}
		body := step.Content
		if !strings.Contains(body, exitedWithCode) {
			// The newest tool step said nothing about an exit code, so there
			// is no failure to speak about.
			return ""
		}
		if strings.Contains(body, exitedWithCode+" 0.") {
			return ""
		}
		if i := strings.Index(body, toolOutputMarker); i >= 0 {
			return strings.TrimSpace(body[i+len(toolOutputMarker):])
		}
		return strings.TrimSpace(body)
	}
	return ""
}

const (
	exitedWithCode   = "The command exited with code"
	toolOutputMarker = "Output:"
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
