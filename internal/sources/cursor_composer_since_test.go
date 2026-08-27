package sources

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
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
	got := texts(1752600200000)
	if strings.Join(got, " ") != "the later answer" {
		t.Errorf("returned %v, want the turn written after the composer stopped advancing", got)
	}
	// Nothing already indexed comes back with it: the bubbles keep their own
	// filter, so the session carries exactly the one new turn.
	ss, err := parseCursorDB(db, time.UnixMilli(1752600200000).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Errorf("the pass returned %d session(s) carrying %d message(s), want one of each", len(ss), messageCount(ss))
	}
	// And it is still a filter: past every bubble, the pass is empty.
	if got := texts(1752601000000); len(got) != 0 {
		t.Errorf("a watermark past every bubble returned %v, want nothing", got)
	}
}

func messageCount(ss []model.Session) int {
	n := 0
	for _, s := range ss {
		n += len(s.Messages)
	}
	return n
}
