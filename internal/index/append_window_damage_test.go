package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// An incremental pass appends to records.bin under the lock and commits the
// manifest after. A reader in that gap holds a complete snapshot, and calling
// it damaged turned an MCP recall away as "indexing" 17 times in 60 (#4266).
// A shorter log is still damage, lock or no lock.
func TestAnAppendInFlightIsNotDamage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "the vorpelsnark retry budget"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got := DamageReason(dir); got != "" {
		t.Fatalf("the fixture is wrong — a fresh store is damaged: %q", got)
	}

	// The pass: lock held, records appended, manifest not yet rewritten.
	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { unlock() }()
	rp := filepath.Join(dir, "records.bin")
	f, err := os.OpenFile(rp, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("records of a session the manifest has not committed yet")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if got := DamageReason(dir); got != "" {
		t.Errorf("an append in flight was called damage: %q", got)
	}
	res, err := SearchDetailed(dir, query.Options{Query: "vorpelsnark"})
	if err != nil || len(res.Sessions) == 0 {
		t.Errorf("the committed snapshot did not answer during the append: %d sessions, %v", len(res.Sessions), err)
	}

	// Truncated below what the manifest committed: damage, with the pass
	// running and without it.
	if err := os.Truncate(rp, 10); err != nil {
		t.Fatal(err)
	}
	if !Damaged(dir) {
		t.Error("a truncated record log under a running pass was not reported")
	}
	unlock()
	unlock = func() {}
	if !Damaged(dir) {
		t.Error("a truncated record log was not reported")
	}
}
