package index

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The substring (fuzzy) tier scans every bucket and used to skip a bucket it
// could not open, so a corrupt store silently under-matched instead of
// surfacing the damage for the recovery path to rebuild.
func TestSubstringTierSurfacesACorruptBucket(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "opencode indexing throughput"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no buckets to corrupt")
	}
	for _, e := range entries {
		if err := os.WriteFile(filepath.Join(dir, "buckets", e.Name()), []byte("XXXXXXXX garbage"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// "code" is a substring of the indexed "opencode", so this reaches the
	// substring scan rather than short-circuiting empty.
	_, _, err = intersectSubstringPostingsDetailed(dir, []string{"code"})
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("want a corrupt-index error from the substring tier, got %v", err)
	}
}

// FirstMatch (the per-prompt hook path) probes candidate queries and used to
// lump a scanRecords error together with "no match" and skip to the next
// candidate, so a corrupt store looked like a prompt with nothing to recall.
func TestFirstMatchSurfacesCorruption(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "proj",
		Messages: []model.Message{{Role: "user", Text: "kafka consumer lag climbing"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	rp := filepath.Join(dir, "records.bin")
	b, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	off := 4
	_, k := binary.Uvarint(b[off:])
	off += k
	_, k = binary.Uvarint(b[off:])
	off += k
	b[off] = 0xFF
	if err := os.WriteFile(rp, b, 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err = FirstMatch(dir, []string{"kafka"}, 5)
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("want a corrupt-index error from FirstMatch, got %v", err)
	}
}

// The offset read path (postings resolve to record offsets) must also surface a
// corrupt record rather than skipping it and reporting a clean result. A posting
// offset is committed, so a record that will not decode there is corruption.
func TestEachRecordAtSurfacesCorruption(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "proj",
		Messages: []model.Message{{Role: "user", Text: "kafka consumer lag climbing"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	rp := filepath.Join(dir, "records.bin")
	b, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	// Flip the first record's encoding flag (after the two leading varints).
	off := 4
	_, k := binary.Uvarint(b[off:])
	off += k
	_, k = binary.Uvarint(b[off:])
	off += k
	b[off] = 0xFF
	if err := os.WriteFile(rp, b, 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(rp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	called := false
	err = eachRecordAt(f, []int64{0}, tablesFromManifest(m), func(Record) { called = true })
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("want a corrupt-index error from the offset path, got %v", err)
	}
	if called {
		t.Fatal("a corrupt record was delivered instead of surfaced")
	}
}

// A record whose framing is intact (valid length prefix, all bytes present) but
// whose payload will not decode is corruption in the middle of the log, not the
// tolerated in-flight tail. The read path must surface it so a rebuild can heal
// the store, not hand back a session with the broken message silently dropped.
func TestReadSurfacesAMidLogDecodeError(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "proj",
		Messages: []model.Message{{Role: "user", Text: "postgres deadlock on the outbox table"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}

	// Flip the record's encoding flag to an unknown value. Layout per record:
	// a 4-byte little-endian length, then payload = [key id varint][source id
	// varint][flag byte]. The bytes stay the same length, so framing still reads
	// and the corruption only shows when the payload is decoded.
	rp := filepath.Join(dir, "records.bin")
	b, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	n := int(binary.LittleEndian.Uint32(b[:4]))
	if n == 0 || 4+n > len(b) {
		t.Fatalf("unexpected record framing: n=%d len=%d", n, len(b))
	}
	off := 4
	_, k := binary.Uvarint(b[off:])
	off += k
	_, k = binary.Uvarint(b[off:])
	off += k // now at the flag byte
	b[off] = 0xFF
	if err := os.WriteFile(rp, b, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = RecentProject(dir, "proj", 0)
	if err == nil {
		t.Fatal("a corrupt record was dropped silently; the read returned no error")
	}
	if !IsCorrupt(err) {
		t.Fatalf("want a corrupt-index error, got %v", err)
	}
}
