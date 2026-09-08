package index

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A sqlite store that cannot be read while a rebuild runs — locked by the
// agent using it, or unreadable for the moment — must not be recorded as
// read: the rebuild says so, doctor counts it, and the next pass reads it
// again once it opens. It used to commit an empty index for the store in
// silence and call it up to date until the next --rebuild.
func TestRebuildRetriesAStoreItCouldNotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file the owner cannot read")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "index")
	db := os.Getenv("DEJA_OPENCODE_DB")
	runStoreSQL(t, db, `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"why does the lockedneedle pager stall"}');`)
	if err := os.Chmod(db, 0); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Ensure(dir, "", true, &out); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if h := IngestHealth(dir)["opencode"]; h.FailedFiles != 1 {
		t.Errorf("the unreadable store was not counted: %+v\n%s", h, out.String())
	}
	if !strings.Contains(out.String(), "could not be read") {
		t.Errorf("the rebuild said nothing about the store it could not read:\n%s", out.String())
	}
	// The file itself is untouched — same size, same mtime — only readable
	// again, which is what the end of a lock looks like.
	if err := os.Chmod(db, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	hits, err := Search(dir, query.Options{Query: "lockedneedle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("the store was not read again once it opened: %d hits\n%s", len(hits), out.String())
	}
}
