package index

import (
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
)

// A session is read through the offset table, and a table that does not fit
// the log it sits beside, whatever is wrong with it, gives the records a walk
// gives.
func TestSessionLoadsThroughTheOffsetTable(t *testing.T) {
	h := newTwoWayEnv(t)
	h.put("app", "s1", richSession("app", "s1", 2, "pool"))
	h.put("app", "s2", richSession("app", "s2", 3, "queue"))
	h.put("svc", "s3", richSession("svc", "s3", 4, "cache"))
	h.put("svc", "s4", richSession("svc", "s4", 5, "ledger"))
	h.put("svc", "s5", richSession("svc", "s5", 6, "outbox"))
	dir := filepath.Join(h.tmp, "a.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	table := filepath.Join(dir, recordOffsetsFile)
	good, err := os.ReadFile(table)
	if err != nil {
		t.Fatalf("a build leaves no offset table: %v", err)
	}
	load := func() ([]string, int64) {
		before := atomic.LoadInt64(&recordOffsetReads)
		s, ok, findErr := FindByIdentity(dir, "claude", "s2")
		if findErr != nil || !ok {
			t.Fatalf("load s2: %v %v", ok, findErr)
		}
		out := make([]string, 0, len(s.Messages))
		for _, m := range s.Messages {
			out = append(out, m.Role+"|"+m.Text)
		}
		return out, atomic.LoadInt64(&recordOffsetReads) - before
	}
	want, reads := load()
	if reads == 0 {
		t.Fatal("loading one session of five walked the log instead of reading the table")
	}

	broken := map[string][]byte{
		"missing":   nil,
		"truncated": good[:len(good)/2],
		"bad magic": append([]byte("XXXXXXXX"), good[8:]...),
		// Every offset moved by one record frame: the reads land on the
		// wrong records, and the key check catches it.
		"wrong offsets": shiftFirstOffset(good),
	}
	for name, b := range broken {
		if b == nil {
			_ = os.Remove(table)
		} else if err = os.WriteFile(table, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if got, _ := load(); !slices.Equal(got, want) {
			t.Errorf("%s table: s2 reads\n%q\nwant\n%q", name, got, want)
		}
	}

	// A log written again under a table built for the old one.
	if err = os.WriteFile(table, good, 0o600); err != nil {
		t.Fatal(err)
	}
	h.put("app", "s2", richSession("app", "s2", 3, "worker"))
	if err = Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	walked := map[string]bool{}
	recs, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Record.Key == "claude:s2" {
			walked[r.Record.Role+"|"+r.Record.Text] = true
		}
	}
	got, _ := load()
	for _, line := range got {
		if !walked[line] {
			t.Errorf("after the rewrite s2 reads a record the log does not hold: %q", line)
		}
	}
}

// shiftFirstOffset adds one to every key's first offset, leaving the header
// and the directory intact: each read lands inside a record, not on one.
func shiftFirstOffset(b []byte) []byte {
	out := append([]byte(nil), b...)
	n := int(binary.LittleEndian.Uint32(out[24+sha256.Size:]))
	data := recordOffsetsHeaderSize + 16*n
	for i := range n {
		p := data + int(binary.LittleEndian.Uint64(out[recordOffsetsHeaderSize+16*i+8:])) //nolint:gosec // offsets the writer just wrote
		kl, w := binary.Uvarint(out[p:])
		p += w + int(kl) //nolint:gosec // a key length the writer just wrote
		_, w = binary.Uvarint(out[p:])
		p += w
		out[p]++
	}
	return out
}
