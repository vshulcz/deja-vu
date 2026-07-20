package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A crash after records flushed but before the manifest committed leaves an
// orphan tail; re-appending it used to duplicate messages silently (#181).
func TestRecordsIntactRejectsOrphanTail(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{RecordsSize: 10}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), make([]byte, 10), 0o600); err != nil {
		t.Fatal(err)
	}
	if !recordsIntact(dir, m) {
		t.Fatal("exact size must be intact")
	}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), make([]byte, 14), 0o600); err != nil {
		t.Fatal(err)
	}
	if recordsIntact(dir, m) {
		t.Fatal("orphan tail must not count as intact")
	}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), make([]byte, 6), 0o600); err != nil {
		t.Fatal(err)
	}
	if recordsIntact(dir, m) {
		t.Fatal("truncated log must not count as intact")
	}
}

func TestSwapIndexDirAndRecovery(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "index.db")
	tmp := filepath.Join(base, "tmp-build")
	for _, d := range []string{dir, tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "marker"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := swapIndexDir(dir, tmp); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "marker"))
	if string(b) != "new" {
		t.Fatalf("marker = %q", b)
	}
	if _, err := os.Stat(dir + ".old"); !os.IsNotExist(err) {
		t.Fatal(".old must be cleaned up after a completed swap")
	}
	// Interrupted swap: dir gone, .old parked — recovery restores it.
	if err := os.Rename(dir, dir+".old"); err != nil {
		t.Fatal(err)
	}
	recoverIndexDir(dir)
	b, _ = os.ReadFile(filepath.Join(dir, "marker"))
	if string(b) != "new" {
		t.Fatalf("recovered marker = %q", b)
	}
	// With a healthy dir, recovery only clears leftovers.
	if err := os.MkdirAll(dir+".old", 0o700); err != nil {
		t.Fatal(err)
	}
	recoverIndexDir(dir)
	if _, err := os.Stat(dir + ".old"); !os.IsNotExist(err) {
		t.Fatal("stale .old must be removed when dir is healthy")
	}
}

func TestWriteBucketLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ab.bin")
	if err := writeBucket(p, map[string][]posting{"tok": {{Off: 1, Sid: 2}}}); err != nil {
		t.Fatal(err)
	}
	got, err := readBucket(p)
	if err != nil || len(got["tok"]) != 1 {
		t.Fatalf("bucket roundtrip = %#v, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}
