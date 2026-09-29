package index

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cursorComposerSQL(id, needle string, at time.Time) string {
	ms := at.UnixMilli()
	return fmt.Sprintf(`insert into cursorDiskKV values
 ('composerData:%[1]s', json('{"composerId":"%[1]s","name":"chat %[1]s","createdAt":%[3]d,"lastUpdatedAt":%[3]d,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1}]}')),
 ('bubbleId:%[1]s:b1', json('{"type":1,"text":"%[2]s question","timestamp":%[3]d,"workspaceProjectDir":"/Users/me/work/app"}'));`, id, needle, ms)
}

// Cursor keeps every chat in one state.vscdb. When a chat goes from it —
// deleted in Cursor, or the whole bloated database moved aside and started
// over — the incremental pass keeps what deja already held, the way it keeps a
// deleted transcript. A full rebuild read the database and wrote only what is
// in it now, so those chats went at the next content-version bump (#3529 is
// the same loss for a transcript file; this is it for a database store).
func TestAFullRebuildKeepsAChatDeletedFromTheCursorDatabase(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	_, _, _, _ = seedTwoTranscripts(t) // environment only; claude is not the harness here
	tmp := t.TempDir()
	root := filepath.Join(tmp, "cursor-user")
	db := filepath.Join(root, "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CURSOR_ROOT", root)
	sql := func(stmt string) {
		t.Helper()
		if out, err := exec.Command("sqlite3", db, stmt).CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v: %s", err, out)
		}
	}
	sql(`create table cursorDiskKV (key text primary key, value text); create table ItemTable (key text primary key, value text);`)
	now := time.Now()
	sql(cursorComposerSQL("comp-a", "florpwidget", now.Add(-time.Hour)))
	sql(cursorComposerSQL("comp-b", "snarfgadget", now.Add(-time.Hour)))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "cursor", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := sessionsFor(t, dir, "florpwidget"); len(got) != 1 {
		t.Fatalf("the seeded chat is not searchable: %v", got)
	}

	// Cursor deletes one chat and writes a new one.
	sql(`delete from cursorDiskKV where key like '%comp-a%';`)
	time.Sleep(10 * time.Millisecond)
	sql(cursorComposerSQL("comp-c", "glimbothing", time.Now()))
	if err := Ensure(dir, "cursor", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := sessionsFor(t, dir, "florpwidget"); len(got) != 1 {
		t.Fatalf("premise: the incremental pass did not keep the deleted chat: %v", got)
	}

	for pass := 1; pass <= 2; pass++ {
		var log bytes.Buffer
		if err := Ensure(dir, "cursor", true, &log); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(log.String(), "1 session no longer in its store — still searchable") {
			t.Errorf("rebuild %d carried the chat and said nothing:\n%s", pass, log.String())
		}
		if got := sessionsFor(t, dir, "florpwidget"); len(got) != 1 || got[0] != "comp-a" {
			t.Errorf("rebuild %d dropped the chat Cursor deleted: %v", pass, got)
		}
		for _, q := range []string{"snarfgadget", "glimbothing"} {
			if got := sessionsFor(t, dir, q); len(got) != 1 {
				t.Errorf("rebuild %d: %q, still in the database, came back %v", pass, q, got)
			}
		}
	}
}
