package main

import (
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A Hermes /branch copies the conversation into a new session, with new
// timestamps, and names its source in model_config._branched_from. The
// branch's recall answered with that source as hit 1 once the source's live
// stamp was gone, after a --resume and /branch or twenty minutes on (#5141).
func TestAHermesBranchsRecallLeavesOutItsSource(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	hermeticEnv(t)
	db := filepath.Join(t.TempDir(), "state.db")
	t.Setenv("DEJA_HERMES_DB", db)
	sqlite := func(script string) {
		t.Helper()
		if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
			t.Fatalf("sqlite: %v %s", err, out)
		}
	}
	turns := func(id string, at int) string {
		return `insert into sessions values('` + id + `','cli',NULL,'/w/app','{}');
insert into messages (session_id,role,content,timestamp) values
 ('` + id + `','user','password hashing uses bcrypt cost 12, marker ospreyquill',` + itoa(at) + `),
 ('` + id + `','assistant','ok, bcrypt cost 12 it is',` + itoa(at+1) + `);
`
	}
	sqlite(`create table sessions (id text primary key, source text, parent_session_id text, cwd text, model_config text);
create table messages (id integer primary key autoincrement, session_id text not null, role text not null, content text,
 tool_call_id text, tool_calls text, tool_name text, timestamp real not null, token_count integer, finish_reason text);
` + turns("20261018_012943_14524c", 1792200000))
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	// The branch, after the build: its first turn comes before any build.
	sqlite(strings.Replace(turns("20261018_013350_8f1cbe", 1792200100),
		`'cli',NULL,'/w/app','{}'`, `'cli','20261018_012943_14524c',NULL,'{"_branched_from":"20261018_012943_14524c"}'`, 1))

	q := json.RawMessage(`{"query":"ospreyquill"}`)
	markSessionLive(dir, "20261018_020000_dddddd")
	if out, err := callMCPTool(dir, "recall", q); err != nil || !strings.Contains(out, "20261018_012943_14524c") {
		t.Fatalf("the control was not answered with the source, so this proves nothing (%v):\n%s", err, out)
	}
	endSessionLive(dir, "20261018_020000_dddddd")

	markSessionLive(dir, "20261018_013350_8f1cbe")
	out, err := callMCPTool(dir, "recall", q)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "20261018_012943_14524c") {
		t.Errorf("the branch's recall answered with the session it was branched from:\n%s", out)
	}
}
