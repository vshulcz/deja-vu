package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	// The roo CLI runs the same extension against a VS Code shim, so its
	// tasks land under a mock storage root rather than any editor's. Same
	// layout, so the parser needs nothing new — only the path.
	bases = append(bases, RooCLIRoot())
	bases = append(bases, rooCustomStorageRoots()...)
	var out []string
	for _, b := range bases {
		if fi, err := os.Stat(b); err == nil && fi.IsDir() {
			out = append(out, b)
		}
	}
	return out
}

// rooCustomStorageRoots reads the `roo-cline.customStoragePath` setting each
// VS Code host may carry. Roo writes it with ConfigurationTarget.Global, so it
// lives in the host's User/settings.json, and when it is set the tasks are
// there and nowhere else — deja would otherwise index none of that history.
func rooCustomStorageRoots() []string {
	var out []string
	for _, settings := range vsCodeUserSettingsPaths() {
		b, err := os.ReadFile(settings)
		if err != nil {
			continue
		}
		// The file is JSONC in practice: comments and trailing commas are
		// normal, so a strict decode would silently skip real users.
		var cfg map[string]any
		if json.Unmarshal(stripJSONCComments(b), &cfg) != nil {
			continue
		}
		if p, _ := cfg["roo-cline.customStoragePath"].(string); strings.TrimSpace(p) != "" {
			out = append(out, expandTilde(strings.TrimSpace(p)))
		}
	}
	return out
}

// vsCodeUserSettingsPaths is the settings file for each host Roo runs in, in
// the same order as the storage roots above.
func vsCodeUserSettingsPaths() []string {
	hosts := []string{"Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"}
	var out []string
	switch runtime.GOOS {
	case "darwin":
		app := filepath.Join(Home(), "Library", "Application Support")
		for _, host := range hosts {
			out = append(out, filepath.Join(app, host, "User", "settings.json"))
		}
	case "windows":
		app := os.Getenv("APPDATA")
		if app == "" {
			app = filepath.Join(Home(), "AppData", "Roaming")
		}
		for _, host := range hosts {
			out = append(out, filepath.Join(app, host, "User", "settings.json"))
		}
	default:
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(Home(), ".config")
		}
		for _, host := range hosts {
			out = append(out, filepath.Join(cfg, host, "User", "settings.json"))
		}
	}
	return out
}

// stripJSONCComments removes // and /* */ comments outside strings. VS Code
// settings files carry them by default, and a strict decode would treat every
// commented settings file as absent.
func stripJSONCComments(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inString, escaped := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && (b[i] != '*' || b[i+1] != '/') {
				i++
			}
			i++
		default:
			out = append(out, c)
		}
	}
	return dropTrailingCommas(out)
}

// dropTrailingCommas removes the comma VS Code tolerates before a closing
// brace or bracket. Settings files written by hand have them, and without
// this the whole file decodes as invalid and the setting is missed.
func dropTrailingCommas(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inString, escaped := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(b) && (b[j] == ' ' || b[j] == '\t' || b[j] == '\n' || b[j] == '\r') {
				j++
			}
			if j < len(b) && (b[j] == '}' || b[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func expandTilde(p string) string {
	if p == "~" {
		return Home()
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(Home(), p[2:])
	}
	return p
}

// RooCLIRoot is where @roo-code/cli keeps tasks and settings: it runs the
// extension against a VS Code shim whose storage base is ~/.vscode-mock.
func RooCLIRoot() string {
	if p := os.Getenv("DEJA_ROO_CLI_ROOT"); p != "" {
		return p
	}
	return filepath.Join(Home(), ".vscode-mock", "global-storage")
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
		// One document per task, as in cline: a file that will not parse is a
		// path deja could not read, not a line (#2232).
		diagFileError(path, err)
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
