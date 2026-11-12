package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Orca (github.com/stablyai/orca) is an Electron agent orchestrator that
// persists its unified session journal in a SQLite database named
// `agent-session-journal.db` under Electron's `userData` directory
// (`packages/SqliteDatabaseHost/src/journal-database-schema.ts`,
// `packages/Shared/src/agent-session-record.ts`,
// `packages/Shared/src/agent-journal-schemas.ts`).
//
//	agent_session_records(session_id TEXT PRIMARY KEY, record_json TEXT NOT NULL)
//	journal_sessions(session_id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, epoch TEXT NOT NULL)
//	journal_rows(session_id TEXT NOT NULL, epoch TEXT NOT NULL, seq INTEGER NOT NULL,
//	             ts INTEGER NOT NULL, row_json TEXT NOT NULL,
//	             PRIMARY KEY (session_id, epoch, seq))
//
// `record_json` carries `{id, workspaceId, cwd, title, createdAt, updatedAt}`
// with millisecond timestamps. `row_json` is an `AgentJournalRenderItem`
// (`{itemId, revision, observedAt, sequence, body}`) where streaming updates to
// an item share `itemId` and increment `revision`; keeping the highest revision
// per `itemId` reconstructs the settled transcript.

// OrcaRoot returns Orca's `userData` directory on this platform.
// `DEJA_ORCA_ROOT` overrides it.
func OrcaRoot() string {
	if p := strings.TrimSpace(os.Getenv("DEJA_ORCA_ROOT")); p != "" {
		return expandTilde(p)
	}
	return orcaDataDir(runtime.GOOS)
}

func orcaDataDir(goos string) string {
	var base string
	switch goos {
	case "darwin":
		base = filepath.Join(Home(), "Library", "Application Support")
	case "windows":
		base = os.Getenv("APPDATA")
		if base == "" {
			base = filepath.Join(Home(), "AppData", "Roaming")
		}
	default:
		base = os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(Home(), ".config")
		}
	}
	lower := filepath.Join(base, "orca")
	if _, err := os.Stat(lower); err == nil {
		return lower
	}
	if upper := filepath.Join(base, "Orca"); goos != "darwin" {
		if _, err := os.Stat(upper); err == nil {
			return upper
		}
	}
	return lower
}

// OrcaDB returns the path to Orca's `agent-session-journal.db`.
// `DEJA_ORCA_DB` overrides it outright.
func OrcaDB() string {
	return EnvPath("DEJA_ORCA_DB", filepath.Join(OrcaRoot(), "agent-session-journal.db"))
}

// OrcaFiles returns OrcaDB when it exists and is non-empty.
func OrcaFiles() []string {
	if fi, err := os.Stat(OrcaDB()); err == nil && fi.Size() > 0 {
		return []string{OrcaDB()}
	}
	return nil
}

// LoadOrca reads every Orca session on this machine.
func LoadOrca() []model.Session {
	db := OrcaDB()
	ss, err := ParseOrcaDB(db)
	return dbRead(db, ss, err)
}

// ParseOrcaDB reads every session in Orca's journal database at db.
func ParseOrcaDB(db string) ([]model.Session, error) {
	return parseOrcaDBWhere(db, "")
}

// ParseOrcaDBSince reads Orca sessions updated after t.
func ParseOrcaDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return ParseOrcaDB(db)
	}
	ms := t.Add(-time.Second).UnixMilli()
	where := fmt.Sprintf(" where coalesce(json_extract(r.record_json,'$.updatedAt'),0) > %d"+
		" or exists (select 1 from journal_rows jr where jr.session_id = r.session_id and jr.ts > %d)", ms, ms)
	return parseOrcaDBWhere(db, where)
}

func parseOrcaDBWhere(db, where string) ([]model.Session, error) {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	hasJS := devinDBTableExists(db, "journal_sessions")
	epochExpr := "''"
	joinJS := ""
	if hasJS {
		epochExpr = "coalesce(js.epoch,'')"
		joinJS = " left join journal_sessions js on js.session_id = r.session_id"
	}
	q := `select json_object(` +
		`'id',r.session_id,` +
		`'epoch',` + epochExpr + `,` +
		`'rec',r.record_json)` +
		` from agent_session_records r` + joinJS + where + ` order by r.rowid`

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
			ID    string `json:"id"`
			Epoch string `json:"epoch"`
			Rec   string `json:"rec"`
		}
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("bad sqlite json: %w", err)
		}
		rows++
		var rec struct {
			ID        string `json:"id"`
			Workspace string `json:"workspaceId"`
			Cwd       string `json:"cwd"`
			Title     string `json:"title"`
			CreatedAt int64  `json:"createdAt"`
			UpdatedAt int64  `json:"updatedAt"`
		}
		_ = json.Unmarshal([]byte(r.Rec), &rec)
		sid := strings.TrimSpace(r.ID)
		if sid == "" {
			sid = strings.TrimSpace(rec.ID)
		}
		if sid == "" {
			continue
		}
		proj := "orca"
		if cwd := strings.TrimSpace(rec.Cwd); cwd != "" {
			proj = projectName(cwd)
		} else if ws := strings.TrimSpace(rec.Workspace); ws != "" {
			proj = projectName(ws)
		}
		s := model.Session{
			Harness: "orca",
			ID:      sid,
			Path:    db,
			Project: proj,
			Title:   strings.TrimSpace(rec.Title),
		}
		if err := orcaLoadJournalRows(&s, db, sid, r.Epoch); err != nil {
			_ = cmd.Wait()
			return nil, err
		}
		if len(s.Messages) == 0 {
			continue
		}
		if rec.CreatedAt > 0 {
			s.Touch(time.UnixMilli(rec.CreatedAt).UTC())
		}
		if rec.UpdatedAt > 0 {
			s.Touch(time.UnixMilli(rec.UpdatedAt).UTC())
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
			if !orcaDBHasTable(db) {
				return nil, nil
			}
			return nil, schemaChanged("orca", err)
		}
		return nil, err
	}
	return ApplySubagentSwitch(out), nil
}

func orcaDBHasTable(db string) bool {
	out, err := sqliteOutput(db, "select count(*) from sqlite_master where type='table' and name='agent_session_records'")
	return err != nil || strings.TrimSpace(string(out)) != "0"
}

func orcaLoadJournalRows(s *model.Session, db, sid, epoch string) error {
	where := fmt.Sprintf("session_id = '%s'", sqlEscape(sid))
	if epoch != "" {
		where += fmt.Sprintf(" and epoch = '%s'", sqlEscape(epoch))
	}
	q := `select json_object('seq',seq,'ts',ts,'row',row_json) from journal_rows where ` + where + ` order by seq asc`
	out, err := sqliteOutput(db, q)
	if err != nil {
		return err
	}
	type rawRow struct {
		Seq int64  `json:"seq"`
		TS  int64  `json:"ts"`
		Row string `json:"row"`
	}
	rows, err := sqliteObjects[rawRow](out)
	if err != nil {
		return err
	}
	type settledItem struct {
		firstSeq int64
		lastSeq  int64
		revision int64
		ts       int64
		body     map[string]any
	}
	byID := map[string]*settledItem{}
	var order []string
	for _, r := range rows {
		var item struct {
			ItemID     string         `json:"itemId"`
			Revision   int64          `json:"revision"`
			ObservedAt int64          `json:"observedAt"`
			Sequence   int64          `json:"sequence"`
			Body       map[string]any `json:"body"`
		}
		if json.Unmarshal([]byte(r.Row), &item) != nil || item.Body == nil {
			continue
		}
		key := strings.TrimSpace(item.ItemID)
		if key == "" {
			key = fmt.Sprintf("__seq_%d", r.Seq)
		}
		ts := item.ObservedAt
		if ts <= 0 {
			ts = r.TS
		}
		seq := item.Sequence
		if seq <= 0 {
			seq = r.Seq
		}
		cur, ok := byID[key]
		if !ok {
			byID[key] = &settledItem{
				firstSeq: seq,
				lastSeq:  r.Seq,
				revision: item.Revision,
				ts:       ts,
				body:     item.Body,
			}
			order = append(order, key)
			continue
		}
		if item.Revision > cur.revision || (item.Revision == cur.revision && r.Seq >= cur.lastSeq) {
			cur.revision = item.Revision
			cur.lastSeq = r.Seq
			if ts > 0 {
				cur.ts = ts
			}
			cur.body = item.Body
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		return byID[order[i]].firstSeq < byID[order[j]].firstSeq
	})
	for _, key := range order {
		it := byID[key]
		var t time.Time
		if it.ts > 0 {
			t = time.UnixMilli(it.ts).UTC()
		}
		orcaEmitBody(s, it.body, t)
	}
	return nil
}

func orcaEmitBody(s *model.Session, body map[string]any, t time.Time) {
	kind, _ := body["kind"].(string)
	switch kind {
	case "message":
		role, _ := body["role"].(string)
		role = strings.ToLower(strings.TrimSpace(role))
		if role != "user" && role != "assistant" {
			return
		}
		blocks, _ := body["blocks"].([]any)
		var texts []string
		var calls []any
		var outputs []string
		for _, b := range blocks {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			bkind, _ := bm["kind"].(string)
			switch bkind {
			case "text":
				if txt, _ := bm["text"].(string); strings.TrimSpace(txt) != "" {
					texts = append(texts, strings.TrimSpace(txt))
				}
			case "tool-call":
				name, _ := bm["name"].(string)
				args := devinCallArgs(bm["input"])
				if name != "" && args != nil {
					call := map[string]any{"type": "tool_use", "name": strings.ToLower(name), "input": args}
					if id, _ := bm["toolCallId"].(string); id != "" {
						call["id"] = id
					}
					calls = append(calls, call)
				}
			case "tool-result":
				if out, _ := bm["output"].(string); strings.TrimSpace(out) != "" {
					outputs = append(outputs, strings.TrimSpace(out))
				}
			}
		}
		if joined := strings.Join(texts, "\n"); joined != "" {
			if !t.IsZero() {
				s.Touch(t)
			}
			s.Messages = append(s.Messages, model.Message{Role: role, Text: joined, Time: t})
		}
		if len(calls) > 0 {
			if work := omnigentWorkRecords(calls, t); len(work) > 0 {
				if !t.IsZero() {
					s.Touch(t)
				}
				s.Messages = append(s.Messages, work...)
			}
		}
		if IndexToolOutput() {
			for _, out := range outputs {
				if !t.IsZero() {
					s.Touch(t)
				}
				s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(out), Time: t})
			}
		}
	case "tool-call":
		name, _ := body["name"].(string)
		args := devinCallArgs(body["input"])
		if name != "" && args != nil {
			call := map[string]any{"type": "tool_use", "name": strings.ToLower(name), "input": args}
			if work := omnigentWorkRecords([]any{call}, t); len(work) > 0 {
				if !t.IsZero() {
					s.Touch(t)
				}
				s.Messages = append(s.Messages, work...)
			}
		}
		if IndexToolOutput() {
			if outMap, ok := body["output"].(map[string]any); ok {
				if head, _ := outMap["head"].(string); strings.TrimSpace(head) != "" {
					if !t.IsZero() {
						s.Touch(t)
					}
					s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(strings.TrimSpace(head)), Time: t})
				}
			}
		}
	case "diff":
		path, _ := body["path"].(string)
		path = strings.TrimSpace(path)
		var patch string
		if pm, ok := body["patch"].(map[string]any); ok {
			patch, _ = pm["head"].(string)
		}
		if path != "" && IndexToolPaths() {
			if !t.IsZero() {
				s.Touch(t)
			}
			s.Messages = append(s.Messages, model.Message{Role: RoleFiles, Text: path, Time: t})
		}
		if strings.TrimSpace(patch) != "" {
			call := map[string]any{
				"type":  "tool_use",
				"name":  "apply_patch",
				"input": map[string]any{"file_path": path, "patch": patch},
			}
			for _, m := range omnigentWorkRecords([]any{call}, t) {
				if m.Role == RoleFiles {
					continue
				}
				if !t.IsZero() {
					s.Touch(t)
				}
				s.Messages = append(s.Messages, m)
			}
		}
	}
}

// OrcaSessionDir returns the working directory recorded for session id in db.
func OrcaSessionDir(db, id string) string {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 || !orcaDBHasTable(db) {
		return ""
	}
	q := `select json_object('cwd',coalesce(json_extract(record_json,'$.cwd'),''),'ws',coalesce(json_extract(record_json,'$.workspaceId'),''))` +
		` from agent_session_records where session_id = '` + sqlEscape(id) + `' limit 1`
	out, err := sqliteOutput(db, q)
	if err != nil {
		return ""
	}
	type row struct {
		Cwd string `json:"cwd"`
		WS  string `json:"ws"`
	}
	rows, err := sqliteObjects[row](out)
	if err != nil || len(rows) == 0 {
		return ""
	}
	if cwd := strings.TrimSpace(rows[0].Cwd); cwd != "" {
		return cwd
	}
	return strings.TrimSpace(rows[0].WS)
}
