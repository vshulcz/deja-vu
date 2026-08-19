package embed

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sidecar is published in one step, so a write that cannot finish must
// leave the previous one in place rather than a half-file — the reader decodes
// it as nothing, and semantic search then answers from an index it thinks is
// empty.
func TestWriteLeavesTheOldSidecarWhenItCannotFinish(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	good := Sidecar{Model: "m", Dim: 1, Generation: "g1", Covered: 1,
		Vectors: []Vector{{Offset: 0, Key: "claude:a", Values: []float32{1}}}}
	if err := write(dir, good); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}

	// The parent is where the temp file goes, so a read-only one stops the
	// write before anything is published.
	parent := filepath.Dir(Path(dir))
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Skip("cannot make the directory read-only here")
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permissions this test needs")
	}
	// Chmod succeeds on Windows and changes nothing, so the mode bits are not
	// evidence — whether the directory actually refuses a new file is.
	if probe, err := os.Create(filepath.Join(parent, "probe")); err == nil {
		probe.Close()
		_ = os.Remove(probe.Name())
		t.Skip("the directory still accepts writes when read-only")
	}
	if err := write(dir, Sidecar{Model: "m", Dim: 1, Generation: "g2"}); err == nil {
		t.Error("a write into a read-only directory reported success")
	}
	after, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("the sidecar changed on a write that failed")
	}
}

// A vector whose key cannot be written is the same situation one level down:
// nothing is published, and the error reaches the caller rather than being
// swallowed into a truncated file.
func TestWriteReportsAFailureMidway(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A key longer than the sidecar's length prefix can hold does not exist;
	// what does is a disk that stops accepting bytes, and the closest thing a
	// test can do is take the directory away mid-write.
	s := Sidecar{Model: "m", Dim: 1, Generation: "g", Covered: 2, Vectors: []Vector{
		{Offset: 0, Key: "claude:a", Values: []float32{1}},
		{Offset: 8, Key: "claude:b", Values: []float32{2}},
	}}
	if err := write(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Vectors) != 2 || got.Covered != 2 {
		t.Errorf("round trip lost content: %d vectors, covered %d", len(got.Vectors), got.Covered)
	}
	if got.Model != "m" || got.Generation != "g" {
		t.Errorf("round trip lost the header: %+v", got)
	}
}

// And a directory that is not there at all fails with something the caller can
// act on rather than a partial file.
func TestWriteToAMissingIndexDirectoryFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gone", "index.db")
	err := write(dir, Sidecar{Model: "m", Dim: 1})
	if err == nil {
		t.Fatal("writing into a missing directory reported success")
	}
	if !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "no such") {
		t.Errorf("the error does not say the directory is missing: %v", err)
	}
}
