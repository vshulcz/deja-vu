package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The value behind that flag is an object of file diffs on current opencode:
// 247 MB of query output against 132 MB for the same 78,690 rows on a 3.4 GB
// store, for a boolean an object can never satisfy (#3556). Nothing in the
// projection may ask for it again.
func TestTheCompactionFlagIsReadByTypeNotByValue(t *testing.T) {
	b, err := os.ReadFile("opencode.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `'summary',json_extract(`) {
		t.Error("the projection carries the summary value again — json_type answers the same question for nothing")
	}
}

// The compaction flag is read by type rather than by value, because current
// opencode writes an object of file diffs under the same key and it runs to
// megabytes (#3556). Every shape that used to mark a message as opencode's own
// digest still does, and the object still does not.
func TestOpencodeReadsTheCompactionFlagWhateverShapeItIsIn(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	for _, tc := range []struct {
		name, summary string
		wantSummary   bool
	}{
		{name: "the boolean opencode writes", summary: `true`, wantSummary: true},
		{name: "a store that writes one", summary: `1`, wantSummary: true},
		{name: "a store that writes the word", summary: `"true"`, wantSummary: true},
		{name: "false is not a digest", summary: `false`},
		{name: "zero is not a digest", summary: `0`},
		{name: "a diff object is not a digest", summary: `{"diffs":{"/w/app.go":"@@ -1 +1 @@"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			db := filepath.Join(tmp, "opencode.db")
			writeStore(t, db, `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant","summary":`+tc.summary+`}');
insert into part values('p1','m1','{"type":"text","text":"what the pass carried over","time":{"start":"2026-01-02T03:00:00Z"}}');`)
			ss, err := ParseOpencodeDB(db)
			if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
				t.Fatalf("len=%d err=%v", len(ss), err)
			}
			got := ss[0].Messages[0].Role
			if (got == RoleSummary) != tc.wantSummary {
				t.Fatalf("role = %q, want summary = %v", got, tc.wantSummary)
			}
			if !strings.Contains(ss[0].Messages[0].Text, "carried over") {
				t.Fatalf("text = %q", ss[0].Messages[0].Text)
			}
		})
	}
}
