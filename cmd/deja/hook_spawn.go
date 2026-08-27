package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// A spawned agent starts with no memory and no way to be given one.
//
// Measured on this machine's store, 289 of 328 sessions that had deja
// available were Task subagents, and they received a recall in 1% of them and
// called the tool in none. The reason is structural rather than a bar being
// too high: the per-prompt hook fires on what a person types, and a subagent's
// prompt is not typed — it is a tool input written by the parent. So the
// surface that carries memory never sees the work that most needs it, while
// the subagent does the reviewing, the hunting and the migrating.
//
// PreToolUse can rewrite a tool's input, which is the one place a subagent's
// prompt can still be reached. Fed through the same recall the per-prompt hook
// runs — same bar, same dedup, same policy — what comes back is appended to
// the prompt the subagent is about to receive.

// spawnPromptFields is where each harness keeps the instruction it hands the
// spawned agent. Claude Code calls it prompt; the others that grew a subagent
// tool since have kept the name, and the fallbacks cost nothing.
var spawnPromptFields = []string{"prompt", "instructions", "task"}

// isSpawnTool reports whether this action spawns an agent. Claude Code calls it
// Task; the name is matched case-insensitively because a harness that wires the
// hook with its own matcher may spell it differently.
func isSpawnTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "task", "agent", "subagent":
		return true
	}
	return false
}

// runHookSpawn answers a PreToolUse on the tool that spawns an agent. The reply
// carries the whole tool input back with the prompt extended, because a
// PreToolUse reply replaces the input rather than merging into it: dropping a
// field here would silently drop the subagent_type or the model the parent
// chose.
func runHookSpawn(dir string, input toolHookInput, raw []byte, stdout io.Writer) error {
	var payload struct {
		ToolInput map[string]json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.ToolInput) == 0 {
		return nil
	}
	field, text := spawnPrompt(payload.ToolInput)
	if field == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	block := spawnRecall(dir, input.SessionID, input.CWD, text)
	if block == "" {
		return nil
	}
	extended, err := json.Marshal(text + "\n\n" + block)
	if err != nil {
		return nil
	}
	payload.ToolInput[field] = extended

	var resp struct {
		HookSpecificOutput struct {
			HookEventName      string                     `json:"hookEventName"`
			PermissionDecision string                     `json:"permissionDecision"`
			UpdatedInput       map[string]json.RawMessage `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	resp.HookSpecificOutput.HookEventName = "PreToolUse"
	// allow, not ask: this changes what the agent is told, not what it is
	// permitted to do, and a prompt for every spawned agent would make the
	// memory the most annoying thing in the session.
	resp.HookSpecificOutput.PermissionDecision = "allow"
	resp.HookSpecificOutput.UpdatedInput = payload.ToolInput
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

func spawnPrompt(in map[string]json.RawMessage) (string, string) {
	for _, f := range spawnPromptFields {
		v, ok := in[f]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err == nil && strings.TrimSpace(s) != "" {
			return f, s
		}
	}
	return "", ""
}

// spawnRecall runs the per-prompt hook over the spawned agent's instructions and
// returns the block it would have injected, or "" for the silence that is the
// usual answer.
//
// The parent's own session id is deliberately not used. Dedup and the cooldown
// are per session, and sharing the parent's would mean a recall the parent was
// shown a minute ago is withheld from an agent that has never seen anything —
// and that a fleet of ten agents spawned together all count as one reader.
// Keyed on the parent plus the instructions instead, so re-spawning the same
// agent twice is the repeat that gets suppressed.
func spawnRecall(dir, sid, cwd, text string) string {
	payload, err := json.Marshal(struct {
		Prompt    string `json:"prompt"`
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
	}{
		Prompt:    text,
		SessionID: "task:" + sid + ":" + shortHash(text),
		CWD:       cwd,
	})
	if err != nil {
		return ""
	}
	var out bytes.Buffer
	// plain: the block itself, not a hook envelope. It is going inside another
	// tool's input, where a JSON reply of its own would be read as text.
	if err := runHookPromptMode(dir, bytes.NewReader(payload), &out, true); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}
