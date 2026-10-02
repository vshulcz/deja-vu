package sources

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cursor writes a composer's `lastUpdatedAt` when it feels like it, and the
// bubbles keep arriving regardless. The pass filtered both sides and read no
// bubble whose composer had not also moved, so a turn written after the
// composer's own stamp was skipped — and the next pass, carrying a later
// watermark, excluded the bubble on its own stamp too (#2159).
func TestACursorTurnSurvivesAComposerThatStoppedAdvancing(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "state.vscdb")
	schema := `create table cursorDiskKV (key text primary key, value text);
insert into cursorDiskKV values
 ('composerData:comp-1', json('{"composerId":"comp-1","name":"Fix the pager","createdAt":1752600000000,"lastUpdatedAt":1752600100000}')),
 ('bubbleId:comp-1:b1', json('{"type":1,"text":"the first question","timestamp":1752600001000,"workspaceProjectDir":"/Users/me/work/my-app"}')),
 ('bubbleId:comp-1:b2', json('{"type":2,"text":"the later answer","timestamp":1752600900000}'));`
	if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	texts := func(ms int64) []string {
		t.Helper()
		ss, err := parseCursorDB(db, time.UnixMilli(ms).UTC())
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range ss {
			for _, m := range s.Messages {
				out = append(out, m.Text)
			}
		}
		return out
	}
	// The premise: a watermark before everything brings both turns.
	if got := texts(1752600000000); len(got) != 2 {
		t.Fatalf("a watermark before the store returned %v, want both turns", got)
	}
	// The watermark a pass would carry after the first turn: past the
	// composer's own stamp, before the later bubble.
	// The composer comes back whole, the turn already indexed with the new
	// one: the index replaces the session with what a pass returns (#4451).
	got := texts(1752600200000)
	if strings.Join(got, " ") != "the first question the later answer" {
		t.Errorf("returned %v, want the composer whole with the turn written after it stopped advancing", got)
	}
	ss, err := parseCursorDB(db, time.UnixMilli(1752600200000).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Errorf("the pass returned %d session(s), want one", len(ss))
	}
	// And it is still a filter: past every bubble, the pass is empty.
	if got := texts(1752601000000); len(got) != 0 {
		t.Errorf("a watermark past every bubble returned %v, want nothing", got)
	}
}

// Past cursorComposerListMax moved composers the pass asks for every composer
// row instead of naming them, and keeps the ones a bubble moved or whose own
// stamp moved: a rename adds no bubble (#4450). The rest are dropped.
func TestACursorPassPastTheKeyListKeepsOnlyMovedComposers(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "state.vscdb")
	var b strings.Builder
	b.WriteString("create table cursorDiskKV (key text primary key, value text);\nbegin;\n")
	moved := cursorComposerListMax + 1
	for i := range moved {
		fmt.Fprintf(&b, "insert into cursorDiskKV values ('composerData:c%d', json('{\"composerId\":\"c%d\",\"name\":\"task %d\",\"createdAt\":1752600000000,\"lastUpdatedAt\":1752600100000}'));\n", i, i, i)
		fmt.Fprintf(&b, "insert into cursorDiskKV values ('bubbleId:c%d:b1', json('{\"type\":1,\"text\":\"turn %d\",\"timestamp\":1752600900000}'));\n", i, i)
	}
	b.WriteString(`insert into cursorDiskKV values ('composerData:renamed', json('{"composerId":"renamed","name":"new name","createdAt":1752600000000,"lastUpdatedAt":1752600950000}'));
insert into cursorDiskKV values ('bubbleId:renamed:b1', json('{"type":1,"text":"an old turn","timestamp":1752600001000}'));
insert into cursorDiskKV values ('composerData:still', json('{"composerId":"still","name":"untouched","createdAt":1752600000000,"lastUpdatedAt":1752600100000}'));
insert into cursorDiskKV values ('bubbleId:still:b1', json('{"type":1,"text":"nothing new","timestamp":1752600001000}'));
commit;`)
	if out, err := exec.Command("sqlite3", db, b.String()).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	ss, err := parseCursorDB(db, time.UnixMilli(1752600200000).UTC())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, s := range ss {
		ids[s.ID] = true
	}
	if len(ss) != moved+1 || !ids["renamed"] || ids["still"] {
		t.Errorf("pass returned %d sessions (renamed %v, still %v), want %d moved and the renamed one", len(ss), ids["renamed"], ids["still"], moved)
	}
}
