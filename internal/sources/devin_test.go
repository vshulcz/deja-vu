package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// devinHome isolates a test from a real Devin CLI install. The store location
// and the home it is derived from are both pinned, so a contributor running
// `devin` locally gets the same test as CI.
func devinHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DEJA_DEVIN_DB", "")
	return home
}

// devinTestDB builds a store from a SQL script at <tmp>/cli/sessions.db, so
// its sibling <tmp>/summaries is where the real store's summaries directory
// sits — devinSummariesDir walks the same two levels up the CLI's layout does.
func devinTestDB(t *testing.T, sql string) (db, dataDir string) {
	t.Helper()
	if !SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	dataDir = t.TempDir()
	db = filepath.Join(dataDir, "cli", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	// The script goes in on stdin, not as an argument: the fixture opens with
	// a `--` comment and the sqlite3 CLI reads a leading `--` as an option.
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sqlite fixture: %v: %s", err, out)
	}
	return db, dataDir
}

func devinFixtureSQL(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "registry", "devin", "devin.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseDevinDBReadsMainChainAndDedupes(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	s := findDevinSession(sessions, "registry-devin")
	if s == nil {
		t.Fatalf("registry-devin not in %#v", sessions)
	}
	if s.Harness != "devin" || s.Project != "w/api" || s.Path != db {
		t.Fatalf("identity = %#v", s)
	}
	if s.Started.IsZero() || s.Updated.IsZero() || !s.Started.Before(s.Updated) {
		t.Fatalf("window = %v..%v", s.Started, s.Updated)
	}
	roles := make([]string, 0, len(s.Messages))
	texts := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		roles = append(roles, m.Role)
		texts = append(texts, m.Text)
	}
	joined := strings.Join(texts, "\n")

	// The rebuilt prefix rows are scaffolding, never the conversation.
	if strings.Contains(joined, "system_info") || strings.Contains(joined, "<rules>") {
		t.Fatalf("prefix rows leaked into the session: %s", joined)
	}
	// The user turn arrives once: node 8 replays node 3 under the same
	// message_id, and keeping both reads the compacted copy as a second ask.
	if n := strings.Count(joined, "the retry loop drops the last attempt"); n != 1 {
		t.Fatalf("user turn appears %d times, want 1: %s", n, joined)
	}
	// The exec call is a command record with its tool result behind it,
	// stamped with the tool row's own exit code rather than a second
	// message. prompt_history commands stand alone, so only the exec one is
	// asked for a result.
	var cmd string
	var hasResult bool
	for i, m := range s.Messages {
		if m.Role == "command" && strings.HasPrefix(m.Text, "$ go test") {
			cmd = m.Text
			hasResult = i+1 < len(s.Messages) && s.Messages[i+1].Role == "tool-output"
		}
	}
	if !hasResult {
		t.Fatalf("command record has no tool result behind it: %#v", roles)
	}
	if !strings.HasPrefix(cmd, "$ go test ./internal/loop") {
		t.Fatalf("command record = %q", cmd)
	}
	if !strings.Contains(cmd, "exit 1") {
		t.Fatalf("command record carries no exit stamp: %q", cmd)
	}
	// The edit call leaves files/edit work records on its file.
	var sawEdit bool
	for _, m := range s.Messages {
		if m.Role == "edit" && strings.HasPrefix(m.Text, "/w/api/loop.go") {
			sawEdit = true
		}
	}
	if !sawEdit {
		t.Fatalf("no edit record for /w/api/loop.go: %s", joined)
	}
	// An unmarked system row is context something injected — the hook's
	// additionalContext lands verbatim — and it is kept as tool output.
	var sawInjected bool
	for _, m := range s.Messages {
		if m.Role == "tool-output" && m.Text == "DEJA-REGISTRY-INJECTED-CONTEXT" {
			sawInjected = true
		}
	}
	if !sawInjected {
		t.Fatalf("injected context dropped: %s", joined)
	}
	// prompt_history: the is_shell rows worth keeping are commands; ls is
	// not, and the prompt itself must not duplicate the user turn.
	var shellCmds int
	for _, m := range s.Messages {
		if m.Role == "command" && strings.HasPrefix(m.Text, "$ ") {
			if strings.Contains(m.Text, "git log --oneline -5") || strings.Contains(m.Text, "make build") {
				shellCmds++
			}
			if strings.Contains(m.Text, "ls -la") {
				t.Fatalf("trivial shell row indexed: %q", m.Text)
			}
		}
	}
	if shellCmds != 2 {
		t.Fatalf("prompt_history commands = %d, want 2: %s", shellCmds, joined)
	}
}

func TestParseDevinDBReadsSideChainsAsSubagents(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	var parent, worker, rewind int
	for i := range sessions {
		switch {
		case sessions[i].ID == "registry-devin-sub":
			parent = i
		case sessions[i].ID == "registry-devin-sub:ag-worker":
			worker = i
		case strings.HasPrefix(sessions[i].ID, "registry-devin-sub:"):
			rewind = i
		}
	}
	if sessions[parent].Kind != "" || sessions[parent].Parent != "" {
		t.Fatalf("main chain = %#v", sessions[parent])
	}
	// A subagent_heads row names the run: its sub-session takes the stable
	// agent id, not a message-derived one.
	w := sessions[worker]
	if w.Kind != "subagent" || w.Parent != "registry-devin-sub" || w.Harness != "devin" {
		t.Fatalf("subagent = %#v", w)
	}
	var sawWorker bool
	for _, m := range w.Messages {
		if m.Text == "registry-worker printed" {
			sawWorker = true
		}
	}
	if !sawWorker {
		t.Fatalf("worker chain has no assistant turn: %#v", w.Messages)
	}
	// The orphan chain no head row declares still surfaces through the leaf
	// walk — a rewind left it behind, and silent loss is not the trade.
	r := sessions[rewind]
	if r.Kind != "subagent" || r.Parent != "registry-devin-sub" {
		t.Fatalf("rewind chain = %#v", r)
	}
	var sawRewind bool
	for _, m := range r.Messages {
		if m.Text == "earlier draft of the answer" {
			sawRewind = true
		}
	}
	if !sawRewind {
		t.Fatalf("orphan chain dropped: %#v", r.Messages)
	}
	// The main chain must not carry the worker's rows, and vice versa.
	for _, m := range sessions[parent].Messages {
		if strings.Contains(m.Text, "registry-worker printed") {
			t.Fatalf("worker turn leaked into the parent chain")
		}
	}
}

func TestParseDevinDBSkipsHiddenSessions(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	for i := range sessions {
		if sessions[i].ID == "registry-devin-hidden" {
			t.Fatalf("hidden session returned: %#v", sessions[i])
		}
		if strings.Contains(sessionText(&sessions[i]), "must not be read") {
			t.Fatalf("hidden session's nodes leaked into %q", sessions[i].ID)
		}
	}
}

func TestParseDevinDBSinceFiltersStaleSessions(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	// Between registry-devin's last activity and registry-devin-sub's. The
	// fresh session brings its subagent side chain with it.
	sessions, err := ParseDevinDBSince(db, time.Unix(1785600120, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 || sessions[0].ID != "registry-devin-sub" ||
		sessions[1].Parent != "registry-devin-sub" || sessions[2].Parent != "registry-devin-sub" {
		t.Fatalf("since-filtered sessions = %#v", sessions)
	}
	all, err := ParseDevinDBSince(db, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("zero since = %d sessions, want 4", len(all))
	}
}

func TestParseDevinDBAttachesSummary(t *testing.T) {
	devinHome(t)
	db, dataDir := devinTestDB(t, devinFixtureSQL(t))
	summaries := filepath.Join(dataDir, "summaries")
	if err := os.MkdirAll(summaries, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(summaries, "registry-devin.md"), []byte("# Session summary\n\ncompaction happened\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	s := findDevinSession(sessions, "registry-devin")
	var saw bool
	for _, m := range s.Messages {
		if m.Role == "summary" && strings.Contains(m.Text, "compaction happened") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("summary file not attached: %#v", s.Messages)
	}
}

func TestParseDevinDBMissingTablesErrors(t *testing.T) {
	devinHome(t)
	// devin creates its tables lazily, so a store without them is an empty
	// store — no sessions, no error.
	db, _ := devinTestDB(t, "create table unrelated(x);")
	sessions, err := ParseDevinDB(db)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("table-less store = %v, %#v", err, sessions)
	}
	// A store that has sessions but not message_nodes is a schema that
	// changed, and that failure is reported.
	db2, _ := devinTestDB(t, "create table sessions(id text, working_directory text, title text, created_at integer, last_activity_at integer, main_chain_id integer, hidden integer not null default 0); insert into sessions values('s','/w','t',1,2,null,0);")
	if _, err := ParseDevinDB(db2); err == nil {
		t.Fatal("a store missing message_nodes parsed without complaint")
	}
}

func TestDevinFilesHonoursEnvAndDiscovers(t *testing.T) {
	devinHome(t)
	if got := DevinSessionsDB(); got != filepath.Join(devinDataDir(), "cli", "sessions.db") {
		t.Fatalf("default store = %q", got)
	}
	if DevinFiles() != nil {
		t.Fatal("empty home still reports stores")
	}
	db, _ := devinTestDB(t, "create table sessions(id text);")
	t.Setenv("DEJA_DEVIN_DB", db)
	files := DevinFiles()
	if len(files) != 1 || files[0] != db {
		t.Fatalf("DEJA_DEVIN_DB discovery = %#v", files)
	}
	if !devinDBMatch(db) {
		t.Fatalf("matcher refuses the env store")
	}
	// The legacy name is read beside the canonical one.
	legacy := filepath.Join(devinDataDir(), "cli", "cli_sessions.db")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_DEVIN_DB", "")
	cmd := exec.CommandContext(t.Context(), "sqlite3", legacy)
	cmd.Stdin = strings.NewReader("create table sessions(id text);")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create legacy fixture: %v: %s", err, out)
	}
	files = DevinFiles()
	if len(files) != 1 || files[0] != legacy {
		t.Fatalf("legacy discovery = %#v", files)
	}
}

func TestDevinSessionDir(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	if got := DevinSessionDir(db, "registry-devin"); got != "/w/api" {
		t.Fatalf("recorded dir = %q", got)
	}
	if got := DevinSessionDir(db, "nobody"); got != "" {
		t.Fatalf("missing session dir = %q", got)
	}
}

func TestLoadDevinReadsDiscoveredStores(t *testing.T) {
	devinHome(t)
	if got := LoadDevin(); got != nil {
		t.Fatalf("empty home LoadDevin = %#v, want nil", got)
	}
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	t.Setenv("DEJA_DEVIN_DB", db)
	got := LoadDevin()
	if findDevinSession(got, "registry-devin") == nil {
		t.Fatalf("LoadDevin = %#v, want the fixture session", got)
	}
	// A store sqlite3 refuses is reported, not fatal: the pass still answers.
	t.Setenv("DEJA_DEVIN_DB", filepath.Join(t.TempDir(), "absent.db"))
	if got := LoadDevin(); got != nil {
		t.Fatalf("absent store LoadDevin = %#v, want nil", got)
	}
}

func TestDevinDataDirFollowsXDG(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps its own root under LOCALAPPDATA, not XDG")
	}
	home := devinHome(t)
	if got := devinDataDir(); got != filepath.Join(home, ".local", "share", "devin") {
		t.Fatalf("default data dir = %q", got)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
	if got := devinDataDir(); got != filepath.Join(home, "xdg", "devin") {
		t.Fatalf("XDG_DATA_HOME ignored: %q", got)
	}
}

func TestDevinContentTextReadsStringsAndParts(t *testing.T) {
	if got := devinContentText("plain"); got != "plain" {
		t.Fatalf("string content = %q", got)
	}
	got := devinContentText([]any{
		map[string]any{"text": "first"},
		"not-a-part",
		map[string]any{"type": "tool_use"},
		map[string]any{"text": "second"},
	})
	if got != "first\nsecond" {
		t.Fatalf("content parts = %q", got)
	}
	if got := devinContentText(42); got != "" {
		t.Fatalf("unknown content = %q, want empty", got)
	}
}

func TestDevinExitCodeReadsTerminalMetadata(t *testing.T) {
	term := map[string]any{"exit": map[string]any{"exit_code": float64(7)}}
	msg := map[string]any{"metadata": map[string]any{"extensions": map[string]any{"chisel/terminal_output": term}}}
	if c, ok := devinExitCode(msg); !ok || c != 7 {
		t.Fatalf("exit code = %d, %v", c, ok)
	}
	term["exit"] = map[string]any{"exit_code": json.Number("3")}
	if c, ok := devinExitCode(msg); !ok || c != 3 {
		t.Fatalf("json.Number exit code = %d, %v", c, ok)
	}
	if _, ok := devinExitCode(map[string]any{}); ok {
		t.Fatal("metadata-less message reports an exit code")
	}
	if _, ok := devinExitCode(map[string]any{"metadata": map[string]any{}}); ok {
		t.Fatal("extension-less message reports an exit code")
	}
}

func TestDevinToolCallsKeepsNamedCalls(t *testing.T) {
	calls := devinToolCalls([]any{
		map[string]any{"name": "exec", "arguments": map[string]any{"command": "ls"}},
		map[string]any{"arguments": map[string]any{}},
		map[string]any{"name": "read"},
		"junk",
	})
	if len(calls) != 1 {
		t.Fatalf("tool calls = %#v", calls)
	}
	if got := devinToolCalls("not-a-list"); got != nil {
		t.Fatalf("non-list tool calls = %#v", got)
	}
}

func TestShortDevinID(t *testing.T) {
	if got := shortDevinID("abcdefghijkl"); got != "abcdefgh" {
		t.Fatalf("long id = %q", got)
	}
	if got := shortDevinID("abc"); got != "abc" {
		t.Fatalf("short id = %q", got)
	}
	if got := shortDevinID(""); got != "sub" {
		t.Fatalf("empty id = %q", got)
	}
}

func findDevinSession(ss []model.Session, id string) *model.Session {
	for i := range ss {
		if ss[i].ID == id {
			return &ss[i]
		}
	}
	return nil
}

// sessionText joins a session's messages for contains-style checks.
func sessionText(s *model.Session) string {
	var b strings.Builder
	for _, m := range s.Messages {
		b.WriteString(m.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestParseDevinDBReadsStoreWithoutHiddenColumn(t *testing.T) {
	devinHome(t)
	// Stores the first builds wrote have no `hidden` column; a query naming
	// it fails the whole read, so the schema is probed before it is named.
	db, _ := devinTestDB(t, `
create table sessions (
    id text primary key, working_directory text, title text,
    created_at integer, last_activity_at integer, main_chain_id integer
);
create table message_nodes (
    row_id integer primary key autoincrement,
    session_id text not null, node_id integer not null,
    parent_node_id integer, chat_message text not null,
    created_at integer, metadata text
);
insert into sessions values ('old-devin','/w','old schema store',1785600000,1785600010,1);
insert into message_nodes (session_id, node_id, parent_node_id, chat_message, created_at) values
('old-devin', 1, null, '{"message_id":"o-u","role":"user","content":"schema predates hidden"}', 1785600001);
`)
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	s := findDevinSession(sessions, "old-devin")
	if s == nil {
		t.Fatalf("old-schema store not read: %#v", sessions)
	}
	if !strings.Contains(sessionText(s), "schema predates hidden") {
		t.Fatalf("old-schema store dropped its chain: %#v", s.Messages)
	}
}

func TestParseDevinDBHonoursIndexToolOutput(t *testing.T) {
	devinHome(t)
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	db, _ := devinTestDB(t, devinFixtureSQL(t))
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	s := findDevinSession(sessions, "registry-devin")
	if s == nil {
		t.Fatalf("registry-devin not in %#v", sessions)
	}
	// With tool output off, neither the tool result nor the unmarked system
	// context lands — the switch is one switch, not one per role.
	for _, m := range s.Messages {
		if m.Role == "tool-output" {
			t.Fatalf("tool output indexed with the flag off: %q", m.Text)
		}
	}
}

func TestDevinToolCallsReadsSerializedArguments(t *testing.T) {
	// Some builds persist arguments as the encoded JSON, not an object;
	// dropping the call loses its whole work record.
	calls := devinToolCalls([]any{
		map[string]any{"id": "c1", "name": "exec", "arguments": `{"command":"go test ./x"}`},
		map[string]any{"id": "c2", "name": "exec", "arguments": "not json"},
		map[string]any{"id": "c3", "name": "read", "arguments": map[string]any{"file_path": "/w/x.go"}},
	})
	if len(calls) != 2 {
		t.Fatalf("tool calls = %#v", calls)
	}
	in, _ := calls[0].(map[string]any)["input"].(map[string]any)
	if in["command"] != "go test ./x" {
		t.Fatalf("serialized arguments = %#v", calls[0])
	}
}

// A subagent_heads table that exists but reads wrong — a schema drifted, a
// locked page — cannot fail the session: the declared heads come back empty
// and the leaf heuristic still surfaces the side chains.
func TestParseDevinDBFallsBackWhenSubagentHeadsMisread(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, `
create table sessions (
    id text primary key, working_directory text, backend_type text,
    model text, agent_mode text, created_at integer, last_activity_at integer,
    title text, main_chain_id integer, shell_last_seen_index integer,
    cogs_json text, workspace_dirs text, hidden integer not null default 0,
    metadata text
);
create table message_nodes (
    row_id integer primary key autoincrement, session_id text not null,
    node_id integer not null, parent_node_id integer,
    chat_message text not null, created_at integer, metadata text
);
create table subagent_heads (wrong_column text);
insert into sessions values (
    's1', '/w/api', 'cli', 'swe-2', 'normal', 1785600000, 1785600090,
    'a session', 2, 0, null, '["/w/api"]', 0, null
);
insert into message_nodes (session_id, node_id, parent_node_id, chat_message, created_at, metadata) values
    ('s1', 1, null, '{"message_id":"m1","role":"user","content":"the ask"}', 1785600001, '{}'),
    ('s1', 2, 1,    '{"message_id":"m2","role":"assistant","content":[{"type":"text","text":"the answer"}]}', 1785600002, '{}'),
    ('s1', 3, null, '{"message_id":"w1","role":"assistant","content":[{"type":"text","text":"a side chain nobody declared"}]}', 1785600003, '{}');
`)
	if heads := devinSubagentHeads(db, "s1", true); heads != nil {
		t.Fatalf("a mis-shaped subagent_heads must read empty, got %#v", heads)
	}
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	side := findDevinSession(sessions, "s1:w1")
	if side == nil || !strings.Contains(side.Messages[0].Text, "a side chain nobody declared") {
		t.Fatalf("the undeclared chain did not fall back to the leaf heuristic: %#v", sessions)
	}
}

// devinStoreDDL is the store schema, copied from
// fixtures/registry/devin/devin.sql, for tests that build their own rows.
const devinStoreDDL = `
create table sessions (
    id text primary key,
    working_directory text,
    backend_type text,
    model text,
    agent_mode text,
    created_at integer,
    last_activity_at integer,
    title text,
    main_chain_id integer,
    shell_last_seen_index integer,
    cogs_json text,
    workspace_dirs text,
    hidden integer not null default 0,
    metadata text
);
create table message_nodes (
    row_id integer primary key autoincrement,
    session_id text not null,
    node_id integer not null,
    parent_node_id integer,
    chat_message text not null,
    created_at integer,
    metadata text
);
create table subagent_heads (
    session_id text not null,
    agent_id text not null,
    chain_node_id integer not null,
    updated_at integer not null,
    primary key (session_id, agent_id)
);
create table prompt_history (
    id integer primary key autoincrement,
    content text,
    timestamp integer,
    session_id text,
    is_shell integer not null default 0
);
`

// A branch off the trunk — an edited ask forking at a node the main chain
// already carries — is one subagent session holding only its own turns, not
// a second copy of the shared prefix keyed on the trunk's root.
func TestParseDevinDBBranchOffTrunkKeepsOnlySuffix(t *testing.T) {
	devinHome(t)
	db, _ := devinTestDB(t, devinStoreDDL+`
insert into sessions values (
    'b1', '/w/api', 'cli', 'swe-2', 'normal', 1785600000, 1785600100,
    'branched session', 5, 0, null, '["/w/api"]', 0, null
);
insert into message_nodes (session_id, node_id, parent_node_id, chat_message, created_at, metadata) values
    ('b1', 1, null, '{"message_id":"u1","role":"user","content":"trunk ask"}', 1785600001, '{}'),
    ('b1', 2, 1,    '{"message_id":"a2","role":"assistant","content":[{"type":"text","text":"trunk answer"}]}', 1785600002, '{}'),
    ('b1', 3, 2,    '{"message_id":"u3","role":"user","content":"branch ask"}', 1785600003, '{}'),
    ('b1', 4, 3,    '{"message_id":"a4","role":"assistant","content":[{"type":"text","text":"branch answer"}]}', 1785600004, '{}'),
    ('b1', 5, 2,    '{"message_id":"u5","role":"user","content":"edited ask a"}', 1785600005, '{}'),
    ('b1', 6, 2,    '{"message_id":"u6","role":"user","content":"edited ask b"}', 1785600006, '{}');
`)
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Fatalf("sessions = %#v", sessions)
	}
	main := findDevinSession(sessions, "b1")
	if main == nil {
		t.Fatalf("main session missing: %#v", sessions)
	}
	mainText := sessionText(main)
	for _, want := range []string{"trunk ask", "trunk answer", "edited ask a"} {
		if !strings.Contains(mainText, want) {
			t.Fatalf("main chain missing %q: %s", want, mainText)
		}
	}
	if strings.Contains(mainText, "branch ask") {
		t.Fatalf("branch turns leaked into the main chain: %s", mainText)
	}
	branch := findDevinSession(sessions, "b1:"+shortDevinID("u3"))
	edited := findDevinSession(sessions, "b1:"+shortDevinID("u6"))
	if branch == nil || edited == nil || branch.ID == edited.ID {
		t.Fatalf("subagent sessions = %#v", sessions)
	}
	for _, sub := range []*model.Session{branch, edited} {
		if sub.Kind != "subagent" || sub.Parent != "b1" {
			t.Fatalf("subagent = %#v", sub)
		}
		text := sessionText(sub)
		if strings.Contains(text, "trunk ask") || strings.Contains(text, "trunk answer") {
			t.Fatalf("%s repeats the shared prefix: %s", sub.ID, text)
		}
	}
	branchText, editedText := sessionText(branch), sessionText(edited)
	if !strings.Contains(branchText, "branch ask") || !strings.Contains(branchText, "branch answer") {
		t.Fatalf("branch subagent lost its own turns: %s", branchText)
	}
	if !strings.Contains(editedText, "edited ask b") {
		t.Fatalf("edited subagent lost its own turn: %s", editedText)
	}
}

// Many branches off one trunk each become their own subagent session under
// their own id — keying on the chain root collapses every one of them onto
// the trunk's first message.
func TestParseDevinDBManyBranchesGetDistinctIDs(t *testing.T) {
	devinHome(t)
	var b strings.Builder
	b.WriteString(devinStoreDDL)
	b.WriteString(`insert into sessions values (
    'big', '/w', 'cli', 'swe-2', 'normal', 1785600000, 1785700000,
    'many branches', 2000, 0, null, '["/w"]', 0, null
);`)
	var rows []string
	for i := 1; i <= 2000; i++ {
		role, parent := "user", "null"
		if i%2 == 0 {
			role = "assistant"
		}
		if i > 1 {
			parent = strconv.Itoa(i - 1)
		}
		rows = append(rows, fmt.Sprintf(
			`('big',%d,%s,'{"message_id":"m%d","role":%q,"content":"trunk %d"}',%d,'{}')`,
			i, parent, i, role, i, 1785600000+i))
	}
	for i := 1; i <= 1000; i++ {
		node := 2000 + i
		rows = append(rows, fmt.Sprintf(
			`('big',%d,%d,'{"message_id":"b%d","role":"user","content":"branch ask %d"}',%d,'{}')`,
			node, i, node, i, 1785700000+i))
	}
	b.WriteString("\ninsert into message_nodes (session_id, node_id, parent_node_id, chat_message, created_at, metadata) values\n")
	b.WriteString(strings.Join(rows, ",\n"))
	b.WriteString(";\n")
	db, _ := devinTestDB(t, b.String())
	sessions, err := ParseDevinDB(db)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	subs := 0
	for i := range sessions {
		s := &sessions[i]
		if s.Kind != "subagent" {
			continue
		}
		subs++
		if ids[s.ID] {
			t.Fatalf("subagent id %q is not unique", s.ID)
		}
		ids[s.ID] = true
		if len(s.Messages) != 1 {
			t.Fatalf("%s carries %d messages — the shared trunk prefix repeated: %#v", s.ID, len(s.Messages), s.Messages)
		}
	}
	if subs != 1000 {
		t.Fatalf("subagent sessions = %d, want 1000", subs)
	}
}
