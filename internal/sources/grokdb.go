package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The maintained grok CLI keeps no session files: everything lands in a
// SQLite database next to the config it shares with the other two CLIs. A
// deja that only walks ~/.grok/sessions sees none of that history.
func GrokDB() string {
	return EnvPath("DEJA_GROK_DB", filepath.Join(GrokRoot(), "grok.db"))
}

func LoadGrokDB() []model.Session {
	ss, err := ParseGrokDBSince(GrokDB(), time.Time{})
	if err != nil {
		return nil
	}
	return ss
}

// ParseGrokDBSince reads sessions whose messages changed after t. Message
// bodies are JSON in a text column, and the assistant side is an array of
// content blocks, so the text is pulled out here rather than in SQL.
func ParseGrokDBSince(db string, t time.Time) ([]model.Session, error) {
	// The sqlite3 CLI creates a missing database on open — never let it.
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	where := ""
	if !t.IsZero() {
		// Both sides normalised, the way the zed reader does it, because the
		// two are written in different shapes: grokDBTime reads sqlite's own
		// "2026-07-27 15:29:47" as happily as an RFC3339 string, and compared
		// as text a space sorts below the T — so on such a store every message
		// stamped later the same day read as older than the watermark and
		// nothing reached the index until the date rolled over (#2150, the
		// shape #2030 fixed for goose).
		//
		// strftime rather than datetime: datetime() drops the fraction, and a
		// message half a second after the watermark would compare equal to it
		// and be left out. A stamp sqlite cannot read at all normalises to
		// null, and those rows are kept rather than dropped — an unreadable
		// stamp is a reason to look at a message, not to hide it.
		//
		// Session-scoped, not message-scoped: what comes back replaces what the
		// index holds for that session, so a session that hands back only its
		// newest turn loses the rest (#2075, the shape goose describes in
		// ParseGooseDBSince).
		const norm = `strftime('%Y-%m-%dT%H:%M:%f',m2.created_at)`
		w := sqlEscape(millisecondBackoff(t))
		where = " and s.id in (select m2.session_id from messages m2 where " +
			norm + " is null or " + norm + " > '" + w + "')"
	}
	// json_object rather than the shell's -json mode, which is quadratic in
	// what it escapes — see sqliteRows.
	q := `select json_object('id',cast(s.id as text),'cwd',cast(s.cwd_last as text),'title',cast(s.title as text),` +
		`'role',cast(m.role as text),'body',cast(m.message_json as text),'at',cast(m.created_at as text)) ` +
		`from sessions s join messages m on m.session_id=s.id` +
		// tool rows hold the results of the assistant's calls (#4498).
		` where m.role in ('user','assistant','tool')` + where +
		` order by s.id,m.seq`
	cmd, stopRead := sqliteReadCmd(db, q)
	defer stopRead()
	dec, err := sqliteRows(cmd)
	if err != nil {
		return nil, err
	}
	by := map[string]*model.Session{}
	var order []string
	rows := 0
	for dec.More() {
		var r struct {
			ID    string `json:"id"`
			CWD   string `json:"cwd"`
			Title string `json:"title"`
			Role  string `json:"role"`
			Body  string `json:"body"`
			At    string `json:"at"`
		}
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("bad sqlite json: %w", err)
		}
		rows++
		if r.ID == "" {
			continue
		}
		text := ""
		if r.Role != "tool" {
			text = grokMessageText(r.Body)
		}
		at := grokDBTime(r.At)
		work := grokDBWork(r.Body, r.CWD, at)
		if strings.TrimSpace(text) == "" && len(work) == 0 {
			continue
		}
		s := by[r.ID]
		if s == nil {
			s = &model.Session{
				ID:      r.ID,
				Harness: "grok",
				Project: projectName(r.CWD),
				Path:    db,
				Title:   r.Title,
				Started: at,
			}
			by[r.ID] = s
			order = append(order, r.ID)
		}
		if s.Started.IsZero() || (!at.IsZero() && at.Before(s.Started)) {
			s.Started = at
		}
		if at.After(s.Updated) {
			s.Updated = at
		}
		if strings.TrimSpace(text) != "" {
			s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: text, Time: at})
		}
		s.Messages = append(s.Messages, work...)
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		if rows == 0 {
			// No stdout means two very different things: a query that matched
			// nothing, or one sqlite3 refused to run because the harness
			// changed its schema. Reporting the second as "no sessions" makes
			// a whole harness disappear from recall while doctor still calls
			// the store healthy.
			return nil, fmt.Errorf("grok: query failed, the store schema may have changed: %w", err)
		}
		return nil, err
	}
	out := make([]model.Session, 0, len(order))
	for _, id := range order {
		out = append(out, *by[id])
	}
	return out, nil
}

// grokMessageText handles both message shapes: a user message carries a plain
// string, an assistant message an array of typed blocks of which only text
// ones are worth indexing.
func grokMessageText(body string) string {
	var doc struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return ""
	}
	var plain string
	if err := json.Unmarshal(doc.Content, &plain); err == nil {
		return plain
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(doc.Content, &blocks); err != nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type != "text" || blk.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(blk.Text)
	}
	return b.String()
}

// grokDevDialect is grok-dev's tool vocabulary (dist/grok/tools.js in 1.1.7):
// bash {command}, read_file, write_file and edit_file under `path`.
var grokDevDialect = toolDialect{
	pathKey:   "path",
	pathTools: map[string]bool{"read_file": true, "write_file": true, "edit_file": true},
	shellTool: "bash",
	editTools: map[string]bool{"write_file": true, "edit_file": true},
}

// grokDBWork reads the work in one stored message. grok-dev keeps each turn as
// an AI SDK message: the assistant's calls are tool-call parts and each
// result a tool-result part on a row of its own. The reader kept text parts
// only, so a grok-dev session was its prompts and prose and nothing it did
// (#4498). Paths are relative to the session's cwd or absolute.
func grokDBWork(body, cwd string, at time.Time) []model.Message {
	var doc struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal([]byte(body), &doc) != nil {
		return nil
	}
	var blocks []struct {
		Type     string `json:"type"`
		ToolName string `json:"toolName"`
		// An object, or the raw string when the model's arguments were not
		// JSON — which must not cost the other parts of the message.
		Input  json.RawMessage `json:"input"`
		Output any             `json:"output"`
	}
	if json.Unmarshal(doc.Content, &blocks) != nil {
		return nil
	}
	var calls []any
	var outs []string
	for _, b := range blocks {
		switch b.Type {
		case "tool-call":
			var in map[string]any
			if b.ToolName == "" || json.Unmarshal(b.Input, &in) != nil || in == nil {
				continue
			}
			if p, _ := in["path"].(string); p != "" {
				in["path"] = resolveToolPath(p, cwd)
			}
			calls = append(calls, map[string]any{"type": "tool_use", "name": b.ToolName, "input": in})
		case "tool-result":
			if out := strings.TrimSpace(grokDBResultText(b.Output)); out != "" {
				outs = append(outs, out)
			}
		}
	}
	var out []model.Message
	if IndexToolPaths() {
		if p := toolPathsIn(calls, grokDevDialect); p != "" {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: at})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(calls, grokDevDialect) {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: at})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(calls, grokDevDialect) {
			out = append(out, model.Message{Role: RoleWrote, Text: w, Time: at})
		}
	}
	if IndexCommands() {
		for _, cmd := range commandsIn(calls, grokDevDialect) {
			out = append(out, model.Message{Role: RoleCommand, Text: cmd, Time: at})
		}
	}
	if IndexToolOutput() {
		for _, o := range outs {
			out = append(out, model.Message{Role: RoleToolOutput, Text: capParsedMessage(o), Time: at})
		}
	}
	return out
}

// grokDBResultText is what a tool printed: an AI SDK output is {type, value},
// where a json value is grok-dev's {success, output | error}.
func grokDBResultText(v any) string {
	o, _ := v.(map[string]any)
	switch val := o["value"].(type) {
	case string:
		return val
	case map[string]any:
		for _, k := range []string{"error", "output", "content"} {
			if s, _ := val[k].(string); strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

func grokDBTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
