package index

import (
	"testing"
)

// The count behind the after-tool hook's line (#2491): sessions carrying one
// wall's signature, gated by the caller's activation the way TopFriction is.
func TestFrictionSessionsCountsAndScopes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	if n := FrictionSessions(dir, 1, nil); n != 0 {
		t.Errorf("an index that does not exist counted %d sessions", n)
	}

	m := Manifest{Sessions: map[string]SessionMeta{
		"claude:a": {ID: "a", Harness: "claude", Project: "app", Hit: []uint64{7, 9}},
		"claude:b": {ID: "b", Harness: "claude", Project: "app", Hit: []uint64{7}},
		"claude:c": {ID: "c", Harness: "claude", Project: "other", Hit: []uint64{7}},
		"claude:d": {ID: "d", Harness: "claude", Project: "app", Hit: []uint64{9}},
		// One session hitting the same wall twice is still one session.
		"claude:e": {ID: "e", Harness: "claude", Project: "app", Hit: []uint64{7, 7}},
	}}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	if n := FrictionSessions(dir, 7, nil); n != 4 {
		t.Errorf("counted %d sessions for the wall, want 4", n)
	}
	if n := FrictionSessions(dir, 7, func(p string) bool { return p == "app" }); n != 3 {
		t.Errorf("counted %d in app, want 3", n)
	}
	if n := FrictionSessions(dir, 42, nil); n != 0 {
		t.Errorf("counted %d for a wall nobody hit", n)
	}

	// An empty dir falls back to the default, which the env var above points
	// at the same place.
	if n := FrictionSessions("", 7, nil); n != 4 {
		t.Errorf("the default dir counted %d, want 4", n)
	}
}
