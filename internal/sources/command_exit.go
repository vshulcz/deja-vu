package sources

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// commandExits joins a command record to the result that closes its call. The
// call and its result are separate parts, often in separate messages, and
// only the result says how the command ended; a reader that emitted the two
// without joining them stored every failure as a bare `$ cmd`, which reads the
// same as a run whose result never arrived (#4496, #4502, #4505, #4507).
type commandExits map[string][]int

// note files the command records appended to msgs from index from on under the
// calls they were read from, in order. calls is what commandCallsIn read off
// the same parts, so the two line up one to one; when they do not, nothing is
// noted rather than a status pinned to the wrong command.
func (c commandExits) note(msgs []model.Message, from int, calls []claudeCommand) {
	var at []int
	for i := from; i < len(msgs); i++ {
		if msgs[i].Role == RoleCommand {
			at = append(at, i)
		}
	}
	if len(at) != len(calls) {
		return
	}
	for k, i := range at {
		if calls[k].ID != "" {
			c[calls[k].ID] = append(c[calls[k].ID], i)
		}
	}
}

// stamp marks the commands of call id with the code their result reported, in
// the marker every other harness writes. cmd, when set, picks the one command
// of a batch the code belongs to: the first of them not stamped yet, since a
// batch that runs a command twice reports each run in order.
func (c commandExits) stamp(msgs []model.Message, id, cmd string, code int) {
	for _, i := range c[id] {
		if i >= len(msgs) || strings.Contains(msgs[i].Text, "  → exit ") {
			continue
		}
		if cmd != "" && msgs[i].Text != "$ "+cmd {
			continue
		}
		msgs[i].Text += fmt.Sprintf("  → exit %d", code)
		if cmd != "" {
			return
		}
	}
}

// joinResultExits is the join for a transcript in Claude's blocks: a tool_use
// with an id in one message, and a tool_result naming it under tool_use_id in
// a later one. It notes the commands blocks' calls appended to msgs from index
// from on, and stamps those a result among blocks reports on, with the code
// read off that result.
func joinResultExits(msgs []model.Message, from int, blocks any, d toolDialect, exits commandExits, code func(result map[string]any) (int, bool)) {
	if !IndexCommands() {
		return
	}
	exits.note(msgs, from, commandCallsIn(blocks, d))
	items, _ := blocks.([]any)
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok || m["type"] != "tool_result" {
			continue
		}
		id, _ := m["tool_use_id"].(string)
		if _, ok := exits[id]; !ok {
			continue
		}
		if n, ok := code(m); ok {
			exits.stamp(msgs, id, "", n)
		}
		// A result answers its call once: a client that hands an id out
		// again, as call_1, means a later call by it.
		delete(exits, id)
	}
}

// commandCallsIn is commandsIn with the id of the call each command came from,
// so a reader can stamp the outcome when the result arrives.
func commandCallsIn(v any, d toolDialect) []claudeCommand {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []claudeCommand
	for _, it := range items {
		name, in, ok := toolPart(it, d)
		if !ok || !d.isShellTool(name) {
			continue
		}
		id := ""
		if m, ok := it.(map[string]any); ok {
			id, _ = m["id"].(string)
		}
		for _, cmd := range commandStrings(in, d) {
			if !worthIndexing(cmd) {
				continue
			}
			out = append(out, claudeCommand{ID: id, Text: "$ " + cmd})
		}
	}
	return out
}

// statusCode reads N off a status line a harness writes around a command's
// output, "<prefix>N<suffix>": Claude's "Exit code 1", pi's and goose's
// "Command exited with code 1", Cline's "Command failed with exit code 1.".
// The line must be the whole status, so output that merely mentions an exit
// code is not taken for one.
func statusCode(line, prefix, suffix string) (int, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix)
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, suffix)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && rest != "" && rest[0] != '+' && rest[0] != '-'
}

// lastLine is firstLine from the other end, where pi and goose put the status
// they add after a command's output.
func lastLine(s string) string {
	s = strings.TrimRight(s, " \t\r\n")
	return strings.TrimSpace(s[strings.LastIndexByte(s, '\n')+1:])
}

// claudeExitResumes is the #4443 rule for Claude's format, which Cherry Studio
// writes too: a tail holding the failed result of a call stored already is
// read whole, or the call never gets the exit its result names; a clean one
// is let go, as it is for pi.
var claudeExitResumes = resumesUnlessAnswering(`"tool_`, func(m map[string]any) ([]string, string) {
	msg, _ := m["message"].(map[string]any)
	items, _ := msg["content"].([]any)
	var calls []string
	for _, it := range items {
		p, _ := it.(map[string]any)
		switch p["type"] {
		case "tool_use":
			calls = append(calls, str(p["id"]))
		case "tool_result":
			failed, _ := p["is_error"].(bool)
			if id := str(p["tool_use_id"]); failed && id != "" && claudeOutcome(id, true, p["content"]).Known {
				return calls, id
			}
		}
	}
	return calls, ""
})

// gooseExitResumes is the same rule for goose's jsonl sessions.
var gooseExitResumes = resumesUnlessAnswering(`"tool`, func(m map[string]any) ([]string, string) {
	items, _ := m["content"].([]any)
	var calls []string
	for _, it := range items {
		p, _ := it.(map[string]any)
		switch p["type"] {
		case "toolRequest":
			calls = append(calls, str(p["id"]))
		case "toolResponse":
			if code, ok := gooseExitCode(p); ok && code != 0 && str(p["id"]) != "" {
				return calls, str(p["id"])
			}
		}
	}
	return calls, ""
})

// qwenExitResumes is the same rule for Qwen Code, which writes Gemini's parts:
// a functionCall in one record, its functionResponse in a tool_result record.
var qwenExitResumes = resumesUnlessAnswering(`"function`, func(m map[string]any) ([]string, string) {
	msg, _ := m["message"].(map[string]any)
	items, _ := msg["parts"].([]any)
	var calls []string
	for _, it := range items {
		p, _ := it.(map[string]any)
		if call, ok := p["functionCall"].(map[string]any); ok {
			calls = append(calls, str(call["id"]))
		}
		resp, ok := p["functionResponse"].(map[string]any)
		if !ok {
			continue
		}
		r, _ := resp["response"].(map[string]any)
		out, _ := r["output"].(string)
		errOut, _ := r["error"].(string)
		if id := str(resp["id"]); id != "" && (geminiExitCode(out) > 0 || geminiExitCode(errOut) > 0) {
			return calls, id
		}
	}
	return calls, ""
})
