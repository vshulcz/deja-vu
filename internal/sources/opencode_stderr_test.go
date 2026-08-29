package sources

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// What sqlite3 says when it refuses, not merely that it did. A person whose
// store deja would not read was told "its format may have changed; please
// report it", and the report they could send said `exit status 1` — which names
// neither a renamed column nor a locked database nor a file that is not a
// database at all (#1642).
func TestAStoreRefusalCarriesWhatSqliteSaid(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	// A real database missing the column the first probe reads, which is the
	// shape a schema change arrives in.
	mk := exec.Command("sqlite3", db, "create table session(id text, directory text);")
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("could not build the fixture: %v: %s", err, out)
	}

	_, err := ParseOpencodeNewest(db)
	if err == nil {
		t.Fatal("a store with no time_created column parsed without complaint")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no such column") {
		t.Errorf("the refusal does not say what sqlite3 said: %q", msg)
	}
	if msg == "exit status 1" {
		t.Errorf("the reason is still the exit code alone: %q", msg)
	}
}

// The quoted complaint is one line and bounded: sqlite3 echoes the statement it
// failed on, and that statement is a screenful.
func TestTheQuotedComplaintIsBounded(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("Error: in prepare, no such column: time_created " +
		strings.Repeat("select s.id, ", 60) + "\nsecond line")
	got := withStderr(errors.New("exit status 1"), &buf).Error()
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("the reason spans lines: %q", got)
	}
	if len(got) > sqliteStderrMax+40 {
		t.Errorf("the reason is %d bytes, the bound is %d", len(got), sqliteStderrMax)
	}
	if !strings.HasPrefix(got, "Error: in prepare, no such column: time_created") {
		t.Errorf("the head, which names the cause, was cut: %q", got)
	}
}

// Nothing on stderr means nothing to add: the caller keeps the error it had.
func TestASilentFailureKeepsItsOwnError(t *testing.T) {
	var empty bytes.Buffer
	want := errors.New("exit status 1")
	if got := withStderr(want, &empty); got != want {
		t.Errorf("a silent failure was rewritten: %v", got)
	}
	_ = os.Stdout
}
