package sources

import (
	"encoding/hex"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func openclawArchiveBody(id, said string) string {
	return `{"type":"session","id":"` + id + `","cwd":"/work/app","timestamp":"2026-08-02T10:00:00Z"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"` + said + `"}]},"timestamp":"2026-08-02T10:01:00Z"}` + "\n"
}

// A reset or a delete renames the transcript rather than removing it, and that
// file is exactly the history someone comes to deja for after losing it — the
// old rule matched sessions/*.jsonl and let it fall out of the index (#2997).
func TestOpenClawReadsWhatAResetLeftBehind(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	archive := filepath.Join(sessions, "aaaa1111.jsonl.reset.1788000000")
	if err := os.WriteFile(archive, []byte(openclawArchiveBody("aaaa1111", "the reset queue keeps double-acking")), 0o644); err != nil {
		t.Fatal(err)
	}

	files := OpenClawSessionFiles()
	if len(files) != 1 || files[0] != archive {
		t.Fatalf("the archived transcript is not offered to the index: %#v", files)
	}
	ss, err := ParseOpenClawFile(archive)
	if err != nil || len(ss) != 1 {
		t.Fatalf("archive parsed to %d sessions: %v", len(ss), err)
	}
	// The id it had before the rename: that is what the session key mapping and
	// anyone looking for it still use.
	if ss[0].ID != "aaaa1111" {
		t.Fatalf("session id = %q, want the id from before the rename", ss[0].ID)
	}
	if ss[0].Path != archive {
		t.Fatalf("session path = %q, want the archive it was read from", ss[0].Path)
	}
	if len(ss[0].Messages) == 0 || !strings.Contains(ss[0].Messages[0].Text, "double-acking") {
		t.Fatalf("the archived turn did not come through: %#v", ss[0].Messages)
	}
}

// Not every transcript carries a session header, and the file name is the only
// place the id is then: the parser strips ".jsonl", which on an archive leaves
// the rename stamp in the id someone would look it up by.
func TestOpenClawArchiveKeepsTheIDFromBeforeTheRename(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	archive := filepath.Join(sessions, "eeee5555.jsonl.deleted.1788000002")
	headerless := `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"no header on this one"}]},"timestamp":"2026-08-02T10:01:00Z"}` + "\n"
	if err := os.WriteFile(archive, []byte(headerless), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseOpenClawFile(archive)
	if err != nil || len(ss) != 1 {
		t.Fatalf("archive parsed to %d sessions: %v", len(ss), err)
	}
	if ss[0].ID != "eeee5555" {
		t.Fatalf("session id = %q, want the id from before the rename", ss[0].ID)
	}
}

// A reset starts a new transcript under the old name. Reading both would index
// one conversation twice, so the live file wins.
func TestOpenClawSkipsAnArchiveWhoseLiveFileIsBack(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	live := filepath.Join(sessions, "bbbb2222.jsonl")
	if err := os.WriteFile(live, []byte(openclawArchiveBody("bbbb2222", "the live turn")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live+".reset.1788000000", []byte(openclawArchiveBody("bbbb2222", "the archived turn")), 0o644); err != nil {
		t.Fatal(err)
	}
	files := OpenClawSessionFiles()
	if len(files) != 1 || files[0] != live {
		t.Fatalf("want only the live transcript, got %#v", files)
	}
}

// Since the SQLite flip an explicit delete writes the archive compressed. zstd
// is already what the Zed and DeepSeek stores are read through.
func TestOpenClawReadsACompressedDeletedTranscript(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is not installed; the compressed archive path needs it")
	}
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	plain := filepath.Join(sessions, "cccc3333.jsonl.deleted.1788000001")
	if err := os.WriteFile(plain, []byte(openclawArchiveBody("cccc3333", "we settled on one shard")), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "zstd", "-q", "--rm", plain).CombinedOutput(); err != nil {
		t.Fatalf("zstd: %v: %s", err, out)
	}
	archive := plain + ".zst"
	if _, err := os.Stat(archive); err != nil {
		t.Fatal(err)
	}
	files := OpenClawSessionFiles()
	if len(files) != 1 || files[0] != archive {
		t.Fatalf("the compressed archive is not offered to the index: %#v", files)
	}
	ss, err := ParseOpenClawFile(archive)
	if err != nil || len(ss) != 1 {
		t.Fatalf("compressed archive parsed to %d sessions: %v", len(ss), err)
	}
	if ss[0].ID != "cccc3333" || !strings.Contains(ss[0].Messages[0].Text, "one shard") {
		t.Fatalf("the deleted session did not come back: %#v", ss[0])
	}
	// The archive, not the temporary copy it was decompressed into: the path is
	// what `deja show` prints and what the next index pass stats.
	if ss[0].Path != archive {
		t.Fatalf("session path = %q, want the archive itself", ss[0].Path)
	}
}

// A compaction checkpoint is a context snapshot rather than a conversation, and
// it stays out — the rule that lets archives in must not let those in with them.
func TestOpenClawStillSkipsCheckpoints(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	name := "dddd4444.checkpoint.aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee.jsonl"
	if err := os.WriteFile(filepath.Join(sessions, name), []byte(openclawArchiveBody("dddd4444", "snapshot")), 0o644); err != nil {
		t.Fatal(err)
	}
	if files := OpenClawSessionFiles(); len(files) != 0 {
		t.Fatalf("a checkpoint was offered to the index: %#v", files)
	}
}

// OpenClaw 2026.7 stamps an archive with toISOString() and ":" swapped for
// "-", not a number, so no reset or delete from a current build was read
// (#4482). The digit stamp stays in case an older build wrote it.
func TestOpenClawReadsAnArchiveWithAnISOStamp(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	iso := filepath.Join(sessions, "aaaa1111.jsonl.reset.2026-10-01T15-18-21.294Z")
	digits := filepath.Join(sessions, "bbbb2222.jsonl.reset.1788000000")
	noMillis := filepath.Join(sessions, "cccc3333.jsonl.deleted.2026-10-01T15-18-21Z")
	for p, said := range map[string]string{iso: "fix the retry loop in the uploader", digits: "rename the flaky queue worker", noMillis: "drop the stale cache"} {
		if err := os.WriteFile(p, []byte(openclawArchiveBody(strings.SplitN(filepath.Base(p), ".", 2)[0], said)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := OpenClawSessionFiles()
	if len(files) != 3 {
		t.Fatalf("archives offered to the index = %#v, want all three", files)
	}
	ss, err := ParseOpenClawFile(iso)
	if err != nil || len(ss) != 1 || ss[0].ID != "aaaa1111" {
		t.Fatalf("ISO-stamped archive parsed to %#v: %v", ss, err)
	}
	for _, p := range OpenClawSidecarFiles() {
		t.Errorf("an archive is counted as a sidecar: %s", p)
	}
}

// OpenClaw 2026.8 keeps sessions in agents/<agent>/agent/openclaw-agent.sqlite
// and leaves the sessions/ JSONL as archives (#2994). A store built from the
// fixture dump must come back with every session, including the one an earlier
// reset window left behind, under the agent's project and the pi-shaped roles.
func openclawTestDB(t *testing.T) (root, db string) {
	t.Helper()
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	sql, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "registry", "openclaw", "agent", "openclaw-agent.sql"))
	if err != nil {
		t.Fatal(err)
	}
	root = t.TempDir()
	db = filepath.Join(root, "main", "agent", "openclaw-agent.sqlite")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(string(sql))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sqlite fixture: %v: %s", err, out)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	return root, db
}

func TestOpenClawSQLiteStoreIsRead(t *testing.T) {
	root, db := openclawTestDB(t)
	if got := OpenClawAgentDBs(); len(got) != 1 || got[0] != db {
		t.Fatalf("OpenClawAgentDBs = %v, want [%s]", got, db)
	}
	sessions, err := ParseOpenClawDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2 (the live one and the reset one): %+v", len(sessions), sessions)
	}
	byID := map[string]int{}
	for i, s := range sessions {
		byID[s.ID] = i
		if s.Harness != "openclaw" || s.Path != db {
			t.Errorf("%s: harness=%q path=%q", s.ID, s.Harness, s.Path)
		}
	}
	live, ok := byID["a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"]
	if !ok {
		t.Fatalf("the migrated session is missing: %v", byID)
	}
	if n := len(sessions[live].Messages); n < 2 {
		t.Errorf("migrated session has %d messages, want the user and assistant turns", n)
	}
	reset, ok := byID["b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e"]
	if !ok {
		t.Fatalf("the session before the reset window is missing: %v", byID)
	}
	rs := sessions[reset]
	if rs.Project != "work/api" {
		t.Errorf("reset session project = %q, want the header cwd's project key %q", rs.Project, "work/api")
	}
	if len(rs.Messages) != 2 || rs.Messages[0].Role != "user" || rs.Messages[1].Role != "assistant" {
		t.Errorf("reset session messages = %+v", rs.Messages)
	}
	if !strings.Contains(rs.Messages[1].Text, "server_idle_timeout") {
		t.Errorf("assistant text = %q", rs.Messages[1].Text)
	}
	// The store counts as an OpenClaw file for discovery and the registry.
	if !strings.HasPrefix(db, root) {
		t.Fatal("test bug")
	}
	all := OpenClawStoreFiles()
	found := false
	for _, p := range all {
		if p == db {
			found = true
		}
	}
	if !found {
		t.Errorf("OpenClawStoreFiles does not list the store: %v", all)
	}
	if h := KindForPath(db); h != "openclaw-db" {
		t.Errorf("KindForPath(db) = %q, want openclaw-db", h)
	}
}

func TestOpenClawSQLiteSinceReturnsWholeSessions(t *testing.T) {
	_, db := openclawTestDB(t)
	// Only the reset session has events after this stamp; it must come back
	// whole (both turns), and the migrated session must not come back at all.
	since := time.UnixMilli(1784813600500)
	sessions, err := ParseOpenClawDBSince(db, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e" {
		t.Fatalf("since = %+v, want only the reset session", sessions)
	}
	if len(sessions[0].Messages) != 2 {
		t.Errorf("a session-scoped cursor hands the session back whole; got %d messages", len(sessions[0].Messages))
	}
	none, err := ParseOpenClawDBSince(db, time.UnixMilli(1790000000000))
	if err != nil || len(none) != 0 {
		t.Errorf("nothing after the future stamp, got %v %v", none, err)
	}
}

func TestOpenClawEmptyStoreIsQuietAndAForeignOneSaysSo(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	empty := filepath.Join(dir, "openclaw-agent.sqlite")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ParseOpenClawDB(empty); err != nil || len(got) != 0 {
		t.Errorf("empty store: %v %v", got, err)
	}
	other := filepath.Join(dir, "other.sqlite")
	if out, err := exec.CommandContext(t.Context(), "sqlite3", other, "create table t(x)").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	// A database that exists but has no transcript_events is a schema deja
	// does not know — the hermes rule: say so, rather than report an empty
	// store that doctor would call healthy.
	if got, err := ParseOpenClawDB(other); err == nil || len(got) != 0 {
		t.Errorf("a store without transcript_events must surface an error, got %v %v", got, err)
	}
}

func TestOpenClawSessionKeyFromInitialSQLiteSchema(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "openclaw-agent.sqlite")
	stmts := `create table sessions(session_id text primary key, session_key text not null);
create table session_routes(session_key text primary key, session_id text not null);
insert into sessions values('old', 'agent:main:main'), ('current', 'agent:main:main'), ('other', 'agent:main:other');
insert into session_routes values('agent:main:main', 'current'), ('agent:main:other', 'other');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("create initial SQLite schema: %v: %s", err, out)
	}
	for _, tc := range []struct {
		id, want string
	}{
		{"current", "agent:main:main"},
		{"other", "agent:main:other"},
		{"old", ""},
		{"missing", ""},
		{"current' OR 1=1 --", ""},
	} {
		got, err := OpenClawSessionKey(db, tc.id)
		if err != nil || got != tc.want {
			t.Errorf("OpenClawSessionKey(%q) = %q, %v; want %q", tc.id, got, err, tc.want)
		}
	}
}

func TestOpenClawSessionKeyRejectsUnknownSQLiteSchema(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "openclaw-agent.sqlite")
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, "create table unrelated(id text)").CombinedOutput(); err != nil {
		t.Fatalf("create unknown schema: %v: %s", err, out)
	}
	if _, err := OpenClawSessionKey(db, "session-id"); err == nil {
		t.Fatal("an unreadable session schema must not look like a missing session")
	}
}

// A gateway heartbeat poll is a turn on agent:main:main that nobody asked for:
// `[OpenClaw heartbeat poll]` and a NO_REPLY. Read as the person's, a window
// that only got polls topped `deja last` under that title (#5319). The lines
// are OpenClaw 2026.10.4's, from a live gateway.
func TestOpenClawHeartbeatPollsAreNotTurns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	poll := `{"type":"message","id":"%s","timestamp":"%s","message":{"role":"user","content":"[OpenClaw heartbeat poll]","provenance":{"kind":"internal_system","sourceTool":"heartbeat"},"__openclaw":{"senderIsOwner":false,"modelPromptProjection":{"version":1,"text":"Follow the heartbeat monitor scratch context when provided. If nothing needs attention, reply NO_REPLY."}}}}
{"type":"message","id":"%s-r","timestamp":"%s","message":{"role":"assistant","content":[{"type":"text","text":"NO_REPLY"}]}}
`
	only := write("hb.jsonl", `{"type":"session","version":4,"id":"hb","timestamp":"2026-10-18T05:42:12Z","cwd":"/work/app"}
`+fmt.Sprintf(poll, "u1", "2026-10-18T05:42:12Z", "u1", "2026-10-18T05:42:16Z"))
	main := write("dm.jsonl", `{"type":"session","version":4,"id":"dm","timestamp":"2026-10-18T05:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-10-18T05:00:01Z","message":{"role":"user","content":"the eviction policy is LRU with a 512 MB cap"}}
{"type":"message","id":"b","timestamp":"2026-10-18T05:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"Noted."}]}}
`+fmt.Sprintf(poll, "u2", "2026-10-18T06:12:00Z", "u2", "2026-10-18T06:12:04Z")+
		`{"type":"message","id":"c","timestamp":"2026-10-18T06:42:00Z","message":{"role":"user","content":"[OpenClaw heartbeat poll]","provenance":{"kind":"internal_system","sourceTool":"heartbeat"}}}
{"type":"message","id":"d","timestamp":"2026-10-18T06:42:04Z","message":{"role":"assistant","content":[{"type":"text","text":"The nightly build failed on main."}]}}
`)
	got, err := ParseOpenClawFile(only)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if len(s.Messages) > 0 {
			t.Errorf("a window holding only heartbeat polls reads as a session: %+v", s.Messages)
		}
	}
	got, err = ParseOpenClawFile(main)
	if err != nil || len(got) != 1 {
		t.Fatalf("dm: %v %v", got, err)
	}
	texts := make([]string, 0, len(got[0].Messages))
	for _, m := range got[0].Messages {
		texts = append(texts, m.Text)
	}
	want := []string{"the eviction policy is LRU with a 512 MB cap", "Noted.", "The nightly build failed on main."}
	if len(texts) != len(want) {
		t.Fatalf("dm messages = %q, want %q", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("dm messages = %q, want %q", texts, want)
		}
	}
}

// OpenClaw 2026.7's default heartbeat prompt says "reply HEARTBEAT_OK", not
// NO_REPLY, to the same poll; every ack stayed in the window as an assistant
// turn (#5326). An ack that carries a report is kept.
func TestOpenClawHeartbeatOKIsNotATurn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	poll := `{"type":"message","id":"%s","timestamp":"2026-07-20T05:42:12Z","message":{"role":"user","content":"[OpenClaw heartbeat poll]"}}
{"type":"message","id":"%s-r","timestamp":"2026-07-20T05:42:16Z","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}
`
	body := `{"type":"session","version":3,"id":"hb","timestamp":"2026-07-20T05:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T05:00:01Z","message":{"role":"user","content":"the eviction policy is LRU with a 512 MB cap"}}
{"type":"message","id":"b","timestamp":"2026-07-20T05:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"Noted."}]}}
`
	var bodySb81 strings.Builder
	for i, ack := range []string{"HEARTBEAT_OK", "**HEARTBEAT_OK**", "HEARTBEAT_OK.", "NO_REPLY NO_REPLY"} {
		fmt.Fprintf(&bodySb81, poll, fmt.Sprint("u", i), fmt.Sprint("u", i), ack)
	}
	body += bodySb81.String()
	report := "HEARTBEAT_OK — but the nightly build failed on main."
	body += fmt.Sprintf(poll, "u9", "u9", report)
	p := filepath.Join(dir, "hb.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ParseOpenClawFile(p)
	if err != nil || len(got) != 1 {
		t.Fatalf("hb: %v %v", got, err)
	}
	texts := make([]string, 0, len(got[0].Messages))
	for _, m := range got[0].Messages {
		texts = append(texts, m.Text)
	}
	want := []string{"the eviction policy is LRU with a 512 MB cap", "Noted.", report}
	if fmt.Sprint(texts) != fmt.Sprint(want) {
		t.Errorf("messages = %q, want %q", texts, want)
	}
}

// Shapes taken from an OpenClaw 2026.10.4 store after one sessions_spawn with
// context "isolated" and one with context "fork": the child's session_nodes
// entry names the requester (spawnedBySessionId, parent_session_key), and a
// fork child's header names the source before the source's copied entries.
const openclawSpawnSQL = `
CREATE TABLE session_nodes (session_key TEXT PRIMARY KEY, current_session_id TEXT NOT NULL, entry_json TEXT NOT NULL,
  parent_session_key TEXT, spawned_by TEXT);
CREATE TABLE session_windows (session_id TEXT PRIMARY KEY, session_key TEXT NOT NULL, created_at INTEGER NOT NULL,
  parent_session_key TEXT, spawned_by TEXT);
CREATE TABLE transcript_events (session_id TEXT NOT NULL, seq INTEGER NOT NULL, event_json TEXT, created_at INTEGER NOT NULL,
  PRIMARY KEY (session_id, seq));
INSERT INTO session_nodes VALUES('agent:main:explicit:p','p','{"sessionId":"p"}',NULL,NULL);
INSERT INTO session_nodes VALUES('agent:main:subagent:1','iso','{"sessionId":"iso","spawnedBy":"agent:main:explicit:p","spawnedBySessionId":"p"}','agent:main:explicit:p','agent:main:explicit:p');
INSERT INTO session_nodes VALUES('agent:main:subagent:2','frk','{"sessionId":"frk","spawnedBySessionId":"p","forkedFromParent":true}','agent:main:explicit:p','agent:main:explicit:p');
INSERT INTO session_nodes VALUES('agent:main:subagent:3','old','not json','agent:main:explicit:p',NULL);
INSERT INTO session_windows VALUES('p','agent:main:explicit:p',1000,NULL,NULL);
INSERT INTO session_windows VALUES('iso','agent:main:subagent:1',2000,'agent:main:explicit:p','agent:main:explicit:p');
INSERT INTO session_windows VALUES('frk','agent:main:subagent:2',3000,'agent:main:explicit:p','agent:main:explicit:p');
INSERT INTO session_windows VALUES('old','agent:main:subagent:3',4000,'agent:main:explicit:p',NULL);
INSERT INTO transcript_events VALUES('p',0,'{"type":"session","version":4,"id":"p","timestamp":"2026-10-18T05:37:20.000Z","cwd":"/work/app"}',1);
INSERT INTO transcript_events VALUES('p',1,'{"type":"message","id":"a","timestamp":"2026-10-18T05:37:25.000Z","message":{"role":"user","content":"remember the codeword FROSTBYTE"}}',2);
INSERT INTO transcript_events VALUES('p',2,'{"type":"message","id":"b","parentId":"a","timestamp":"2026-10-18T05:37:27.000Z","message":{"role":"assistant","content":[{"type":"text","text":"remembered"}]}}',3);
INSERT INTO transcript_events VALUES('iso',0,'{"type":"session","version":4,"id":"iso","timestamp":"2026-10-18T05:35:53.000Z","cwd":"/work/app"}',4);
INSERT INTO transcript_events VALUES('iso',1,'{"type":"message","id":"c","timestamp":"2026-10-18T05:35:54.000Z","message":{"role":"user","content":"<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>\n\n[Subagent Context] You are running as a subagent (depth 1/5).\n\n[Subagent Task]\n\n<<<END_OPENCLAW_INTERNAL_CONTEXT>>>\n\nwrite the notes file\n\n<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>\n\nBegin.\n\n<<<END_OPENCLAW_INTERNAL_CONTEXT>>>"}}',5);
INSERT INTO transcript_events VALUES('iso',2,'{"type":"message","id":"d","parentId":"c","timestamp":"2026-10-18T05:35:55.000Z","message":{"role":"assistant","content":[{"type":"text","text":"wrote notes"}]}}',6);
INSERT INTO transcript_events VALUES('frk',0,'{"type":"session","version":4,"id":"frk","timestamp":"2026-10-18T05:37:30.749Z","cwd":"/work/app","parentSession":"sqlite:main:p:/home/u/.openclaw/agents/main/agent/openclaw-agent.sqlite"}',7);
INSERT INTO transcript_events VALUES('frk',1,'{"type":"message","id":"a","timestamp":"2026-10-18T05:37:25.000Z","message":{"role":"user","content":"remember the codeword FROSTBYTE"}}',8);
INSERT INTO transcript_events VALUES('frk',2,'{"type":"message","id":"b","parentId":"a","timestamp":"2026-10-18T05:37:27.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"t1","name":"write","arguments":{"path":"parent.txt","content":"the parent wrote this before the fork happened"}}]}}',9);
INSERT INTO transcript_events VALUES('frk',3,'{"type":"message","id":"e","parentId":"b","timestamp":"2026-10-18T05:37:31.653Z","message":{"role":"user","content":"write the fork file"}}',10);
INSERT INTO transcript_events VALUES('frk',4,'{"type":"message","id":"f","parentId":"e","timestamp":"2026-10-18T05:37:33.000Z","message":{"role":"assistant","content":[{"type":"text","text":"wrote fork"}]}}',11);
INSERT INTO transcript_events VALUES('old',0,'{"type":"message","id":"g","timestamp":"2026-10-18T05:40:00.000Z","message":{"role":"user","content":"a child whose entry is unreadable"}}',12);
`

func openclawByID(ss []model.Session) map[string]model.Session {
	out := map[string]model.Session{}
	for _, s := range ss {
		out[s.ID] = s
	}
	return out
}

func TestOpenClawSubagentsHangUnderTheirParent(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	root := t.TempDir()
	db := filepath.Join(root, "main", "agent", "openclaw-agent.sqlite")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(openclawSpawnSQL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create store: %v: %s", err, out)
	}
	ss, err := ParseOpenClawDB(db)
	if err != nil {
		t.Fatal(err)
	}
	by := openclawByID(ss)
	if p := by["p"]; p.Kind != "" || p.Parent != "" {
		t.Errorf("the requester is nobody's child: kind=%q parent=%q", p.Kind, p.Parent)
	}
	for _, id := range []string{"iso", "frk", "old"} {
		if s := by[id]; s.Kind != "subagent" || s.Parent != "p" {
			t.Errorf("%s: kind=%q parent=%q, want a subagent of p", id, s.Kind, s.Parent)
		}
	}
	iso := by["iso"]
	if len(iso.Messages) == 0 || iso.Messages[0].Text != "write the notes file" {
		t.Errorf("iso first turn = %q, want the task without OpenClaw's runtime blocks", iso.Messages[0].Text)
	}
	frk := by["frk"]
	for _, m := range frk.Messages {
		if strings.Contains(m.Text, "FROSTBYTE") || strings.Contains(m.Text, "parent.txt") {
			t.Errorf("fork child holds its parent's copied entry: %+v", m)
		}
	}
	if len(frk.Messages) != 2 || frk.Messages[0].Text != "write the fork file" {
		t.Errorf("fork child messages = %+v, want only its own two turns", frk.Messages)
	}
}

// A JSONL store keeps the same entry fields in sessions.json, by key. A branch
// names its source in the header and nothing spawned it.
func TestOpenClawJSONLSubagentAndBranch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("sessions.json", `{"agent:main:main":{"sessionId":"p"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"}}`)
	kid := write("kid.jsonl", `{"type":"session","version":3,"id":"kid","timestamp":"2026-07-20T09:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T09:00:01Z","message":{"role":"user","content":"child task"}}
`)
	br := write("br.jsonl", `{"type":"session","version":3,"id":"br","timestamp":"2026-07-20T10:00:00Z","cwd":"/work/app","parentSession":"`+filepath.ToSlash(filepath.Join(dir, "p.jsonl"))+`"}
{"type":"message","id":"a","timestamp":"2026-07-20T09:30:00Z","message":{"role":"user","content":"copied from the source"}}
{"type":"message","id":"b","parentId":"a","timestamp":"2026-07-20T10:00:01Z","message":{"role":"user","content":"after the branch"}}
`)
	got, err := ParseOpenClawFile(kid)
	if err != nil || len(got) != 1 {
		t.Fatalf("kid: %v %v", got, err)
	}
	if got[0].Kind != "subagent" || got[0].Parent != "p" {
		t.Errorf("kid: kind=%q parent=%q, want a subagent of p", got[0].Kind, got[0].Parent)
	}
	got, err = ParseOpenClawFile(br)
	if err != nil || len(got) != 1 {
		t.Fatalf("br: %v %v", got, err)
	}
	if got[0].Kind != "fork" || got[0].Parent != "p" {
		t.Errorf("br: kind=%q parent=%q, want a fork of p", got[0].Kind, got[0].Parent)
	}
	if len(got[0].Messages) != 1 || got[0].Messages[0].Text != "after the branch" {
		t.Errorf("br messages = %+v, want only what came after the branch", got[0].Messages)
	}
}

func TestOpenClawParentSessionForms(t *testing.T) {
	for in, want := range map[string]string{
		"sqlite:main:q-parent2:/home/u/.openclaw/agents/main/agent/openclaw-agent.sqlite": "q-parent2",
		"/home/u/.openclaw/agents/main/sessions/abc.jsonl":                                "abc",
		"/home/u/.openclaw/agents/main/sessions/abc.jsonl.reset.2026-07-20T09-00-00.000Z": "abc",
		"prev-id": "prev-id",
		"  ":      "",
	} {
		if got := openclawParentSession(in); got != want {
			t.Errorf("openclawParentSession(%q) = %q, want %q", in, got, want)
		}
	}
	if got := openclawUserText("<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>> unclosed"); got != "<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>> unclosed" {
		t.Errorf("an unclosed block is kept, got %q", got)
	}
}

// The session OpenClaw wrote to last is read off the store, not the index,
// so an MCP call from a hookless TUI can tell which conversation is asking
// (#5271).
func TestOpenClawNewestSessionReadsTheStore(t *testing.T) {
	root, _ := openclawTestDB(t)
	at := time.UnixMilli(1784813602000)
	id, when := OpenClawNewestSession(time.UnixMilli(1784538000000))
	if id != "b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e" || !when.Equal(at) {
		t.Fatalf("newest = %q at %v, want the reset window's at %v", id, when, at)
	}
	if id, _ := OpenClawNewestSession(at); id != "" {
		t.Errorf("nothing was written after %v, got %q", at, id)
	}

	// A transcript file written after the store's last event wins.
	p := filepath.Join(root, "main", "sessions", "tui-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"type":"session","version":3,"id":"tui-1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := at.Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if id, when := OpenClawNewestSession(at.Add(-time.Hour)); id != "tui-1" || !when.Equal(later) {
		t.Errorf("newest = %q at %v, want tui-1 at %v", id, when, later)
	}
}

// sessions.json holds a key's current window only. After /new, a child
// spawned from the old window resolved to the new one, a session started two
// hours after it (#5307). The spawn call's result in the old window names the
// child's key, which is what the parent is found by.
func TestOpenClawJSONLChildKeepsTheWindowThatSpawnedIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// The spawn result as OpenClaw 2026.7 writes it: JSON text in a toolResult.
	spawn := `{"type":"message","id":"r","timestamp":"2026-07-20T08:59:59Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"sessions_spawn","content":[{"type":"text","text":"{\"status\":\"accepted\",\"childSessionKey\":\"agent:main:subagent:1\",\"runId\":\"r1\"}"}]}}`
	write("p.jsonl", `{"type":"session","version":3,"id":"p","timestamp":"2026-07-20T08:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T08:59:58Z","message":{"role":"user","content":"spawn a helper"}}
`+spawn+"\n")
	kid := write("kid.jsonl", `{"type":"session","version":3,"id":"kid","timestamp":"2026-07-20T09:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T09:00:01Z","message":{"role":"user","content":"child task"}}
`)
	parentOf := func() string {
		t.Helper()
		got, err := ParseOpenClawFile(kid)
		if err != nil || len(got) != 1 {
			t.Fatalf("kid: %v %v", got, err)
		}
		return got[0].Parent
	}
	write("sessions.json", `{"agent:main:main":{"sessionId":"p"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"}}`)
	if got := parentOf(); got != "p" {
		t.Fatalf("before /new: parent %q, want p", got)
	}

	// A window from before the child that only mentions its key in a turn
	// did not spawn it.
	write("p0.jsonl", `{"type":"session","version":3,"id":"p0","timestamp":"2026-07-20T08:30:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T08:30:01Z","message":{"role":"user","content":"is agent:main:subagent:1\" a key?"}}
`)
	// /new: a new window under the same key, which mentions the child too.
	write("p2.jsonl", `{"type":"session","version":3,"id":"p2","timestamp":"2026-07-20T11:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T11:00:01Z","message":{"role":"user","content":"what did agent:main:subagent:1\" do?"}}
`)
	write("sessions.json", `{"agent:main:main":{"sessionId":"p2"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"}}`)
	if got := parentOf(); got != "p" {
		t.Errorf("after /new: parent %q, want p, the window that spawned it", got)
	}

	// The old window archived by a reset is still the spawner.
	if err := os.Rename(filepath.Join(dir, "p.jsonl"), filepath.Join(dir, "p.jsonl.reset.2026-07-20T10-59-00.000Z")); err != nil {
		t.Fatal(err)
	}
	write("sessions.json", `{"agent:main:main":{"sessionId":"p2"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"} }`)
	if got := parentOf(); got != "p" {
		t.Errorf("after a reset archived it: parent %q, want p", got)
	}

	// Nothing names the child: no parent, rather than a window opened after it.
	if err := os.Remove(filepath.Join(dir, "p.jsonl.reset.2026-07-20T10-59-00.000Z")); err != nil {
		t.Fatal(err)
	}
	write("sessions.json", `{"agent:main:main":{"sessionId":"p2"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"}}  `)
	if got := parentOf(); got != "" {
		t.Errorf("with no spawner on disk: parent %q, want none", got)
	}
}

// Resolving a child spawned before a /new read every transcript in the
// directory, again for each such child and again on every sessions.json
// rewrite, which OpenClaw does each turn: 60 children by 300 transcripts took
// 12 s for one parse (#5332). The transcripts are read once between them.
func TestOpenClawSpawnWindowReadsEachTranscriptOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const kids, others = 20, 30
	store := `{"agent:main:main":{"sessionId":"cur"}`
	old := `{"type":"session","version":3,"id":"old","timestamp":"2026-07-20T08:00:00Z"}` + "\n"
	var oldSb98 strings.Builder
	var storeSb98 strings.Builder
	for i := range kids {
		fmt.Fprintf(&oldSb98, `{"type":"message","id":"r%d","timestamp":"2026-07-20T08:10:00Z","message":{"role":"toolResult","toolName":"sessions_spawn","content":[{"type":"text","text":"{\\"childSessionKey\\":\\"agent:main:subagent:%d\\"}"}]}}`+"\n", i, i)
		write(fmt.Sprintf("kid%d.jsonl", i), fmt.Sprintf(`{"type":"session","version":3,"id":"kid%d","timestamp":"2026-07-20T09:00:00Z"}
{"type":"message","id":"a","timestamp":"2026-07-20T09:00:01Z","message":{"role":"user","content":"child task"}}
`, i))
		fmt.Fprintf(&storeSb98, `,"agent:main:subagent:%d":{"sessionId":"kid%d","spawnedBy":"agent:main:main"}`, i, i)
	}
	old += oldSb98.String()
	store += storeSb98.String()
	write("old.jsonl", old)
	write("cur.jsonl", `{"type":"session","version":3,"id":"cur","timestamp":"2026-07-20T11:00:00Z"}`+"\n")
	for i := range others {
		write(fmt.Sprintf("o%d.jsonl", i), fmt.Sprintf(`{"type":"session","version":3,"id":"o%d","timestamp":"2026-07-19T08:00:00Z"}`+"\n", i))
	}
	write("sessions.json", store+"}")
	files := int64(kids + others + 2)

	parse := func(i int) {
		t.Helper()
		got, err := ParseOpenClawFile(filepath.Join(dir, fmt.Sprintf("kid%d.jsonl", i)))
		if err != nil || len(got) != 1 || got[0].Parent != "old" {
			t.Fatalf("kid%d: %+v %v, want parent old", i, got, err)
		}
	}
	before := openclawScanReads.Load()
	parse(0)
	if n := openclawScanReads.Load() - before; n > files {
		t.Errorf("one parse read %d transcripts, want at most the %d in the directory", n, files)
	}
	// OpenClaw rewrites sessions.json every turn; that alone reads nothing again.
	before = openclawScanReads.Load()
	for i := range kids {
		now := time.Now().Add(time.Duration(i+1) * time.Second)
		if err := os.Chtimes(filepath.Join(dir, "sessions.json"), now, now); err != nil {
			t.Fatal(err)
		}
		parse(i)
	}
	if n := openclawScanReads.Load() - before; n != 0 {
		t.Errorf("%d parses after sessions.json rewrites read %d transcripts again, want 0", kids, n)
	}
	// A transcript that grew is read again, and only that one, with or
	// without a sessions.json rewrite.
	write("cur.jsonl", `{"type":"session","version":3,"id":"cur","timestamp":"2026-07-20T11:00:00Z"}`+"\n"+`{"type":"message","id":"a","timestamp":"2026-07-20T11:00:01Z","message":{"role":"user","content":"next"}}`+"\n")
	before = openclawScanReads.Load()
	parse(1)
	if n := openclawScanReads.Load() - before; n != 1 {
		t.Errorf("after one transcript changed a parse read %d, want 1", n)
	}

	// A spawn result that lands after the parse, with sessions.json as it
	// was, still moves the child to the window that names it.
	write("mid.jsonl", `{"type":"session","version":3,"id":"mid","timestamp":"2026-07-20T08:30:00Z"}`+"\n")
	parse(2)
	write("mid.jsonl", `{"type":"session","version":3,"id":"mid","timestamp":"2026-07-20T08:30:00Z"}`+"\n"+
		`{"type":"message","id":"r","timestamp":"2026-07-20T08:40:00Z","message":{"role":"toolResult","toolName":"sessions_spawn","content":[{"type":"text","text":"{\\"childSessionKey\\":\\"agent:main:subagent:2\\"}"}]}}`+"\n")
	got, err := ParseOpenClawFile(filepath.Join(dir, "kid2.jsonl"))
	if err != nil || len(got) != 1 || got[0].Parent != "mid" {
		t.Errorf("kid2 after a later spawn result: %+v %v, want parent mid", got, err)
	}
}

// A spawner window archived compressed was never opened by the lookup, so its
// child got no parent (#5340). It is read like the plain archive, and once.
func TestOpenClawChildKeepsASpawnerArchivedAsZst(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd CLI not installed")
	}
	dir := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	spawn := `{"type":"message","id":"r","timestamp":"2026-07-20T08:59:59Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"sessions_spawn","content":[{"type":"text","text":"{\"status\":\"accepted\",\"childSessionKey\":\"agent:main:subagent:1\",\"runId\":\"r1\"}"}]}}`
	plain := write("p.jsonl.deleted.2026-07-20T10-59-00.000Z", `{"type":"session","version":3,"id":"p","timestamp":"2026-07-20T08:00:00Z","cwd":"/work/app"}
`+spawn+"\n")
	if out, err := exec.CommandContext(t.Context(), "zstd", "-q", "--rm", plain).CombinedOutput(); err != nil {
		t.Fatalf("zstd: %v: %s", err, out)
	}
	kid := write("kid.jsonl", `{"type":"session","version":3,"id":"kid","timestamp":"2026-07-20T09:00:00Z","cwd":"/work/app"}
{"type":"message","id":"a","timestamp":"2026-07-20T09:00:01Z","message":{"role":"user","content":"child task"}}
`)
	write("p2.jsonl", `{"type":"session","version":3,"id":"p2","timestamp":"2026-07-20T11:00:00Z","cwd":"/work/app"}`+"\n")
	store := write("sessions.json", `{"agent:main:main":{"sessionId":"p2"},"agent:main:subagent:1":{"sessionId":"kid","spawnedBy":"agent:main:main"}}`)
	parentOf := func() string {
		t.Helper()
		got, err := ParseOpenClawFile(kid)
		if err != nil || len(got) != 1 {
			t.Fatalf("kid: %v %v", got, err)
		}
		return got[0].Parent
	}
	// Without zstd the archive is left unread and not remembered, so the
	// first parse with zstd installed finds the spawner.
	zstdPin.mu.Lock()
	prevPinned, prevOK := zstdPin.pinned, zstdPin.ok
	zstdPin.pinned, zstdPin.ok = true, false
	zstdPin.mu.Unlock()
	got := parentOf()
	zstdPin.mu.Lock()
	zstdPin.pinned, zstdPin.ok = prevPinned, prevOK
	zstdPin.mu.Unlock()
	if got != "" {
		t.Fatalf("without zstd: parent %q, want none", got)
	}
	if got := parentOf(); got != "p" {
		t.Fatalf("spawner archived as .zst: parent %q, want p", got)
	}
	before := openclawScanReads.Load()
	now := time.Now().Add(time.Second)
	if err := os.Chtimes(store, now, now); err != nil {
		t.Fatal(err)
	}
	if got := parentOf(); got != "p" {
		t.Fatalf("after a sessions.json rewrite: parent %q, want p", got)
	}
	if n := openclawScanReads.Load() - before; n != 0 {
		t.Errorf("a sessions.json rewrite read %d transcripts again, want 0", n)
	}
}

func TestOpenClawSessionFiles(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "registry", "openclaw", "agents"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	files := OpenClawSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v, want exactly the live transcript (checkpoint and sessions.json skipped)", files)
	}
	if base := filepath.Base(files[0]); base != "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d.jsonl" {
		t.Fatalf("unexpected transcript %q", base)
	}
}

func TestParseOpenClawFile(t *testing.T) {
	path := filepath.Join("..", "..", "fixtures", "registry", "openclaw", "agents", "main", "sessions",
		"a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d.jsonl")
	sessions, err := ParseOpenClawFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	s := sessions[0]
	if s.Harness != "openclaw" {
		t.Fatalf("harness = %q", s.Harness)
	}
	if s.ID != "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d" {
		t.Fatalf("id = %q", s.ID)
	}
	if len(s.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(s.Messages))
	}
	if s.Messages[0].Role != "user" || s.Messages[1].Role != "assistant" || s.Messages[2].Role != RoleSummary {
		t.Fatalf("roles = %q,%q,%q", s.Messages[0].Role, s.Messages[1].Role, s.Messages[2].Role)
	}
	// Header cwd promotes to the project key.
	if s.Project != "workspace/registry-demo" {
		t.Fatalf("project = %q", s.Project)
	}
}

func TestOpenClawProjectFallback(t *testing.T) {
	if got := openclawProject("/x/agents/work/sessions/s.jsonl"); got != "openclaw-work" {
		t.Fatalf("project = %q, want openclaw-work", got)
	}
}

// The trajectory log is OpenClaw's runtime artifact, not a conversation: read
// as one, it was a second session for every run (#4477).
func TestOpenClawTrajectoryIsNotATranscript(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	dir := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"s1.jsonl", "s1.trajectory.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := OpenClawSessionFiles()
	if slices.Contains(got, filepath.Join(dir, "s1.trajectory.jsonl")) || len(got) != 1 {
		t.Errorf("transcripts = %v, want only s1.jsonl", got)
	}
}

// OpenClaw archives the trajectory with the transcript on a delete, under the
// same .deleted.<ts> suffix. That copy is the same runtime log, not history.
func TestOpenClawArchivedTrajectoryIsNotATranscript(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	dir := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"s1.jsonl.deleted.1788000002", "s1.trajectory.jsonl.deleted.1788000002"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := OpenClawSessionFiles()
	if len(got) != 1 || got[0] != filepath.Join(dir, "s1.jsonl.deleted.1788000002") {
		t.Errorf("transcripts = %v, want only the archived s1.jsonl", got)
	}
}

// OpenClaw 2026.9.9 stores an event of 1 KiB or more as a zstd frame in
// event_zstd with event_json null. Read for event_json alone, those events,
// a compaction summary among them, were not there at all.
func TestOpenClawReadsCompressedEvents(t *testing.T) {
	if !SQLite3Available() || !ZstdAvailable() {
		t.Skip("sqlite3 or zstd not installed")
	}
	const id = "c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f"
	events := []string{
		`{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-01T09:00:00Z","cwd":"/w/p"}`,
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-10-01T09:00:01Z","message":{"role":"user","content":"why does the pool stall"}}`,
		`{"type":"compaction","id":"c1","parentId":"u1","timestamp":"2026-10-01T09:00:02Z","summary":"ZSTD-SUMMARY ` + strings.Repeat("the pool stalled on the default timeout. ", 40) + `","firstKeptEntryId":"u1","tokensBefore":9000}`,
		`{"type":"message","id":"a1","parentId":"c1","timestamp":"2026-10-01T09:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"ZSTD-ANSWER ` + strings.Repeat("raise server_idle_timeout. ", 50) + `"}]}}`,
	}
	sql := "create table transcript_events (session_id text not null, seq integer not null, event_json text, created_at integer not null, event_zstd blob, event_utf8_bytes integer, navigation_json text, primary key (session_id, seq));\n"
	var sqlSb29 strings.Builder
	for i, e := range events {
		row := "'" + strings.ReplaceAll(e, "'", "''") + "',null,null"
		if len(e) >= 1024 {
			cmd := exec.CommandContext(t.Context(), "zstd", "-c", "-q", "-1")
			cmd.Stdin = strings.NewReader(e)
			frame, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			row = "null,X'" + hex.EncodeToString(frame) + "'," + strconv.Itoa(len(e))
		}
		sqlSb29.WriteString("insert into transcript_events (session_id,seq,event_json,event_zstd,event_utf8_bytes,created_at) values ('" + id + "'," + strconv.Itoa(i) + "," + row + ",1790000000000);\n")
	}
	sql += sqlSb29.String()
	db := filepath.Join(t.TempDir(), "main", "agent", "openclaw-agent.sqlite")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(t.Context(), "sqlite3", db)
	build.Stdin = strings.NewReader(sql)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build store: %v: %s", err, out)
	}
	check := func(t *testing.T) {
		t.Helper()
		ss, err := ParseOpenClawDB(db)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse: %v %d", err, len(ss))
		}
		got := make([]string, 0, len(ss[0].Messages))
		for _, m := range ss[0].Messages {
			got = append(got, m.Role+":"+strings.Fields(m.Text)[0])
		}
		if want := "user:why|summary:ZSTD-SUMMARY|assistant:ZSTD-ANSWER"; strings.Join(got, "|") != want {
			t.Errorf("messages = %q, want %q", strings.Join(got, "|"), want)
		}
	}
	check(t)

	// A corrupt frame loses its own event, not the session's other ones.
	corrupt := exec.CommandContext(t.Context(), "sqlite3", db, "update transcript_events set event_zstd = X'28B52FFD00' where seq = 2")
	if out, err := corrupt.CombinedOutput(); err != nil {
		t.Fatalf("corrupt: %v: %s", err, out)
	}
	ss, err := ParseOpenClawDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	roles := make([]string, 0, len(ss[0].Messages))
	for _, m := range ss[0].Messages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "user,assistant" || !strings.Contains(ss[0].Messages[1].Text, "ZSTD-ANSWER") {
		t.Errorf("after a corrupt frame: %v", roles)
	}
}
