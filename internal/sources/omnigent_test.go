package sources

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestOmnigentParseAndSince(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "omnigent.db")
	buildSQLiteFixture(t, filepath.Join("..", "..", "fixtures", "registry", "omnigent", "omnigent.sql"), db)
	t.Setenv("DEJA_OMNIGENT_DB", db)

	if got := OmnigentDB(); got != db {
		t.Fatalf("OmnigentDB() = %q, want %q", got, db)
	}
	ss := LoadOmnigent()
	if len(ss) != 1 {
		t.Fatalf("LoadOmnigent() got %d sessions, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "omnigent" || s.ID != "0195a8c0-7b20-7000-8000-112233445566" {
		t.Fatalf("got harness=%q id=%q", s.Harness, s.ID)
	}
	if s.Title != "Pin queue consumer prefetch count" {
		t.Fatalf("Title = %q", s.Title)
	}
	if s.Project != "workspace/omnigent-worker" {
		t.Fatalf("Project = %q, want workspace/omnigent-worker", s.Project)
	}
	if len(s.Messages) < 3 {
		t.Fatalf("Messages = %d, want >= 3", len(s.Messages))
	}

	since, err := ParseOmnigentDBSince(db, time.Unix(1752915000, 0).UTC())
	if err != nil || len(since) != 1 {
		t.Fatalf("ParseOmnigentDBSince: len=%d err=%v", len(since), err)
	}
	zero, err := ParseOmnigentDBSince(db, time.Time{})
	if err != nil || len(zero) != 1 {
		t.Fatalf("ParseOmnigentDBSince zero: len=%d err=%v", len(zero), err)
	}
	after, err := ParseOmnigentDBSince(db, time.Unix(1752916000, 0).UTC())
	if err != nil || len(after) != 0 {
		t.Fatalf("ParseOmnigentDBSince future: len=%d err=%v", len(after), err)
	}
	if got := OmnigentSessionDir(db, "0195a8c0-7b20-7000-8000-112233445566"); got != "/workspace/omnigent-worker" {
		t.Fatalf("OmnigentSessionDir() = %q", got)
	}
	if got := OmnigentSessionDir(db, "missing"); got != "" {
		t.Fatalf("OmnigentSessionDir(missing) = %q", got)
	}
}

func TestOmnigentPathsAndHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OMNIGENT_ROOT", "")
	t.Setenv("OMNIGENT_DATA_DIR", "")
	t.Setenv("DEJA_OMNIGENT_DB", "")

	if got, want := OmnigentRoot(), filepath.Join(home, ".omnigent"); got != want {
		t.Fatalf("OmnigentRoot() default = %q, want %q", got, want)
	}
	t.Setenv("OMNIGENT_DATA_DIR", "~/omni-data")
	if got, want := OmnigentRoot(), filepath.Join(home, "omni-data"); got != want {
		t.Fatalf("OmnigentRoot() OMNIGENT_DATA_DIR = %q, want %q", got, want)
	}
	t.Setenv("DEJA_OMNIGENT_ROOT", "~/deja-omni")
	if got, want := OmnigentRoot(), filepath.Join(home, "deja-omni"); got != want {
		t.Fatalf("OmnigentRoot() DEJA_OMNIGENT_ROOT = %q, want %q", got, want)
	}

	missingDB := filepath.Join(t.TempDir(), "missing.db")
	t.Setenv("DEJA_OMNIGENT_DB", missingDB)
	if got := OmnigentFiles(); got != nil {
		t.Fatalf("OmnigentFiles() missing = %v, want nil", got)
	}
	emptyDB := filepath.Join(t.TempDir(), "empty.db")
	if err := os.WriteFile(emptyDB, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OMNIGENT_DB", emptyDB)
	if got := OmnigentFiles(); got != nil {
		t.Fatalf("OmnigentFiles() empty = %v, want nil", got)
	}
	okDB := filepath.Join(t.TempDir(), "ok.db")
	if err := os.WriteFile(okDB, []byte("sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OMNIGENT_DB", okDB)
	if got := OmnigentFiles(); len(got) != 1 || got[0] != okDB {
		t.Fatalf("OmnigentFiles() non-empty = %v, want [%q]", got, okDB)
	}

	if got := omnigentUUID("0195a8c07b2070008000112233445566"); got != "0195a8c0-7b20-7000-8000-112233445566" {
		t.Fatalf("omnigentUUID(32-hex) = %q", got)
	}
	if got := omnigentUUID("0195a8c07b207000800011223344556z"); got != "0195a8c07b207000800011223344556z" {
		t.Fatalf("omnigentUUID(non-hex) = %q", got)
	}
	if got := omnigentUUID(""); got != "" {
		t.Fatalf("omnigentUUID(empty) = %q", got)
	}

	for _, tc := range []struct {
		in   any
		want int
	}{
		{float64(1), 1},
		{json.Number("6"), 6},
		{"message", 1},
		{"function_call", 2},
		{"function_call_output", 3},
		{"compaction", 6},
		{"terminal_command", 11},
		{"other", 0},
		{nil, 0},
	} {
		if got := omnigentItemType(tc.in); got != tc.want {
			t.Errorf("omnigentItemType(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}

	if got := omnigentContentText("str"); got != "str" {
		t.Fatalf("omnigentContentText(str) = %q", got)
	}
	if got := omnigentContentText([]any{"a", map[string]any{"input_text": "b"}, map[string]any{"output_text": "c"}}); got != "a\nb\nc" {
		t.Fatalf("omnigentContentText(slice) = %q", got)
	}
	if got := omnigentContentText(map[string]any{"text": "d"}); got != "d" {
		t.Fatalf("omnigentContentText(map) = %q", got)
	}
	if got := omnigentContentText(42); got != "" {
		t.Fatalf("omnigentContentText(42) = %q", got)
	}
}

func TestOmnigentMinimalSchemaAndItemTypes(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	missingDB := filepath.Join(dir, "missing.db")
	if ss, err := ParseOmnigentDB(missingDB); err != nil || ss != nil {
		t.Fatalf("ParseOmnigentDB(missing) = %v, %v", ss, err)
	}
	if got := OmnigentSessionDir(missingDB, "x"); got != "" {
		t.Fatalf("OmnigentSessionDir(missing) = %q", got)
	}

	unrelatedDB := filepath.Join(dir, "unrelated.db")
	cmd := exec.CommandContext(t.Context(), "sqlite3", unrelatedDB)
	cmd.Stdin = strings.NewReader("CREATE TABLE misc(id INT); INSERT INTO misc VALUES (1);")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create unrelated db: %v: %s", err, out)
	}
	if omnigentDBHasTable(unrelatedDB) {
		t.Fatal("omnigentDBHasTable(unrelated) = true, want false")
	}
	if ss, err := ParseOmnigentDB(unrelatedDB); err != nil || ss != nil {
		t.Fatalf("ParseOmnigentDB(unrelated) = %v, %v", ss, err)
	}
	if got := OmnigentSessionDir(unrelatedDB, "x"); got != "" {
		t.Fatalf("OmnigentSessionDir(unrelated) = %q", got)
	}

	db := filepath.Join(dir, "minimal.db")
	sql := `
CREATE TABLE conversations(workspace_id TEXT, id TEXT PRIMARY KEY, created_at INTEGER, updated_at INTEGER, title TEXT, parent_conversation_id TEXT, fork_from_item_id TEXT);
CREATE TABLE conversation_items(workspace_id TEXT, conversation_id TEXT, position INTEGER, id TEXT, created_at INTEGER, type INTEGER, data TEXT);
INSERT INTO conversations VALUES ('ws1', '0195a8c07b2070008000000000000002', 1752915001, 1752915010, '', '0195a8c07b2070008000000000000001', '');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 1, 'i1', 1752915001, 1, '{"role":"user","is_meta":true,"content":"ignored"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 2, 'i2', 1752915002, 1, '{"role":"system","content":"ignored"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 3, 'i3', 1752915003, 1, '{"role":"user","content":"Retry consumer loop\ndetails"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 4, 'i4', 1752915004, 1, '{"role":"assistant","content":"Updated backoff"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 5, 'i5', 1752915005, 2, '{"name":"edit_file","call_id":"c1","arguments":"{\"file_path\":\"/Users/alex/app/worker.py\",\"old_string\":\"1\",\"new_string\":\"2\"}"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 6, 'i6', 1752915006, 3, '{"output":"patched worker.py"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 7, 'i7', 1752915007, 6, '{"summary":"Compacted retry context"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 8, 'i8', 1752915008, 11, '{"kind":"input","input":"pytest tests/test_worker.py"}');
INSERT INTO conversation_items VALUES ('ws1', '0195a8c07b2070008000000000000002', 9, 'i9', 1752915009, 11, '{"kind":"output","stdout":"1 passed","stderr":""}');
`
	cmd2 := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd2.Stdin = strings.NewReader(sql)
	if out, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("create minimal db: %v: %s", err, out)
	}

	t.Setenv("DEJA_SUBAGENTS", "1")
	ss, err := ParseOmnigentDB(db)
	if err != nil {
		t.Fatalf("ParseOmnigentDB: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("ParseOmnigentDB got %d sessions, want 1", len(ss))
	}
	if ss[0].Kind != model.KindSubagent {
		t.Fatalf("ss[0].Kind = %q, want %q", ss[0].Kind, model.KindSubagent)
	}
	if ss[0].Title != "Retry consumer loop" {
		t.Fatalf("ss[0].Title = %q, want %q", ss[0].Title, "Retry consumer loop")
	}
}
