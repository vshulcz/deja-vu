package sources

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func seedCursorStamped(t *testing.T, db string, stamp int64) {
	t.Helper()
	schema := fmt.Sprintf(`create table cursorDiskKV (key text primary key, value text);
insert into cursorDiskKV values
 ('composerData:comp-1', json('{"composerId":"comp-1","name":"pool sizing","createdAt":%[1]d,"lastUpdatedAt":%[1]d,"fullConversationHeadersOnly":[{"bubbleId":"b1","type":1}]}')),
 ('bubbleId:comp-1:b1', json('{"type":1,"text":"the pool was exhausted","timestamp":%[1]d,"workspaceProjectDir":"/tmp/app"}'));`, stamp)
	if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
}

// Every source reads a numeric stamp through unixGuess, which takes either
// unit. Cursor had its own millisecond-only reader, so a seconds-stamped store
// was dated to three weeks after the epoch and sat behind everything deja knows
// (#2094).
func TestACursorStoreStampedInSecondsIsNotDatedTo1970(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	when := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	for _, unit := range []struct {
		name  string
		stamp int64
	}{
		{"milliseconds", when.UnixMilli()},
		{"seconds", when.Unix()},
	} {
		db := filepath.Join(t.TempDir(), "state.vscdb")
		seedCursorStamped(t, db, unit.stamp)
		ss, err := ParseCursorDB(db)
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || len(ss[0].Messages) != 1 {
			t.Fatalf("%s: the fixture did not come back whole, so this measures nothing: %#v", unit.name, ss)
		}
		for what, got := range map[string]time.Time{
			"started": ss[0].Started, "updated": ss[0].Updated,
			"the message": ss[0].Messages[0].Time,
		} {
			if !got.UTC().Equal(when) {
				t.Errorf("%s: %s is %s, want %s", unit.name, what, got.UTC().Format(time.RFC3339), when.Format(time.RFC3339))
			}
		}
	}
}

// And the clause that decides what a changed store hands back reads the same
// units the reader does, or the two disagree the way opencode's did (#2086).
func TestTheCursorSinceClauseTakesEitherUnit(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	newer := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	watermark := newer.Add(-time.Hour)
	for _, unit := range []struct {
		name  string
		stamp int64
	}{
		{"milliseconds", newer.UnixMilli()},
		{"seconds", newer.Unix()},
	} {
		db := filepath.Join(t.TempDir(), "state.vscdb")
		seedCursorStamped(t, db, unit.stamp)
		if whole, err := ParseCursorDBSince(db, time.Time{}); err != nil || len(whole) != 1 {
			t.Fatalf("%s: the fixture is not readable at all: %d sessions err=%v", unit.name, len(whole), err)
		}
		ss, err := ParseCursorDBSince(db, watermark)
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 {
			t.Errorf("%s: a turn newer than the watermark did not come back: %d sessions", unit.name, len(ss))
		}
	}
}

// The other half of that: a millisecond store must still be filtered, or the
// watermark buys nothing on the store it exists for.
func TestTheCursorSinceClauseStillFilters(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	older := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	db := filepath.Join(t.TempDir(), "state.vscdb")
	seedCursorStamped(t, db, older.UnixMilli())
	ss, err := ParseCursorDBSince(db, older.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Errorf("a turn older than the watermark came back anyway: %d sessions", len(ss))
	}
}
