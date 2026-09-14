package index

import "testing"

// What a version difference costs the first question after an upgrade. A store
// this build can read answers under its own older rules while the re-read runs
// behind it; one whose layout this build cannot read, or whose text predates a
// redaction fix, does not answer until it has been rebuilt (#3552, #3535).
func TestOnlyALayoutOrARedactionBumpStopsAnIndexFromAnswering(t *testing.T) {
	for _, tc := range []struct {
		name  string
		m     Manifest
		build int
		must  bool
	}{
		{
			name:  "the index this build writes",
			m:     Manifest{Version: version, Format: onDiskFormat},
			build: version,
		},
		{
			// The case this exists for: the next derivation bump, reading the
			// index the build before it wrote.
			name:  "a derivation bump this build can read",
			m:     Manifest{Version: redactionFloor, Format: onDiskFormat},
			build: redactionFloor + 1,
		},
		{
			name:  "text written before deja could redact it",
			m:     Manifest{Version: redactionFloor - 1, Format: onDiskFormat},
			build: version,
			must:  true,
		},
		{
			name:  "a layout this build cannot read",
			m:     Manifest{Version: version, Format: onDiskFormat - 1},
			build: version,
			must:  true,
		},
		{
			name:  "an index from a newer build",
			m:     Manifest{Version: version + 1, Format: onDiskFormat},
			build: version,
			must:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRebuildBeforeAnswering(tc.m, tc.build); got != tc.must {
				t.Fatalf("mustRebuildBeforeAnswering = %v, want %v", got, tc.must)
			}
		})
	}
}

// The floor is a claim about the version list in index.go, not a free number:
// it names the last bump that changed what may be shown, so it can never point
// past the version this build writes.
func TestTheRedactionFloorNamesAVersionThatExists(t *testing.T) {
	if redactionFloor > version {
		t.Fatalf("redactionFloor %d is past version %d — every index would rebuild before answering", redactionFloor, version)
	}
	if redactionFloor <= 0 {
		t.Fatalf("redactionFloor = %d", redactionFloor)
	}
}
