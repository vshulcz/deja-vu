package main

import (
	"bytes"
	"encoding/json"
	"os"
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

// latestUserRequest returns the newest user turn in an antigravity transcript,
// or "" when the file holds none it can read.
func latestUserRequest(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	size := fi.Size()
	offset := int64(0)
	if size > antigravityTranscriptTail {
		offset = size - antigravityTranscriptTail
	}
	buf := make([]byte, size-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return ""
	}
	lines := strings.Split(string(buf), "\n")
	// Newest first, and a partial first line from the tail cut simply fails to
	// decode — which is the right answer for it.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var step struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(line), &step) != nil || step.Type != "USER_INPUT" {
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
