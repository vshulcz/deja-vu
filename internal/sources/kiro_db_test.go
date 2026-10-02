package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// kiroTestDB seeds a data.sqlite3 with the conversations_v2 table as kiro-cli
// 2.22.0 creates it.
func kiroTestDB(t *testing.T, rows ...string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "data.sqlite3")
	schema := `CREATE TABLE conversations_v2 (key TEXT NOT NULL, conversation_id TEXT NOT NULL, value TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY (key, conversation_id));
` + strings.Join(rows, "\n")
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	t.Setenv("DEJA_KIRO_DB", db)
	return db
}

const kiroDBConversation = `{"conversation_id":"bf73e1f5-0000-4000-8000-000000000002","history":[
 {"user":{"env_context":{"env_state":{"current_working_directory":"/tmp/proj"}},"content":{"Prompt":{"prompt":"fix the retry loop"}},"timestamp":"2026-10-01T10:00:00+00:00"},
  "assistant":{"ToolUse":{"message_id":"m2","content":"Checking the loop.","tool_uses":[{"id":"t1","name":"execute_bash","args":{"command":"go test ./retry"}},{"id":"t2","name":"fs_write","args":{"command":"create","path":"/tmp/proj/retry.go","file_text":"package retry\n\nconst maxAttemptsBeforeGivingUp = 3\n"}}]}}},
 {"user":{"content":{"ToolUseResults":{"tool_use_results":[{"tool_use_id":"t1","content":[{"Json":{"exit_status":"0","stdout":"ok  retry 0.01s","stderr":""}}],"status":"Success"},{"tool_use_id":"t2","content":[{"Text":""}],"status":"Success"}]}},"timestamp":null},
  "assistant":{"Response":{"message_id":"m3","content":"The loop now stops after three attempts."}}}]}`

func kiroDBRow(key, id, value string, updated int64) string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	return "INSERT INTO conversations_v2 VALUES (" + q(key) + "," + q(id) + "," + q(value) + "," +
		strconv.FormatInt(updated, 10) + "," + strconv.FormatInt(updated, 10) + ");"
}

// `kiro-cli chat --no-interactive` writes nothing under ~/.kiro/sessions: the
// conversation is a row of data.sqlite3, and every headless run was missing
// from the index (#4300).
func TestKiroDBReadsNoInteractiveConversations(t *testing.T) {
	db := kiroTestDB(t, kiroDBRow("/tmp/proj", "bf73e1f5-0000-4000-8000-000000000002", kiroDBConversation, 1790848800000))
	ss, err := ParseKiroDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want the one conversation", len(ss))
	}
	s := ss[0]
	if s.Harness != "kiro" || s.ID != "bf73e1f5-0000-4000-8000-000000000002" {
		t.Errorf("harness/id = %s/%s, want kiro and the conversation_id", s.Harness, s.ID)
	}
	if !strings.Contains(s.Project, "proj") {
		t.Errorf("project = %q, want it from the row's directory", s.Project)
	}
	got := kiroRoles(s)
	if !kiroHas(got["user"], "fix the retry loop") {
		t.Errorf("the prompt is missing: %q", got["user"])
	}
	if !kiroHas(got["assistant"], "Checking the loop.") || !kiroHas(got["assistant"], "three attempts") {
		t.Errorf("the replies are missing: %q", got["assistant"])
	}
	if !kiroHas(got[RoleCommand], "$ go test ./retry") {
		t.Errorf("the execute_bash call is not a command: %q", got[RoleCommand])
	}
	if !kiroHas(got[RoleFiles], "/tmp/proj/retry.go") || len(got[RoleWrote]) == 0 {
		t.Errorf("the fs_write call is not a file and a write: %+v", s.Messages)
	}
	if !kiroHas(got[RoleToolOutput], "ok  retry") {
		t.Errorf("the tool output is missing: %q", got[RoleToolOutput])
	}
	if s.Started.IsZero() {
		t.Error("the session has no time")
	}
}

// The incremental read returns only conversations written after the
// watermark, whole, so what comes back replaces what the index held.
func TestKiroDBSinceFilters(t *testing.T) {
	old := strings.Replace(kiroDBConversation, "bf73e1f5-0000-4000-8000-000000000002", "aa000000-0000-4000-8000-000000000001", 1)
	db := kiroTestDB(t,
		kiroDBRow("/tmp/proj", "bf73e1f5-0000-4000-8000-000000000002", kiroDBConversation, 1790848800000),
		kiroDBRow("/tmp/old", "aa000000-0000-4000-8000-000000000001", old, 1790000000000),
	)
	ss, err := ParseKiroDBSince(db, time.UnixMilli(1790500000000))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "bf73e1f5-0000-4000-8000-000000000002" {
		t.Fatalf("since read = %+v, want only the newer conversation", ss)
	}
}

// The registry has to list and claim the store, or incremental ingest never
// opens it.
func TestKiroClaimsItsDB(t *testing.T) {
	t.Setenv("DEJA_KIRO_ROOT", t.TempDir())
	db := kiroTestDB(t, kiroDBRow("/tmp/proj", "bf73e1f5-0000-4000-8000-000000000002", kiroDBConversation, 1790848800000))
	files := KiroSessionFiles()
	if len(files) != 1 || files[0] != db {
		t.Fatalf("files = %v, want the store", files)
	}
	claimed := false
	for _, h := range allHarnesses() {
		for _, k := range h.Kinds {
			if k.Match(db) {
				claimed = h.Name == "kiro"
			}
		}
	}
	if !claimed {
		t.Error("no kiro kind claims the store")
	}
	if ss := LoadKiro(); len(ss) != 1 {
		t.Errorf("LoadKiro = %d sessions, want the store's one", len(ss))
	}
}

// kiro-cli creates data.sqlite3 on first launch and conversations_v2 only once
// a headless chat is saved, so a fresh install has a store without the table.
// That is an empty store, not a schema change to report.
func TestKiroDBWithoutTheTableIsEmpty(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "data.sqlite3")
	cmd := exec.Command("sqlite3", db, "CREATE TABLE state (key TEXT PRIMARY KEY, value BLOB);")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	ss, err := ParseKiroDB(db)
	if err != nil || len(ss) != 0 {
		t.Fatalf("ParseKiroDB = %d sessions, %v; want none and no error", len(ss), err)
	}
}

// Without the sqlite3 CLI the store is there and unread, which is the missing
// tool to name, not a format to report.
func TestKiroDBNamesTheMissingSQLite3(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "data.sqlite3")
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_KIRO_DB", db)
	t.Setenv("PATH", dir)
	if SQLite3Available() {
		t.Skip("sqlite3 still resolvable")
	}
	if got := SkipReason("kiro"); got != SQLite3NotFound {
		t.Errorf("SkipReason(kiro) = %q, want %q", got, SQLite3NotFound)
	}
}
