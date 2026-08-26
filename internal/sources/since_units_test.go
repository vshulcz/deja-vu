package sources

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// parseTimeAny takes a stamp in either unit, so a since clause may not assume
// one. opencode's compared everything against milliseconds, which puts every
// seconds-stamped row below the watermark for ever: the store is read once and
// then never updated again, silently (#2064).
func TestOpencodeSinceTakesASecondsStampedRow(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	// The turn is newer than the watermark. Written in seconds, which is what
	// unixGuess's lower branch exists for.
	newer := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	watermark := newer.Add(-time.Hour)
	stmts := fmt.Sprintf(`
create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, data text, time_created integer);
create table part (id text primary key, message_id text, data text);
insert into session values ('s1','/tmp/app',%[1]d,%[1]d);
insert into message values ('m1','s1','{"role":"user"}',%[1]d);
insert into part values ('p1','m1',json_object('type','text','text','the autovacuum thread stalls'));
`, newer.Unix())
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	// The premise: a full read does take this row, so a since clause that
	// drops it is losing something the reader understands.
	whole, err := ParseOpencodeDBSince(db, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 1 {
		t.Fatalf("the fixture is not readable at all, so this measures nothing: %d sessions", len(whole))
	}

	ss, err := ParseOpencodeDBSince(db, watermark)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("a turn stamped %s in seconds did not come back for a watermark of %s: %d sessions",
			newer.Format(time.RFC3339), watermark.Format(time.RFC3339), len(ss))
	}
}

// The other half: a millisecond store must still be filtered, or the watermark
// buys nothing on the multi-GB stores it exists for.
func TestOpencodeSinceStillFiltersAMillisecondStore(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	older := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	watermark := older.Add(time.Hour)
	stmts := fmt.Sprintf(`
create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, data text, time_created integer);
create table part (id text primary key, message_id text, data text);
insert into session values ('s1','/tmp/app',%[1]d,%[1]d);
insert into message values ('m1','s1','{"role":"user"}',%[1]d);
insert into part values ('p1','m1',json_object('type','text','text','the autovacuum thread stalls'));
`, older.UnixMilli())
	if out, err := exec.Command("sqlite3", db, stmts).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	if whole, err := ParseOpencodeDBSince(db, time.Time{}); err != nil || len(whole) != 1 {
		t.Fatalf("the fixture is not readable at all: %d sessions err=%v", len(whole), err)
	}
	ss, err := ParseOpencodeDBSince(db, watermark)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Errorf("a turn older than the watermark came back anyway: %d sessions", len(ss))
	}
}
