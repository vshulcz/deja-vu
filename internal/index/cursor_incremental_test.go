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

// A cursor session untouched since the watermark must survive an incremental
// pass triggered by a change to the shared state.vscdb.
func TestCursorIncrementalKeepsUntouchedSessions(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "index")
	db := filepath.Join(os.Getenv("DEJA_CURSOR_ROOT"), "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := func(extra string) {
		schema := `create table if not exists cursorDiskKV (key text primary key, value text);
insert or replace into cursorDiskKV values
 ('composerData:old', json('{"composerId":"old","name":"Old chat","createdAt":1752600000000,"lastUpdatedAt":1752600100000,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1}]}')),
 ('bubbleId:old:b1', json('{"type":1,"text":"oldsessionfact about the pager","timestamp":1752600001000,"workspaceProjectDir":"/w/app"}'))` + extra + `;`
		if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
			t.Fatalf("seed: %v: %s", err, out)
		}
	}
	seed("")
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(dir, search.Options{Query: "oldsessionfact", All: true}); len(hits) == 0 {
		t.Fatal("old cursor session not indexed on first pass")
	}
	// Add a NEW session (post-watermark) and touch the DB mtime.
	seed(`,
 ('composerData:new', json('{"composerId":"new","name":"New chat","createdAt":1752700000000,"lastUpdatedAt":1752700100000,"fullConversationHeadersOnly":[{"bubbleId":"c1","type":1}]}')),
 ('bubbleId:new:c1', json('{"type":1,"text":"newsessionfact about caching","timestamp":1752700001000,"workspaceProjectDir":"/w/app"}'))`)
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(db, future, future)
	if err := Ensure(dir, "", false, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(dir, search.Options{Query: "newsessionfact", All: true}); len(hits) == 0 {
		t.Fatal("new cursor session not indexed on incremental pass")
	}
	if hits, _ := Search(dir, search.Options{Query: "oldsessionfact", All: true}); len(hits) == 0 {
		t.Fatal("REGRESSION: untouched cursor session vanished after incremental pass")
	}
	fmt.Fprintln(os.Stderr, "both cursor sessions survived incremental")
}
