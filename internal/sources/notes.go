package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

type note struct {
	TS      string `json:"ts"`
	Project string `json:"project"`
	Text    string `json:"text"`
}

func NotesFile() string {
	if p := os.Getenv("DEJA_NOTES_FILE"); p != "" {
		return p
	}
	// An explicit XDG_DATA_HOME wins on every platform so relocation and
	// hermetic tests behave the same everywhere.
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "deja", "notes.jsonl")
	}
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			return filepath.Join(dir, "deja", "notes.jsonl")
		}
		return filepath.Join(Home(), "AppData", "Roaming", "deja", "notes.jsonl")
	}
	return filepath.Join(Home(), ".local", "share", "deja", "notes.jsonl")
}

func AppendNote(project, text string, now time.Time) error {
	project = strings.TrimSpace(project)
	if project == "" {
		return fmt.Errorf("project required")
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("text required")
	}
	path := NotesFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("notes file is a symlink")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := f.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		return err
	}
	if now.IsZero() {
		now = time.Now()
	}
	return json.NewEncoder(f).Encode(note{TS: now.UTC().Format(time.RFC3339Nano), Project: project, Text: text})
}

func LoadNotes() []model.Session {
	ss, _ := ParseNotesFile(NotesFile())
	return ss
}

func ParseNotesFile(path string) ([]model.Session, error) {
	return ParseNotesFileFromOffset(path, 0)
}

func ParseNotesFileFromOffset(path string, offset int64) ([]model.Session, error) {
	byDay := map[string]*model.Session{}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		ts, _ := m["ts"].(string)
		project, _ := m["project"].(string)
		text, _ := m["text"].(string)
		t, parseErr := time.Parse(time.RFC3339Nano, ts)
		if parseErr != nil || strings.TrimSpace(project) == "" || strings.TrimSpace(text) == "" {
			return
		}
		project = strings.TrimSpace(project)
		day := t.UTC().Format("2006-01-02")
		key := project + "\x00" + day
		s := byDay[key]
		if s == nil {
			s = &model.Session{ID: "deja-" + day + "-" + project, Harness: "deja", Project: project, Path: path}
			byDay[key] = s
		}
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{Role: "user", Text: text, Time: t})
	})
	if err != nil {
		return nil, err
	}
	out := make([]model.Session, 0, len(byDay))
	for _, s := range byDay {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project == out[j].Project {
			return out[i].Started.Before(out[j].Started)
		}
		return out[i].Project < out[j].Project
	})
	return out, nil
}
