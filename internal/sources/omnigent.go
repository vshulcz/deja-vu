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

// Omnigent (github.com/omnigent-ai/omnigent) keeps every conversation and
// sub-agent run in one SQLite database at `~/.omnigent/chat.db` (relocated by
// `OMNIGENT_DATA_DIR`, verified against `omnigent/config.py`,
// `omnigent/db/models/conversation.py`, and `omnigent/db/enum_codecs.py`).
//
//	conversations(workspace_id, id, created_at, updated_at, title,
//	              parent_conversation_id, fork_from_item_id)
//	omnigent_conversation_metadata(workspace_id, id, workspace, kind,
//	                               task_summary, project_id)
//	projects(workspace_id, id, name)
//	conversation_items(workspace_id, conversation_id, position, id, created_at,
//	                   type, data)
//
// `id`, `parent_conversation_id`, `project_id`, and `conversation_id` are
// stored as 16-byte UUID blobs (`Uuid16`) or hex strings. `created_at` and
// `updated_at` are unix seconds. `conversation_items.type` is an integer code
// (`enum_codecs.py`):
//
//	1 = message             {"role":"user|assistant","content":[{"type","text"}],"is_meta":bool}
//	2 = function_call       {"name","arguments","call_id"}
//	3 = function_call_output{"call_id","output"}
//	6 = compaction          {"summary"}
//	11 = terminal_command   {"kind":"input|output","input","stdout","stderr"}

// OmnigentRoot returns Omnigent's data directory. `DEJA_OMNIGENT_ROOT` or
// `OMNIGENT_DATA_DIR` overrides `~/.omnigent`.
func OmnigentRoot() string {
	if p := strings.TrimSpace(os.Getenv("DEJA_OMNIGENT_ROOT")); p != "" {
		return expandTilde(p)
	}
	if p := strings.TrimSpace(os.Getenv("OMNIGENT_DATA_DIR")); p != "" {
		return expandTilde(p)
	}
	return filepath.Join(Home(), ".omnigent")
}

// OmnigentDB is Omnigent's SQLite store. `DEJA_OMNIGENT_DB` overrides it.
func OmnigentDB() string {
	return EnvPath("DEJA_OMNIGENT_DB", filepath.Join(OmnigentRoot(), "chat.db"))
}

// OmnigentFiles returns OmnigentDB when it exists and is non-empty.
func OmnigentFiles() []string {
	if fi, err := os.Stat(OmnigentDB()); err == nil && fi.Size() > 0 {
		return []string{OmnigentDB()}
	}
	return nil
}

// LoadOmnigent reads every Omnigent conversation on this machine.
func LoadOmnigent() []model.Session {
	db := OmnigentDB()
	ss, err := ParseOmnigentDB(db)
	return dbRead(db, ss, err)
}

// ParseOmnigentDB reads every conversation in the Omnigent SQLite store at db.
func ParseOmnigentDB(db string) ([]model.Session, error) {
	return parseOmnigentDBWhere(db, "")
}

// ParseOmnigentDBSince reads Omnigent conversations updated after t.
func ParseOmnigentDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return ParseOmnigentDB(db)
	}
	return parseOmnigentDBWhere(db, fmt.Sprintf(" where c.updated_at > %d", t.Add(-time.Second).Unix()))
}

func omnigentUUID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if len(s) == 32 && isHexString(s) {
		return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
	}
	return strings.TrimSpace(raw)
}

func isHexString(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return len(s) > 0
}

func omnigentHexExpr(col string) string {
	return fmt.Sprintf("case when typeof(%s)='blob' then lower(hex(%s)) else coalesce(cast(%s as text),'') end", col, col, col)
}

func omnigentDBTableExists(db, table string) bool {
	return devinDBTableExists(db, table)
}

func parseOmnigentDBWhere(db, where string) ([]model.Session, error) {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	hasMeta := omnigentDBTableExists(db, "omnigent_conversation_metadata")
	hasProjects := omnigentDBTableExists(db, "projects")

	wsExpr := "''"
	kindExpr := "1"
	summaryExpr := "''"
	joinMeta := ""
	if hasMeta {
		wsExpr = "coalesce(m.workspace,'')"
		kindExpr = "coalesce(m.kind,1)"
		summaryExpr = "coalesce(m.task_summary,'')"
		joinMeta = " left join omnigent_conversation_metadata m on m.workspace_id = c.workspace_id and m.id = c.id"
		if hasProjects {
			wsExpr = "coalesce(nullif(m.workspace,''), nullif(p.name,''), '')"
			joinMeta += " left join projects p on p.workspace_id = m.workspace_id and p.id = m.project_id"
		}
	}

	q := `select json_object(` +
		`'id',` + omnigentHexExpr("c.id") + `,` +
		`'parent',` + omnigentHexExpr("c.parent_conversation_id") + `,` +
		`'title',coalesce(c.title,''),` +
		`'created',coalesce(c.created_at,0),` +
		`'updated',coalesce(c.updated_at,0),` +
		`'workspace',` + wsExpr + `,` +
		`'kind',` + kindExpr + `,` +
		`'summary',` + summaryExpr + `)` +
		` from conversations c` + joinMeta + where + ` order by c.created_at`

	cmd, stopRead := sqliteReadCmd(db, q)
	defer stopRead()
	dec, err := sqliteRows(cmd)
	if err != nil {
		return nil, err
	}
	var out []model.Session
	rows := 0
	for dec.More() {
		var r struct {
			ID        string `json:"id"`
			Parent    string `json:"parent"`
			Title     string `json:"title"`
			Created   int64  `json:"created"`
			Updated   int64  `json:"updated"`
			Workspace string `json:"workspace"`
			Kind      int    `json:"kind"`
			Summary   string `json:"summary"`
		}
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("bad sqlite json: %w", err)
		}
		rows++
		id := omnigentUUID(r.ID)
		if id == "" {
			continue
		}
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = strings.TrimSpace(r.Summary)
		}
		proj := "omnigent"
		if ws := strings.TrimSpace(r.Workspace); ws != "" {
			proj = projectName(ws)
		}
		s := model.Session{
			Harness: "omnigent",
			ID:      id,
			Path:    db,
			Project: proj,
			Title:   title,
		}
		parent := omnigentUUID(r.Parent)
		if r.Kind == 2 || parent != "" {
			s.Kind = model.KindSubagent
			s.Parent = parent
		}
		if err := omnigentLoadItems(&s, db, r.ID); err != nil {
			_ = cmd.Wait()
			return nil, err
		}
		if len(s.Messages) == 0 {
			continue
		}
		if r.Created > 0 {
			s.Touch(time.Unix(r.Created, 0).UTC())
		}
		if r.Updated > 0 {
			s.Touch(time.Unix(r.Updated, 0).UTC())
		}
		if s.Title == "" {
			for _, m := range s.Messages {
				if m.Role == "user" && strings.TrimSpace(m.Text) != "" {
					s.Title = strings.TrimSpace(strings.SplitN(strings.TrimSpace(m.Text), "\n", 2)[0])
					break
				}
			}
		}
		out = append(out, s)
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		if rows == 0 {
			if !omnigentDBHasTable(db) {
				return nil, nil
			}
			return nil, schemaChanged("omnigent", err)
		}
		return nil, err
	}
	return ApplySubagentSwitch(out), nil
}

func omnigentContentText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var out []string
		for _, it := range c {
			switch x := it.(type) {
			case string:
				if strings.TrimSpace(x) != "" {
					out = append(out, x)
				}
			case map[string]any:
				for _, k := range []string{"text", "input_text", "output_text", "content"} {
					if t, _ := x[k].(string); strings.TrimSpace(t) != "" {
						out = append(out, t)
						break
					}
				}
			}
		}
		return strings.Join(out, "\n")
	case map[string]any:
		if t, _ := c["text"].(string); t != "" {
			return t
		}
	}
	return ""
}

func omnigentDBHasTable(db string) bool {
	return omnigentDBTableExists(db, "conversations")
}

var omnigentDialect = toolDialect{
	pathKey:    "file_path",
	pathKeyAlt: "path",
	pathTools: map[string]bool{
		"read": true, "read_file": true, "write": true, "write_file": true,
		"edit": true, "edit_file": true, "apply_patch": true,
	},
	shellTools: map[string]bool{
		"bash": true, "shell": true, "exec_command": true, "terminal": true,
	},
	editTools: map[string]bool{
		"edit": true, "edit_file": true, "write": true, "write_file": true, "apply_patch": true,
	},
}

func omnigentLoadItems(s *model.Session, db, rawID string) error {
	esc := sqlEscape(rawID)
	cond := fmt.Sprintf("(%s = '%s' or %s = '%s')",
		omnigentHexExpr("conversation_id"), strings.ToLower(esc),
		omnigentHexExpr("conversation_id"), sqlEscape(omnigentUUID(rawID)))
	q := `select json_object('type',type,'created',coalesce(created_at,0),'data',coalesce(data,''))` +
		` from conversation_items where ` + cond + ` order by position asc`
	out, err := sqliteOutput(db, q)
	if err != nil {
		return err
	}
	type itemRow struct {
		Type    any    `json:"type"`
		Created int64  `json:"created"`
		Data    string `json:"data"`
	}
	rows, err := sqliteObjects[itemRow](out)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var t time.Time
		if r.Created > 0 {
			t = time.Unix(r.Created, 0).UTC()
		}
		switch omnigentItemType(r.Type) {
		case 1: // message
			var m struct {
				Role    string `json:"role"`
				IsMeta  bool   `json:"is_meta"`
				Content any    `json:"content"`
			}
			if json.Unmarshal([]byte(r.Data), &m) != nil || m.IsMeta {
				continue
			}
			role := strings.ToLower(strings.TrimSpace(m.Role))
			if role != "user" && role != "assistant" {
				continue
			}
			txt := strings.TrimSpace(omnigentContentText(m.Content))
			if txt == "" {
				continue
			}
			if !t.IsZero() {
				s.Touch(t)
			}
			s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
		case 2: // function_call
			var fc struct {
				Name      string `json:"name"`
				Arguments any    `json:"arguments"`
				CallID    string `json:"call_id"`
			}
			if json.Unmarshal([]byte(r.Data), &fc) != nil || fc.Name == "" {
				continue
			}
			args := devinCallArgs(fc.Arguments)
			if args == nil {
				continue
			}
			call := map[string]any{"type": "tool_use", "name": strings.ToLower(fc.Name), "input": args}
			if fc.CallID != "" {
				call["id"] = fc.CallID
			}
			if work := omnigentWorkRecords([]any{call}, t); len(work) > 0 {
				if !t.IsZero() {
					s.Touch(t)
				}
				s.Messages = append(s.Messages, work...)
			}
		case 3: // function_call_output
			if !IndexToolOutput() {
				continue
			}
			var fo struct {
				Output any `json:"output"`
			}
			if json.Unmarshal([]byte(r.Data), &fo) != nil {
				continue
			}
			txt := strings.TrimSpace(omnigentContentText(fo.Output))
			if txt == "" {
				continue
			}
			if !t.IsZero() {
				s.Touch(t)
			}
			s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(txt), Time: t})
		case 6: // compaction
			var cp struct {
				Summary string `json:"summary"`
			}
			if json.Unmarshal([]byte(r.Data), &cp) != nil {
				continue
			}
			if sum := strings.TrimSpace(cp.Summary); sum != "" {
				if !t.IsZero() {
					s.Touch(t)
				}
				s.Messages = append(s.Messages, model.Message{Role: RoleSummary, Text: capParsedMessage(sum), Time: t})
			}
		case 11: // terminal_command
			var tc struct {
				Kind   string `json:"kind"`
				Input  string `json:"input"`
				Stdout string `json:"stdout"`
				Stderr string `json:"stderr"`
			}
			if json.Unmarshal([]byte(r.Data), &tc) != nil {
				continue
			}
			switch tc.Kind {
			case "input":
				if !IndexCommands() {
					continue
				}
				cmd := strings.TrimSpace(tc.Input)
				if !worthIndexing(cmd) {
					continue
				}
				if !t.IsZero() {
					s.Touch(t)
				}
				s.Messages = append(s.Messages, model.Message{Role: RoleCommand, Text: "$ " + cmd, Time: t})
			case "output":
				if !IndexToolOutput() {
					continue
				}
				txt := strings.TrimSpace(strings.TrimSpace(tc.Stdout) + "\n" + strings.TrimSpace(tc.Stderr))
				if txt == "" {
					continue
				}
				if !t.IsZero() {
					s.Touch(t)
				}
				s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(txt), Time: t})
			}
		}
	}
	return nil
}

func omnigentItemType(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return int(n)
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "message", "1":
			return 1
		case "function_call", "2":
			return 2
		case "function_call_output", "3":
			return 3
		case "compaction", "6":
			return 6
		case "terminal_command", "11":
			return 11
		}
	}
	return 0
}

func omnigentWorkRecords(calls []any, t time.Time) []model.Message {
	if len(calls) == 0 {
		return nil
	}
	var out []model.Message
	if IndexToolPaths() {
		if p := toolPathsIn(calls, omnigentDialect); p != "" {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: t})
		}
	}
	if IndexWrites() {
		for _, w := range wroteRecordsIn(calls, omnigentDialect) {
			out = append(out, model.Message{Role: RoleWrote, Text: w, Time: t})
		}
	}
	if IndexEdits() {
		for _, span := range editSpansIn(calls, omnigentDialect) {
			out = append(out, model.Message{Role: RoleEdit, Text: span, Time: t})
		}
	}
	if IndexCommands() {
		for _, cmd := range commandsIn(calls, omnigentDialect) {
			out = append(out, model.Message{Role: RoleCommand, Text: cmd, Time: t})
		}
	}
	return out
}

// OmnigentSessionDir returns the workspace recorded for conversation id in db.
func OmnigentSessionDir(db, id string) string {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 || !omnigentDBTableExists(db, "omnigent_conversation_metadata") {
		return ""
	}
	esc := sqlEscape(id)
	hexID := strings.ReplaceAll(strings.ToLower(esc), "-", "")
	cond := fmt.Sprintf("(%s = '%s' or %s = '%s')",
		omnigentHexExpr("id"), hexID,
		omnigentHexExpr("id"), strings.ToLower(esc))
	q := `select json_object('dir',coalesce(workspace,'')) from omnigent_conversation_metadata where ` + cond + ` limit 1`
	out, err := sqliteOutput(db, q)
	if err != nil {
		return ""
	}
	type row struct {
		Dir string `json:"dir"`
	}
	rows, err := sqliteObjects[row](out)
	if err != nil || len(rows) == 0 {
		return ""
	}
	return strings.TrimSpace(rows[0].Dir)
}
