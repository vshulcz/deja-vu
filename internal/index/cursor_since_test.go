package index

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func cursorBubble(cid, bid string, typ int, text string, at int64) string {
	return fmt.Sprintf(`insert or replace into cursorDiskKV values ('bubbleId:%s:%s', json('{"type":%d,"text":"%s","timestamp":%d,"workspaceProjectDir":"/tmp/proj"}'));`+"\n", cid, bid, typ, text, at)
}

func cursorComposer(cid, name string, created, updated int64) string {
	return fmt.Sprintf(`insert or replace into cursorDiskKV values ('composerData:%s', json('{"composerId":"%s","name":"%s","createdAt":%d,"lastUpdatedAt":%d}'));`+"\n", cid, cid, name, created, updated)
}

// The since read handed back a composer's new bubbles alone. A rename, which
// rewrites composerData and adds no bubble, was dropped for having none
// (#4450), and a continued chat had its words, asked and touched recomputed
// from the latest turns only (#4451).
func TestACursorChatContinuedOrRenamedMatchesARebuild(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	isolateStores(t, tmp)
	root := filepath.Join(tmp, "cursor")
	t.Setenv("DEJA_CURSOR_ROOT", root)
	db := filepath.Join(root, "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	sql := func(stmts string) {
		t.Helper()
		if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v %s", err, out)
		}
	}
	base := int64(1788253200000)
	sql("create table cursorDiskKV (key text primary key, value text);\n" +
		cursorComposer("comp-retry", "fix the retry loop", base, base+2000) +
		cursorBubble("comp-retry", "b1", 1, "fix the retry loop", base+1000) +
		cursorBubble("comp-retry", "b2", 2, "looking at the retry loop", base+2000) +
		cursorComposer("comp-other", "other chat", base, base+500) +
		cursorBubble("comp-other", "o1", 1, "inspect the cursor db", base+500))
	dir := filepath.Join(tmp, "index.db")
	indexPass(t, dir)

	sql(cursorBubble("comp-retry", "b3", 1, "now run the tests", base+60000) +
		cursorBubble("comp-retry", "b4", 2, "the tests fail in retry", base+61000) +
		cursorComposer("comp-retry", "fix the retry loop", base, base+61000))
	indexPass(t, dir)
	matchesRebuild(t, dir, "cursor", "comp-retry")

	sql(cursorComposer("comp-retry", "Retry loop never stops", base, base+300000))
	indexPass(t, dir)
	matchesRebuild(t, dir, "cursor", "comp-retry")
	matchesRebuild(t, dir, "cursor", "comp-other")
}
