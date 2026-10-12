package sources

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// opencodeV2Many writes a 2.x store of n sessions with the index opencode
// creates on session_message, plus ids holding LIKE wildcards and a quote.
func opencodeV2Many(t *testing.T, n int) (string, []string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	var b strings.Builder
	b.WriteString(`create table session_v2(id text primary key, project_id text, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table session_message(id text primary key, session_id text not null, type text not null, seq integer, time_created integer, time_updated integer, data text not null);
create index session_message_session_type_seq_idx on session_message(session_id,type,seq);
begin;
`)
	var ids []string
	for i := 0; i < n; i++ {
		ids = append(ids, fmt.Sprintf("ses_%04d", i))
	}
	ids = append(ids, "ses_a_b", "ses_axb", "ses_a%c", "ses_o'q")
	for i, id := range ids {
		q := strings.ReplaceAll(id, "'", "''")
		at := int64(1767409200000) + int64(i)*60000
		fmt.Fprintf(&b, "insert into session_v2 values('%s','p1',null,'/w','session %d',%d,%d);\n", q, i, at, at+3000)
		fmt.Fprintf(&b, "insert into session_message values('%s-1','%s','user',1,%d,%d,'{\"time\":{\"created\":%d},\"text\":\"why does the pool %d leak\"}');\n", q, q, at, at, at, i)
		fmt.Fprintf(&b, "insert into session_message values('%s-2','%s','assistant',2,%d,%d,'{\"time\":{\"created\":%d},\"content\":[{\"type\":\"text\",\"text\":\"the pool %d is not closed\"},"+
			"{\"type\":\"tool\",\"id\":\"c1\",\"name\":\"bash\",\"state\":{\"status\":\"completed\",\"input\":{\"command\":\"go test ./pool/%d\"},\"content\":[{\"type\":\"text\",\"text\":\"--- FAIL: TestPool\"}],\"metadata\":{\"exit\":1}},\"time\":{\"start\":%d}}]}');\n",
			q, q, at+1000, at+1000, at+1000, i, i, at+2000)
		fmt.Fprintf(&b, "insert into session_message values('%s-3','%s','compaction',3,%d,%d,'{\"time\":{\"created\":%d},\"summary\":\"pool %d summary\"}');\n", q, q, at+3000, at+3000, at+3000, i)
	}
	b.WriteString("commit;\n")
	if out, err := exec.Command("sqlite3", db, b.String()).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	return db, ids
}

// A new session's lineage is read by its exact id. The read must return that
// session, and only it, the way the whole-store read sees it, wildcards and
// quotes in the id included (#4951).
func TestLoadOpencodeSessionReadsOneSessionAsTheStoreReadDoes(t *testing.T) {
	db, ids := opencodeV2Many(t, 20)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OPENCODE_DB", db)
	all, err := ParseOpencodeDB(db)
	if err != nil || len(all) != len(ids) {
		t.Fatalf("whole store: len=%d err=%v", len(all), err)
	}
	for _, want := range all {
		got := LoadOpencodeSession(want.ID)
		if len(got) != 1 {
			t.Fatalf("%q: got %d sessions", want.ID, len(got))
		}
		if !reflect.DeepEqual(got[0], want) {
			t.Errorf("%q read alone differs from the store read:\n got %+v\nwant %+v", want.ID, got[0], want)
		}
	}
	if got := LoadOpencodeSession("ses_a"); len(got) != 0 {
		t.Errorf("an id is not a prefix: ses_a read %d sessions", len(got))
	}
}

// The restriction has to reach each session_message branch before its JSON is
// expanded. An s.id in (...) outside the CTE did not, and SQLite scanned and
// decoded every message of the store to return a few sessions (#4951).
func TestOpencodeV2SessionReadUsesTheMessageIndex(t *testing.T) {
	db, _ := opencodeV2Many(t, 3)
	only := "in ('ses_0001','ses_0002')"
	q := opencodeV2Query("session_v2", " and s.id "+only, 0, only)
	out, err := exec.Command("sqlite3", "-readonly", db, "explain query plan "+q).CombinedOutput()
	if err != nil {
		t.Fatalf("plan: %v %s", err, out)
	}
	plan := string(out)
	if strings.Contains(plan, "SCAN sm") {
		t.Errorf("a branch scans session_message:\n%s", plan)
	}
	if n := strings.Count(plan, "SEARCH sm USING INDEX session_message_session_type_seq_idx"); n != 3 {
		t.Errorf("%d of 3 branches search the index:\n%s", n, plan)
	}
}

// The since read names the changed sessions first and reads only them, each
// whole, the same as the whole-store read; with more of them than it names
// one by one it reads through the subquery and gets the same answer.
func TestOpencodeV2SinceReadsTheChangedSessionsWhole(t *testing.T) {
	db, ids := opencodeV2Many(t, 30)
	all, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]int{}
	for i, s := range all {
		byID[s.ID] = i
	}
	// Sessions 25 and later started at least 25 minutes after the first.
	since := time.UnixMilli(1767409200000 + 25*60000).Add(opencodeSinceSlack)
	check := func(label string) {
		t.Helper()
		got, err := ParseOpencodeDBSince(db, since)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(ids)-25 {
			t.Fatalf("%s: read %d sessions, want %d", label, len(got), len(ids)-25)
		}
		for _, s := range got {
			i, ok := byID[s.ID]
			if !ok || !reflect.DeepEqual(s, all[i]) {
				t.Errorf("%s: %q differs from the whole-store read", label, s.ID)
			}
		}
	}
	check("named")
	defer func(n int) { opencodeSetCap = n }(opencodeSetCap)
	opencodeSetCap = 3
	check("past the cap")
}
