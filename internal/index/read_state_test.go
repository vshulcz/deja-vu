package index

import "testing"

// How old the index is and what this build may do with it are different
// questions. FormatDirection answers the first; the sentence doctor prints was
// written for the second, and driving it from the first called a store one
// content version behind unreadable while `--deep` re-parsed its sessions
// (#3597).
func TestReadStateOf(t *testing.T) {
	dir := t.TempDir()
	if got := ReadStateOf(dir); got != ReadStateCurrent {
		t.Errorf("no manifest = %v, want ReadStateCurrent", got)
	}
	for _, tc := range []struct {
		name    string
		version int
		format  int
		want    ReadState
	}{
		{"what this build writes", version, onDiskFormat, ReadStateCurrent},
		{"a layout this build cannot read", version, onDiskFormat + 1, ReadStateUnreadable},
		{"written by a newer deja", version + 1, onDiskFormat, ReadStateNewer},
		{"below the redaction floor", redactionFloor - 1, onDiskFormat, ReadStateWithheld},
		{"at the floor but behind", redactionFloor, onDiskFormat, floorState()},
		{"one derivation behind", version - 1, onDiskFormat, olderRulesState()},
	} {
		m := Manifest{Version: tc.version, Format: tc.format, Sessions: map[string]SessionMeta{}}
		if err := writeManifest(dir, m); err != nil {
			t.Fatal(err)
		}
		if got := ReadStateOf(dir); got != tc.want {
			t.Errorf("%s (version %d, format %d) = %v, want %v",
				tc.name, tc.version, tc.format, got, tc.want)
		}
	}

	// The layout outranks the version: a store this build cannot open is not
	// merely holding text written under older rules.
	m := Manifest{Version: redactionFloor - 1, Format: onDiskFormat + 1, Sessions: map[string]SessionMeta{}}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if got := ReadStateOf(dir); got != ReadStateUnreadable {
		t.Errorf("an unreadable layout below the floor = %v, want ReadStateUnreadable", got)
	}
}

// floorState and olderRulesState keep the table honest as the constants move.
// A release where redactionFloor has caught up with version leaves no room for
// the states between them, and a table that hard-coded one would then be
// asserting something the build cannot produce.
func floorState() ReadState {
	if redactionFloor < version {
		return ReadStateOlderRules
	}
	return ReadStateCurrent
}

func olderRulesState() ReadState {
	if version-1 < redactionFloor {
		return ReadStateWithheld
	}
	return ReadStateOlderRules
}

// mustRebuildBeforeAnswering and ReadStateOf must not disagree: they are the
// same rule, and the two states that stop an answer are the two that force the
// rebuild (#3598 turned on exactly that pairing).
func TestReadStateAgreesWithMustRebuild(t *testing.T) {
	for _, m := range []Manifest{
		{Version: version, Format: onDiskFormat},
		{Version: version, Format: onDiskFormat + 1},
		{Version: version + 1, Format: onDiskFormat},
		{Version: redactionFloor - 1, Format: onDiskFormat},
		{Version: version - 1, Format: onDiskFormat},
	} {
		blocks := mustRebuildBeforeAnswering(m, version)
		dir := t.TempDir()
		m.Sessions = map[string]SessionMeta{}
		if err := writeManifest(dir, m); err != nil {
			t.Fatal(err)
		}
		state := ReadStateOf(dir)
		quiet := state == ReadStateWithheld || state == ReadStateUnreadable
		if blocks != quiet {
			t.Errorf("version %d format %d: mustRebuild=%v but state=%v",
				m.Version, m.Format, blocks, state)
		}
	}
}
