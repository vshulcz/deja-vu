package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func walDB(t *testing.T, dir, name string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	db := filepath.Join(dir, name)
	mk := exec.Command("sqlite3", db,
		"pragma journal_mode=wal; create table t(x); insert into t values(42);")
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	// The resting state: SQLite removes both sidecars when the last connection
	// closes cleanly, and some builds leave empty ones behind.
	_ = os.Remove(db + "-wal")
	_ = os.Remove(db + "-shm")
	return db
}

// A WAL store with no sidecar is the state every one of these databases sits in
// when its agent is not running, and Apple's bundled sqlite3 cannot open it
// read-only. deja shells out to whatever sqlite3 is on PATH, which on a stock
// Mac is that one (#1642).
func TestAWalStoreAtRestIsReadThroughImmutable(t *testing.T) {
	db := walDB(t, t.TempDir(), "store.db")
	target := sqliteTarget(db)
	if !strings.Contains(target, "immutable=1") {
		t.Fatalf("a WAL store with no sidecar was handed over as %q", target)
	}
	out, err := exec.Command("sqlite3", "-readonly", target, "select x from t").CombinedOutput()
	if err != nil {
		t.Fatalf("the store did not read: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "42" {
		t.Errorf("read %q, want the row back", out)
	}
}

// With a sidecar there may be frames that have not reached the file, and
// immutable would hide them. The plain path is what goes.
func TestAStoreWithAWalSidecarIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	db := walDB(t, dir, "live.db")
	if err := os.WriteFile(db+"-wal", []byte("frames"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sqliteTarget(db); got != db {
		t.Errorf("a live store was declared immutable: %q", got)
	}
}

// A rollback-journal database opens read-only without a sidecar, so nothing is
// claimed about it.
func TestARollbackJournalStoreIsLeftAlone(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	db := filepath.Join(t.TempDir(), "plain.db")
	mk := exec.Command("sqlite3", db, "create table t(x); insert into t values(1);")
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	if got := sqliteTarget(db); got != db {
		t.Errorf("a rollback-journal store was declared immutable: %q", got)
	}
}

// Neither a missing file nor something that is not a database is claimed to be
// anything: the caller's own error is the better message.
func TestAMissingOrForeignFileIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.db")
	if got := sqliteTarget(missing); got != missing {
		t.Errorf("a missing file was rewritten: %q", got)
	}
	foreign := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(foreign, []byte("this is not a database at all, truly"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sqliteTarget(foreign); got != foreign {
		t.Errorf("a text file was rewritten: %q", got)
	}
}

// A store under a directory with a space or a question mark still reaches
// sqlite3 whole: the URI form escapes per segment.
func TestAPathWithAwkwardCharactersSurvivesTheURI(t *testing.T) {
	// A name Windows accepts: `?` is not a filename character there, so the
	// old fixture could not be created at all and the test failed before it
	// measured anything. `#` needs the same per-segment escaping a `?` does,
	// and the `?` case is kept where it can exist (#2808).
	name := "My Store#v2"
	if runtime.GOOS != "windows" {
		name += "?raw"
	}
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db := walDB(t, dir, "store.db")
	target := sqliteTarget(db)
	if strings.Contains(target, " ") || strings.Contains(target, "#v2") {
		t.Fatalf("the path went out unescaped: %q", target)
	}
	out, err := exec.Command("sqlite3", "-readonly", target, "select x from t").CombinedOutput()
	if err != nil {
		t.Fatalf("the store did not read: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "42" {
		t.Errorf("read %q, want the row back", out)
	}
}
