package index

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An older-ZCode snapshot is rewritten whole on each turn, and each rewrite
// added its turns again: 6 messages where a rebuild had 4. Once the session
// was restored into the CLI database a rebuild read it from there alone,
// while the unchanged snapshot kept its turns beside the database's (#4448).
func TestAZCodeSnapshotRewrittenAndRestoredMatchesARebuild(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	isolateStores(t, tmp)
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(tmp, "absent"))
	db := filepath.Join(tmp, "zcode", "db.sqlite")
	t.Setenv("DEJA_ZCODE_DB", db)
	root := filepath.Join(tmp, "zcode-v2")
	t.Setenv("DEJA_ZCODE_LEGACY_ROOT", root)
	snap := filepath.Join(root, "5f0c1e9a", "task-1.json")
	msgs := []string{
		`{"role":"user","content":"fix the retry loop, it never stops","timestamp":1790762400000}`,
		`{"role":"assistant","content":"capped it at five attempts","timestamp":1790762460000}`,
		`{"role":"user","content":"now run the tests","timestamp":1790762520000}`,
		`{"role":"assistant","content":"tests pass","timestamp":1790762580000}`,
	}
	write := func(n int, at time.Time) {
		t.Helper()
		writeAt(t, snap, `{"meta":{"taskId":"task-1","workspacePath":"/tmp/proj","title":"fix the retry loop","createdAt":1790762400000},"messages":[`+
			strings.Join(msgs[:n], ",")+`]}`, at)
	}
	write(2, time.Now().Add(-time.Hour))
	dir := filepath.Join(tmp, "index.db")
	indexPass(t, dir)
	write(4, time.Now().Add(-time.Minute))
	indexPass(t, dir)
	matchesRebuild(t, dir, "zcode", "task-1")

	// The user restores it: the database now holds the session, and the
	// snapshot stays as it was.
	ms := int64(1790762400000)
	seed := `create table session (id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part (id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);
` + fmt.Sprintf(`insert into session values ('task-1','p','','/tmp/proj','restored',%d,%d);
insert into message values ('m1','task-1',%d,%d,'{"role":"user","time":{"created":%d}}');
insert into part values ('p1','m1','task-1',%d,%d,'{"type":"text","text":"fix the retry loop, it never stops"}');
`, ms, ms+1000, ms, ms, ms, ms, ms)
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sqlite3", db, seed).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	indexPass(t, dir)
	matchesRebuild(t, dir, "zcode", "task-1")
}
