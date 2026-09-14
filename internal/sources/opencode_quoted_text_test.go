package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A part whose text is full of quotes and newlines — an agent pasting JSON, or
// a test run printing diffs — is the ordinary case on a real store, and it used
// to be the case that never finished. The sqlite3 shell's own -json formatter
// is quadratic in the number of characters it escapes: 4 MB of text with a
// quote every sixteenth byte takes it 412 seconds against 0.04 with
// json_object, and an index run over a store like that never returned (#3553).
func TestOpencodeReadsAQuoteHeavyPartWithoutStalling(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	// The bulk is built inside SQLite so the fixture stays a few hundred bytes:
	// 4 MB of text carrying ~250,000 quotes, with a needle in front holding
	// every character JSON has to escape.
	needle := `he said "no" \ then` + "\n\ttab and ⌘"
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"assistant"}');
insert into part select 'p1','m1',json_object(
  'type','text',
  'text',` + sqlQuote(needle) + `||replace(hex(randomblob(2000000)),'A','"'),
  'time',json_object('start','2026-01-02T03:00:00Z'));`
	sql := filepath.Join(tmp, "setup.sql")
	if err := os.WriteFile(sql, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	setup := exec.Command("sqlite3", db)
	f, err := os.Open(sql)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	setup.Stdin = f
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}

	type result struct {
		ss  []model.Session
		err error
	}
	done := make(chan result, 1)
	go func() {
		ss, err := ParseOpencodeDB(db)
		done <- result{ss, err}
	}()
	var got result
	select {
	case got = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("reading one 4 MB part took over 30 seconds — the escaping path is quadratic again")
	}
	if got.err != nil || len(got.ss) != 1 {
		t.Fatalf("len=%d err=%v", len(got.ss), got.err)
	}
	var text string
	for _, m := range got.ss[0].Messages {
		if m.Role == "assistant" {
			text = m.Text
		}
	}
	if !strings.HasPrefix(text, needle) {
		t.Fatalf("text = %.80q — quotes, backslashes, tabs, newlines and non-ASCII all have to survive the framing", text)
	}
}

// sqlQuote renders a Go string as a SQL literal for the fixture above.
func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
