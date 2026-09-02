package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A bucket file written by another version of deja is not a damaged one, and
// the line a reader gets is the only thing that tells them which it is: the
// first wording sent someone looking for a disk fault they did not have
// (#492).
func TestAnIndexFromAnotherVersionIsNotCalledDamaged(t *testing.T) {
	tmp := t.TempDir()
	buckets := filepath.Join(tmp, "buckets", "x00.bin")
	if err := os.MkdirAll(filepath.Dir(buckets), 0o755); err != nil {
		t.Fatal(err)
	}
	// A bucket in the shape the previous format wrote: right size, old magic.
	if err := os.WriteFile(buckets, append([]byte("DJB1"), 0x00), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := openBucketDir(buckets)
	if err == nil {
		t.Fatal("a bucket in the old format was accepted")
	}
	if !IsCorrupt(err) {
		t.Errorf("the old format is not reported as unreadable: %v", err)
	}
	if !strings.Contains(err.Error(), "older deja") || !strings.Contains(err.Error(), "DJB1") {
		t.Errorf("the error does not say which version wrote it: %v", err)
	}
	if got := damagedOrOutdated(err); got != "index written by another version of deja" {
		t.Errorf("the line a reader sees is %q", got)
	}

	// And a bucket that really is corrupt still reads as damage.
	if err := os.WriteFile(buckets, []byte{0x01, 0x02, 0x03, 0x04, 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = openBucketDir(buckets)
	if err == nil {
		t.Fatal("a bucket with no magic at all was accepted")
	}
	if got := damagedOrOutdated(err); got != "index damaged" {
		t.Errorf("a corrupt bucket reads as %q", got)
	}
	if strings.ContainsAny(err.Error(), "\x00\x01\x02") {
		t.Errorf("raw bytes reached the message: %q", err.Error())
	}
}
