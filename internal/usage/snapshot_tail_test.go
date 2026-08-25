package usage

import (
	"os"
	"testing"
)

// A process killed between a snapshot record and its newline costs that record.
// It cost every later one too: the next digest was appended onto the partial
// line, so one line held two objects and parsed as neither, and `deja log
// --last` reported nothing on a machine that had just served two (#1965). The
// usage log closed this in #1901; its sibling had not.
func TestASnapshotAfterAnInterruptedWriteIsStillReadable(t *testing.T) {
	dir := t.TempDir()
	RecordDigestPolicy(dir, KindHook, "the first digest", 2, 4000, "local-only")

	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SnapshotPath(dir), b[:len(b)-1], 0o600); err != nil {
		t.Fatal(err)
	}

	RecordDigestPolicy(dir, KindHook, "the second digest", 2, 4000, "local-only")

	got := Snapshots(dir, 0)
	if len(got) == 0 {
		t.Fatal("one interrupted write lost every digest in the file")
	}
	if got[0].Digest != "the second digest" {
		t.Errorf("newest digest is %q, want the one just served", got[0].Digest)
	}
	// The interrupted record survives too: what the kill cost it was the
	// newline, not the JSON, and the write after it no longer takes it down.
	if len(got) != 2 {
		t.Errorf("readable snapshots: %d, want both", len(got))
	}
}

// The same file, rotated by the write that follows a partial tail. Rotation
// rewrites from what the reader accepts, so it is the second place a
// half-written line could take the rest of the file with it — and it turns out
// to be the one place that already healed it: the rewrite terminates every
// line, so the write after a rotation never glued anything. A guard on a path
// that was right, not a repeat of the one above.
func TestRotationSurvivesAPartialTail(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'x'
	}
	// Past the rotation threshold, measured rather than assumed: 300 records of
	// this size sat under it, and the test then rotated nothing.
	for {
		RecordDigestPolicy(dir, KindHook, string(big), 2, 4000, "local-only")
		fi, err := os.Stat(SnapshotPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Size() >= 512<<10 {
			break
		}
	}
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SnapshotPath(dir), b[:len(b)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}

	RecordDigestPolicy(dir, KindHook, "after the rotation", 2, 4000, "local-only")

	after, err := os.Stat(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() >= before.Size() {
		t.Fatalf("no rotation happened: %d bytes before, %d after", before.Size(), after.Size())
	}
	got := Snapshots(dir, 0)
	if len(got) == 0 {
		t.Fatal("the rotation emptied the file")
	}
	if got[0].Digest != "after the rotation" {
		t.Errorf("newest digest is %d bytes of the wrong record", len(got[0].Digest))
	}
}
