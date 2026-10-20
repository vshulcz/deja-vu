package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// MiniMax Code (MCode) is a desktop agent whose plugin hooks are given one
// JSON event on stdin and are read back as one JSON object on stdout: the
// text reaches the turn under hookSpecificOutput.additionalContext, and a
// reply that names a different event is dropped.
//
// The payload is Claude-shaped — the event name, the prompt, the tool name
// and its input, the session id, the cwd — so the hooks read it unchanged.
// Only the way the answer is written back is MCode's own, and the --mcode
// flag says so: the same hook, the same text, another envelope.
//
// A flag rather than a second set of hooks, because the text a hook decides
// on is the same either way. What differs is only where the host looks for
// it, and a host that looks in the wrong place drops the answer silently —
// the memory is served and the model never sees it.

// mcodeHookOutput is set by --mcode: the answer goes out in MiniMax Code's
// shape rather than in Claude's.
var mcodeHookOutput bool

// mcodeHookFlag reports whether the command line asked for MCode's shape.
func mcodeHookFlag(rest []string) bool {
	for _, a := range rest {
		if a == "--mcode" || a == "-mcode" {
			return true
		}
	}
	return false
}

// mcodeHookResponse is the answer MiniMax Code reads. The event name has to
// be the one the hook was fired for, so it is carried rather than assumed.
type mcodeHookResponse struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// emitMcodeContext writes the answer MiniMax Code reads. A hook with nothing
// to say writes nothing at all, which is how a hook stays quiet.
func emitMcodeContext(w io.Writer, event, context string) {
	context = strings.TrimSpace(context)
	if context == "" {
		return
	}
	if event == "" {
		event = "UserPromptSubmit"
	}
	var resp mcodeHookResponse
	resp.HookSpecificOutput.HookEventName = event
	resp.HookSpecificOutput.AdditionalContext = context
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	fmt.Fprintln(w, string(b))
}
