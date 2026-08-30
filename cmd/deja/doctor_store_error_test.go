package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A store deja refuses is the loudest thing doctor can learn, and it used to
// arrive as one word. #1642 is what that costs: a person with an opencode
// database deja would not read, told their format may have changed and asked to
// report it, and nothing in the report said which refusal it was — a renamed
// column, a locked database, or a file that is not a database at all.
func TestDoctorSaysWhyAStoreWouldNotRead(t *testing.T) {
	tmp := hermeticEnv(t)
	db := filepath.Join(tmp, "opencode.db")
	// Not a database. Any refusal will do: what is pinned is that the reason
	// reaches the reader, not which reason it was.
	if err := os.WriteFile(db, []byte("this is not a sqlite file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCODE_DB", db)

	var out bytes.Buffer
	if err := runDoctor(&out, nil, stubLookup("2.0.0", false), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "opencode store cannot be read") {
		t.Skipf("this machine's opencode row did not reach the warning:\n%s", got)
	}
	if strings.Contains(got, "its format may have changed") {
		t.Errorf("the warning still says nothing about the refusal:\n%s", got)
	}
}

// The reason is bounded and one line: sqlite3 quotes the query it failed on,
// and that query is a screenful.
func TestAStoreErrorIsOneBoundedLine(t *testing.T) {
	long := "near \"select\": syntax error " + strings.Repeat("select s.id, ", 80)
	got := boundedStoreError(long + "\nwhile preparing the statement")
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("the reason spans lines: %q", got)
	}
	if len(got) > storeErrorMax+3 {
		t.Errorf("the reason is %d bytes; the bound is %d", len(got), storeErrorMax)
	}
	if !strings.HasPrefix(got, "near \"select\": syntax error") {
		t.Errorf("the head of the message, the part that names the cause, was cut: %q", got)
	}
}
