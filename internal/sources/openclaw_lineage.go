package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
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

// openclawHeartbeatPoll is the user text OpenClaw's gateway writes for a
// heartbeat run; the prompt the model saw rides in __openclaw.
const openclawHeartbeatPoll = "[OpenClaw heartbeat poll]"

// skipHeartbeat drops a gateway heartbeat poll and its silent reply. Each
// poll runs a turn on agent:main:main that nobody asked for, and read as one
// it titled a window that only got polls and topped `deja last` (#5319). A
// reply that says something is kept: the poll found work to report.
func (r *piReader) skipHeartbeat(role string, msg map[string]any, txt string) bool {
	switch role {
	case "user":
		prov, _ := msg["provenance"].(map[string]any)
		r.inHeartbeat = prov["sourceTool"] == "heartbeat" || strings.TrimSpace(txt) == openclawHeartbeatPoll
		return r.inHeartbeat
	case "assistant":
		return r.inHeartbeat && openclawHeartbeatAck(txt)
	}
	return false
}

// openclawSilentRE is NO_REPLY alone, as OpenClaw's silent-reply check takes
// it (src/auto-reply/tokens.ts).
var openclawSilentRE = regexp.MustCompile(`(?i)^\s*NO_REPLY(?:\s+NO_REPLY)*\s*$`)

// openclawAckEdgeRE is HEARTBEAT_OK at either edge of a reply, with the
// trailing punctuation and the markup OpenClaw strips around it.
var openclawAckEdgeRE = regexp.MustCompile(`^[*` + "`" + `~_\s]*(?:HEARTBEAT_OK[^\w]{0,4}\s*)+|(?:\s*HEARTBEAT_OK[^\w]{0,4})+[*` + "`" + `~_\s]*$`)

// openclawHeartbeatAck reports whether a heartbeat reply is only the ack:
// NO_REPLY, which a heartbeat prompt asks for since 2026.10, or HEARTBEAT_OK,
// which the 2026.7 default prompt asks for (src/auto-reply/heartbeat.ts),
// with nothing else said (#5326).
func openclawHeartbeatAck(txt string) bool {
	if openclawSilentRE.MatchString(txt) {
		return true
	}
	if !strings.Contains(txt, "HEARTBEAT_OK") {
		return false
	}
	rest := openclawAckEdgeRE.ReplaceAllString(strings.TrimSpace(txt), "")
	return strings.TrimSpace(strings.Trim(rest, "*`~_")) == ""
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
	for c := range strings.FieldsSeq(string(cols)) {
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
// sessions.json beside the transcripts. The answer holds until that file
// changes, or, when it needed the transcripts, until one of them does;
// OpenClaw rewrites sessions.json every turn, so what the transcripts say
// about spawns is kept per file and only a changed transcript is read again.
func openclawStoreSpawns(dir string) map[string]string {
	p := filepath.Join(dir, "sessions.json")
	openclawSpawnCache.Lock()
	defer openclawSpawnCache.Unlock()
	sfi, err := os.Stat(p)
	if err != nil {
		return nil
	}
	if c, ok := openclawSpawnCache.parents[dir]; ok && c.store.Equal(sfi.ModTime()) && c.size == sfi.Size() {
		if !c.scanned {
			return c.parents
		}
		if _, changed := openclawScanDir(dir); !changed {
			return c.parents
		}
	}
	store := openclawReadStore(p)
	if store == nil {
		return nil
	}
	var scan []openclawSpawnScan
	scanned := false
	parents := map[string]string{}
	for childKey, e := range store {
		if e.SessionID == "" {
			continue
		}
		parent := e.SpawnedBySessionID
		if parent == "" {
			k := e.ParentSessionKey
			if k == "" {
				k = e.SpawnedBy
			}
			if parent = store[k].SessionID; parent != "" {
				childStart := openclawHeaderStart(filepath.Join(dir, e.SessionID+".jsonl"))
				curStart := openclawHeaderStart(filepath.Join(dir, parent+".jsonl"))
				if !childStart.IsZero() && !curStart.IsZero() && curStart.After(childStart) {
					if !scanned {
						scan, _ = openclawScanDir(dir)
						scanned = true
					}
					parent = openclawWindowBefore(scan, childKey, e.SessionID, childStart)
				}
			}
		}
		if parent != "" {
			parents[e.SessionID] = parent
		}
	}
	if openclawSpawnCache.parents == nil {
		openclawSpawnCache.parents = map[string]openclawSpawnParents{}
	}
	openclawSpawnCache.parents[dir] = openclawSpawnParents{store: sfi.ModTime(), size: sfi.Size(), scanned: scanned, parents: parents}
	return parents
}

type openclawStoreEntry struct {
	SessionID          string `json:"sessionId"`
	SpawnedBy          string `json:"spawnedBy"`
	ParentSessionKey   string `json:"parentSessionKey"`
	SpawnedBySessionID string `json:"spawnedBySessionId"`
}

// openclawReadStore is sessions.json's entries.
func openclawReadStore(p string) map[string]openclawStoreEntry {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var store map[string]openclawStoreEntry
	if json.Unmarshal(b, &store) != nil {
		return nil
	}
	return store
}

// openclawWindowBefore is the window that spawned a child when its requester
// key has moved on. sessions.json holds a key's current window only, so after
// a /new every child of the old window was filed under the new one, a session
// started after the child (#5307). The spawn call's result names the child's
// key in the requester's transcript: the window taken is the latest one that
// opened by the time the child did and names it, live or archived, compressed
// or not (#5340), and none when nothing does — no parent rather than one that
// never spawned it.
func openclawWindowBefore(scan []openclawSpawnScan, childKey, childID string, childStart time.Time) string {
	var best string
	var bestAt time.Time
	for _, f := range scan {
		if !f.keys[childKey] || f.id == "" || f.id == childID || f.at.IsZero() || f.at.After(childStart) || !f.at.After(bestAt) {
			continue
		}
		best, bestAt = f.id, f.at
	}
	return best
}

// openclawSpawnScan is what one transcript says about spawns: its header and
// the child keys its sessions_spawn results name.
type openclawSpawnScan struct {
	size int64
	mod  time.Time
	id   string
	at   time.Time
	keys map[string]bool
}

// openclawScanDir reads every transcript in dir once for its spawn results.
// A file whose size and mtime have not moved is not read again, so a parse
// costs a directory listing and the transcripts that changed since the last
// one (#5332). changed reports a transcript added, removed or moved since.
// Caller holds openclawSpawnCache.
func openclawScanDir(dir string) (out []openclawSpawnScan, changed bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, true
	}
	if openclawSpawnCache.m == nil {
		openclawSpawnCache.m = map[string]map[string]openclawSpawnScan{}
	}
	old := openclawSpawnCache.m[dir]
	cur := make(map[string]openclawSpawnScan, len(entries))
	out = make([]openclawSpawnScan, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || openclawCheckpointRE.MatchString(name) || strings.Contains(name, ".trajectory.") ||
			!strings.HasSuffix(name, ".jsonl") && !openclawArchiveRE.MatchString(name) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		f, ok := old[name]
		if !ok || f.size != fi.Size() || !f.mod.Equal(fi.ModTime()) {
			var read bool
			if f, read = openclawScanFile(filepath.Join(dir, name)); !read {
				// A .zst left unread for want of zstd is not kept, so the
				// first parse with zstd installed reads it.
				changed = true
				continue
			}
			f.size, f.mod = fi.Size(), fi.ModTime()
			changed = true
		}
		cur[name] = f
		out = append(out, f)
	}
	if len(cur) != len(old) {
		changed = true
	}
	openclawSpawnCache.m[dir] = cur
	return out, changed
}

var openclawChildKeyRE = regexp.MustCompile(`childSessionKey(?:\\*")?\s*:\s*\\*"([^"\\]+)`)

// openclawScanFile reads a transcript's header and the child keys its spawn
// results name as childSessionKey; a key merely mentioned in a turn is not
// one. Only lines holding that word are matched. A .zst archive is decoded
// first; read is false when zstd is missing, so the answer is not kept.
func openclawScanFile(p string) (f openclawSpawnScan, read bool) {
	if strings.HasSuffix(p, ".zst") {
		if !ZstdAvailable() {
			return f, false
		}
		openclawScanReads.Add(1)
		plain, err := zstdDecoded(p, "openclaw")
		if err != nil {
			return f, true
		}
		return openclawScanLines(bytes.NewReader(plain)), true
	}
	openclawScanReads.Add(1)
	fh, err := os.Open(p)
	if err != nil {
		return f, true
	}
	defer fh.Close()
	return openclawScanLines(fh), true
}

func openclawScanLines(src io.Reader) openclawSpawnScan {
	var f openclawSpawnScan
	r := bufio.NewReaderSize(src, 64<<10)
	needle := []byte("childSessionKey")
	for first := true; ; first = false {
		line, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// A line longer than the buffer is gathered whole.
			buf := append([]byte(nil), line...)
			for errors.Is(err, bufio.ErrBufferFull) {
				line, err = r.ReadSlice('\n')
				buf = append(buf, line...)
			}
			line = buf
		}
		if first {
			f.id, f.at = openclawHeader(line)
		}
		if bytes.Contains(line, needle) {
			for _, m := range openclawChildKeyRE.FindAllSubmatch(line, -1) {
				if f.keys == nil {
					f.keys = map[string]bool{}
				}
				f.keys[string(m[1])] = true
			}
		}
		if err != nil {
			return f
		}
	}
}

// openclawHeaderStart is when the transcript at p opened, by its header.
func openclawHeaderStart(p string) time.Time {
	f, err := os.Open(p)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadBytes('\n')
	_, at := openclawHeader(line)
	return at
}

// openclawHeader is the session id and start a transcript's first line names.
func openclawHeader(b []byte) (string, time.Time) {
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		b = b[:i]
	}
	var h struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Timestamp any    `json:"timestamp"`
	}
	if json.Unmarshal(b, &h) != nil || h.Type != "session" {
		return "", time.Time{}
	}
	return h.ID, parseTimeAny(h.Timestamp)
}

// openclawScanReads counts transcripts openclawScanFile has opened.
var openclawScanReads atomic.Int64

type openclawSpawnParents struct {
	store   time.Time
	size    int64
	scanned bool
	parents map[string]string
}

var openclawSpawnCache struct {
	sync.Mutex
	parents map[string]openclawSpawnParents
	m       map[string]map[string]openclawSpawnScan
}
