package sources

import (
	"encoding/json"
	"fmt"
	"io"
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
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Rows stream through the decoder rather than landing in one buffer: a
	// store of a few gigabytes is normal for a daily-reset agent, and
	// holding every event_json in memory before the first session is built
	// is what the hermes reader was written to avoid.
	dec := json.NewDecoder(stdout)
	tok, err := dec.Token()
	if err != nil {
		waitErr := cmd.Wait()
		if err == io.EOF {
			// No stdout is a query that matched nothing or one sqlite3 refused
			// to run; the second must not read as an empty store, or a whole
			// harness vanishes from recall while doctor calls it healthy.
			if waitErr != nil {
				return nil, fmt.Errorf("openclaw: query failed, the store schema may have changed: %w", waitErr)
			}
			return nil, nil
		}
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		_ = cmd.Wait()
		return nil, fmt.Errorf("bad sqlite json")
	}
	project := "openclaw-" + openclawDBAgent(db)
	var out []model.Session
	var s *model.Session
	flush := func() {
		if s != nil && len(s.Messages) > 0 {
			out = append(out, *s)
		}
		s = nil
	}
	for dec.More() {
		var r struct {
			SessionID string `json:"session_id"`
			Event     string `json:"event_json"`
		}
		if err := dec.Decode(&r); err != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("bad sqlite json")
		}
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
	if _, err := dec.Token(); err != nil { // the closing ']'
		_ = cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}
