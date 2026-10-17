package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An opencode fork's first MCP recall answered with its source: the fork is
// not indexed on its first turn, and the MCP exclude set was built from the
// index alone, so nothing tied the fork to the session it copied. The hooks
// read such a fork off opencode's db and left the source out (#5129).
func TestAnUnindexedOpencodeForksMCPRecallLeavesOutItsSource(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	hermeticEnv(t)
	db := filepath.Join(t.TempDir(), "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	sqlite := func(script string) {
		t.Helper()
		if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
			t.Fatalf("sqlite: %v %s", err, out)
		}
	}
	// opencode copies the source's messages into the fork with their times.
	session := func(id, msg string) string {
		return `insert into session values('` + id + `', '/w/oc', '2026-10-17T21:20:00Z', '2026-10-17T21:20:05Z');
insert into message values('` + msg + `u', '` + id + `', 1792271200000, '{"role":"user"}');
insert into part values('` + msg + `pu', '` + msg + `u', '{"type":"text","text":"retry the marmot upload with token quokkaflux"}');
insert into message values('` + msg + `a', '` + id + `', 1792271201000, '{"role":"assistant"}');
insert into part values('` + msg + `pa', '` + msg + `a', '{"type":"text","text":"Retrying the marmot upload now."}');
`
	}
	sqlite(`create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
` + session("ses_source", "s") + session("ses_sibling", "b") + `
insert into message values('bu2', 'ses_sibling', 1792271300000, '{"role":"user"}');
insert into part values('bpu2', 'bu2', '{"type":"text","text":"the sibling went on with token wombatzeal"}');`)
	// The source edited a file: the index files its diff store after its
	// turns, and the fork's own store carries no diff.
	diffs := filepath.Join(filepath.Dir(db), "storage", "session_diff")
	if err := os.MkdirAll(diffs, 0o755); err != nil {
		t.Fatal(err)
	}
	diff := `[{"file": "up.go", "status": "modified", "additions": 1, "deletions": 1, "patch": "--- a/up.go\n+++ b/up.go\n@@ -1,1 +1,1 @@\n-retries := 3\n+retries := 5\n"}]`
	if err := os.WriteFile(filepath.Join(diffs, "ses_source.json"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	sqlite(session("ses_fork", "f"))

	q := json.RawMessage(`{"query":"quokkaflux"}`)
	// Control: another live session is answered with the source.
	markSessionLive(dir, "ses_someone_else")
	if out, err := callMCPTool(dir, "recall", q); err != nil || !strings.Contains(out, "ses_source") {
		t.Fatalf("the control was not answered with the source, so this proves nothing (%v):\n%s", err, out)
	}
	endSessionLive(dir, "ses_someone_else")

	markSessionLive(dir, "ses_fork")
	out, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "ses_source") {
		t.Errorf("the fork's first recall answered with the session it was forked from:\n%s", out)
	}
	// A sibling fork that went on holds work this fork never saw.
	out, err = callMCPTool(dir, "recall", json.RawMessage(`{"query":"wombatzeal"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ses_sibling") {
		t.Errorf("a sibling fork that went on was left out of the fork's recall:\n%s", out)
	}
}
