package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	q := `select e.session_id, e.event_json from transcript_events e` + where +
		` order by e.session_id, e.seq`
	cmd := exec.Command("sqlite3", "-readonly", "-json", sqliteTarget(db), ".timeout 5000", q)
	out, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok || len(out) == 0 {
			// The store may be mid-migration or the schema older than this
			// reader; a missing table is not a broken index.
			return nil, nil
		}
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	var rows []struct {
		SessionID string `json:"session_id"`
		Event     string `json:"event_json"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("bad sqlite json")
	}
	project := "openclaw-" + openclawDBAgent(db)
	var out2 []model.Session
	var s *model.Session
	flush := func() {
		if s != nil && len(s.Messages) > 0 {
			out2 = append(out2, *s)
		}
		s = nil
	}
	for _, r := range rows {
		if s == nil || s.ID != r.SessionID {
			flush()
			s = &model.Session{Harness: "openclaw", ID: r.SessionID, Project: project, Path: db}
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(r.Event), &m); err != nil {
			continue
		}
		piShapedLine(s, m, true)
	}
	flush()
	return out2, nil
}
