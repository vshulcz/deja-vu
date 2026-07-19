package index

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestReadRecordRejectsHugeLengthPrefix(t *testing.T) {
	p := filepath.Join(t.TempDir(), "records.bin")
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], 0xFFFFFFFF) // ~4GiB claimed
	if err := os.WriteFile(p, append(hdr[:], 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	err := eachRecord(p, func(Record) { t.Fatal("should not decode a corrupt record") })
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("expected corrupt-index error, got %v", err)
	}
}
