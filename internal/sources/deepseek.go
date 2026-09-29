package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// DeepSeek Harness (`dsh`) keeps one append-only log per session under
// $DSH_HOME/sessions/<workspace-slug>/session-<uuid>/. The log was
// session.jsonl.zstd when this reader was written; dsh has since moved to
// session.v3.jsonl.zstd with the same records, and a session directory can keep
// a header-only file under the old name beside its v3 log. The
// file is a JSONL stream written as consecutive zstd frames by default, with
// raw lines available as a configuration; both are read here, chosen by the
// extension the harness wrote.
//
// The first line is the session header (id, createdAt, cwd). Every line after
// it is one event: {type, seq, time, data}. Three of those types carry the
// conversation.
//
// A person's turn is `user/message` with data.source.kind == "user". The same
// type also carries what plugins splice in — the sandbox policy snapshot, the
// skill catalogue — under a different source kind, and those are the harness
// talking to itself, not history worth recalling.
//
// The agent's turn is written twice. It streams as `assistant/chunk` deltas —
// and long runs of those are packed into rows of another type entirely, so the
// stream alone is not readable without unpacking it — and then lands complete
// in one `assistant/message` whose data.message.content is a block array. The
// complete one is what deja reads; the deltas are the fallback for a run that
// was interrupted before it landed.
//
// Reasoning blocks in that array are the model thinking out loud, not what it
// told the person, so they stay out of the transcript.
//
// Tool output is its own event, `tool/result`, whose content nests a
// tool-result block around the text.
//
// Verified against dsh 0.1.1-rc.2 driven by a local model, over sessions that
// answered, called a tool, and failed before answering.

// DSHHome is the harness's own home directory, following its DSH_HOME variable.
func DSHHome() string {
	if p := os.Getenv("DSH_HOME"); p != "" {
		return p
	}
	return filepath.Join(Home(), ".dsh")
}

// DeepSeekRoot is where dsh keeps session logs. DEJA_DEEPSEEK_ROOT relocates
// the read for tests and for a relocated install.
func DeepSeekRoot() string {
	return EnvPath("DEJA_DEEPSEEK_ROOT", filepath.Join(DSHHome(), "sessions"))
}

// deepSeekLogNames is every name dsh gives a session log. Discovery and the
// incremental index both match on this one list, so a new name cannot reach
// one of them and miss the other.
var deepSeekLogNames = []string{
	"session.jsonl", "session.jsonl.zstd",
	"session.v3.jsonl", "session.v3.jsonl.zstd",
}

func isDeepSeekLog(p string) bool {
	base := filepath.Base(p)
	if _, _, ok := parseCanonicalDeepSeekLogFilename(base); ok {
		return true
	}
	for _, name := range deepSeekLogNames {
		if hasBase(p, name) {
			return true
		}
	}
	return false
}

func DeepSeekSessionFiles() []string {
	return walkFiles(DeepSeekRoot(), isDeepSeekLog)
}

func LoadDeepSeek() []model.Session {
	return parseFiles(DeepSeekSessionFiles(), ParseDeepSeekFile)
}

func ParseDeepSeekFile(path string) ([]model.Session, error) {
	trace, err := ReadDeepSeekTrace(path)
	if err != nil {
		return nil, err
	}
	if trace.Unsupported {
		return nil, nil
	}

	id := strings.TrimPrefix(trace.Header.ID, "session-")
	if id == "" {
		id = strings.TrimPrefix(filepath.Base(filepath.Dir(path)), "session-")
	}

	s := model.Session{
		Harness: "deepseek",
		ID:      id,
		Project: "-",
		Path:    path,
	}
	if trace.Header.CWD != "" {
		s.Project = projectName(trace.Header.CWD)
	}
	if trace.Header.ParentSession != "" {
		s.Parent = strings.TrimPrefix(trace.Header.ParentSession, "session-")
	}
	if trace.Header.Origin != "" {
		s.Kind = trace.Header.Origin
	}
	if trace.Header.AgentPreset != "" {
		s.Agent = trace.Header.AgentPreset
	}
	s.Touch(trace.Header.CreatedAt)

	type toolCallInfo struct {
		name string
		args map[string]any
		time time.Time
		seq  int
	}
	callsByID := make(map[string]toolCallInfo)
	callsBySeq := make(map[int]toolCallInfo)

	var pending []string
	var pendingAt time.Time
	flush := func() {
		text := strings.TrimSpace(strings.Join(pending, ""))
		pending = nil
		if text == "" {
			return
		}
		s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: pendingAt})
	}

	for _, ev := range trace.Events {
		at := ev.Time
		switch ev.Type {
		case "session":
			var data map[string]any
			if json.Unmarshal(ev.Data, &data) == nil {
				if sid, _ := data["id"].(string); sid != "" {
					s.ID = strings.TrimPrefix(sid, "session-")
				}
				if cwd, _ := data["cwd"].(string); cwd != "" {
					s.Project = projectName(cwd)
				}
			}
		case "session/title":
			var data struct {
				Title string `json:"title"`
			}
			if json.Unmarshal(ev.Data, &data) == nil && data.Title != "" {
				s.Title = data.Title
			}
		case "user/message":
			var data map[string]any
			if json.Unmarshal(ev.Data, &data) != nil || !deepSeekSpokenByUser(data) {
				continue
			}
			flush()
			if text := deepSeekContentText(data["content"]); text != "" {
				s.Messages = append(s.Messages, model.Message{Role: "user", Text: text, Time: at})
				s.Touch(at)
			}
		case "assistant/chunk":
			var data struct {
				Chunk struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"chunk"`
			}
			if json.Unmarshal(ev.Data, &data) == nil && data.Chunk.Type == "text-delta" && data.Chunk.Text != "" {
				pending = append(pending, data.Chunk.Text)
				pendingAt = at
				s.Touch(at)
			}
		case "text-chunks":
			var data struct {
				Texts []any `json:"texts"`
			}
			if json.Unmarshal(ev.Data, &data) == nil {
				for _, part := range deepSeekPackedTexts(data.Texts) {
					pending = append(pending, part)
					pendingAt = at
					s.Touch(at)
				}
			}
		case "assistant/message":
			pending = nil
			var data struct {
				Message struct {
					Content any `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(ev.Data, &data) == nil {
				if text := deepSeekContentText(data.Message.Content); text != "" {
					s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: at})
					s.Touch(at)
				}
			}
		case "step/end", "turn/end":
			flush()
		case "tool/call":
			var data struct {
				CallID    string `json:"callId"`
				Name      string `json:"name"`
				Arguments any    `json:"arguments"`
			}
			if json.Unmarshal(ev.Data, &data) == nil {
				info := toolCallInfo{
					name: data.Name,
					args: parseToolArguments(data.Arguments),
					time: at,
					seq:  ev.Seq,
				}
				if data.CallID != "" {
					callsByID[data.CallID] = info
				}
				if ev.HasSeq {
					callsBySeq[ev.Seq] = info
				}
			}
		case "tool/result":
			flush()
			var data struct {
				Message struct {
					Source struct {
						CallID string `json:"callId"`
					} `json:"source"`
					Content any `json:"content"`
				} `json:"message"`
			}
			_ = json.Unmarshal(ev.Data, &data)

			var call toolCallInfo
			var found bool
			if data.Message.Source.CallID != "" {
				call, found = callsByID[data.Message.Source.CallID]
			}
			if !found && len(ev.SourceEventSeqs) > 0 {
				for _, sSeq := range ev.SourceEventSeqs {
					if c, ok := callsBySeq[sSeq]; ok {
						call = c
						found = true
						break
					}
				}
			}

			if found {
				tTime := call.time
				if tTime.IsZero() {
					tTime = at
				}
				lowerName := strings.ToLower(call.name)
				switch lowerName {
				case "bash":
					if cmd := str(call.args["command"]); cmd != "" && IndexCommands() && worthIndexing(cmd) {
						s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: "$ " + cmd, Time: tTime})
						s.Touch(tTime)
					}
				case "read":
					p := str(call.args["file_path"])
					if p == "" {
						p = str(call.args["path"])
					}
					if p != "" && IndexToolPaths() {
						s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: p, Time: tTime})
						s.Touch(tTime)
					}
				case "write":
					p := str(call.args["file_path"])
					if p == "" {
						p = str(call.args["path"])
					}
					content := str(call.args["content"])
					if p != "" && IndexToolPaths() {
						s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: p, Time: tTime})
						s.Touch(tTime)
					}
					if p != "" && content != "" && IndexWrites() {
						if rec := WroteRecord(p, content); rec != "" {
							s.Messages = append(s.Messages, model.Message{Role: RoleWrote, Text: rec, Time: tTime})
							s.Touch(tTime)
						}
					}
				case "edit":
					p := str(call.args["file_path"])
					if p == "" {
						p = str(call.args["path"])
					}
					oldStr := str(call.args["old_string"])
					newStr := str(call.args["new_string"])
					if p != "" && IndexToolPaths() {
						s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: p, Time: tTime})
						s.Touch(tTime)
					}
					if p != "" && oldStr != "" && IndexEdits() {
						span := oldStr
						if len(span) > editSpanMax {
							span = span[:editSpanMax]
						}
						s.Messages = append(s.Messages, model.Message{Role: RoleEdit, Text: p + "\n" + span, Time: tTime})
						s.Touch(tTime)
					}
					if p != "" && newStr != "" && IndexWrites() {
						if rec := WroteRecord(p, newStr); rec != "" {
							s.Messages = append(s.Messages, model.Message{Role: RoleWrote, Text: rec, Time: tTime})
							s.Touch(tTime)
						}
					}
				default:
					p := str(call.args["file_path"])
					if p == "" {
						p = str(call.args["path"])
					}
					if p != "" && IndexToolPaths() {
						s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: p, Time: tTime})
						s.Touch(tTime)
					}
				}
			}

			if text := deepSeekToolText(data.Message.Content); text != "" && IndexToolOutput() {
				s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: text, Time: at})
				s.Touch(at)
			}
		}
	}
	flush()
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}

func parseToolArguments(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	if s, ok := v.(string); ok {
		var m map[string]any
		if json.Unmarshal([]byte(s), &m) == nil {
			return m
		}
	}
	return nil
}

// deepSeekSpokenByUser separates what a person typed from what a plugin spliced
// into the same event type. Without it every session opens on the sandbox
// policy snapshot and the skill catalogue, which is the harness describing
// itself.
func deepSeekSpokenByUser(data map[string]any) bool {
	source, _ := data["source"].(map[string]any)
	kind, _ := source["kind"].(string)
	return kind == "user"
}

func deepSeekContentText(v any) string {
	parts, ok := v.([]any)
	if !ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	}
	var out []string
	for _, p := range parts {
		block, ok := p.(map[string]any)
		if !ok {
			continue
		}
		// "reasoning" is the model thinking out loud; "text" is what it said.
		if t, _ := block["type"].(string); t != "" && t != "text" {
			continue
		}
		if text, _ := block["text"].(string); text != "" {
			out = append(out, text)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// deepSeekPackedTexts reads the texts of a packed chunk row. The row keeps the
// deltas verbatim in order; the timings beside them reconstruct each member's
// own clock, which a transcript does not need.
func deepSeekPackedTexts(v any) []string {
	parts, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if text, _ := p.(string); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// deepSeekToolText unwraps tool output, which nests one block array inside
// another: the outer block says which call this answers, the inner one carries
// the text the tool printed.
func deepSeekToolText(v any) string {
	blocks, ok := v.([]any)
	if !ok {
		return deepSeekContentText(v)
	}
	var out []string
	for _, b := range blocks {
		block, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if text := deepSeekContentText(block["content"]); text != "" {
			out = append(out, text)
			continue
		}
		if text, _ := block["text"].(string); text != "" {
			out = append(out, text)
			continue
		}
		if val, _ := block["value"].(string); val != "" {
			out = append(out, val)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// readDeepSeekLog returns the log as plain JSONL. The default encoding is a
// chain of zstd frames, and deja carries no Go dependencies, so the frames go
// through the same `zstd` CLI the Zed store already needs — see SkipReason for
// what a machine without it is told.
func readDeepSeekLog(path string) ([]byte, error) {
	raw, _, _, err := readDeepSeekBytes(path)
	return raw, err
}
