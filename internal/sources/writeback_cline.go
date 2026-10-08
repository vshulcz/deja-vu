package sources

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Cline's CLI keeps a session in sessions/<id>/: <id>.messages.json is the
// transcript and <id>.json the manifest `cline --id` opens it by. Deleting a
// session from cline's history removes the directory and its row in
// db/sessions.db; `cline history` and `cline --id` then read the manifest
// alone, so writing it back takes the two files. Cline 3.0.69 refuses a
// manifest whose cwd, workspace_root, provider or model is empty. The index
// does not keep the directory a session ran in, so it comes from cline's own
// records: the database row while it is there, else the hook log, which names
// each session's workspace root. Provider and model are what cline shows in
// its history; it continues with the provider configured now either way.

func init() {
	registerWriteBack("cline", func() []string { return []string{ClineSessionsDir()} }, renderCline)
	registerWriteBackExtras("cline", clineWriteBackExtras)
}

var clineWriteBackID = regexp.MustCompile(`^[0-9A-Za-z_-]{1,128}$`)

func renderCline(s model.Session, turns []model.Message) (string, []byte, error) {
	if filepath.Base(s.Path) == "api_conversation_history.json" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "it is a task of the Cline VS Code extension, which lists tasks from its own taskHistory.json; deja does not write that file"}
	}
	dir := filepath.Dir(s.Path)
	if filepath.Base(dir) != s.ID || filepath.Base(s.Path) != s.ID+".messages.json" || !clineWriteBackID.MatchString(s.ID) {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no sessions/<id>/<id>.messages.json path for this session, which is where `cline --id` looks"}
	}
	type block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type message struct {
		ID      string  `json:"id"`
		Role    string  `json:"role"`
		Content []block `json:"content"`
		TS      int64   `json:"ts"`
	}
	doc := struct {
		Version   int       `json:"version"`
		UpdatedAt string    `json:"updated_at"`
		Agent     string    `json:"agent"`
		SessionID string    `json:"sessionId"`
		Messages  []message `json:"messages"`
	}{Version: 1, UpdatedAt: writeBackStamp(turns[len(turns)-1].Time), Agent: "lead", SessionID: s.ID}
	for i, m := range turns {
		doc.Messages = append(doc.Messages, message{
			ID: writeBackUUID(s.ID, i), Role: m.Role,
			Content: []block{{Type: "text", Text: m.Text}}, TS: m.Time.UnixMilli(),
		})
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", nil, err
	}
	return s.Path, append(data, '\n'), nil
}

func clineWriteBackExtras(s model.Session, turns []model.Message, f WriteBackFile) ([]WriteBackPart, []WriteBackPart, error) {
	rec := clineSessionRecord(s.ID)
	if rec.CWD == "" {
		return nil, nil, &WriteBackRefusal{Harness: s.Harness, Reason: "cline's manifest needs the directory the session ran in; the index does not keep it, and neither cline's sessions.db nor its hook log still names it"}
	}
	if rec.WorkspaceRoot == "" {
		rec.WorkspaceRoot = rec.CWD
	}
	if rec.Provider == "" || rec.Model == "" {
		rec.Provider, rec.Model = clineLastProvider()
	}
	if rec.Provider == "" {
		rec.Provider = "unknown"
	}
	if rec.Model == "" {
		rec.Model = "unknown"
	}
	start := turns[0].Time
	if !s.Started.IsZero() && s.Started.Before(start) {
		start = s.Started
	}
	prompt := ""
	for _, m := range turns {
		if m.Role == "user" {
			prompt = m.Text
			break
		}
	}
	title := strings.TrimSpace(s.Title)
	if title == "" {
		title = firstLineTrim(prompt)
	}
	man := map[string]any{
		"version":        1,
		"session_id":     s.ID,
		"source":         "cli",
		"pid":            0,
		"started_at":     writeBackStamp(start),
		"ended_at":       writeBackStamp(turns[len(turns)-1].Time),
		"status":         "completed",
		"interactive":    false,
		"provider":       rec.Provider,
		"model":          rec.Model,
		"cwd":            rec.CWD,
		"workspace_root": rec.WorkspaceRoot,
		"enable_tools":   true,
		"enable_spawn":   false,
		"enable_teams":   false,
		"prompt":         prompt,
		"metadata":       map[string]any{"title": title},
		"messages_path":  f.Path,
	}
	data, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	side := WriteBackPart{Path: filepath.Join(filepath.Dir(f.Path), s.ID+".json"), Data: append(data, '\n')}
	return []WriteBackPart{side}, nil, nil
}

type clineRecord struct {
	CWD           string `json:"cwd"`
	WorkspaceRoot string `json:"workspace_root"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
}

// clineSessionRecord is what cline itself still holds about a session whose
// directory is gone.
func clineSessionRecord(id string) clineRecord {
	if !clineWriteBackID.MatchString(id) {
		return clineRecord{}
	}
	db := filepath.Join(ClineConfigDir(), "db", "sessions.db")
	if _, err := os.Stat(db); err == nil && SQLite3Available() {
		out, err := sqliteOutput(db, "select json_object('cwd', cwd, 'workspace_root', workspace_root, 'provider', provider, 'model', model) from sessions where session_id='"+id+"' limit 1;")
		if err == nil {
			if rows, err := sqliteObjects[clineRecord](out); err == nil && len(rows) == 1 && rows[0].CWD != "" {
				return rows[0]
			}
		}
	}
	return clineRecord{CWD: clineHookLogRoot(id)}
}

// clineHookLogRoot is the workspace root cline's hook log recorded for a
// session, the last one it names.
func clineHookLogRoot(id string) string {
	f, err := os.Open(filepath.Join(ClineConfigDir(), "logs", "hooks.jsonl"))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	root := ""
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), id) {
			continue
		}
		var e struct {
			SessionContext struct {
				RootSessionID string `json:"rootSessionId"`
			} `json:"sessionContext"`
			WorkspaceInfo struct {
				RootPath string `json:"rootPath"`
			} `json:"workspaceInfo"`
			WorkspaceRoots []string `json:"workspaceRoots"`
		}
		if json.Unmarshal(line, &e) != nil || e.SessionContext.RootSessionID != id {
			continue
		}
		if p := e.WorkspaceInfo.RootPath; filepath.IsAbs(p) {
			root = p
		} else if len(e.WorkspaceRoots) > 0 && filepath.IsAbs(e.WorkspaceRoots[0]) {
			root = e.WorkspaceRoots[0]
		}
	}
	return root
}

// clineLastProvider is the provider and model cline was last set to use. Only
// those two fields are read from the settings file, which also holds keys.
func clineLastProvider() (string, string) {
	b, err := os.ReadFile(filepath.Join(ClineConfigDir(), "settings", "providers.json"))
	if err != nil {
		return "", ""
	}
	var doc struct {
		LastUsedProvider string `json:"lastUsedProvider"`
		Providers        map[string]struct {
			Settings struct {
				Model string `json:"model"`
			} `json:"settings"`
		} `json:"providers"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return "", ""
	}
	return doc.LastUsedProvider, doc.Providers[doc.LastUsedProvider].Settings.Model
}
