package sources

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Copilot Chat's edit tools keep both sides of a change in their arguments,
// which VS Code stores with the request: chatSessions carry them under
// result.metadata.toolCallRounds[].toolCalls[] as {name, arguments, id}, the
// arguments a JSON string, and the agent transcripts under tool.execution_start.
// The textEditGroup part the reader took before has the written text only, so
// restore and the replaced-span rule of blame had nothing from this harness
// (#595). Names and keys are the extension's: replace_string_in_file
// {filePath, oldString, newString}, multi_replace_string_in_file
// {replacements: [{filePath, oldString, newString}]}, create_file {filePath,
// content}, and apply_patch {input} in codex's patch format.
var copilotChatDialect = toolDialect{
	pathKey:   "filePath",
	pathTools: map[string]bool{"replace_string_in_file": true, "create_file": true},
	editTools: map[string]bool{"replace_string_in_file": true, "create_file": true},
	oldKey:    "oldString",
	newKey:    "newString",
}

// copilotChatChange is what one edit call did: the files it names, the spans
// it replaced, and what it wrote.
type copilotChatChange struct {
	files, spans, wrote []string
}

// copilotChatCallChange reads one edit call. A call that is not an edit
// returns nothing.
func copilotChatCallChange(name string, args map[string]any) copilotChatChange {
	var c copilotChatChange
	if args == nil {
		return c
	}
	if name == "apply_patch" {
		if in, _ := args["input"].(string); in != "" {
			c.files, c.spans, c.wrote = applyPatch(in, func(p string) string { return chatResourcePath(p) })
		}
		return c
	}
	var calls []any
	switch name {
	case "multi_replace_string_in_file":
		for _, r := range copilotChatSlice(args["replacements"]) {
			if m, ok := r.(map[string]any); ok {
				calls = append(calls, copilotChatEditCall("replace_string_in_file", m))
			}
		}
	case "replace_string_in_file", "create_file":
		calls = append(calls, copilotChatEditCall(name, args))
	default:
		return c
	}
	if p := toolPathsIn(calls, copilotChatDialect); p != "" {
		c.files = strings.Split(p, "\n")
	}
	c.spans = editSpansIn(calls, copilotChatDialect)
	c.wrote = wroteRecordsIn(calls, copilotChatDialect)
	return c
}

// copilotChatEditCall is one call in the tool_use shape the shared extractors
// read, its path made a plain path when VS Code wrote a URI.
func copilotChatEditCall(name string, args map[string]any) map[string]any {
	in := args
	if p, _ := args["filePath"].(string); p != "" {
		if plain := chatResourcePath(p); plain != p {
			in = make(map[string]any, len(args))
			for k, v := range args {
				in[k] = v
			}
			in["filePath"] = plain
		}
	}
	return map[string]any{"type": "tool_use", "name": name, "input": in}
}

// copilotChatRoundEdits reads the replaced side of the edits a chatSessions
// request made, from its toolCallRounds. A call whose result says it failed
// changed nothing and is skipped. The written side and the files already come
// from the response's textEditGroup parts, so only the spans are added here.
func copilotChatRoundEdits(req map[string]any, t time.Time) []model.Message {
	if !IndexEdits() {
		return nil
	}
	result, _ := req["result"].(map[string]any)
	meta, _ := result["metadata"].(map[string]any)
	if meta == nil {
		return nil
	}
	results, _ := meta["toolCallResults"].(map[string]any)
	var out []model.Message
	for _, r := range copilotChatSlice(meta["toolCallRounds"]) {
		round, _ := r.(map[string]any)
		for _, c := range copilotChatSlice(round["toolCalls"]) {
			call, _ := c.(map[string]any)
			name, _ := call["name"].(string)
			raw, _ := call["arguments"].(string)
			if name == "" || raw == "" {
				continue
			}
			id, _ := call["id"].(string)
			if copilotChatCallFailed(results[id]) {
				continue
			}
			var args map[string]any
			if json.Unmarshal([]byte(raw), &args) != nil {
				continue
			}
			for _, span := range copilotChatCallChange(name, args).spans {
				out = append(out, model.Message{Role: RoleEdit, Text: span, Time: t})
			}
		}
	}
	return out
}

// copilotChatCallFailed reports whether a stored tool result is the extension
// refusing the call: "ERROR: Your input to the tool was invalid", "ERROR while
// calling tool: File already exists", "Applying patch failed with error".
func copilotChatCallFailed(res any) bool {
	m, _ := res.(map[string]any)
	for _, p := range copilotChatSlice(m["content"]) {
		part, _ := p.(map[string]any)
		v, _ := part["value"].(string)
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "ERROR") || strings.HasPrefix(v, "Applying patch failed") {
			return true
		}
	}
	return false
}

// copilotChatChangeRecords turns one call's change into the edit and wrote
// records, for the store that has no textEditGroup to take the written side
// from.
func copilotChatChangeRecords(c copilotChatChange, t time.Time) []model.Message {
	var out []model.Message
	if IndexEdits() {
		for _, span := range c.spans {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: t})
		}
	}
	if IndexWrites() {
		for _, w := range c.wrote {
			out = append(out, model.Message{Role: RoleWrote, Text: w, Time: t})
		}
	}
	return out
}
