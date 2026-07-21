package usage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordDigestAndSnapshots(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordDigest(dir, KindHook, "first digest", 2)
	RecordDigest(dir, KindRecall, "second digest", 1)

	snaps := Snapshots(dir, 10)
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(snaps))
	}
	if snaps[0].Digest != "second digest" || snaps[0].Kind != KindRecall || snaps[0].Sessions != 1 {
		t.Fatalf("newest snapshot = %+v", snaps[0])
	}
	if snaps[1].Digest != "first digest" {
		t.Fatalf("oldest snapshot = %+v", snaps[1])
	}

	events := Events(dir, 10)
	if len(events) != 2 || events[0].Kind != KindRecall || events[0].Bytes != len("second digest") {
		t.Fatalf("events = %+v", events)
	}
}

func TestRecordDigestEmptySkipsSnapshot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	RecordDigest(dir, KindRecall, "", 0)
	if snaps := Snapshots(dir, 10); len(snaps) != 0 {
		t.Fatalf("empty digest recorded a snapshot: %+v", snaps)
	}
	events := Events(dir, 10)
	if len(events) != 1 || !events[0].Empty {
		t.Fatalf("empty event = %+v", events)
	}
}

func TestSnapshotRotationKeepsNewest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	big := strings.Repeat("x", 60<<10)
	for i := 0; i < 12; i++ {
		RecordDigest(dir, KindHook, big+string(rune('a'+i)), 1)
	}
	snaps := Snapshots(dir, 0)
	if len(snaps) == 0 || len(snaps) > snapshotsToKeep+2 {
		t.Fatalf("rotation kept %d snapshots", len(snaps))
	}
	if !strings.HasSuffix(snaps[0].Digest, "l") {
		t.Fatalf("newest snapshot lost after rotation, tail = %q", snaps[0].Digest[len(snaps[0].Digest)-1:])
	}
}

func TestEventsLimitAndOrder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	for i := 0; i < 5; i++ {
		RecordResult(dir, KindSearch, i, 0, false)
	}
	events := Events(dir, 3)
	if len(events) != 3 || events[0].Bytes != 4 || events[2].Bytes != 2 {
		t.Fatalf("events = %+v", events)
	}
}
