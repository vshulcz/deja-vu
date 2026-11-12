package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOrcaParseAndSince(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "orca.db")
	buildSQLiteFixture(t, filepath.Join("..", "..", "fixtures", "registry", "orca", "orca.sql"), db)
	t.Setenv("DEJA_ORCA_DB", db)

	if got := OrcaDB(); got != db {
		t.Fatalf("OrcaDB() = %q, want %q", got, db)
	}
	ss := LoadOrca()
	if len(ss) != 1 {
		t.Fatalf("LoadOrca() got %d sessions, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "orca" || s.ID != "orca-sess-01" {
		t.Fatalf("got harness=%q id=%q", s.Harness, s.ID)
	}
	if s.Title != "Fix webhook signature verification" {
		t.Fatalf("Title = %q", s.Title)
	}
	if s.Project != "workspace/orca-payments" {
		t.Fatalf("Project = %q, want workspace/orca-payments", s.Project)
	}
	if len(s.Messages) < 3 {
		t.Fatalf("Messages = %d, want >= 3", len(s.Messages))
	}

	since, err := ParseOrcaDBSince(db, time.UnixMilli(1752915000000).UTC())
	if err != nil || len(since) != 1 {
		t.Fatalf("ParseOrcaDBSince: len=%d err=%v", len(since), err)
	}
	zero, err := ParseOrcaDBSince(db, time.Time{})
	if err != nil || len(zero) != 1 {
		t.Fatalf("ParseOrcaDBSince zero: len=%d err=%v", len(zero), err)
	}
	after, err := ParseOrcaDBSince(db, time.UnixMilli(1752916000000).UTC())
	if err != nil || len(after) != 0 {
		t.Fatalf("ParseOrcaDBSince future: len=%d err=%v", len(after), err)
	}
}

func TestOrcaPathsAndFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ORCA_ROOT", "")
	t.Setenv("DEJA_ORCA_DB", "")
	t.Setenv("APPDATA", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	if got := OrcaRoot(); got == "" {
		t.Fatal("OrcaRoot() returned empty")
	}
	t.Setenv("DEJA_ORCA_ROOT", "~/orca-custom")
	if got, want := OrcaRoot(), filepath.Join(home, "orca-custom"); got != want {
		t.Fatalf("OrcaRoot() override = %q, want %q", got, want)
	}

	if got, want := orcaDataDir("darwin"), filepath.Join(home, "Library", "Application Support", "orca"); got != want {
		t.Fatalf("orcaDataDir(darwin) = %q, want %q", got, want)
	}
	if got, want := orcaDataDir("windows"), filepath.Join(home, "AppData", "Roaming", "orca"); got != want {
		t.Fatalf("orcaDataDir(windows) default = %q, want %q", got, want)
	}
	winAppData := filepath.Join(home, "win-appdata")
	t.Setenv("APPDATA", winAppData)
	if got, want := orcaDataDir("windows"), filepath.Join(winAppData, "orca"); got != want {
		t.Fatalf("orcaDataDir(windows) APPDATA = %q, want %q", got, want)
	}

	if got, want := orcaDataDir("linux"), filepath.Join(home, ".config", "orca"); got != want {
		t.Fatalf("orcaDataDir(linux) default = %q, want %q", got, want)
	}
	xdgLower := filepath.Join(home, "xdg-lower")
	if err := os.MkdirAll(filepath.Join(xdgLower, "orca"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdgLower)
	if got, want := orcaDataDir("linux"), filepath.Join(xdgLower, "orca"); got != want {
		t.Fatalf("orcaDataDir(linux) lower = %q, want %q", got, want)
	}
	xdgUpper := filepath.Join(home, "xdg-upper")
	if err := os.MkdirAll(filepath.Join(xdgUpper, "Orca"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdgUpper)
	if got, want := orcaDataDir("linux"), filepath.Join(xdgUpper, "Orca"); !strings.EqualFold(got, want) {
		t.Fatalf("orcaDataDir(linux) upper = %q, want %q", got, want)
	}

	missingDB := filepath.Join(t.TempDir(), "missing.db")
	t.Setenv("DEJA_ORCA_DB", missingDB)
	if got := OrcaFiles(); got != nil {
		t.Fatalf("OrcaFiles() missing = %v, want nil", got)
	}
	emptyDB := filepath.Join(t.TempDir(), "empty.db")
	if err := os.WriteFile(emptyDB, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ORCA_DB", emptyDB)
	if got := OrcaFiles(); got != nil {
		t.Fatalf("OrcaFiles() empty = %v, want nil", got)
	}
	okDB := filepath.Join(t.TempDir(), "ok.db")
	if err := os.WriteFile(okDB, []byte("sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_ORCA_DB", okDB)
	if got := OrcaFiles(); len(got) != 1 || got[0] != okDB {
		t.Fatalf("OrcaFiles() non-empty = %v, want [%q]", got, okDB)
	}
}

func TestOrcaJournalBranchesAndSessionDir(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	missingDB := filepath.Join(dir, "missing.db")
	if ss, err := ParseOrcaDB(missingDB); err != nil || ss != nil {
		t.Fatalf("ParseOrcaDB(missing) = %v, %v", ss, err)
	}
	if got := OrcaSessionDir(missingDB, "x"); got != "" {
		t.Fatalf("OrcaSessionDir(missing) = %q", got)
	}

	unrelatedDB := filepath.Join(dir, "unrelated.db")
	cmd := exec.CommandContext(t.Context(), "sqlite3", unrelatedDB)
	cmd.Stdin = strings.NewReader("CREATE TABLE misc(id INT); INSERT INTO misc VALUES (1);")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create unrelated db: %v: %s", err, out)
	}
	if orcaDBHasTable(unrelatedDB) {
		t.Fatal("orcaDBHasTable(unrelatedDB) = true, want false")
	}
	if ss, err := ParseOrcaDB(unrelatedDB); err != nil || ss != nil {
		t.Fatalf("ParseOrcaDB(unrelated) = %v, %v", ss, err)
	}
	if got := OrcaSessionDir(unrelatedDB, "x"); got != "" {
		t.Fatalf("OrcaSessionDir(unrelated) = %q", got)
	}

	db := filepath.Join(dir, "journal.db")
	sql := `
CREATE TABLE agent_session_records(session_id TEXT PRIMARY KEY, record_json TEXT NOT NULL);
CREATE TABLE journal_rows(session_id TEXT NOT NULL, epoch TEXT NOT NULL, seq INTEGER NOT NULL, ts INTEGER NOT NULL, row_json TEXT NOT NULL, PRIMARY KEY (session_id, epoch, seq));
INSERT INTO agent_session_records VALUES ('s-ws', '{"id":"s-ws","workspaceId":"/Users/alex/work/orca-ws","cwd":"","title":"","createdAt":1752915001000,"updatedAt":1752915005000}');
INSERT INTO agent_session_records VALUES ('s-cwd', '{"id":"s-cwd","workspaceId":"/Users/alex/work/ws","cwd":"/Users/alex/work/orca-cwd","title":"Explicit"}');
INSERT INTO journal_rows VALUES ('s-ws', '', 1, 1752915001000, '{"itemId":"m1","revision":1,"observedAt":1752915001000,"sequence":1,"body":{"kind":"message","role":"user","blocks":[{"kind":"text","text":"Old draft"}]}}');
INSERT INTO journal_rows VALUES ('s-ws', '', 2, 1752915002000, '{"itemId":"m1","revision":2,"observedAt":1752915002000,"sequence":1,"body":{"kind":"message","role":"user","blocks":[{"kind":"text","text":"Settled title line\nmore context"}]}}');
INSERT INTO journal_rows VALUES ('s-ws', '', 3, 1752915002500, '{"itemId":"","revision":1,"observedAt":0,"sequence":0,"body":{"kind":"message","role":"assistant","blocks":[{"kind":"text","text":"Inspecting handler"},{"kind":"tool-call","toolCallId":"tc1","name":"read_file","input":{"file_path":"/Users/alex/work/orca-ws/handler.go"}},{"kind":"tool-result","output":"package handler"}]}}');
INSERT INTO journal_rows VALUES ('s-ws', '', 4, 1752915003000, '{"itemId":"tc-top","revision":1,"observedAt":1752915003000,"sequence":4,"body":{"kind":"tool-call","name":"bash","input":{"command":"go test ./..."},"output":{"head":"ok  orca-ws 0.1s"}}}');
INSERT INTO journal_rows VALUES ('s-ws', '', 5, 1752915004000, '{"itemId":"diff-1","revision":1,"observedAt":1752915004000,"sequence":5,"body":{"kind":"diff","path":"/Users/alex/work/orca-ws/handler.go","patch":{"head":"@@ -1 +1,2 @@\n package handler\n+// ok"}}}');
INSERT INTO journal_rows VALUES ('s-ws', '', 6, 1752915004500, 'not-json');
INSERT INTO journal_rows VALUES ('s-ws', '', 7, 1752915004600, '{"itemId":"sys","revision":1,"body":{"kind":"message","role":"system","blocks":[{"kind":"text","text":"ignored"}]}}');
INSERT INTO journal_rows VALUES ('s-cwd', '', 1, 1752915001000, '{"itemId":"u1","revision":1,"observedAt":1752915001000,"sequence":1,"body":{"kind":"message","role":"user","blocks":[{"kind":"text","text":"Check cwd"}]}}');
`
	cmd2 := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd2.Stdin = strings.NewReader(sql)
	if out, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("create journal db: %v: %s", err, out)
	}

	ss, err := ParseOrcaDB(db)
	if err != nil {
		t.Fatalf("ParseOrcaDB: %v", err)
	}
	if len(ss) != 2 {
		t.Fatalf("ParseOrcaDB got %d sessions, want 2", len(ss))
	}
	if ss[0].Title != "Settled title line" {
		t.Fatalf("ss[0].Title = %q, want %q", ss[0].Title, "Settled title line")
	}
	var joined strings.Builder
	for _, m := range ss[0].Messages {
		joined.WriteString(m.Text)
		joined.WriteByte('\n')
	}
	if !strings.Contains(joined.String(), "Inspecting handler") {
		t.Fatalf("expected Inspecting handler in messages: %q", joined.String())
	}
	if got := OrcaSessionDir(db, "s-ws"); got != "/Users/alex/work/orca-ws" {
		t.Fatalf("OrcaSessionDir(s-ws) = %q", got)
	}
	if got := OrcaSessionDir(db, "s-cwd"); got != "/Users/alex/work/orca-cwd" {
		t.Fatalf("OrcaSessionDir(s-cwd) = %q", got)
	}
	if got := OrcaSessionDir(db, "unknown"); got != "" {
		t.Fatalf("OrcaSessionDir(unknown) = %q", got)
	}
}
