package sources

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The since clause filters on the message row's column and the reader dated the
// message from the blob, so a row stamped in the column alone came back with no
// time at all — the year zero, published as it is by `show --json` and treated
// as one turn by an index that keys duplicates on (role, time, text) (#2086).
func TestAnOpencodeMessageIsDatedByWhateverCarriesTheStamp(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	started := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	inColumn := time.Date(2026, 1, 1, 12, 30, 0, 0, time.UTC)
	inBlob := time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC)
	sql := `create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, data text, time_created integer);
create table part (id text primary key, message_id text, data text);
insert into session values ('s1','/tmp/app',` + ms(started) + `,` + ms(inBlob) + `);
insert into message values ('m1','s1','{"role":"user"}',` + ms(inColumn) + `);
insert into part values ('p1','m1',json_object('type','text','text','the column carries this one'));
insert into message values ('m2','s1','{"role":"user","time":{"created":` + ms(inBlob) + `}}',` + ms(inBlob) + `);
insert into part values ('p2','m2',json_object('type','text','text','the blob carries this one'));
insert into message values ('m3','s1','{"role":"user"}',0);
insert into part values ('p3','m3',json_object('type','text','text','nothing carries this one'));`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	ss, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 3 {
		t.Fatalf("the fixture did not come back whole, so this measures nothing: %#v", ss)
	}
	want := map[string]time.Time{
		"the column carries this one": inColumn,
		"the blob carries this one":   inBlob,
		// Neither has one, so it belongs to its conversation — the fallback
		// cursor has taken since bubbles without stamps turned up there.
		"nothing carries this one": started,
	}
	for _, m := range ss[0].Messages {
		if got := m.Time.UTC(); !got.Equal(want[m.Text]) {
			t.Errorf("%q is dated %s, want %s", m.Text, got, want[m.Text].UTC())
		}
	}
}

// ms is what opencode writes: epoch milliseconds.
func ms(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }
