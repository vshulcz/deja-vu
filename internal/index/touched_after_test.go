package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A fork's source is cut at the fork point, and Touched has no times: only a
// files record stamped after the cut says the source worked on the file since
// (#5341). One that names no such record is kept counted.
func TestTouchedAfterReadsTheFilesRecordTimes(t *testing.T) {
	at := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	turn := func(role, text string, i int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(i) * time.Minute)}
	}
	ss := []model.Session{{Harness: "claude", ID: "src", Project: "p", Messages: []model.Message{
		turn("user", "flush the ledger", 0), turn(roleFiles, "/work/app/ledger.go", 1),
		turn("user", "check the notes", 10), turn(roleFiles, "/work/app/notes.md", 11),
	}}}
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	meta := SessionMeta{Harness: "claude", ID: "src"}
	cut := at.Add(5 * time.Minute)
	for _, c := range []struct {
		name, path string
		at         time.Time
		want       bool
	}{
		{"touched only before the cut", "/work/app/ledger.go", cut, false},
		{"control: the same file with the cut before its edit", "/work/app/ledger.go", at, true},
		{"touched after the cut", "/work/app/notes.md", cut, true},
		{"no record names it", "/work/app/other.go", cut, true},
	} {
		if got := TouchedAfter(dir, meta, c.path, c.at); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if !TouchedAfter(filepath.Join(t.TempDir(), "missing"), meta, "/work/app/ledger.go", cut) {
		t.Error("with no index the session should stay counted")
	}
}
