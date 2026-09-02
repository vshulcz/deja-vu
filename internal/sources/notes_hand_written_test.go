package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A session id is what every other surface keys on, so it must not carry a
// path that climbs out of the tree.
func TestANotesProjectCannotClimbOutOfTheTree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	body := `{"ts":"` + at + `","project":"../../etc","text":"a note filed against a path"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseNotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want the note", len(ss))
	}
	if strings.Contains(ss[0].ID, "..") || strings.Contains(ss[0].ID, "/") {
		t.Errorf("session id carries a path: %q", ss[0].ID)
	}
}
