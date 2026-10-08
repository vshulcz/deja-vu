package sources

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// OpenClaw's SQLite flip (openclaw/openclaw#98236, shipped in 2026.8.x) made
// agents/<agent>/agent/openclaw-agent.sqlite the runtime store: session rows
// and transcript events live there, and the JSONL files under sessions/ are
// migration inputs or archives. transcript_events.event_json carries the same
// lines the JSONL held, so the pi-shaped line reader is shared; what changes is
// where the lines come from and that reset/rollover boundaries keep the earlier
// session in the store rather than renaming a file.

// OpenClawAgentDBs lists the per-agent SQLite stores under the agents root.
func OpenClawAgentDBs() []string {
	root := OpenClawRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var dbs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		db := filepath.Join(root, e.Name(), "agent", "openclaw-agent.sqlite")
		if fi, err := os.Stat(db); err == nil && !fi.IsDir() {
			dbs = append(dbs, db)
		}
	}
	return dbs
}

// OpenClawStoreFiles is every OpenClaw store deja reads: the legacy transcript
// files and the per-agent SQLite databases.
func OpenClawStoreFiles() []string {
	return append(OpenClawSessionFiles(), OpenClawAgentDBs()...)
}

// openclawDBAgent is the agent id a store belongs to: agents/<agent>/agent/db.
func openclawDBAgent(db string) string {
	return filepath.Base(filepath.Dir(filepath.Dir(db)))
}

// openclawDBSessionKey resolves the current window, not merely any historical
// window with the same key: a reset keeps the old transcript in the database,
// but `openclaw chat --session <key>` opens only the current one.
func openclawDBSessionKey(db, id string) (string, error) {
	known, err := sqliteOutput(db, `select name from sqlite_master where type='table' `+
		`and name in ('session_nodes','session_windows','sessions','session_routes')`)
	if err != nil {
		return "", fmt.Errorf("openclaw: read session schema: %w", err)
	}
	tables := make(map[string]bool)
	for _, name := range strings.Fields(string(known)) {
		tables[name] = true
	}
	quotedID := strings.ReplaceAll(id, "'", "''")
	var query string
	switch {
	case tables["session_windows"] && tables["session_nodes"]:
		query = `select json_object('key', w.session_key) from session_windows w ` +
			`join session_nodes n on n.session_key = w.session_key ` +
			`and n.current_session_id = w.session_id where w.session_id = '` + quotedID + `'`
	case tables["sessions"] && tables["session_routes"]:
		// The first 2026.8 schema called windows `sessions` and kept the
		// current id for each key in session_routes.
		query = `select json_object('key', s.session_key) from sessions s ` +
			`join session_routes r on r.session_key = s.session_key ` +
			`and r.session_id = s.session_id where s.session_id = '` + quotedID + `'`
	default:
		return "", fmt.Errorf("openclaw: store has no supported current-session mapping")
	}
	raw, err := sqliteOutput(db, query)
	if err != nil {
		return "", fmt.Errorf("openclaw: read current session key: %w", err)
	}
	type keyRow struct {
		Key string `json:"key"`
	}
	rows, err := sqliteObjects[keyRow](raw)
	if err != nil {
		return "", fmt.Errorf("openclaw: decode current session key: %w", err)
	}
	if len(rows) != 1 {
		return "", nil
	}
	return rows[0].Key, nil
}

// ParseOpenClawDB reads every session held in a per-agent store.
func ParseOpenClawDB(db string) ([]model.Session, error) {
	return parseOpenClawDBWhere(db, "")
}

// ParseOpenClawDBSince reads the sessions that gained an event after t, whole:
// the cursor is session-scoped so what comes back replaces the session in the
// index rather than adding a tail to it.
func ParseOpenClawDBSince(db string, t time.Time) ([]model.Session, error) {
	return parseOpenClawDBWhere(db, fmt.Sprintf(
		" where e.session_id in (select session_id from transcript_events where created_at > %d)",
		t.Add(-time.Second).UnixMilli()))
}

func parseOpenClawDBWhere(db, where string) ([]model.Session, error) {
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return nil, nil
	}
	// json_object rather than the shell's -json mode, which is quadratic in
	// what it escapes — see sqliteRows. An event_json is nothing but quotes
	// and backslashes.
	q := `select json_object('session_id',cast(e.session_id as text),'event_json',cast(e.event_json as text)` +
		openclawZstdColumns(db) + `) from transcript_events e` + where + ` order by e.session_id, e.seq`
	cmd, stopRead := sqliteReadCmd(db, q)
	defer stopRead()
	// Rows stream through the decoder rather than landing in one buffer: a
	// store of a few gigabytes is normal for a daily-reset agent, and
	// holding every event_json in memory before the first session is built
	// is what the hermes reader was written to avoid.
	dec, err := sqliteRows(cmd)
	if err != nil {
		return nil, err
	}
	rows := 0
	project := "openclaw-" + openclawDBAgent(db)
	var out []model.Session
	var s *model.Session
	var pending []openclawEventRow
	flush := func() {
		if s == nil {
			return
		}
		openclawInflate(pending)
		r := newPiReader(s, true)
		for _, row := range pending {
			var m map[string]any
			if err := json.Unmarshal([]byte(row.Event), &m); err != nil {
				continue
			}
			r.line(m)
		}
		r.finish()
		if len(s.Messages) > 0 {
			out = append(out, *s)
		}
		s, pending = nil, nil
	}
	for dec.More() {
		var row openclawEventRow
		if err := dec.Decode(&row); err != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("bad sqlite json: %w", err)
		}
		rows++
		if s == nil || s.ID != row.SessionID {
			flush()
			s = &model.Session{Harness: "openclaw", ID: row.SessionID, Project: project, Path: db}
		}
		pending = append(pending, row)
	}
	flush()
	if _, err := dec.Token(); err != nil && err != io.EOF {
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		if rows == 0 {
			// No stdout is a query that matched nothing or one sqlite3 refused
			// to run; the second must not read as an empty store, or a whole
			// harness vanishes from recall while doctor calls it healthy.
			return nil, fmt.Errorf("openclaw: query failed, the store schema may have changed: %w", err)
		}
		return nil, err
	}
	return out, nil
}

// openclawEventRow is one transcript event. Since 2026.9.9 an event of 1 KiB
// or more is stored zstd-compressed when that saves a tenth of it: event_json
// is null, event_zstd holds one frame, and event_utf8_bytes its decoded size
// (src/config/sessions/transcript-payload.ts prepareTranscriptPayload). A
// compaction summary is usually one of those.
type openclawEventRow struct {
	SessionID string `json:"session_id"`
	Event     string `json:"event_json"`
	Zstd      string `json:"zstd"`
	RawBytes  int    `json:"raw"`
}

// openclawZstdColumns selects the compressed payload, on a store that has one.
func openclawZstdColumns(db string) string {
	out, err := sqliteOutput(db, "select count(*) from pragma_table_info('transcript_events') where name in ('event_zstd','event_utf8_bytes')")
	if err != nil || strings.TrimSpace(string(out)) != "2" {
		return ""
	}
	return `,'zstd',case when e.event_json is null then hex(e.event_zstd) end,'raw',e.event_utf8_bytes`
}

// openclawInflate fills in the events a session stored compressed. Their frames
// go through one zstd run and the output is cut at each event's recorded size;
// a run that fails, or whose sizes do not add up, falls back to one frame at a
// time, so a corrupt frame costs only its own event. Without zstd they stay
// empty and are skipped, as before.
func openclawInflate(rows []openclawEventRow) {
	var at []int
	var frames [][]byte
	total := 0
	for i, r := range rows {
		if r.Event != "" || r.Zstd == "" {
			continue
		}
		frame, err := hex.DecodeString(r.Zstd)
		if err != nil || len(frame) == 0 {
			continue
		}
		at = append(at, i)
		frames = append(frames, frame)
		total += r.RawBytes
	}
	if len(frames) == 0 || !ZstdAvailable() {
		return
	}
	if out, _, err := zstdRun(bytes.Join(frames, nil)); err == nil && len(out) == total {
		for _, i := range at {
			n := rows[i].RawBytes
			rows[i].Event, out = string(out[:n]), out[n:]
		}
		return
	}
	for k, i := range at {
		if out, _, err := zstdRun(frames[k]); err == nil {
			rows[i].Event = string(out)
		}
	}
}
