package index

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// seedCursorStore writes one composer and its bubbles into a Cursor store.
func seedCursorStore(t *testing.T, db, composer string, bubbles [][2]any) {
	t.Helper()
	var last int64
	headers := ""
	for i, b := range bubbles {
		if ts := b[1].(int64); ts > last {
			last = ts
		}
		if i > 0 {
			headers += ","
		}
		headers += fmt.Sprintf(`{"bubbleId":"b%d","type":1}`, i)
	}
	stmts := "create table if not exists cursorDiskKV (key text primary key, value text);\n"
	stmts += fmt.Sprintf("insert or replace into cursorDiskKV values ('composerData:%[1]s', json('{\"composerId\":\"%[1]s\",\"name\":\"work\",\"createdAt\":%[2]d,\"lastUpdatedAt\":%[2]d,\"fullConversationHeadersOnly\":[%[3]s]}'));\n", composer, last, headers)
	for i, b := range bubbles {
		stmts += fmt.Sprintf("insert or replace into cursorDiskKV values ('bubbleId:%s:b%d', json('{\"type\":1,\"text\":\"%s\",\"timestamp\":%d,\"workspaceProjectDir\":\"/tmp/app\"}'));\n",
			composer, i, b[0].(string), b[1].(int64))
	}
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// A store's watermark is what deja asks it for next time, so it has to be the
// newest session in THAT store. Taking the newest across the harness stamped a
// quiet Cursor workspace with the busy one's time, and everything the quiet
// store gained below that line was skipped for ever (#2071).
func TestEachCursorStoreKeepsItsOwnWatermark(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	root := filepath.Join(tmp, "Cursor", "User")
	busy := filepath.Join(root, "globalStorage")
	quiet := filepath.Join(root, "workspaceStorage", "ws1")
	for _, d := range []string{busy, quiet} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CURSOR_ROOT", root)
	busyDB := filepath.Join(busy, "state.vscdb")
	quietDB := filepath.Join(quiet, "state.vscdb")

	const base = int64(1767268800000) // 2026-01-01 12:00, in milliseconds
	const hour = int64(3600000)
	seedCursorStore(t, busyDB, "comp-busy", [][2]any{{"marker-busy-one", base + 10*hour}})
	seedCursorStore(t, quietDB, "comp-quiet", [][2]any{{"marker-quiet-one", base}})

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits := func(marker string) int {
		t.Helper()
		got, err := Search(dir, search.Options{Query: marker, All: true})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	// The premise: both stores were read, so the watermarks below are about
	// two stores that each hold something.
	if hits("marker-busy-one") != 1 || hits("marker-quiet-one") != 1 {
		t.Fatalf("the two stores were not both indexed, so this measures nothing")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[quietDB].LastUpdated > m.Files[busyDB].LastUpdated {
		t.Fatalf("the quiet store is stamped later than the busy one: %d vs %d",
			m.Files[quietDB].LastUpdated, m.Files[busyDB].LastUpdated)
	}
	if m.Files[quietDB].LastUpdated == m.Files[busyDB].LastUpdated {
		t.Errorf("both stores carry one watermark (%d), so the quiet one is stamped with the busy one's newest",
			m.Files[quietDB].LastUpdated)
	}

	// A turn lands in the quiet workspace: newer than anything that store
	// holds, older than the busy store's newest.
	seedCursorStore(t, quietDB, "comp-quiet", [][2]any{
		{"marker-quiet-one", base}, {"marker-quiet-two", base + hour},
	})
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("marker-quiet-two"); n != 1 {
		t.Errorf("the turn added to the quiet store is not indexed: %d hits", n)
	}
}

// opencode is the exception the rule above has to keep: its rows carry the
// project directory in Path, not the store path, so filtering on Path would
// zero its watermark and re-read the whole database on every pass.
func TestTheOpencodeStoreStillGetsAWatermark(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	stamped := `create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, data text, time_created integer);
create table part (id text primary key, message_id text, data text);
insert into session values ('s1','/tmp/app',1767268800000,1767268800000);
insert into message values ('m1','s1','{"role":"user"}',1767268800000);
insert into part values ('p1','m1',json_object('type','text','text','the pool was exhausted'));`
	if out, err := exec.Command("sqlite3", db, stamped).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the row really does name the project rather than the store.
	if s := m.Sessions["opencode:s1"]; s.Path == db {
		t.Fatalf("opencode now records the store path, so this measures nothing: %s", s.Path)
	}
	if m.Files[db].LastUpdated == 0 {
		t.Error("the opencode store carries no watermark, so every pass reads it whole")
	}
}
