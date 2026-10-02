package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// `kiro-cli chat --no-interactive` writes nothing under ~/.kiro/sessions: the
// conversation is a row of kiro-cli's own data.sqlite3, table
// conversations_v2, keyed by the directory it ran in (#4300). The row's value
// is the whole conversation as JSON, and `history` is the part with the turns:
//
//	{"user":{"content":{"Prompt":{"prompt":"…"}} | {"ToolUseResults":{…}},
//	         "timestamp":"<RFC3339>"|null},
//	 "assistant":{"ToolUse":{"content":"…","tool_uses":[{"id","name","args"}]}}
//	          | {"Response":{"content":"…"}}}
//
// Checked against kiro-cli 2.22.0; `kiro-cli chat --resume-id
// <conversation_id>` reopens a row, so the id is the one resume takes.

// kiroDataDir is kiro-cli's local data directory, the platform's own.
func kiroDataDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(Home(), "Library", "Application Support", "kiro-cli")
	case "windows":
		app := os.Getenv("LOCALAPPDATA")
		if app == "" {
			app = filepath.Join(Home(), "AppData", "Local")
		}
		return filepath.Join(app, "kiro-cli")
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(Home(), ".local", "share")
	}
	return filepath.Join(data, "kiro-cli")
}

// KiroDB is that store. DEJA_KIRO_DB replaces it.
func KiroDB() string {
	return EnvPath("DEJA_KIRO_DB", filepath.Join(kiroDataDir(), "data.sqlite3"))
}

// ParseKiroDB reads every conversation in the store.
func ParseKiroDB(db string) ([]model.Session, error) {
	return parseKiroDBWhere(db, "")
}

// ParseKiroDBSince reads the conversations written after t, whole: a row is
// the whole conversation, so what comes back replaces it in the index.
func ParseKiroDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return ParseKiroDB(db)
	}
	return parseKiroDBWhere(db, fmt.Sprintf(" where updated_at > %d", t.Add(-time.Second).UnixMilli()))
}

func parseKiroDBWhere(db, where string) ([]model.Session, error) {
	// The sqlite3 CLI creates a missing database on open — never let it.
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	// Only the history leaves sqlite: the rest of a value is the tool
	// specifications, tens of kilobytes a row. A value that is not JSON gives
	// null rather than failing the query for every other row.
	q := `select json_object('id',conversation_id,'key',key,'history',` +
		`case when json_valid(value) then json_extract(value,'$.history') end,` +
		`'created',created_at,'updated',updated_at) from conversations_v2` + where +
		` order by updated_at`
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
			ID      string          `json:"id"`
			Key     string          `json:"key"`
			History json.RawMessage `json:"history"`
			Created int64           `json:"created"`
			Updated int64           `json:"updated"`
		}
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("bad sqlite json: %w", err)
		}
		rows++
		if r.ID == "" {
			continue
		}
		s := model.Session{Harness: "kiro", ID: r.ID, Path: db}
		if r.Key != "" {
			s.Project = projectName(r.Key)
		}
		kiroDBHistory(&s, r.History)
		if len(s.Messages) == 0 {
			continue
		}
		if r.Created > 0 {
			s.Touch(time.UnixMilli(r.Created).UTC())
		}
		if r.Updated > 0 {
			s.Touch(time.UnixMilli(r.Updated).UTC())
		}
		out = append(out, s)
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		if rows == 0 {
			// kiro-cli creates the file on first launch and the table only
			// when a headless chat is saved: a fresh install is an empty
			// store, not a schema change to report.
			if !kiroDBHasTable(db) {
				return nil, nil
			}
			// No rows can be an empty table or a schema sqlite3 refused, and
			// only the second is worth saying.
			return nil, fmt.Errorf("kiro: query failed, the store schema may have changed: %w", err)
		}
		return nil, err
	}
	return out, nil
}

// kiroDBHasTable reports whether the store has conversations_v2 yet. A query
// that cannot run says true, so a store sqlite3 refuses is still reported.
func kiroDBHasTable(db string) bool {
	out, err := sqliteOutput(db, "select count(*) from sqlite_master where type='table' and name='conversations_v2'")
	return err != nil || strings.TrimSpace(string(out)) != "0"
}

// kiroDBHistory appends a conversation's turns. A turn without its own stamp
// (tool results are written with null) takes the last one seen.
func kiroDBHistory(s *model.Session, raw json.RawMessage) {
	var history []struct {
		User struct {
			Content map[string]struct {
				Prompt  string `json:"prompt"`
				Results []struct {
					Content any `json:"content"`
				} `json:"tool_use_results"`
			} `json:"content"`
			Timestamp string `json:"timestamp"`
		} `json:"user"`
		Assistant map[string]struct {
			Content  string `json:"content"`
			ToolUses []struct {
				Name string         `json:"name"`
				Args map[string]any `json:"args"`
			} `json:"tool_uses"`
		} `json:"assistant"`
	}
	if json.Unmarshal(raw, &history) != nil {
		return
	}
	var t time.Time
	for _, turn := range history {
		if at := parseTimeAny(turn.User.Timestamp); !at.IsZero() {
			t = at
		}
		// Prompt, ToolUseResults, and CancelledToolUses, which carries both.
		var results []string
		for _, c := range turn.User.Content {
			if strings.TrimSpace(c.Prompt) != "" {
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: "user", Text: c.Prompt, Time: t})
			}
			for _, r := range c.Results {
				if out := kiroResultText(r.Content); out != "" {
					results = append(results, out)
				}
			}
		}
		if work := kiroWorkRecords(nil, results, t); len(work) > 0 {
			s.Touch(t)
			s.Messages = append(s.Messages, work...)
		}
		// ToolUse or Response; both carry the text, only the first the calls.
		for _, a := range turn.Assistant {
			if strings.TrimSpace(a.Content) != "" {
				s.Touch(t)
				s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: a.Content, Time: t})
			}
			var calls []any
			for _, u := range a.ToolUses {
				if u.Name != "" && u.Args != nil {
					calls = append(calls, kiroToolCall(u.Name, u.Args))
				}
			}
			if work := kiroWorkRecords(calls, nil, t); len(work) > 0 {
				s.Touch(t)
				s.Messages = append(s.Messages, work...)
			}
		}
	}
}

// KiroDBSessionDir is the directory a data.sqlite3 conversation ran in: the
// row's key, which kiro-cli sets to the working directory. "" when the row is
// not there or sqlite3 cannot answer (#4305).
func KiroDBSessionDir(db, id string) string {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return ""
	}
	q := `select json_object('key',key) from conversations_v2 where conversation_id = '` +
		strings.ReplaceAll(id, "'", "''") + `' limit 1`
	cmd, stop := sqliteReadCmd(db, q)
	defer stop()
	dec, err := sqliteRows(cmd)
	if err != nil {
		return ""
	}
	var r struct {
		Key string `json:"key"`
	}
	if dec.More() && dec.Decode(&r) != nil {
		r.Key = ""
	}
	_ = cmd.Wait()
	return r.Key
}
