package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func OpencodeDB() string {
	return EnvPath("DEJA_OPENCODE_DB", filepath.Join(Home(), ".local", "share", "opencode", "opencode.db"))
}

func LoadOpencode() []model.Session {
	ss, _ := ParseOpencodeDBWhere(OpencodeDB(), "", 0)
	return ss
}

func LoadOpencodeMatching(q string) []model.Session {
	where := fmt.Sprintf(" and lower(p.data) like '%%%s%%'", sqlQuote(strings.ToLower(q)))
	ss, _ := ParseOpencodeDBWhere(OpencodeDB(), where, 5000)
	return ss
}

func LoadOpencodeRecent(n int) []model.Session {
	ss, _ := ParseOpencodeDBWhere(OpencodeDB(), "", n*20)
	return ss
}

func LoadOpencodeSince(t time.Time) []model.Session {
	ss, _ := ParseOpencodeDBSince(OpencodeDB(), t)
	return ss
}

func LoadOpencodePrefix(p string) []model.Session {
	where := fmt.Sprintf(" and s.id like '%s%%'", sqlQuote(p))
	ss, _ := ParseOpencodeDBWhere(OpencodeDB(), where, 0)
	return ss
}

func ParseOpencodeDB(db string) ([]model.Session, error) {
	return ParseOpencodeDBWhere(db, "", 0)
}

func ParseOpencodeDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return ParseOpencodeDBWhere(db, "", 0)
	}
	rfc := sqlQuote(t.UTC().Format(time.RFC3339Nano))
	ms := t.UnixMilli()
	where := fmt.Sprintf(" and (m.time_created > %d or m.time_created > '%s' or json_extract(p.data,'$.time.start') > %d or json_extract(p.data,'$.time.start') > '%s')", ms, rfc, ms, rfc)
	return ParseOpencodeDBWhere(db, where, 0)
}

func ParseOpencodeDBWhere(db, where string, limit int) ([]model.Session, error) {
	// The sqlite3 CLI CREATES a missing database file on open — never let it.
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	lim := ""
	if limit > 0 {
		lim = fmt.Sprintf(" limit %d", limit)
	}
	// Narrow projection: shipping full m.data/p.data JSON blobs through the
	// sqlite3 pipe on multi-GB stores takes minutes; extracting just the
	// needed scalars keeps the dump to tens of MB and seconds.
	q := `select s.id,s.directory,s.time_created,s.time_updated,` +
		`json_extract(m.data,'$.role') as role,` +
		`json_extract(p.data,'$.text') as text,` +
		`json_extract(p.data,'$.time.start') as pt,` +
		`json_extract(m.data,'$.time.created') as mt ` +
		`from session s join message m on m.session_id=s.id join part p on p.message_id=m.id ` +
		`where json_extract(p.data,'$.type')='text'` + where + ` order by s.id,m.time_created,p.id` + lim
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
			dir, _ := r["directory"].(string)
			s = &model.Session{Harness: "opencode", ID: id, Project: projectName(dir), Path: dir, Started: parseTimeAny(r["time_created"]), Updated: parseTimeAny(r["time_updated"])}
			by[id] = s
		}
		role := str(r["role"])
		txt := str(r["text"])
		if txt == "" {
			continue
		}
		if len(txt) > 64*1024 {
			txt = txt[:64*1024]
		}
		t := parseTimeAny(r["pt"])
		if t.IsZero() {
			t = parseTimeAny(r["mt"])
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
		out = append(out, *s)
	}
	return out, nil
}

func OpencodeCounts() (sessions, messages int, err error) {
	if fi, e := os.Stat(OpencodeDB()); e != nil || fi.Size() == 0 {
		return 0, 0, nil
	}
	cmd := exec.Command("sqlite3", OpencodeDB(), "select (select count(*) from session),(select count(*) from part where json_extract(data,'$.type')='text')")
	b, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	f := strings.Split(strings.TrimSpace(string(b)), "|")
	if len(f) == 2 {
		_, _ = fmt.Sscanf(f[0], "%d", &sessions)
		_, _ = fmt.Sscanf(f[1], "%d", &messages)
	}
	return
}

func sqlQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }

func str(v any) string { s, _ := v.(string); return s }
func parseNestedTime(m map[string]any, k, sub string) time.Time {
	if x, ok := m[k].(map[string]any); ok {
		return parseTimeAny(x[sub])
	}
	return time.Time{}
}
