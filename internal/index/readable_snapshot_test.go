package index

import (
	"path/filepath"
	"testing"
)

// A version bump forces a rebuild, and during it every reading surface chooses
// between an answer under the old rules and no answer at all. ReadableSnapshot
// is that choice: the layout this build writes can be read, a layout it cannot
// be sure of cannot.
func TestReadableSnapshotSeparatesLayoutFromContent(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "readable")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	if !ReadableSnapshot(dir) {
		t.Fatal("a store this build just wrote reads as unreadable")
	}
	// What an upgrade leaves behind: stale content, this build's layout.
	if err := SetManifestVersionForTest(dir, CurrentVersionForTest()-1); err != nil {
		t.Fatal(err)
	}
	if IsCurrentVersion(dir) {
		t.Fatal("the fixture is not one version behind")
	}
	if !ReadableSnapshot(dir) {
		t.Error("a stale content version made the layout unreadable")
	}
	// What a store written before the field existed carries.
	if err := SetManifestFormatForTest(dir, 0); err != nil {
		t.Fatal(err)
	}
	if ReadableSnapshot(dir) {
		t.Error("a store that cannot say its layout was read anyway")
	}
	// And a layout from some future build is equally not this one's.
	if err := SetManifestFormatForTest(dir, 999); err != nil {
		t.Fatal(err)
	}
	if ReadableSnapshot(dir) {
		t.Error("a layout this build does not write was read anyway")
	}
	// A directory with no manifest at all has nothing to read.
	if ReadableSnapshot(filepath.Join(tmp, "absent")) {
		t.Error("a missing store reads as readable")
	}
}

// AnswersWhileRebuilding is ReadableSnapshot with the redaction floor: a store
// below it reads, but must not be quoted while it is re-read.
func TestAnswersWhileRebuildingHoldsTheRedactionFloor(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "floor")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := SetManifestVersionForTest(dir, CurrentVersionForTest()-1); err != nil {
		t.Fatal(err)
	}
	if !AnswersWhileRebuilding(dir) {
		t.Error("a store one content version behind does not answer")
	}
	if err := SetManifestVersionForTest(dir, redactionFloor-1); err != nil {
		t.Fatal(err)
	}
	if !ReadableSnapshot(dir) || AnswersWhileRebuilding(dir) {
		t.Error("a store below the redaction floor answers while rebuilding")
	}
	if err := SetManifestVersionForTest(dir, CurrentVersionForTest()-1); err != nil {
		t.Fatal(err)
	}
	if err := SetManifestFormatForTest(dir, 0); err != nil {
		t.Fatal(err)
	}
	if AnswersWhileRebuilding(dir) {
		t.Error("a layout this build cannot read answers while rebuilding")
	}
	if AnswersWhileRebuilding(filepath.Join(tmp, "absent")) {
		t.Error("a missing store answers")
	}
}
