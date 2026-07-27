package index

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCorruptBucketDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"huge count":   append(append([]byte(bucketMagic), encodeUvarint(1<<62)...), 0),
		"huge token":   append(append([]byte(bucketMagic), encodeUvarint(1)...), encodeUvarint(1<<62)...),
		"truncated":    []byte(bucketMagic),
		"bad magic":    []byte("XXXXXXXX"),
		"empty file":   {},
		"garbage tail": append([]byte(bucketMagic), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff),
	}
	for name, body := range cases {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: panicked instead of failing: %v", name, r)
				}
			}()
			_, f, err := openBucketDir(p)
			if f != nil {
				f.Close()
			}
			if err == nil {
				t.Fatalf("%s: accepted as valid", name)
			}
			if !errors.Is(err, errCorruptIndex) && !os.IsNotExist(err) {
				t.Logf("%s: err=%v (not errCorruptIndex)", name, err)
			}
		}()
	}
}

func encodeUvarint(v uint64) []byte {
	b := make([]byte, binary.MaxVarintLen64)
	return b[:binary.PutUvarint(b, v)]
}

// A block length under MaxUint32 still passed, so a bucket naming four
// gigabytes for one token turned a recoverable corruption into an OOM.
func TestBucketBlockLengthBoundedByFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.bin")
	body := append([]byte(bucketMagic), encodeUvarint(1)...)
	body = append(body, encodeUvarint(1)...)
	body = append(body, 'x')
	body = append(body, encodeUvarint(4_000_000_000)...)
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	_, f, err := openBucketDir(p)
	if f != nil {
		f.Close()
	}
	if err == nil {
		t.Fatal("a four-gigabyte posting block in a 20-byte file was accepted")
	}
	if !errors.Is(err, errCorruptIndex) {
		t.Fatalf("err = %v, want errCorruptIndex so the caller rebuilds", err)
	}
}
