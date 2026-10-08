package sources

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// kiro-cli writes two file layouts, and both are written back:
//
//	cli/<id>.jsonl + cli/<id>.json            kiro-cli (v2)
//	<workspace>/sess_<id>/messages.jsonl
//	         + <workspace>/sess_<id>/session.json  kiro-cli --v3
//
// The directory the session ran in is not in the index. kiro-cli 2.22.0
// takes an empty cwd and rewrites it to the directory it is resumed from, and
// --v3 opens a session whose workspacePaths is empty in the current one.
//
// kiro-cli refuses a history that does not alternate ("invalid conversation
// history received"), and the index drops the tool calls that sat between two
// answers, so a run of turns from one side goes back as one turn, a reply
// before the first prompt is left out, and so is a last prompt nobody
// answered.
//
// A conversation in kiro-cli's SQLite store (data.sqlite3) is a row holding
// the model's whole request state, and a sess_ session the v3 index does not
// list is the Kiro IDE's; neither is written.
func init() {
	registerWriteBack("kiro", func() []string { return []string{KiroRoot()} }, renderKiro)
	registerWriteBackExtras("kiro", kiroHeaderSidecar)
}

func renderKiro(s model.Session, turns []model.Message) (string, []byte, error) {
	if s.Path == KiroDB() || !strings.HasSuffix(s.Path, ".jsonl") {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "kiro-cli keeps this conversation in its SQLite store (data.sqlite3), as a row of the model's full request state the index does not hold"}
	}
	turns = kiroAlternating(turns)
	if len(turns) == 0 {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index holds no prompt with an answer for this session, and kiro-cli refuses a history without one"}
	}
	var lines []any
	switch {
	case kiroUnderSessDir(s.Path):
		if !KiroV3Session(s.Path) {
			return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "session belongs to the Kiro IDE, which reopens it from its own history, not from a file deja can write"}
		}
		if filepath.Base(s.Path) != "messages.jsonl" || filepath.Base(filepath.Dir(s.Path)) != s.ID {
			return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no <workspace>/<id>/messages.jsonl path for this session, which is where kiro-cli --v3 looks"}
		}
		for i, m := range turns {
			payload := map[string]any{"type": m.Role, "content": m.Text}
			if m.Role == "user" {
				payload["images"] = []any{}
				payload["documents"] = []any{}
			}
			lines = append(lines, map[string]any{"id": writeBackUUID(s.ID, i), "timestamp": writeBackStamp(m.Time), "payload": payload})
		}
	default:
		if filepath.Base(s.Path) != s.ID+".jsonl" || filepath.Base(filepath.Dir(s.Path)) != "cli" {
			return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no cli/<id>.jsonl path for this session, which is where kiro-cli looks"}
		}
		for i, m := range turns {
			data := map[string]any{
				"message_id": writeBackUUID(s.ID, i),
				"content":    []any{map[string]any{"kind": "text", "data": m.Text}},
			}
			kind := "AssistantMessage"
			if m.Role == "user" {
				// kiro-cli 2.22.0 stamps the prompt only.
				kind = "Prompt"
				data["meta"] = map[string]any{"timestamp": m.Time.Unix()}
			}
			lines = append(lines, map[string]any{"version": "v1", "kind": kind, "data": data})
		}
	}
	data, err := jsonLines(lines)
	return s.Path, data, err
}

// kiroAlternating joins runs of turns from one side and drops a reply before
// the first prompt and a prompt after the last reply.
func kiroAlternating(turns []model.Message) []model.Message {
	var out []model.Message
	for _, m := range turns {
		if len(out) == 0 && m.Role != "user" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			out[n-1].Text += "\n\n" + m.Text
			continue
		}
		out = append(out, m)
	}
	if n := len(out); n > 0 && out[n-1].Role == "user" {
		out = out[:n-1]
	}
	return out
}

func kiroHeaderSidecar(s model.Session, turns []model.Message, f WriteBackFile) (sidecars, appends []WriteBackPart, err error) {
	path := f.Path
	if t := kiroAlternating(turns); len(t) > 0 {
		turns = t
	}
	start, end := turns[0].Time, turns[len(turns)-1].Time
	if !s.Started.IsZero() && s.Started.Before(start) {
		start = s.Started
	}
	title := any(nil)
	if t := strings.TrimSpace(s.Title); t != "" && !s.AgentTitle {
		title = t
	}
	var doc map[string]any
	var side string
	if kiroUnderSessDir(path) {
		side = filepath.Join(filepath.Dir(path), "session.json")
		doc = map[string]any{
			"schemaVersion":    "1.0.0",
			"dataModelVersion": 1,
			"id":               s.ID,
			"title":            title,
			"agentMode":        "vibe",
			"workspacePaths":   []any{},
			"rootPaths":        []any{},
			"createdAt":        writeBackStamp(start),
			"lastModifiedAt":   writeBackStamp(end),
		}
	} else {
		side = strings.TrimSuffix(path, ".jsonl") + ".json"
		// The header kiro-cli 2.22.0 writes for a new session.
		doc = map[string]any{
			"session_id": s.ID,
			"cwd":        "",
			"created_at": writeBackStamp(start),
			"updated_at": writeBackStamp(end),
			"title":      title,
			"session_state": map[string]any{
				"version": "v1",
				"conversation_metadata": map[string]any{
					"user_turn_metadatas":     []any{},
					"last_context_usage":      nil,
					"user_turn_start_request": nil,
					"last_request":            nil,
				},
				"rts_model_state": map[string]any{
					"conversation_id":          s.ID,
					"model_info":               nil,
					"context_usage_percentage": nil,
				},
				"permissions": map[string]any{
					"filesystem": map[string]any{
						"allowed_read_paths":  []any{},
						"allowed_write_paths": []any{},
						"denied_read_paths":   []any{},
						"denied_write_paths":  []any{},
					},
					"trusted_tools":    []any{},
					"denied_tools":     []any{},
					"allowed_commands": []any{},
				},
				"agent_name": nil,
				"goal":       nil,
			},
		}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return []WriteBackPart{{Path: side, Data: append(data, '\n')}}, nil, nil
}
