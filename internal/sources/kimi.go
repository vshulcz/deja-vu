package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Kimi Code (github.com/MoonshotAI/kimi-code) keeps everything under
// $KIMI_CODE_HOME (default ~/.kimi-code):
//
//	session_index.jsonl
//	sessions/<workDirKey>/<sessionId>/state.json
//	sessions/<workDirKey>/<sessionId>/agents/main/wire.jsonl
//
// wire.jsonl is the append-only main-agent transcript. User turns arrive as
// context.append_message records; streamed assistant turns never do — they
// have to be reconstructed from step.begin → content.part → step.end loop
// events. Tool calls and their results arrive as loop events too (tool.call,
// tool.result) and become work records (#655). Only the main agent is indexed;
// sub-agents and think-parts are skipped by design (issue #248).

// KimiConfigDir is the native Kimi Code home. DEJA_KIMI_ROOT intentionally
// does not affect it because that variable only relocates reads.
func KimiConfigDir() string { return EnvPath("KIMI_CODE_HOME", filepath.Join(Home(), ".kimi-code")) }

func KimiRoot() string { return EnvPath("DEJA_KIMI_ROOT", KimiConfigDir()) }

func KimiSessionFiles() []string {
	return walkFiles(filepath.Join(KimiRoot(), "sessions"), func(p string) bool {
		return filepath.Base(p) == "wire.jsonl" && filepath.Base(filepath.Dir(p)) == "main"
	})
}

func LoadKimi() []model.Session { return parseFiles(KimiSessionFiles(), ParseKimiFile) }

func ParseKimiFile(path string) ([]model.Session, error) {
	return parseKimiFileFromOffset(path, 0)
}

func ParseKimiFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseKimiFileFromOffset(path, offset)
}

// kimiDialect is Kimi Code's tool vocabulary, read off wire.jsonl transcripts
// its own CLI had just written. The names mirror Claude Code's — Kimi Code is
// built in that shape — but the file key is `path` where Claude's is
// `file_path`, and only Edit carries a replaced span; there is no MultiEdit.
// ReadMediaFile is Kimi's own path tool. The shell tool's argument key is
// `command`, which is what commandsIn already reads.
var kimiDialect = toolDialect{
	pathKey:   "path",
	pathTools: map[string]bool{"Read": true, "Edit": true, "Write": true, "ReadMediaFile": true},
	shellTool: "Bash",
	editTools: map[string]bool{"Edit": true},
}

// kimiState is the slice of state.json deja cares about.
type kimiState struct {
	Title     string `json:"title"`
	WorkDir   string `json:"workDir"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func parseKimiFileFromOffset(path string, offset int64) ([]model.Session, error) {
	// .../sessions/<workDirKey>/<sessionId>/agents/main/wire.jsonl
	sessionDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	s := model.Session{
		Harness: "kimi",
		ID:      filepath.Base(sessionDir),
		Path:    path,
	}
	var st kimiState
	if b, err := os.ReadFile(filepath.Join(sessionDir, "state.json")); err == nil {
		if json.Unmarshal(b, &st) == nil {
			s.Title = strings.TrimSpace(st.Title)
			s.Project = projectName(st.WorkDir)
			s.Touch(parseTimeAny(st.CreatedAt))
			s.Touch(parseTimeAny(st.UpdatedAt))
		}
	}
	// Streamed assistant text accumulates across content.part events and is
	// flushed on step.end — or at EOF, so a response mid-stream when the
	// indexer runs is not lost (the remainder lands on the next incremental
	// pass as its own message).
	var pending strings.Builder
	var pendingTime any
	flush := func() {
		if text := strings.TrimSpace(pending.String()); text != "" {
			s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: parseTimeAny(pendingTime)})
		}
		pending.Reset()
		pendingTime = nil
	}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		switch m["type"] {
		case "context.append_message":
			msg, _ := m["message"].(map[string]any)
			if msg == nil {
				return
			}
			role, _ := msg["role"].(string)
			if role != "user" && role != "assistant" {
				return
			}
			if role == "assistant" {
				// Non-streamed assistant records exist in older protocols;
				// close any open step first to keep message order stable.
				flush()
			}
			text := kimiText(msg["content"])
			t := parseTimeAny(m["time"])
			s.Touch(t)
			if text != "" {
				s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
			}
		case "context.append_loop_event":
			e, _ := m["event"].(map[string]any)
			if e == nil {
				return
			}
			switch e["type"] {
			case "step.begin":
				flush()
			case "content.part":
				p, _ := e["part"].(map[string]any)
				if p == nil || p["type"] != "text" {
					return
				}
				text, _ := p["text"].(string)
				if text == "" {
					return
				}
				if t := parseTimeAny(m["time"]); !t.IsZero() {
					s.Touch(t)
					if pendingTime == nil {
						pendingTime = m["time"]
					}
				}
				pending.WriteString(text)
			case "step.end":
				flush()
			case "tool.call":
				// A call sits between the text parts of its step, so the
				// pending stream is flushed before the record to keep the
				// words that led to the call ahead of it. The flush happens
				// only when the call yields a record: with every DEJA_INDEX_*
				// switch off, the messages come out exactly as they did
				// before tool events were read.
				name, _ := e["name"].(string)
				args, _ := e["args"].(map[string]any)
				if name == "" || args == nil {
					return
				}
				part := []any{map[string]any{"type": "tool_use", "name": name, "input": args}}
				t := parseTimeAny(m["time"])
				var records []model.Message
				if IndexToolPaths() {
					if p := toolPathsIn(part, kimiDialect); p != "" {
						records = append(records, model.Message{Role: RoleFiles, Text: p, Time: t})
					}
				}
				if IndexEdits() {
					for _, span := range editSpansIn(part, kimiDialect) {
						records = append(records, model.Message{Role: RoleEdit, Text: span, Time: t})
					}
				}
				if IndexCommands() {
					for _, cmd := range commandsIn(part, kimiDialect) {
						records = append(records, model.Message{Role: RoleCommand, Text: cmd, Time: t})
					}
				}
				if len(records) == 0 {
					return
				}
				flush()
				s.Touch(t)
				s.Messages = append(s.Messages, records...)
			case "tool.result":
				// Every observed result shape carries its text under
				// `output` — plain, with a note, truncated, or flagged
				// isError. The error ones are kept on purpose: the error a
				// command hit is exactly what a later search reaches for.
				if !IndexToolOutput() {
					return
				}
				r, _ := e["result"].(map[string]any)
				out, _ := r["output"].(string)
				if out = strings.TrimSpace(out); out == "" {
					return
				}
				t := parseTimeAny(m["time"])
				flush()
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: out, Time: t})
			}
		}
	})
	flush()
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

// kimiText joins the text parts of an append_message content array.
func kimiText(v any) string {
	parts, ok := v.([]any)
	if !ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	}
	var b strings.Builder
	for _, pv := range parts {
		p, ok := pv.(map[string]any)
		if !ok || p["type"] != "text" {
			continue
		}
		if t, _ := p["text"].(string); t != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(t)
		}
	}
	return strings.TrimSpace(b.String())
}
