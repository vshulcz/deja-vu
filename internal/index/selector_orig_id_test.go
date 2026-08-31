package index

import "testing"

// A session that arrived by sync is keyed here by the id the import gave it,
// so the id the reader knows — the one the machine that recorded it prints,
// the one a note promoted from it carries — matched nothing, and the only way
// to forget it was an id nobody chose (#2843).
func TestASelectorFindsAnImportedSessionByTheIdItCameWith(t *testing.T) {
	meta := SessionMeta{Harness: "claude", ID: ImportedSessionID("claude", "longs"), OrigID: "longs"}
	for _, sel := range []string{"longs", "claude:longs", meta.ID, "claude:" + meta.ID} {
		if !selectorMatches(meta, sel) {
			t.Errorf("%q does not name the session", sel)
		}
	}
	// And it stays a name rather than a substring: a session that merely
	// starts the same way is not the one the reader asked for.
	if selectorMatches(SessionMeta{Harness: "claude", ID: "other", OrigID: "longshoreman"}, "longs") {
		t.Error("a prefix of another session's original id matched")
	}
	if selectorMatches(meta, "codex:longs") {
		t.Error("the harness is not being checked")
	}
	if selectorMatches(SessionMeta{Harness: "claude", ID: "other", OrigID: "longshoreman"}, "claude:longs") {
		t.Error("a prefix of another session's original id matched through the harness form")
	}
}

// And the round trip: what forget takes, unforget gives back by the same
// string. Taking a session by an id whose undo needs a different one is worse
// than not taking it (#2843).
func TestATombstoneAnswersToTheIdItWasForgottenBy(t *testing.T) {
	key := "claude:" + ImportedSessionID("claude", "longs")
	for _, sel := range []string{"longs", key, ImportedSessionID("claude", "longs")} {
		if !tombstoneMatches(key, sel) {
			t.Errorf("%q does not lift the tombstone forget wrote for it", sel)
		}
	}
	if tombstoneMatches(key, "someone-elses-session") {
		t.Error("an unrelated id lifted the tombstone")
	}
}
