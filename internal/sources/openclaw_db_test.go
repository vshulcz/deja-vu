package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	cmd := exec.Command("sqlite3", db)
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

func TestOpenClawEmptyOrForeignStoreIsQuiet(t *testing.T) {
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
	if out, err := exec.Command("sqlite3", other, "create table t(x)").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got, err := ParseOpenClawDB(other); err != nil || len(got) != 0 {
		t.Errorf("a store without transcript_events must read as nothing, not an error: %v %v", got, err)
	}
}
