package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Roo Code (github.com/RooCodeInc/Roo-Code) is a Cline fork whose VS Code
// extension (rooveterinaryinc.roo-cline) keeps tasks under the host's
// globalStorage:
//
//	tasks/<taskId>/api_conversation_history.json  (transcript, Cline shape)
//	tasks/<taskId>/history_item.json              (id, ts, task, workspace)
//	tasks/_index.json                             (index; not needed here)
//
// Verified against Roo-Code source (src/shared/globalFileNames.ts,
// src/core/task-persistence) — the transcript format matches Cline's legacy
// store, but metadata is per-task history_item.json instead of a global
// taskHistory.json. Text-only turns are indexed; the same envelope
// unwrapping applies.

func RooRoots() []string {
	if list := os.Getenv("DEJA_ROO_ROOTS"); list != "" {
		var out []string
		for _, p := range filepath.SplitList(list) {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	const ext = "rooveterinaryinc.roo-cline"
	var bases []string
	switch runtime.GOOS {
	case "darwin":
		app := filepath.Join(Home(), "Library", "Application Support")
		for _, host := range []string{"Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"} {
			bases = append(bases, filepath.Join(app, host, "User", "globalStorage", ext))
		}
	case "windows":
		app := os.Getenv("APPDATA")
		if app == "" {
			app = filepath.Join(Home(), "AppData", "Roaming")
		}
		for _, host := range []string{"Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"} {
			bases = append(bases, filepath.Join(app, host, "User", "globalStorage", ext))
		}
	default:
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(Home(), ".config")
		}
		for _, host := range []string{"Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"} {
			bases = append(bases, filepath.Join(cfg, host, "User", "globalStorage", ext))
		}
	}
	var out []string
	for _, b := range bases {
		if fi, err := os.Stat(b); err == nil && fi.IsDir() {
			out = append(out, b)
		}
	}
	return out
}

func RooTaskFiles() []string {
	var files []string
	for _, root := range RooRoots() {
		files = append(files, walkFiles(filepath.Join(root, "tasks"), func(p string) bool {
			return filepath.Base(p) == "api_conversation_history.json"
		})...)
	}
	return files
}

func LoadRoo() []model.Session { return parseFiles(RooTaskFiles(), ParseRooTask) }

type rooHistoryItem struct {
	ID        string `json:"id"`
	TS        int64  `json:"ts"`
	Task      string `json:"task"`
	Workspace string `json:"workspace"`
}

func ParseRooTask(path string) ([]model.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var turns []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &turns); err != nil {
		diagMalformedLine(path)
		return nil, nil
	}
	taskDir := filepath.Dir(path)
	taskID := filepath.Base(taskDir)
	s := model.Session{Harness: "roo", ID: "roo-task-" + taskID, Path: path, Project: "roo"}
	base := time.Time{}
	var item rooHistoryItem
	if hb, err := os.ReadFile(filepath.Join(taskDir, "history_item.json")); err == nil && json.Unmarshal(hb, &item) == nil {
		s.Title = firstLineTrim(item.Task)
		if item.Workspace != "" {
			s.Project = claudeProjectName(pathToProjectKey(item.Workspace))
		}
		if item.TS > 0 {
			base = time.UnixMilli(item.TS)
		}
	}
	if base.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			base = fi.ModTime()
		}
	}
	for ti, m := range turns {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		text := clineContentText(m.Content)
		if m.Role == "user" {
			text = unwrapClineTask(text)
		}
		if text == "" {
			continue
		}
		ts := base.Add(time.Duration(ti) * time.Second)
		s.Touch(ts)
		s.Messages = append(s.Messages, model.Message{Role: m.Role, Text: text, Time: ts})
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}
