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

// HermesHome is the Hermes root: profiles, plugins and config.yaml live here.
func HermesHome() string {
	if p := os.Getenv("DEJA_HERMES_HOME"); p != "" {
		return p
	}
	return filepath.Join(Home(), ".hermes")
}

// Hermes keeps one SQLite store per profile under ~/.hermes/profiles/<name>,
// so a user running several profiles has several stores and all of them count.
func HermesProfilesRoot() string {
	if p := os.Getenv("DEJA_HERMES_PROFILES_ROOT"); p != "" {
		return p
	}
	return filepath.Join(HermesHome(), "profiles")
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
	var out []string
	// 0.17 keeps one store at the root; older builds shard it per profile.
	// Both shapes exist in the wild, so both are looked for.
	if db := filepath.Join(HermesHome(), "state.db"); nonEmptyFile(db) {
		out = append(out, db)
	}
	entries, err := os.ReadDir(HermesProfilesRoot())
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		db := filepath.Join(HermesProfilesRoot(), e.Name(), "state.db")
		if nonEmptyFile(db) {
			out = append(out, db)
		}
	}
	return out
}

// HermesSessionFiles is the store list the indexer stats for changes. The
// Postgres store, when opted in, rides along as a token the index fingerprints
// instead of stats.
func HermesSessionFiles() []string {
	files := HermesDBs()
	if dsn := HermesPGDSN(); dsn != "" {
		files = append(files, HermesPGStorePath(dsn))
	}
	return files
}

func LoadHermes() []model.Session {
	var out []model.Session
	for _, db := range HermesDBs() {
		ss, _ := ParseHermesDB(db)
		out = append(out, ss...)
	}
	if dsn := HermesPGDSN(); dsn != "" {
		ss, _ := ParseHermesPG(dsn, 0)
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
//
// A second back from the watermark, which is the guard grok's reader spells in
// milliseconds (#2150). The column is REAL but a store may write whole seconds
// into it, and then every message sharing the watermark's second compares equal
// to it and a strict `>` leaves it out for good — measured on such a store, 0
// of 2 came back. The cost is re-reading one second of history, and those turns
// are already held (#2075).
func ParseHermesDBSince(db string, t time.Time) ([]model.Session, error) {
	if t.IsZero() {
		return parseHermesDBWhere(db, "")
	}
	// By session, so the session comes back whole: what a store parsed from its
	// watermark hands back replaces what the index holds for that key, and a
	// return of the newest turn alone takes the earlier ones with it (#2075).
	// The whole-second floor is then harmless — it can only re-offer a message
	// the pass was going to replace anyway.
	return parseHermesDBWhere(db, fmt.Sprintf(
		" and session_id in (select session_id from messages where timestamp > %d)",
		t.Add(-time.Second).Unix()))
}

func parseHermesDBWhere(db, where string) ([]model.Session, error) {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	// content is null for tool-call rows; those carry no prose worth indexing.
	q := `select session_id,role,content,timestamp from messages ` +
		`where role in ('user','assistant') and content is not null and content <> ''` + where +
		` order by session_id,timestamp,id`
	cmd := exec.Command("sqlite3", "-readonly", "-json", sqliteTarget(db), ".timeout 5000", q)
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
		waitErr := cmd.Wait()
		if err == io.EOF {
			// No stdout means two very different things: a query that matched
			// nothing, or one sqlite3 refused to run because the harness
			// changed its schema. Reporting the second as "no sessions" makes
			// a whole harness disappear from recall while doctor still calls
			// the store healthy.
			if waitErr != nil {
				return nil, fmt.Errorf("hermes: query failed, the store schema may have changed: %w", waitErr)
			}
			return nil, nil
		}
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		_ = cmd.Wait()
		return nil, fmt.Errorf("bad sqlite json")
	}
	out, err := decodeHermesArray(dec, hermesProfile(db), db)
	if err != nil {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// decodeHermesArray reads a json array of {session_id,role,content,timestamp}
// rows — the same shape sqlite3 -json and Postgres json_agg produce — into
// sessions. dec is positioned just past the opening '['; it is left just past
// the closing ']'. project and path stamp every session.
func decodeHermesArray(dec *json.Decoder, project, path string) ([]model.Session, error) {
	by := map[string]*model.Session{}
	var order []string
	for dec.More() {
		var r map[string]any
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		id := str(r["session_id"])
		if id == "" {
			continue
		}
		s := by[id]
		if s == nil {
			s = &model.Session{Harness: "hermes", ID: id, Project: project, Path: path}
			by[id] = s
			order = append(order, id)
		}
		txt := strings.TrimSpace(str(r["content"]))
		if txt == "" {
			continue
		}
		txt = capParsedMessage(txt)
		t := hermesTime(r["timestamp"])
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{Role: str(r["role"]), Text: txt, Time: t})
	}
	if _, err := dec.Token(); err != nil {
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

func nonEmptyFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Size() > 0
}

// hermesProfile names the session's project after the profile directory, the
// only grouping Hermes stores — there is no working directory in the schema.
func hermesProfile(db string) string {
	name := filepath.Base(filepath.Dir(db))
	// The root store has no profile directory to be named after.
	if name == filepath.Base(HermesHome()) {
		return "hermes"
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "hermes"
	}
	return name
}
