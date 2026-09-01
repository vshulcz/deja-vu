package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gooseFixture(t *testing.T) (root, jsonl string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("DEJA_GOOSE_ROOT", filepath.Join(root, "goose"))
	dir := filepath.Join(root, "goose", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl = filepath.Join(dir, "20250724_1.jsonl")
	return root, jsonl
}

const gooseJSONL = `{"description":"demo session","id":"20250724_1","created_at":"2026-07-24T10:00:00Z","updated_at":"2026-07-24T10:00:02Z","working_dir":"/workspace/demo","extension_data":{},"message_count":2}
{"id":"msg1","role":"user","created":1784278801,"content":[{"type":"text","text":"Say hello"}]}
{"id":"msg2","role":"assistant","created":1784278802,"content":[{"type":"text","text":"Hello from Goose!"}]}
`

func TestParseGooseFile(t *testing.T) {
	_, jsonl := gooseFixture(t)
	if err := os.WriteFile(jsonl, []byte(gooseJSONL), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGooseFile(jsonl)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "goose" || s.ID != "20250724_1" || s.Project != "demo" || s.Title != "demo session" {
		t.Fatalf("session = %#v", s)
	}
	if len(s.Messages) != 2 || s.Messages[0].Role != "user" || s.Messages[1].Text != "Hello from Goose!" {
		t.Fatalf("messages = %#v", s.Messages)
	}
}

func TestParseGooseFileFromOffset(t *testing.T) {
	_, jsonl := gooseFixture(t)
	head := gooseJSONL
	if err := os.WriteFile(jsonl, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	off := int64(len(head))
	appendLine := `{"id":"msg3","role":"user","created":1784278803,"content":[{"type":"text","text":"follow up"}]}
`
	f, err := os.OpenFile(jsonl, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(appendLine); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	ss, err := ParseGooseFileFromOffset(jsonl, off)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 || ss[0].Messages[0].Text != "follow up" {
		t.Fatalf("incremental = %#v, %v", ss, err)
	}
}

func TestParseGooseFileTornTail(t *testing.T) {
	_, jsonl := gooseFixture(t)
	torn := gooseJSONL + `{"id":"msg3","role":"user","cre`
	if err := os.WriteFile(jsonl, []byte(torn), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseGooseFile(jsonl)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("torn tail should keep prior messages: %#v, %v", ss, err)
	}
}

func TestGooseSessionFiles(t *testing.T) {
	root, jsonl := gooseFixture(t)
	if err := os.WriteFile(jsonl, []byte(gooseJSONL), 0o644); err != nil {
		t.Fatal(err)
	}
	files := GooseSessionFiles()
	if len(files) != 1 || files[0] != jsonl {
		t.Fatalf("files = %#v", files)
	}
	db := filepath.Join(root, "goose", "sessions", "sessions.db")
	if err := os.WriteFile(db, []byte("sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	files = GooseSessionFiles()
	if len(files) != 2 {
		t.Fatalf("files with db = %#v", files)
	}
}

func TestParseGooseDB(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("DEJA_GOOSE_ROOT", filepath.Join(root, "goose"))
	dir := filepath.Join(root, "goose", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "sessions.db")
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('20250724_2','n','sqlite demo','/workspace/demo','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z');
insert into messages values (1,'20250724_2','user','[{"type":"text","text":"db hello"}]',1784282401);
insert into messages values (2,'20250724_2','assistant','[{"type":"text","text":"db reply"}]',1784282402);`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("create db: %v: %s", err, out)
	}
	ss, err := ParseGooseDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "20250724_2" || len(ss[0].Messages) != 2 {
		t.Fatalf("db sessions = %#v, %v", ss, err)
	}
}

// GOOSE_PATH_ROOT relocates config, data and state together. Ignoring it
// means indexing zero sessions for anyone who sets it.
func TestGooseDataDirFollowsPathRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOOSE_PATH_ROOT", root)
	if got, want := GooseDataDir(), filepath.Join(root, "data"); got != want {
		t.Fatalf("GooseDataDir() = %q, want %q", got, want)
	}
	// It wins over the platform default and over XDG.
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg"))
	if got, want := GooseDataDir(), filepath.Join(root, "data"); got != want {
		t.Fatalf("XDG took precedence: %q", got)
	}
}

// Goose stores subagent turns and cron-recipe runs in the same table as real
// sessions. Recall is about the user's own work, so those stay out; rows from
// before the column existed have no type and stay in. The acp session type is
// a user-visible Goose session and belongs in recall alongside plain user
// sessions.
func TestParseGooseDBSkipsNonUserSessions(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "sessions.db")
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text, session_type text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('s_user','n','mine','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','user');
insert into sessions values ('s_acp','n','acp','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','acp');
insert into sessions values ('s_term','n','terminal','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','terminal');
insert into sessions values ('s_gw','n','gateway','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','gateway');
insert into sessions values ('s_sub','n','subagent','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','subagent');
insert into sessions values ('s_cron','n','cron','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z','scheduled');
insert into sessions values ('s_old','n','legacy','/w','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z',null);
insert into messages values (1,'s_user','user','[{"type":"text","text":"mine"}]',1784282401);
insert into messages values (2,'s_acp','user','[{"type":"text","text":"acp sessions"}]',1784282401);
insert into messages values (3,'s_term','user','[{"type":"text","text":"terminal session"}]',1784282401);
insert into messages values (4,'s_gw','user','[{"type":"text","text":"gateway noise"}]',1784282401);
insert into messages values (5,'s_sub','user','[{"type":"text","text":"subagent noise"}]',1784282401);
insert into messages values (6,'s_cron','user','[{"type":"text","text":"cron noise"}]',1784282401);
insert into messages values (7,'s_old','user','[{"type":"text","text":"legacy"}]',1784282401);`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("create db: %v: %s", err, out)
	}
	ss, err := ParseGooseDB(db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range ss {
		got[s.ID] = true
	}
	if !got["s_user"] || !got["s_old"] || !got["s_acp"] || !got["s_term"] {
		t.Fatalf("dropped sessions that belong in recall: %v", got)
	}
	if got["s_sub"] || got["s_cron"] || got["s_gw"] {
		t.Fatalf("subagent, scheduled or gateway runs leaked into recall: %v", got)
	}
}
