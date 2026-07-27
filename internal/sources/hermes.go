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

// Hermes keeps one SQLite store per profile under ~/.hermes/profiles/<name>,
// so a user running several profiles has several stores and all of them count.
func HermesProfilesRoot() string {
	if p := os.Getenv("DEJA_HERMES_PROFILES_ROOT"); p != "" {
		return p
	}
	return filepath.Join(Home(), ".hermes", "profiles")
}

// HermesDBs lists every profile store that exists and has content. A single
// store can be forced with DEJA_HERMES_DB, which is what the tests use.
func HermesDBs() []string {
	if p := os.Getenv("DEJA_HERMES_DB"); p != "" {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			return []string{p}
		}
		return nil
	}
	entries, err := os.ReadDir(HermesProfilesRoot())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		db := filepath.Join(HermesProfilesRoot(), e.Name(), "state.db")
		if fi, err := os.Stat(db); err == nil && fi.Size() > 0 {
			out = append(out, db)
		}
	}
	return out
}

// HermesSessionFiles is the store list the indexer stats for changes.
func HermesSessionFiles() []string { return HermesDBs() }

func LoadHermes() []model.Session {
	var out []model.Session
	for _, db := range HermesDBs() {
		ss, _ := ParseHermesDB(db)
		out = append(out, ss...)
	}
	return out
}

func ParseHermesDB(db string) ([]model.Session, error) {
	return parseHermesDBWhere(db, "")
}

// ParseHermesDBSince reads only what changed, so a re-index of an unchanged
// profile costs one query instead of the whole history. timestamp is REAL
// seconds since the epoch in Hermes' schema.
func ParseHermesDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return parseHermesDBWhere(db, "")
	}
	return parseHermesDBWhere(db, fmt.Sprintf(" and timestamp > %d", t.Unix()))
}

func parseHermesDBWhere(db, where string) ([]model.Session, error) {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	// content is null for tool-call rows; those carry no prose worth indexing.
	q := `select session_id,role,content,timestamp from messages ` +
		`where role in ('user','assistant') and content is not null and content <> ''` + where +
		` order by session_id,timestamp,id`
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
			return nil, nil // no rows: sqlite3 -json prints nothing at all
		}
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		_ = cmd.Wait()
		return nil, fmt.Errorf("bad sqlite json")
	}
	project := hermesProfile(db)
	by := map[string]*model.Session{}
	var order []string
	for dec.More() {
		var r map[string]any
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return nil, err
		}
		id := str(r["session_id"])
		if id == "" {
			continue
		}
		s := by[id]
		if s == nil {
			s = &model.Session{Harness: "hermes", ID: id, Project: project, Path: db}
			by[id] = s
			order = append(order, id)
		}
		txt := strings.TrimSpace(str(r["content"]))
		if txt == "" {
			continue
		}
		if len(txt) > 64*1024 {
			txt = txt[:64*1024]
		}
		t := hermesTime(r["timestamp"])
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{Role: str(r["role"]), Text: txt, Time: t})
	}
	if _, err := dec.Token(); err != nil {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	out := make([]model.Session, 0, len(order))
	for _, id := range order {
		s := by[id]
		if len(s.Messages) == 0 {
			continue
		}
		if s.Title == "" {
			s.Title = firstLineTrim(s.Messages[0].Text)
		}
		out = append(out, *s)
	}
	return out, nil
}

// hermesTime reads Hermes' REAL epoch seconds. The shared parser handles
// integer epochs and RFC 3339, but a fractional second arrives as 1785000000.5
// and would otherwise land as the zero time — which sorts as ancient and never
// surfaces in recall.
func hermesTime(v any) time.Time {
	var secs float64
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return parseTimeAny(v)
		}
		secs = f
	case float64:
		secs = n
	default:
		return parseTimeAny(v)
	}
	if secs <= 0 {
		return time.Time{}
	}
	sec := int64(secs)
	return time.Unix(sec, int64((secs-float64(sec))*1e9)).UTC()
}

// hermesProfile names the session's project after the profile directory, the
// only grouping Hermes stores — there is no working directory in the schema.
func hermesProfile(db string) string {
	name := filepath.Base(filepath.Dir(db))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "hermes"
	}
	return name
}
