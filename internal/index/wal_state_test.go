package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A SQLite store in WAL mode takes its writes in <db>-wal and touches the
// database file only at a checkpoint. Hermes checkpoints every 50 writes of
// one process, and five sessions sat in state.db-wal while the walk called
// state.db unchanged and the index stayed at one session (#5140).
func TestAStoreThatWroteOnlyItsWALReadsAsChanged(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "state.db")
	wal := db + "-wal"
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wal, []byte("frames"), 0o644); err != nil {
		t.Fatal(err)
	}
	walk := func() FileState {
		t.Helper()
		fi, err := os.Lstat(db)
		if err != nil {
			t.Fatal(err)
		}
		return walkFileState(db, fi, nil)
	}
	before := walk()
	if !sameFile(before, walk()) {
		t.Fatal("two walks of an untouched store differ")
	}
	if err := os.WriteFile(wal, []byte("frames and a new session"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(wal, later, later); err != nil {
		t.Fatal(err)
	}
	if sameFile(before, walk()) {
		t.Error("a store whose WAL took new writes reads as unchanged, so the sessions in it are never indexed")
	}
	// The checkpoint that removes the log on close is a change too.
	grown := walk()
	if err := os.Remove(wal); err != nil {
		t.Fatal(err)
	}
	if sameFile(grown, walk()) {
		t.Error("a store whose WAL went away reads as unchanged")
	}

	// A transcript is not a database: nothing beside it is looked at.
	j := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(j, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(j+"-wal", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Lstat(j)
	first := walkFileState(j, fi, nil)
	if err := os.WriteFile(j+"-wal", []byte("something else"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !sameFile(first, walkFileState(j, fi, nil)) {
		t.Error("a jsonl transcript took a -wal file beside it as its log")
	}
}
