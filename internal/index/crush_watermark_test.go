package index

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// crushEnv registers one Crush project in a fresh home and returns the path of
// its store, which does not exist until crushSQL writes to it.
func crushEnv(t *testing.T) (tmp, db string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	tmp = t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	data := filepath.Join(tmp, "crushdata")
	t.Setenv("DEJA_CRUSH_ROOT", data)
	project := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(filepath.Join(project, ".crush"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := fmt.Sprintf(`{"projects":[{"path":%q,"data_dir":%q}]}`, project, filepath.Join(project, ".crush"))
	if err := os.WriteFile(filepath.Join(data, "projects.json"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	db = filepath.Join(project, ".crush", "crush.db")
	crushSQL(t, db, `create table sessions (id text primary key, parent_session_id text, title text not null,
  updated_at integer not null, created_at integer not null);
create table messages (id text primary key, session_id text not null, role text not null,
  parts text not null default '[]', created_at integer not null, updated_at integer not null);`)
	return tmp, db
}

func crushSQL(t *testing.T, db, sql string) {
	t.Helper()
	c := exec.Command("sqlite3", db)
	c.Stdin = strings.NewReader(sql)
	if o, err := c.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, o)
	}
}

// crushTurn adds a user message and moves the session's updated_at to its
// stamp, which is what Crush's own update does.
func crushTurn(t *testing.T, db, session, id, text string, at int) {
	t.Helper()
	crushSQL(t, db, fmt.Sprintf(`insert or ignore into sessions values (%[1]q,null,'retry',%[4]d,%[4]d);
insert into messages values (%[2]q,%[1]q,'user','[{"type":"text","data":{"text":%[3]q}}]',%[4]d,%[4]d);
update sessions set updated_at=%[4]d where id=%[1]q;`, session, id, text, at))
}

// Every Crush message used to re-read the whole project store: the store was
// never stamped, so the since parser never ran, and 3000 sessions were replaced
// for one turn (#4381). A quiet session's row rewritten in place without its
// updated_at moving is the witness: a whole read picks the new text up, a read
// from the watermark does not.
func TestTheCrushStoreIsAskedOnlyForWhatIsNew(t *testing.T) {
	tmp, db := crushEnv(t)
	const at = 1784282400
	crushTurn(t, db, "quiet", "q1", "marker-crush-quiet", at-100)
	crushTurn(t, db, "busy", "b1", "marker-crush-one", at)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if zedHits(t, dir, "marker-crush-quiet") != 1 || zedHits(t, dir, "marker-crush-one") != 1 {
		t.Fatalf("the store was not indexed, so this measures nothing")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[db].LastUpdated == 0 {
		t.Errorf("the store carries no watermark, so the next pass reads it whole")
	}

	crushSQL(t, db, `update messages set parts='[{"type":"text","data":{"text":"marker-crush-rewritten"}}]' where id='q1';`)
	// In the watermark's own second: Crush stamps whole seconds, and a strict
	// > against the last turn's second would never ask for this one.
	crushTurn(t, db, "busy", "b2", "marker-crush-two", at)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := zedHits(t, dir, "marker-crush-rewritten"); n != 0 {
		t.Errorf("the quiet session was read again: the pass read the whole store")
	}
	if n := zedHits(t, dir, "marker-crush-quiet"); n != 1 {
		t.Errorf("the session the pass did not ask about was dropped: %d hits", n)
	}
	if n := zedTurns(t, dir, "marker-crush-two"); n != 1 {
		t.Errorf("the added turn is held %d times", n)
	}
	// The busy session comes back whole and replaces what was held.
	if n := zedTurns(t, dir, "marker-crush-one"); n != 1 {
		t.Errorf("the earlier turn is held %d times", n)
	}
}
