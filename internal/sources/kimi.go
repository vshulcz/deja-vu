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
// events. Only the main agent is indexed; sub-agents, tool payloads and
// think-parts are skipped by design (issue #248).

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
