package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// GooseDataDir is Block's Goose CLI data root. On Linux it honors XDG_DATA_HOME;
// on Windows it uses %APPDATA%\Block\goose\data.
func GooseDataDir() string {
	if runtime.GOOS == "windows" {
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			return filepath.Join(appdata, "Block", "goose", "data")
		}
		return filepath.Join(Home(), "AppData", "Roaming", "Block", "goose", "data")
	}
	if runtime.GOOS == "linux" {
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "goose")
		}
	}
	return filepath.Join(Home(), ".local", "share", "goose")
}

// GooseRoot is the session-reading root; DEJA_GOOSE_ROOT overrides it without
// affecting where Goose itself stores data.
func GooseRoot() string { return EnvPath("DEJA_GOOSE_ROOT", GooseDataDir()) }

func gooseSessionsDir() string { return filepath.Join(GooseRoot(), "sessions") }

// GooseDB is the SQLite session store used by Goose >= 1.10.0.
func GooseDB() string {
	if p := os.Getenv("DEJA_GOOSE_DB"); p != "" {
		return p
	}
	return filepath.Join(gooseSessionsDir(), "sessions.db")
}

func gooseJSONLFiles() []string {
	return walkFiles(gooseSessionsDir(), func(p string) bool {
		return strings.HasSuffix(p, ".jsonl") && filepath.Base(p) != "sessions.db"
	})
}

// GooseJSONLFiles lists legacy JSONL session files (pre-1.10.0 storage).
func GooseJSONLFiles() []string { return gooseJSONLFiles() }

// GooseSessionFiles lists legacy JSONL sessions and the SQLite store when present.
func GooseSessionFiles() []string {
	out := gooseJSONLFiles()
	if fi, err := os.Stat(GooseDB()); err == nil && fi.Size() > 0 {
		out = append(out, GooseDB())
	}
	return out
}

func LoadGoose() []model.Session {
	ss := parseFiles(gooseJSONLFiles(), ParseGooseFile)
	dbSS, _ := ParseGooseDB(GooseDB())
	return append(ss, dbSS...)
}

func ParseGooseFile(path string) ([]model.Session, error) {
	return parseGooseFileFromOffset(path, 0)
}

func ParseGooseFileFromOffset(path string, offset int64) ([]model.Session, error) {
	return parseGooseFileFromOffset(path, offset)
}

func parseGooseFileFromOffset(path string, offset int64) ([]model.Session, error) {
	s := model.Session{
		Harness: "goose",
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path:    path,
	}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		role, hasRole := m["role"].(string)
		if !hasRole {
			if id, _ := m["id"].(string); id != "" {
				s.ID = id
			}
			if desc, _ := m["description"].(string); strings.TrimSpace(desc) != "" {
				s.Title = strings.TrimSpace(desc)
			}
			if wd, _ := m["working_dir"].(string); wd != "" {
				s.Project = projectName(wd)
			}
			s.Touch(parseTimeAny(m["created_at"]))
			s.Touch(parseTimeAny(m["updated_at"]))
			return
		}
		if role != "user" && role != "assistant" {
			return
		}
		t := parseTimeAny(m["created"])
		if t.IsZero() {
			t = parseTimeAny(m["timestamp"])
		}
		s.Touch(t)
		text := gooseText(m["content"])
		if text != "" {
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
		}
	})
	if s.Project == "" {
		s.Project = "goose"
	}
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

func gooseText(v any) string {
	switch x := v.(type) {
	case string:
		var parsed any
		if json.Unmarshal([]byte(x), &parsed) == nil {
			return textFromContent(parsed)
		}
		return strings.TrimSpace(x)
	default:
		return textFromContent(v)
	}
}

func ParseGooseDB(db string) ([]model.Session, error) {
	return parseGooseDBWhere(db, "", 0)
}

func ParseGooseDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return parseGooseDBWhere(db, "", 0)
	}
	sec := t.Unix()
	rfc := sqlQuote(t.UTC().Format(time.RFC3339Nano))
	where := fmt.Sprintf(" and (m.created_timestamp > %d or s.updated_at > '%s')", sec, rfc)
	return parseGooseDBWhere(db, where, 0)
}

func parseGooseDBWhere(db, where string, limit int) ([]model.Session, error) {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	lim := ""
	if limit > 0 {
		lim = fmt.Sprintf(" limit %d", limit)
	}
	q := `select s.id,s.working_dir,s.description,s.created_at,s.updated_at,` +
		`m.role,m.content_json,m.created_timestamp ` +
		`from sessions s join messages m on m.session_id=s.id ` +
		`where m.role in ('user','assistant')` + where +
		` order by s.id,m.created_timestamp,m.id` + lim
	cmd := exec.Command("sqlite3", "-json", db, q)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(stdout)
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		_ = cmd.Wait()
		if err == io.EOF {
			return nil, nil
		}
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		_ = cmd.Wait()
		return nil, fmt.Errorf("bad sqlite json")
	}
	by := map[string]*model.Session{}
	for dec.More() {
		var r map[string]any
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return nil, err
		}
		id, _ := r["id"].(string)
		if id == "" {
			continue
		}
		s := by[id]
		if s == nil {
			dir, _ := r["working_dir"].(string)
			desc, _ := r["description"].(string)
			s = &model.Session{
				Harness: "goose",
				ID:      id,
				Project: projectName(dir),
				Path:    db,
				Title:   strings.TrimSpace(desc),
				Started: parseTimeAny(r["created_at"]),
				Updated: parseTimeAny(r["updated_at"]),
			}
			if s.Project == "" {
				s.Project = "goose"
			}
			by[id] = s
		}
		role := str(r["role"])
		txt := gooseText(r["content_json"])
		if txt == "" {
			continue
		}
		if len(txt) > 64*1024 {
			txt = txt[:64*1024]
		}
		t := parseTimeAny(r["created_timestamp"])
		if t.IsZero() {
			t = s.Updated
		}
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: t})
	}
	if _, err := dec.Token(); err != nil {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	var out []model.Session
	for _, s := range by {
		if len(s.Messages) == 0 {
			continue
		}
		out = append(out, *s)
	}
	return out, nil
}
