package index

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// goose hands back every turn of a session it touched, so each pass re-offers
// messages the index already holds — and the incremental writer appends what it
// is given without asking whether it has seen it. What keeps a re-offered turn
// from becoming a second record is the whole-store rewrite
// (`wholeStoresThisPass`/`rereadsWholeSessions`), which is a property of the
// pass rather than of the writer, and nothing held it: the tests beside this
// one pin the ingest counts and the survival of earlier turns, neither of which
// moves when a message is stored twice.
//
// A duplicate is not a cosmetic matter here: the same sentence comes back as
// two hits in one session, and the count beside a search result is what a
// reader uses to judge whether a session is the one.
func TestAReofferedTurnDoesNotBecomeASecondRecord(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	db := filepath.Join(tmp, "goose.db")
	t.Setenv("DEJA_GOOSE_DB", db)

	const first = "the pgbouncer pool timed out during the migration"
	seedGooseTurn(t, db, "g1", "user", first, 1767322800)
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	records := func() int {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		if err := eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
			if strings.Contains(r.Text, "pgbouncer pool timed out") {
				n++
			}
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := records(); got != 1 {
		t.Fatalf("the first pass stored the turn %d times, so this measures nothing", got)
	}
	// Two more passes, each of which hands the first turn back with the new one.
	for i, ts := range []int64{1767326400, 1767330000} {
		seedGooseTurn(t, db, "g1", "assistant", "a short answer", ts)
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if got := records(); got != 1 {
			t.Errorf("after pass %d the store holds the same turn %d times", i+2, got)
		}
	}
	// And the reader sees one of it: a session whose every line is doubled
	// reads as a conversation that repeated itself.
	s, ok, err := FindByIdentity(dir, "goose", "g1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the session is gone from the index")
	}
	seen := 0
	for _, msg := range s.Messages {
		if strings.Contains(msg.Text, "pgbouncer pool timed out") {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("the session carries the same turn %d times", seen)
	}
	res, err := SearchDetailed(dir, search.Options{Query: "pgbouncer pool", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 {
		t.Fatalf("search returned %d sessions, want the one", len(res.Sessions))
	}
	if n := len(res.Sessions[0].Messages); n != 1 {
		t.Errorf("search shows the turn %d times in one session", n)
	}
}
