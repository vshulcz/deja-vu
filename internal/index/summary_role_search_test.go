package index

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// End to end for the role #3384 added, through the path `deja search` takes:
// retrieval, then ranking. A harness's compaction summary is in the index and
// answers when it is asked for, and it does not answer a question that did not.
//
// Both halves matter. Dropping the summary loses the only record of the turns
// that were compacted away; leaving it as speech is what made 19.6% of the
// indexed text of those sessions a paraphrase nobody said, competing with the
// conversation it paraphrases.
func TestACompactionSummaryAnswersOnlyWhenAskedFor(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := hermeticIndexEnv(t)
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w/app','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user","agent":"build"}');
insert into part values('p1','m1','{"type":"text","text":"the invoice exporter drops every third retry","time":{"start":"2026-01-02T03:00:00Z"}}');
insert into message values('m2','s1',1767409260000,'{"role":"assistant","agent":"build"}');
insert into part values('p2','m2','{"type":"text","text":"capped the retries at three","time":{"start":"2026-01-02T03:01:00Z"}}');
insert into message values('m3','s1',1767409320000,'{"role":"assistant","summary":true,"mode":"compaction","agent":"compaction"}');
insert into part values('p3','m3','{"type":"text","text":"## Goal quazzlebolt the widget pipeline until the drops stop","time":{"start":"2026-01-02T03:02:00Z"}}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	hits := func(o search.Options) []search.Hit {
		t.Helper()
		ss, err := Search(dir, o)
		if err != nil {
			t.Fatal(err)
		}
		out, err := search.Run(ss, o)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	// A word that exists only inside the summary.
	if got := hits(search.Options{Query: "quazzlebolt widget pipeline", All: true}); len(got) != 0 {
		t.Errorf("an ordinary question was answered from the summary: %d hit(s), %q",
			len(got), got[0].Snippets)
	}
	// Asked for by role, it is there: the compacted half is not lost.
	asked := hits(search.Options{Query: "quazzlebolt widget pipeline", Role: "summary", All: true})
	if len(asked) != 1 || len(asked[0].Snippets) == 0 ||
		!strings.Contains(asked[0].Snippets[0], "quazzlebolt") {
		t.Errorf("--role summary does not answer from the summary: %#v", asked)
	}
	// And the conversation is still ordinary speech.
	if got := hits(search.Options{Query: "invoice exporter retry", All: true}); len(got) != 1 {
		t.Fatalf("the question itself is no longer findable: %d hit(s)", len(got))
	}
}
