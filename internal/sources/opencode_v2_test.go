package sources

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// opencodeV2Fixture writes a store in opencode 2.x's schema, with the shapes a
// live 2.0.12 database carries (#3924): a user turn keeping its text at the top
// of `data`, an assistant turn holding text and tool parts under `$.content`, a
// tool naming itself under `$.name`, and a command's output in the block list
// under `$.state.content`.
func opencodeV2Fixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	patch := `*** Begin Patch\n*** Update File: /w/app.go\n@@\n-func old() error {\n+func nw() error {\n*** End Patch`
	script := `create table session_v2(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session_v2 values('s1','p1',null,'/w','retry flake in the payments suite',1767409200000,1767495600000);
insert into session_message values('m1','s1','user',1,1767409200000,1767409200000,'{"metadata":{},"time":{"created":1767409200000},"text":"why does TestRetry flake"}');
insert into session_message values('m2','s1','assistant',2,1767409201000,1767409201000,'{"time":{"created":1767409201000},"content":[` +
		`{"type":"text","text":"the timeout is too short"},` +
		`{"type":"reasoning","text":"thinking about it"},` +
		`{"type":"tool","id":"call_1","name":"bash","state":{"status":"completed","input":{"command":"go test ./pkg/ -run Retry"},"content":[{"type":"text","text":"--- FAIL: TestRetry"}],"metadata":{"exit":1}},"time":{"start":1767409202000}},` +
		`{"type":"tool","id":"call_2","name":"bash","state":{"status":"completed","input":{"command":"ls -la"},"content":[],"metadata":{"exit":0}},"time":{"start":1767409203000}},` +
		`{"type":"tool","id":"call_3","name":"read","state":{"status":"completed","input":{"filePath":"/w/retry.go"},"content":[{"type":"text","text":"package main // thousands of lines"}]},"time":{"start":1767409204000}},` +
		`{"type":"tool","id":"call_4","name":"apply_patch","state":{"status":"completed","input":{"patchText":"` + patch + `"}},"time":{"start":1767409205000}}` +
		`]}');
insert into session_message values('m3','s1','synthetic',3,1767409206000,1767409206000,'{"time":{"created":1767409206000},"text":"Continue if you have next steps"}');
insert into session_message values('m4','s1','model-switched',4,1767409207000,1767409207000,'{"time":{"created":1767409207000},"text":"switched to another model"}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	return db
}

func TestOpencodeReadsTheV2Schema(t *testing.T) {
	db := opencodeV2Fixture(t)
	ss, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatalf("a 2.x store must parse, not report the schema missing: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want the one the store holds", len(ss))
	}
	s := ss[0]
	if s.Project != projectName("/w") || s.Path != "/w" {
		t.Errorf("project = %q path = %q — session_v2.directory still names the project", s.Project, s.Path)
	}
	if s.Title != "retry flake in the payments suite" {
		t.Errorf("title = %q", s.Title)
	}
	var user, assistant, cmds, outs, files, edits []string
	for _, m := range s.Messages {
		switch m.Role {
		case "user":
			user = append(user, m.Text)
		case "assistant":
			assistant = append(assistant, m.Text)
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleToolOutput:
			outs = append(outs, m.Text)
		case RoleFiles:
			files = append(files, m.Text)
		case RoleEdit:
			edits = append(edits, m.Text)
		}
	}
	// The role lives in the message's `type` column now; without reading it
	// every turn would come back as the same speaker.
	if len(user) != 1 || user[0] != "why does TestRetry flake" {
		t.Errorf("user turns = %v", user)
	}
	if len(assistant) != 1 || assistant[0] != "the timeout is too short" {
		t.Errorf("assistant turns = %v", assistant)
	}
	if len(cmds) != 1 || !strings.HasPrefix(cmds[0], "$ go test ./pkg/ -run Retry") {
		t.Fatalf("commands = %v — `ls` is not work worth recording", cmds)
	}
	if !strings.Contains(cmds[0], "→ exit 1") {
		t.Errorf("a non-zero exit belongs on the command: %q", cmds[0])
	}
	// `$.state.output` became the block list `$.state.content`.
	if len(outs) != 1 || !strings.Contains(outs[0], "--- FAIL: TestRetry") {
		t.Errorf("tool output = %v", outs)
	}
	for _, o := range outs {
		if strings.Contains(o, "thousands of lines") {
			t.Error("a file body is not command output, on either schema")
		}
	}
	if len(files) != 1 || files[0] != "/w/retry.go" {
		t.Errorf("files = %v", files)
	}
	if len(edits) != 1 || !strings.HasPrefix(edits[0], "/w/app.go\n") {
		t.Errorf("edits = %v", edits)
	}
	// opencode's own turns — the "Continue if you have next steps" line and the
	// switch notices — are not the person talking. They carried `$.synthetic`
	// on the old schema and are their own message types here.
	for _, m := range s.Messages {
		if strings.Contains(m.Text, "Continue if you have next steps") ||
			strings.Contains(m.Text, "switched to another model") {
			t.Errorf("%s: %q is opencode talking to itself", m.Role, m.Text)
		}
	}
	if s.Started.IsZero() || s.Updated.IsZero() {
		t.Errorf("started = %v updated = %v — the epoch milliseconds still date the session", s.Started, s.Updated)
	}
}

func TestOpencodeV2StoreAnswersTheReadsBesideTheProjection(t *testing.T) {
	db := opencodeV2Fixture(t)
	// doctor asks this one first, and on a 2.x store it asked `session`, which
	// is the query that reported the whole harness unreadable (#3924).
	ss, err := ParseOpencodeNewest(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("newest: len=%d err=%v", len(ss), err)
	}
	if got := opencodeSessionTable(db); got != "session_v2" {
		t.Fatalf("session table = %q", got)
	}
	if titles := opencodeTitles(db); titles["s1"] != "retry flake in the payments suite" {
		t.Errorf("titles = %v", titles)
	}
}

func TestOpencodeStillReadsTheV1Schema(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant"}');
insert into part values('p1','m1','{"type":"text","text":"the timeout is too short","time":{"start":"2026-01-02T03:00:00Z"}}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	if opencodeV2(db) {
		t.Fatal("a 1.x store must not be read as 2.x")
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
}

// The dev build of 2026-09-21 creates `session_message` and leaves it empty
// while the turns stay in `message` and `part`. Reading such a store the 2.x
// way returns nothing at all, and nothing is the answer that made this bug hard
// to see: the store looks healthy and holds no sessions.
func TestOpencodeReadsTheOldTablesWhileTheNewOneIsEmpty(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text primary key, directory text, title text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session values('s1','/w','the payments suite','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant"}');
insert into part values('p1','m1','{"type":"text","text":"the timeout is too short","time":{"start":"2026-01-02T03:00:00Z"}}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	if opencodeV2(db) {
		t.Fatal("an empty session_message is a store that has not moved yet")
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("len=%d err=%v — the turns are still in message and part", len(ss), err)
	}
}

// Upstream keeps the sessions in `session` and moves the turns into
// `session_message`; the store in #3924 had `session_v2` and no `session`. Both
// are read, and what decides is where the turns are.
func TestOpencodeReadsTheNewTurnsBesideTheOldSessionTable(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session values('s1','/w','the payments suite',1767409200000,1767409300000);
insert into session_message values('m1','s1','user',1,1767409200000,1767409200000,'{"time":{"created":1767409200000},"text":"why does TestRetry flake"}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	if !opencodeV2(db) {
		t.Fatal("turns in session_message are a 2.x store whatever the sessions table is called")
	}
	if got := opencodeSessionTable(db); got != "session" {
		t.Fatalf("session table = %q — session_v2 is the name only where there is no session", got)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	if ss[0].Messages[0].Text != "why does TestRetry flake" {
		t.Fatalf("message = %q", ss[0].Messages[0].Text)
	}
}

func TestOpencodeV2SinceReadsOnlyWhatIsNewer(t *testing.T) {
	db := opencodeV2Fixture(t)
	// The fixture's turns are stamped 2026-01-02; a watermark after them leaves
	// nothing to read, and one before them brings the session back.
	after, err := ParseOpencodeDBSince(db, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("got %d sessions — nothing in the store is newer than the watermark", len(after))
	}
	before, err := ParseOpencodeDBSince(db, time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(before) != 1 {
		t.Fatalf("len=%d err=%v", len(before), err)
	}
}

// The names inside a turn come from opencode's own schema (packages/schema
// session-message.ts, packages/core tool/*.ts), not from the report: the file a
// tool opened is `path` where the old parts wrote `filePath`, a command's exit
// status is in the tool's structured output where the old parts wrote
// `state.metadata.exit`, and a compaction carries its summary on the message.
func TestOpencodeV2ReadsTheUpstreamFieldNames(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	script := `create table session(id text primary key, directory text, title text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text, type text, seq integer, time_created integer, time_updated integer, data text);
insert into session values('s1','/w','the payments suite',1767409200000,1767409300000);
insert into session_message values('m1','s1','assistant',1,1767409201000,1767409201000,'{"time":{"created":1767409201000},"content":[` +
		`{"type":"tool","id":"c1","name":"read","state":{"status":"completed","input":{"path":"/w/retry.go"},"content":[{"type":"text","text":"package main"}],"structured":{}},"time":{"created":1767409201000}},` +
		`{"type":"tool","id":"c2","name":"bash","state":{"status":"completed","input":{"command":"go test ./pkg/ -run Retry"},"content":[{"type":"text","text":"--- FAIL: TestRetry"}],"structured":{"exit":2,"truncated":false}},"time":{"created":1767409202000}}` +
		`]}');
insert into session_message values('m2','s1','compaction',2,1767409203000,1767409203000,'{"time":{"created":1767409203000},"reason":"auto","summary":"we pinned pgx 5.4.3 for the pgbouncer issue","recent":""}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil || len(ss) != 1 {
		t.Fatalf("len=%d err=%v", len(ss), err)
	}
	var files, cmds, summaries []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleFiles:
			files = append(files, m.Text)
		case RoleCommand:
			cmds = append(cmds, m.Text)
		case RoleSummary:
			summaries = append(summaries, m.Text)
		}
	}
	if len(files) != 1 || files[0] != "/w/retry.go" {
		t.Errorf("files = %v — the new read tool names it `path`", files)
	}
	if len(cmds) != 1 || !strings.Contains(cmds[0], "→ exit 2") {
		t.Errorf("commands = %v — the status is in the tool's structured output", cmds)
	}
	if len(summaries) != 1 || !strings.Contains(summaries[0], "pgx 5.4.3") {
		t.Errorf("summaries = %v — a compaction is the only record of what it compacted", summaries)
	}
}
