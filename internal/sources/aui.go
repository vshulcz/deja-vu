package sources

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// JetBrains agents report their work as AUI block events: Junie CLI writes them
// to events.jsonl inside a SessionA2uxEvent, and the IDE's AI Assistant writes
// the same blocks to aia-task-history/<task>.events, base64 lines under an
// AUI_EVENTS_V1 header. A block is updated in place: every update carries the
// block's stepId, the CLI replays all of a task's blocks once more when it
// finishes, and the IDE streams a markdown block as textChunk pieces. So the
// events are folded per stepId first and read once, in the order each block
// first appeared.
//
// The kinds read: MarkdownBlockUpdatedEvent (the agent's text),
// ResultBlockUpdatedEvent (the task result), TerminalBlockUpdatedEvent
// (command, exitCode, output), ViewFilesBlockUpdatedEvent (files read) and
// FileChangesBlockUpdatedEvent (before and after content of each file).
// Thoughts, tool titles and status lines are the UI talking, not the work.

// auiEvent is one event of either store, its kind without the package prefix
// the IDE writes.
type auiEvent struct {
	Kind string
	Body map[string]any
	At   time.Time
}

type auiStep struct {
	kind string
	body map[string]any
	text string
	at   time.Time
}

// auiKind drops the package the IDE puts in front of an event's kind:
// com.intellij.ml.llm.aui.events.api.TerminalBlockUpdatedEvent.
func auiKind(k string) string {
	if i := strings.LastIndexByte(k, '.'); i >= 0 {
		return k[i+1:]
	}
	return k
}

// auiPrompt is a user turn among the events, which is not a block.
const auiPrompt = "prompt"

// auiRecords folds events into the records of a session. cwd resolves the
// relative paths the IDE writes.
func auiRecords(events []auiEvent, cwd string) []model.Message {
	var order []*auiStep
	steps := map[string]*auiStep{}
	for _, e := range events {
		if e.Kind == auiPrompt {
			text, _ := e.Body["prompt"].(string)
			order = append(order, &auiStep{kind: auiPrompt, text: text, at: e.At})
			continue
		}
		id, _ := e.Body["stepId"].(string)
		s := steps[id]
		if s == nil || id == "" {
			s = &auiStep{kind: e.Kind, at: e.At}
			if id != "" {
				steps[id] = s
			}
			order = append(order, s)
		}
		if chunk, ok := e.Body["textChunk"].(string); ok {
			s.text += chunk
		}
		s.body = e.Body
	}
	var out []model.Message
	for _, s := range order {
		out = append(out, s.records(cwd)...)
	}
	return out
}

func (s *auiStep) records(cwd string) []model.Message {
	str := func(k string) string { v, _ := s.body[k].(string); return v }
	msg := func(role, text string) model.Message { return model.Message{Role: role, Text: text, Time: s.at} }
	switch s.kind {
	case auiPrompt:
		if t := strings.TrimSpace(s.text); t != "" {
			return []model.Message{msg("user", t)}
		}
	case "MarkdownBlockUpdatedEvent":
		t := s.text
		if v := str("text"); v != "" {
			t = v
		}
		if t = strings.TrimSpace(t); t != "" {
			return []model.Message{msg("assistant", t)}
		}
	case "ResultBlockUpdatedEvent":
		// The changes it lists are the ones the file blocks already gave.
		if t := strings.TrimSpace(withoutAdditionalContext(str("result"))); t != "" {
			return []model.Message{msg("assistant", t)}
		}
	case "TerminalBlockUpdatedEvent":
		return s.terminalRecords()
	case "ViewFilesBlockUpdatedEvent":
		if !IndexToolPaths() {
			return nil
		}
		var files []string
		list, _ := s.body["files"].([]any)
		for _, f := range list {
			m, _ := f.(map[string]any)
			p, _ := m["relativePath"].(string)
			if p == "" {
				p, _ = m["path"].(string)
			}
			if p != "" && !strings.ContainsAny(p, "\n\r") {
				files = append(files, resolveToolPath(p, cwd))
			}
		}
		if len(files) > 0 {
			return []model.Message{msg(RoleFiles, strings.Join(files, "\n"))}
		}
	case "FileChangesBlockUpdatedEvent":
		list, _ := s.body["changes"].([]any)
		var files, spans, wrote []string
		for _, c := range list {
			m, _ := c.(map[string]any)
			p := firstString(m, "afterRelativePath", "afterPath", "beforeRelativePath", "beforePath")
			if p == "" || strings.ContainsAny(p, "\n\r") {
				continue
			}
			p = resolveToolPath(p, cwd)
			files = append(files, p)
			old, written := changedLines(auiContent(m["beforeContent"]), auiContent(m["afterContent"]))
			if old != "" {
				if len(old) > editSpanMax {
					old = old[:editSpanMax]
				}
				spans = append(spans, p+"\n"+old)
			}
			if rec := WroteRecord(p, written); rec != "" {
				wrote = append(wrote, rec)
			}
		}
		return patchRecords(files, spans, wrote, s.at)
	}
	return nil
}

// terminalRecords is the command a terminal block ran, stamped with its exit
// code the way every other reader stamps one, and what it printed. The IDE's
// blocks carry a FAILED status and no code.
func (s *auiStep) terminalRecords() []model.Message {
	var out []model.Message
	cmd, _ := s.body["command"].(string)
	if IndexCommands() && worthIndexing(cmd) {
		text := "$ " + cmd
		if code, ok := auiExitCode(s.body["exitCode"]); ok && code != 0 {
			text += "  → exit " + strconv.Itoa(code)
		}
		out = append(out, model.Message{Role: RoleCommand, Text: text, Time: s.at})
	}
	output, _ := s.body["output"].(string)
	if IndexToolOutput() && strings.TrimSpace(output) != "" {
		out = append(out, model.Message{Role: RoleToolOutput, Text: capParsedMessage(output), Time: s.at})
	}
	return out
}

// auiExitCode is a block's exit code: a json.Number from scanJSONL, a float64
// from the IDE's records.
func auiExitCode(v any) (int, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	case float64:
		return int(n), true
	}
	return 0, false
}

func auiContent(v any) string {
	m, _ := v.(map[string]any)
	t, _ := m["text"].(string)
	return t
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, _ := m[k].(string); v != "" {
			return v
		}
	}
	return ""
}

// changedLines is the run of lines a whole-file rewrite replaced, and the run
// it wrote in their place: the two contents with their common head and tail
// taken off. A new file replaced nothing.
func changedLines(before, after string) (old, written string) {
	if before == after {
		return "", ""
	}
	if before == "" {
		return "", after
	}
	a := strings.Split(before, "\n")
	b := strings.Split(after, "\n")
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	return strings.Join(a[head:len(a)-tail], "\n"), strings.Join(b[head:len(b)-tail], "\n")
}

// withoutAdditionalContext drops the <additional_context> blocks Junie puts
// in front of a prompt or a tool result for what its hooks answered. That is
// a hook talking (deja's own recall among them), not the person or the agent.
func withoutAdditionalContext(s string) string {
	const open, close = "<additional_context>", "</additional_context>"
	for {
		i := strings.Index(s, open)
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], close)
		if j < 0 {
			return strings.TrimSpace(s[:i])
		}
		s = s[:i] + s[i+j+len(close):]
	}
}
