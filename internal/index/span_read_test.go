package index

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSpanFixture writes the given records back to back and returns their
// offsets in write order, which is the order the scan sees them in.
func writeSpanFixture(t *testing.T, texts []string) (string, []int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "records.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tables := newRecordTables()
	var offs []int64
	for i, s := range texts {
		off, err := writeRecord(f, Record{
			Key:        "k",
			SourcePath: "p",
			Role:       "user",
			Text:       s,
			Time:       time.Unix(int64(1700000000+i), 0).UTC(),
		}, tables)
		if err != nil {
			t.Fatal(err)
		}
		offs = append(offs, off)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path, offs
}

func collectSpan(t *testing.T, path string, offs []int64) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var got []string
	if err := eachRecordAt(f, offs, newRecordTables(), func(r Record) { got = append(got, r.Text) }); err != nil {
		t.Fatalf("eachRecordAt over valid data: %v", err)
	}
	return got
}

// TestEachRecordAtMatchesIndividualReads is the property that matters: reading
// a run of records in coalesced spans must return exactly what reading them one
// at a time returns. The mix is deliberate — small records that share a span, a
// record far larger than the read-ahead window, and one pushed past spanGapMax
// so a new span has to start.
func TestEachRecordAtMatchesIndividualReads(t *testing.T) {
	texts := []string{
		"short one",
		"short two",
		strings.Repeat("a long record that cannot fit in the speculative window ", 4000),
		"after the long one",
		strings.Repeat("filler to push the next record past the gap limit ", 1200),
		"in a second span",
	}
	path, offs := writeSpanFixture(t, texts)

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, off := range offs {
		r, err := readRecordAt(f, off, newRecordTables())
		if err != nil {
			t.Fatalf("readRecordAt(%d): %v", off, err)
		}
		want = append(want, r.Text)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	got := collectSpan(t, path, offs)
	if len(got) != len(want) {
		t.Fatalf("got %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d differs: got %d bytes, want %d", i, len(got[i]), len(want[i]))
		}
	}
}

// TestEachRecordAtReportsAnUnreadableFile checks the part that would fail
// quietly: a file that cannot be read must surface the IO error, not hand back a
// clean-looking empty result. A closed file is a real IO error, not corruption,
// so it must propagate as itself and not be mistaken for a store to rebuild.
func TestEachRecordAtReportsAnUnreadableFile(t *testing.T) {
	path, offs := writeSpanFixture(t, []string{"one", "two", "three"})
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	n := 0
	err = eachRecordAt(f, offs, newRecordTables(), func(Record) { n++ })
	if err == nil {
		t.Fatal("a closed file was read as an empty result")
	}
	if IsCorrupt(err) {
		t.Fatalf("a closed file was misreported as index corruption: %v", err)
	}
	if n != 0 {
		t.Fatalf("closed file yielded %d records", n)
	}
}

// TestDecodeRecordInRejectsOutOfRange covers the bounds the span decoder is
// responsible for; each of these has to fall back to a single read rather than
// decode whatever bytes happen to follow.
func TestDecodeRecordInRejectsOutOfRange(t *testing.T) {
	b := []byte{4, 0, 0, 0, 1, 2}
	for _, rel := range []int{-1, len(b), len(b) - 2} {
		if _, ok := decodeRecordIn(b, rel, newRecordTables()); ok {
			t.Fatalf("rel=%d decoded out of range", rel)
		}
	}
	huge := []byte{255, 255, 255, 255}
	if _, ok := decodeRecordIn(huge, 0, newRecordTables()); ok {
		t.Fatal("oversized length decoded")
	}
}

// TestReadRecordAtCorruptHeader covers the two ways a header can be wrong: a
// file that ends inside it, and a length that claims more than any record is
// allowed to be. Both are index corruption, and both have to be reported
// rather than turned into a decode of arbitrary bytes.
func TestReadRecordAtCorruptHeader(t *testing.T) {
	dir := t.TempDir()

	short := filepath.Join(dir, "short.bin")
	if err := os.WriteFile(short, []byte{1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(short)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readRecordAt(f, 0, newRecordTables()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("partial header err=%v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	huge := filepath.Join(dir, "huge.bin")
	hdr := make([]byte, 8)
	binary.LittleEndian.PutUint32(hdr, ^uint32(0))
	if err := os.WriteFile(huge, hdr, 0o600); err != nil {
		t.Fatal(err)
	}
	f2, err := os.Open(huge)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f2.Close() }()
	if _, err := readRecordAt(f2, 0, newRecordTables()); !errors.Is(err, errCorruptIndex) {
		t.Fatalf("oversized length err=%v", err)
	}
}
