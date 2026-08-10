package sources

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A busy sqlite store (another connection holding a write lock) used to make
// sqlite3 exit "database is locked" with empty stdout, so the whole harness's
// history silently vanished from the index run — during active use, when the
// freshest sessions exist. The parser now passes -readonly and .timeout so it
// waits out the lock instead of dropping everything.
func TestOpencodeReadsThroughAWriteLock(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	script := `pragma journal_mode=delete;
create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('s1','/w','2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','s1',1767409200000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"LOCKEDMARK content"}');`
	if out, err := exec.Command("sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}

	// Hold an exclusive write lock via a background sqlite3 fed through stdin,
	// released when we close stdin — after the parser has run.
	holder := exec.Command("sqlite3", db)
	pipe, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := pipe.Write([]byte("begin exclusive;\ninsert into part values('p2','m1','x');\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // let the exclusive lock take hold

	done := make(chan struct{})
	go func() {
		time.Sleep(400 * time.Millisecond)
		_, _ = pipe.Write([]byte("rollback;\n"))
		_ = pipe.Close()
		close(done)
	}()

	ss, err := ParseOpencodeDB(db)
	if err != nil {
		t.Fatalf("parse failed under a write lock: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("the locked store's history was dropped: got %d sessions", len(ss))
	}
	<-done
	_ = holder.Wait()
}
