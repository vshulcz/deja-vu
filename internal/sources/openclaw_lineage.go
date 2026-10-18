package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// OpenClaw runs a sub-agent (sessions_spawn) in a session of its own and
// records who asked for it on the session entry: spawnedBy and
// parentSessionKey name the requester's key, and since the SQLite flip
// spawnedBySessionId names its session (src/agents/subagent-spawn.ts). A
// child spawned with context:"fork", and a branch of a session, open on a
// header whose parentSession names the source, followed by the source's
// entries copied with their own timestamps
// (session-accessor.sqlite-parent-fork buildForkedChildTranscriptEvents,
// SessionManager.createBranchedSession).

var openclawSQLiteMarkerRE = regexp.MustCompile(`^sqlite:([^:]+):([^:]+):(.*)$`)

// openclawParentSession is the session id a header's parentSession names: a
// sqlite:<agent>:<id>:<store> marker, the transcript path of a JSONL store,
// or the bare id a branch writes.
func openclawParentSession(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if m := openclawSQLiteMarkerRE.FindStringSubmatch(v); m != nil {
		return m[2]
	}
	if strings.ContainsAny(v, `/\`) || strings.Contains(v, ".jsonl") {
		return strings.TrimSuffix(filepath.Base(openclawArchiveLive(filepath.FromSlash(v))), ".jsonl")
	}
	return v
}

// forkHeader reads an OpenClaw header that names a source session. The
// entries before the header's own time are the source's, copied: read as the
// child's they titled it with the parent's first prompt and answered searches
// for what only the parent said.
func (r *piReader) forkHeader(m map[string]any) {
	if !r.openclaw {
		return
	}
	p := openclawParentSession(str(m["parentSession"]))
	if p == "" || p == r.s.ID {
		return
	}
	r.s.Kind, r.s.Parent = "fork", p
	// OpenClaw stamps every header it writes; one without a time keeps the
	// whole transcript rather than guessing where the copy ends.
	r.copiedBefore = parseTimeAny(m["timestamp"])
}

// copied reports whether an entry stamped t is part of the source's copy.
func (r *piReader) copied(t time.Time) bool {
	return !r.copiedBefore.IsZero() && !t.IsZero() && t.Before(r.copiedBefore)
}

const (
	openclawContextOpen  = "<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>"
	openclawContextClose = "<<<END_OPENCLAW_INTERNAL_CONTEXT>>>"
)

// openclawUserText drops the runtime blocks OpenClaw wraps around what it
// hands an agent — a sub-agent's task opens on "[Subagent Context] You are
// running as a subagent", which became its title. A block with no end is
// left as it is.
func openclawUserText(txt string) string {
	if !strings.Contains(txt, openclawContextOpen) {
		return txt
	}
	var b strings.Builder
	rest := txt
	for {
		i := strings.Index(rest, openclawContextOpen)
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], openclawContextClose)
		if j < 0 {
			break
		}
		b.WriteString(rest[:i])
		b.WriteString("\n")
		rest = rest[i+j+len(openclawContextClose):]
	}
	b.WriteString(rest)
	return strings.TrimSpace(b.String())
}

// applyOpenClawSpawns hangs each spawned session under the one that spawned
// it. A fork's header already named its source; the spawn record says the
// source asked for it, which makes it a sub-agent rather than a branch.
func applyOpenClawSpawns(ss []model.Session, parents map[string]string) {
	for i := range ss {
		if p := parents[ss[i].ID]; p != "" && p != ss[i].ID {
			ss[i].Kind, ss[i].Parent = "subagent", p
		}
	}
}

// openclawDBSpawns maps each window a store's agent spawned to the session
// that spawned it. The entry's spawnedBySessionId is exact; without it the
// requester key resolves to the window it had when the child was created.
func openclawDBSpawns(db string) map[string]string {
	cols, err := sqliteOutput(db, `select m.name || '.' || p.name from sqlite_master m, pragma_table_info(m.name) p `+
		`where m.type='table' and m.name in ('session_windows','session_nodes') `+
		`and p.name in ('parent_session_key','spawned_by','current_session_id','entry_json')`)
	if err != nil {
		return nil
	}
	have := map[string]bool{}
	for _, c := range strings.Fields(string(cols)) {
		have[c] = true
	}
	if !have["session_windows.parent_session_key"] || !have["session_windows.spawned_by"] {
		return nil
	}
	key := `coalesce(w.parent_session_key, w.spawned_by)`
	byID := `null`
	from := `session_windows w`
	if have["session_nodes.current_session_id"] && have["session_nodes.entry_json"] && have["session_nodes.parent_session_key"] {
		from += ` left join session_nodes n on n.current_session_id = w.session_id and n.session_key = w.session_key`
		key = `coalesce(w.parent_session_key, w.spawned_by, n.parent_session_key, n.spawned_by)`
		byID = `case when json_valid(n.entry_json) then json_extract(n.entry_json, '$.spawnedBySessionId') end`
	}
	q := `select json_object('id', w.session_id, 'parent', coalesce(` + byID + `, ` +
		`(select p.session_id from session_windows p where p.session_key = ` + key +
		` and p.created_at <= w.created_at order by p.created_at desc limit 1))) ` +
		`from ` + from + ` where ` + key + ` is not null`
	raw, err := sqliteOutput(db, q)
	if err != nil {
		return nil
	}
	type row struct {
		ID     string `json:"id"`
		Parent string `json:"parent"`
	}
	rows, err := sqliteObjects[row](raw)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, r := range rows {
		if r.Parent != "" {
			out[r.ID] = r.Parent
		}
	}
	return out
}

// openclawStoreSpawns is openclawDBSpawns for a JSONL store, read from the
// sessions.json beside the transcripts. One file serves every transcript in
// the directory, so it is read once per change.
func openclawStoreSpawns(dir string) map[string]string {
	p := filepath.Join(dir, "sessions.json")
	openclawSpawnCache.Lock()
	defer openclawSpawnCache.Unlock()
	fi, err := os.Stat(p)
	if err != nil {
		return nil
	}
	if c, ok := openclawSpawnCache.m[p]; ok && c.mod.Equal(fi.ModTime()) && c.size == fi.Size() {
		return c.parents
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var store map[string]struct {
		SessionID          string `json:"sessionId"`
		SpawnedBy          string `json:"spawnedBy"`
		ParentSessionKey   string `json:"parentSessionKey"`
		SpawnedBySessionID string `json:"spawnedBySessionId"`
	}
	if json.Unmarshal(b, &store) != nil {
		return nil
	}
	parents := map[string]string{}
	for _, e := range store {
		if e.SessionID == "" {
			continue
		}
		parent := e.SpawnedBySessionID
		if parent == "" {
			k := e.ParentSessionKey
			if k == "" {
				k = e.SpawnedBy
			}
			parent = store[k].SessionID
		}
		if parent != "" {
			parents[e.SessionID] = parent
		}
	}
	if openclawSpawnCache.m == nil {
		openclawSpawnCache.m = map[string]openclawSpawnFile{}
	}
	openclawSpawnCache.m[p] = openclawSpawnFile{mod: fi.ModTime(), size: fi.Size(), parents: parents}
	return parents
}

type openclawSpawnFile struct {
	mod     time.Time
	size    int64
	parents map[string]string
}

var openclawSpawnCache struct {
	sync.Mutex
	m map[string]openclawSpawnFile
}
