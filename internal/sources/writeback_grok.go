package sources

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Grok Build keeps a session in sessions/<url-encoded cwd>/<id>/: the ACP
// stream in updates.jsonl and the session's record in summary.json. `grok
// --resume <id>` rebuilds the model's history from updates.jsonl when
// chat_history.jsonl is missing, and refuses a session ("Session does not
// exist") unless summary.json carries info, session_summary, created_at,
// updated_at, num_messages, current_model_id and chat_format_version, all of
// which deja can fill. The model id is not in the index and is left empty;
// grok takes the one it is started with.
func init() {
	registerWriteBack("grok", func() []string { return []string{filepath.Join(GrokRoot(), "sessions")} }, renderGrok)
	registerWriteBackExtras("grok", grokSummarySidecar)
}

func renderGrok(s model.Session, turns []model.Message) (string, []byte, error) {
	if filepath.Base(s.Path) != "updates.jsonl" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "session comes from the grok-dev database (grok.db), which no grok command reopens"}
	}
	if filepath.Base(filepath.Dir(s.Path)) != s.ID {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no sessions/<dir>/<id>/updates.jsonl path for this session, which is where `grok --resume` looks"}
	}
	var lines []any
	prompt := 0
	for i, m := range turns {
		kind := "user_message_chunk"
		update := map[string]any{"content": map[string]any{"type": "text", "text": m.Text}}
		meta := map[string]any{"agentTimestampMs": m.Time.UnixMilli()}
		if m.Role == "assistant" {
			kind = "agent_message_chunk"
			// Its own prompt id per turn, so two answers in a row stay two.
			meta["promptId"] = writeBackUUID(s.ID, i)
		} else {
			update["_meta"] = map[string]any{"promptIndex": prompt}
			prompt++
		}
		update["sessionUpdate"] = kind
		lines = append(lines, map[string]any{
			"timestamp": m.Time.Unix(),
			"method":    "session/update",
			"params": map[string]any{
				"sessionId": s.ID,
				"update":    update,
				"_meta":     meta,
			},
		})
	}
	data, err := jsonLines(lines)
	return s.Path, data, err
}

func grokSummarySidecar(s model.Session, turns []model.Message, f WriteBackFile) (sidecars, appends []WriteBackPart, err error) {
	path := f.Path
	start, end := turns[0].Time, turns[len(turns)-1].Time
	if !s.Started.IsZero() && s.Started.Before(start) {
		start = s.Started
	}
	title := ""
	if !s.AgentTitle {
		title = strings.TrimSpace(s.Title)
	}
	doc := map[string]any{
		"info":                map[string]any{"id": s.ID, "cwd": grokCWDFromPath(path)},
		"session_summary":     title,
		"created_at":          writeBackStamp(start),
		"updated_at":          writeBackStamp(end),
		"num_messages":        len(turns),
		"current_model_id":    "",
		"chat_format_version": 1,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return []WriteBackPart{{Path: filepath.Join(filepath.Dir(path), "summary.json"), Data: append(data, '\n')}}, nil, nil
}
