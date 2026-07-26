package index

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A bucket file written by a different index layout — or simply truncated —
// used to reach make() with a length taken from the file itself, which panics
// instead of failing. Found in the field: an older deja binary reading a newer
// index crashed the harness hook it was running under.
func TestOpenBucketDirRejectsImpossibleCounts(t *testing.T) {
	cases := map[string]func() []byte{
		"entry count larger than the file": func() []byte {
			b := append([]byte{}, bucketMagic...)
			return binary.AppendUvarint(b, 1<<62)
		},
		"token longer than the file": func() []byte {
			b := append([]byte{}, bucketMagic...)
			b = binary.AppendUvarint(b, 1)
			return binary.AppendUvarint(b, 1<<62)
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bucket")
			if err := os.WriteFile(path, build(), 0o644); err != nil {
				t.Fatal(err)
			}
			entries, f, err := openBucketDir(path)
			if f != nil {
				f.Close()
			}
			if err == nil {
				t.Fatalf("accepted a corrupt bucket: %d entries", len(entries))
			}
			if !errors.Is(err, errCorruptIndex) {
				t.Fatalf("err = %v, want errCorruptIndex so callers can rebuild", err)
			}
		})
	}
}
