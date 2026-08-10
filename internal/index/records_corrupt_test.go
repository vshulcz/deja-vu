package index

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

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
