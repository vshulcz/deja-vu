package sources

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Copilot CLI keeps a session in session-state/<id>/events.jsonl, one event
// per line, each with a UUID id chained by parentId. `copilot --resume=<id>`
// rebuilds the conversation from the user.message and assistant.message
// events and writes workspace.yaml and the rest itself. It checks the
// envelope strictly: a non-UUID id or a session.start without copilotVersion
// is "corrupted". The directory the session ran in comes from Copilot's own
// session-store.db, which keeps a row after the session's directory is gone.

func init() {
	registerWriteBack("copilot", func() []string { return []string{CopilotRoot()} }, renderCopilot)
}

var copilotSessionIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func renderCopilot(s model.Session, turns []model.Message) (string, []byte, error) {
	if filepath.Base(s.Path) != "events.jsonl" || filepath.Base(filepath.Dir(s.Path)) != s.ID || !copilotSessionIDPattern.MatchString(s.ID) {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no session-state/<id>/events.jsonl path for this session, which is where Copilot CLI looks"}
	}
	start := turns[0].Time
	if !s.Started.IsZero() && s.Started.Before(start) {
		start = s.Started
	}
	context := map[string]any{}
	if cwd := copilotStoreCWD(filepath.Dir(filepath.Dir(filepath.Dir(s.Path))), s.ID); cwd != "" {
		context["cwd"] = cwd
	}
	id := writeBackUUID(s.ID, 0)
	lines := []any{map[string]any{
		"type": "session.start",
		"data": map[string]any{
			"sessionId":       s.ID,
			"version":         1,
			"producer":        "copilot-agent",
			"copilotVersion":  "1.0.79",
			"startTime":       writeBackStamp(start),
			"contextTier":     nil,
			"context":         context,
			"alreadyInUse":    false,
			"remoteSteerable": false,
		},
		"id":        id,
		"timestamp": writeBackStamp(start),
		"parentId":  nil,
	}}
	parent := id
	for i, m := range turns {
		id := writeBackUUID(s.ID, i+1)
		ev := map[string]any{"id": id, "timestamp": writeBackStamp(m.Time), "parentId": parent}
		if m.Role == "user" {
			ev["type"] = "user.message"
			ev["data"] = map[string]any{"content": m.Text}
		} else {
			ev["type"] = "assistant.message"
			ev["data"] = map[string]any{"messageId": writeBackUUID(s.ID+":message", i), "content": m.Text, "toolRequests": []any{}}
		}
		lines = append(lines, ev)
		parent = id
	}
	data, err := jsonLines(lines)
	return s.Path, data, err
}

// copilotStoreCWD is the directory session-store.db in Copilot's home recorded
// for a session, or "".
func copilotStoreCWD(home, id string) string {
	if !copilotSessionIDPattern.MatchString(id) {
		return ""
	}
	out, err := sqliteOutput(filepath.Join(home, "session-store.db"), "select cwd from sessions where id='"+id+"' limit 1")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
