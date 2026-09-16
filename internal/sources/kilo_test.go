package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Kilo Code is a Roo fork that vendors OpenCode, so it has two stores and deja
// needs no new parser for either: the extension writes Roo's task shape and
// the CLI writes OpenCode's message schema (#3643). What is new is the paths
// and the name on the session.
func TestKiloReadsTheExtensionTasks(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "globalStorage", KiloExtensionID)
	task := filepath.Join(root, "tasks", "kilo-task-1")
	if err := os.MkdirAll(task, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_KILO_ROOTS", root)
	t.Setenv("DEJA_KILO_DB", filepath.Join(home, "absent.db"))

	transcript := `[{"role":"user","content":[{"type":"text","text":"<task>why does the migration hang</task>"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"the advisory lock was never released"}]}]`
	if err := os.WriteFile(filepath.Join(task, "api_conversation_history.json"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	item := `{"id":"kilo-task-1","ts":1784278800000,"task":"why does the migration hang","workspace":"/work/api"}`
	if err := os.WriteFile(filepath.Join(task, "history_item.json"), []byte(item), 0o644); err != nil {
		t.Fatal(err)
	}

	files := KiloTaskFiles()
	if len(files) != 1 {
		t.Fatalf("task files = %v", files)
	}
	ss := LoadKilo()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "kilocode" {
		t.Errorf("harness = %q, want kilocode — a Kilo session must not report as Roo", s.Harness)
	}
	if s.ID != "kilocode-task-kilo-task-1" {
		t.Errorf("id = %q", s.ID)
	}
	if s.Project == "roo" || s.Project == "" {
		t.Errorf("project = %q, want the workspace from history_item.json", s.Project)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the two turns", len(s.Messages))
	}
	if !strings.Contains(s.Messages[0].Text, "why does the migration hang") {
		t.Errorf("the task envelope was not unwrapped: %q", s.Messages[0].Text)
	}
	if !strings.Contains(s.Messages[1].Text, "advisory lock") {
		t.Errorf("the reply is missing: %q", s.Messages[1].Text)
	}

	// The registry has to claim the file, or incremental ingest treats a Kilo
	// task as unknown content and never reads it again.
	kind := ""
	for _, h := range Registry() {
		if h.Name != "kilocode" {
			continue
		}
		for _, k := range h.Kinds {
			if k.Match(files[0]) {
				kind = k.Name
			}
		}
	}
	if kind != "kilocode-task" {
		t.Errorf("registry kind = %q, want kilocode-task", kind)
	}
}

// A Roo task must not be read as Kilo's, nor the other way round: the two
// extensions live in sibling directories under the same globalStorage.
func TestKiloAndRooDoNotClaimEachOther(t *testing.T) {
	home := t.TempDir()
	kiloRoot := filepath.Join(home, "globalStorage", KiloExtensionID)
	rooRoot := filepath.Join(home, "globalStorage", "rooveterinaryinc.roo-cline")
	for _, root := range []string{kiloRoot, rooRoot} {
		dir := filepath.Join(root, "tasks", "t1")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `[{"role":"user","content":[{"type":"text","text":"hello"}]}]`
		if err := os.WriteFile(filepath.Join(dir, "api_conversation_history.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_KILO_ROOTS", kiloRoot)
	t.Setenv("DEJA_ROO_ROOTS", rooRoot)
	t.Setenv("DEJA_KILO_DB", filepath.Join(home, "absent.db"))

	kiloFiles, rooFiles := KiloTaskFiles(), RooTaskFiles()
	if len(kiloFiles) != 1 || !strings.Contains(kiloFiles[0], KiloExtensionID) {
		t.Fatalf("kilo files = %v", kiloFiles)
	}
	if len(rooFiles) != 1 || strings.Contains(rooFiles[0], KiloExtensionID) {
		t.Fatalf("roo files = %v", rooFiles)
	}
	for _, h := range Registry() {
		if h.Name != "kilocode" {
			continue
		}
		for _, k := range h.Kinds {
			if k.Name == "kilocode-task" && k.Match(rooFiles[0]) {
				t.Error("the kilocode kind claims a Roo task")
			}
		}
	}
}

func TestKiloDatabasePathFollowsXDGAndTheOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DEJA_KILO_DB", "")
	if got, want := KiloDB(), filepath.Join(home, ".local", "share", "kilo", "kilo.db"); got != want {
		t.Errorf("KiloDB = %q, want %q", got, want)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	if got, want := KiloDB(), filepath.Join(home, "data", "kilo", "kilo.db"); got != want {
		t.Errorf("with XDG_DATA_HOME, KiloDB = %q, want %q", got, want)
	}
	t.Setenv("DEJA_KILO_DB", filepath.Join(home, "elsewhere.db"))
	if got, want := KiloDB(), filepath.Join(home, "elsewhere.db"); got != want {
		t.Errorf("with the override, KiloDB = %q, want %q", got, want)
	}
}

// The CLI store is OpenCode's schema, so the same rows read as Kilo sessions.
// Skipped without sqlite3, like every other database store here.
func TestKiloReadsTheCLIDatabase(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 CLI not installed")
	}
	home := t.TempDir()
	db := filepath.Join(home, "kilo.db")
	t.Setenv("DEJA_KILO_DB", db)
	t.Setenv("DEJA_KILO_ROOTS", filepath.Join(home, "absent"))

	// OpenCode's own schema, the shape fixtures/registry/opencode/opencode.sql
	// carries, with a Kilo session in it.
	schema := `create table session (id text primary key, directory text, time_created any, time_updated any);
create table message (id text primary key, session_id text, time_created any, data text);
create table part (id text primary key, message_id text, data text);
insert into session values ('kilo-1', '/work/api', '2026-07-17T09:00:00Z', '2026-07-17T09:00:02Z');
insert into message values ('m-user', 'kilo-1', 1784278801000, '{"role":"user","time":{"created":"2026-07-17T09:00:01Z"}}');
insert into message values ('m-assistant', 'kilo-1', 1784278802000, '{"role":"assistant","time":{"created":"2026-07-17T09:00:02Z"}}');
insert into part values ('p-user', 'm-user', '{"type":"text","text":"why does the migration hang","time":{"start":"2026-07-17T09:00:01Z"}}');
insert into part values ('p-assistant', 'm-assistant', '{"type":"text","text":"the advisory lock was never released","time":{"start":"2026-07-17T09:00:02Z"}}');
`
	if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	ss := LoadKilo()
	if len(ss) == 0 {
		t.Fatal("the CLI database was not read")
	}
	for _, s := range ss {
		if s.Harness != "kilocode" {
			t.Fatalf("harness = %q, want kilocode", s.Harness)
		}
	}
	found := false
	for _, s := range ss {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "advisory lock") {
				found = true
			}
		}
	}
	if !found {
		t.Error("the reply from the database is missing")
	}
}
