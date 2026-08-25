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
	// The interrupted record itself is gone, which is the cost of the kill and
	// not of the write after it.
	if len(got) != 2 {
		t.Logf("readable snapshots: %d (the interrupted line may or may not survive)", len(got))
	}
}

// The same file, rotated in the same run: rotation rewrites from what the
// reader accepts, so a partial tail must not take the rest of the file with it.
func TestRotationSurvivesAPartialTail(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'x'
	}
	for i := 0; i < 300; i++ {
		RecordDigestPolicy(dir, KindHook, string(big), 2, 4000, "local-only")
	}
	b, err := os.ReadFile(SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SnapshotPath(dir), b[:len(b)-1], 0o600); err != nil {
		t.Fatal(err)
	}

	RecordDigestPolicy(dir, KindHook, "after the rotation", 2, 4000, "local-only")

	got := Snapshots(dir, 0)
	if len(got) == 0 {
		t.Fatal("the rotation emptied the file")
	}
	if got[0].Digest != "after the rotation" {
		t.Errorf("newest digest is %q, want the one just served", got[0].Digest)
	}
	for _, s := range got {
		if !s.usable() {
			t.Errorf("rotation kept a line the reader will not accept: %#v", s)
		}
	}
}
