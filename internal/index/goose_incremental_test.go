package index

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A goose session untouched since the watermark must survive an incremental
// pass triggered by a change to the shared sessions.db.
func TestGooseIncrementalKeepsUntouchedSessions(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	tmp := hermeticIndexEnv(t)
	t.Setenv("DEJA_GOOSE_ROOT", filepath.Join(tmp, "goose"))
	dir := filepath.Join(tmp, "index")
	db := filepath.Join(os.Getenv("DEJA_GOOSE_ROOT"), "sessions", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := func(extra string) {
		schema := `create table if not exists sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table if not exists messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert or replace into sessions values ('20250724_old','old','Old chat','/w/app','2026-07-24T10:00:00Z','2026-07-24T10:00:01Z');
insert or replace into messages values (1,'20250724_old','user','[{"type":"text","text":"oldgoosefact about the pager"}]',1784278801);
insert or replace into messages values (2,'20250724_old','assistant','[{"type":"text","text":"oldgoose reply"}]',1784278802)` + extra + `;`
		if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
			t.Fatalf("seed: %v: %s", err, out)
		}
	}
	seed("")
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(dir, search.Options{Query: "oldgoosefact", All: true}); len(hits) == 0 {
		t.Fatal("old goose session not indexed on first pass")
	}
	seed(`;
insert or replace into sessions values ('20250724_new','new','New chat','/w/app','2026-07-24T11:00:00Z','2026-07-24T11:00:02Z');
insert or replace into messages values (3,'20250724_new','user','[{"type":"text","text":"newgoosefact about caching"}]',1784282401);
insert or replace into messages values (4,'20250724_new','assistant','[{"type":"text","text":"newgoose reply"}]',1784282402)`)
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(db, future, future)
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(dir, search.Options{Query: "newgoosefact", All: true}); len(hits) == 0 {
		t.Fatal("new goose session not indexed on incremental pass")
	}
	if hits, _ := Search(dir, search.Options{Query: "oldgoosefact", All: true}); len(hits) == 0 {
		t.Fatal("REGRESSION: untouched goose session vanished after incremental pass")
	}
	fmt.Fprintln(os.Stderr, "both goose sessions survived incremental")
}
