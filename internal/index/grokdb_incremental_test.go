package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A store read from a watermark hands back the new turns alone, so the pass has
// to keep the records it already holds for that store. Three stores were never
// stamped and so were always read whole; stamping them (#2075) makes their
// parse partial, and the rule that keeps their records has to follow —
// otherwise the second pass drops every turn but the newest.
func TestGrokDBAppendKeepsTheEarlierTurns(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	db := filepath.Join(tmp, "grok.db")
	t.Setenv("DEJA_GROK_DB", db)
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))

	run := func(sql string) {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(sql); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sqlite3", db)
		cmd.Stdin = f
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v: %s", err, out)
		}
	}
	run(`CREATE TABLE workspaces (id TEXT PRIMARY KEY, canonical_path TEXT);
CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, title TEXT, cwd_last TEXT, created_at TEXT);
CREATE TABLE messages (session_id TEXT, seq INTEGER, role TEXT, message_json TEXT, created_at TEXT);
INSERT INTO sessions VALUES ('gdb1','w1','Pool timeouts','/work/api','2026-07-27T10:00:00.000Z');
INSERT INTO messages VALUES ('gdb1',0,'user','{"role":"user","content":"grokfirstneedle opening question"}','2026-07-27T10:00:00.000Z');
INSERT INTO sessions VALUES ('gdb2','w1','Quiet one','/work/api','2026-07-26T10:00:00.000Z');
INSERT INTO messages VALUES ('gdb2',0,'user','{"role":"user","content":"grokquietneedle a session nobody touches again"}','2026-07-26T10:00:00.000Z');`)

	indexDir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(indexDir, search.Options{Query: "grokfirstneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}
	run(`INSERT INTO messages VALUES ('gdb1',1,'assistant','{"role":"assistant","content":[{"type":"text","text":"groksecondneedle the answer"}]}','2026-07-27T11:00:00.000Z');`)
	if err := EnsureForSearch(indexDir, search.Options{Query: "groksecondneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"grokfirstneedle", "groksecondneedle", "grokquietneedle"} {
		ss, err := Search(indexDir, search.Options{Query: needle, Harness: "grok"})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 {
			t.Errorf("%s: %d hits after the append, want 1", needle, len(ss))
		}
	}
}
