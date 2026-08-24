package index

import "testing"

// Sync keeps both copies when a session id exists on two machines, and the
// imported row keeps the id it had there (#1049). Nothing connected the two, so
// a reader saw one session's two histories as two unrelated sessions with
// contradictory conclusions (#1775).
func TestTwinsAcrossMachinesFindEachOther(t *testing.T) {
	metas := []SessionMeta{
		{ID: "dup-2", Harness: "claude", Project: "proj"},
		{ID: "imported-d5b1285ecbfe", Harness: "claude", Project: "imported:proj", OrigID: "dup-2"},
		{ID: "other", Harness: "claude", Project: "proj"},
		{ID: "imported-elsewhere", Harness: "claude", Project: "imported:proj", OrigID: "never-here"},
		{ID: "dup-2", Harness: "codex", Project: "proj"},
	}
	twins := TwinSessions(metas)

	if got := twins["claude:dup-2"]; got != "claude:imported-d5b1285ecbfe" {
		t.Errorf("the local copy points at %q", got)
	}
	if got := twins["claude:imported-d5b1285ecbfe"]; got != "claude:dup-2" {
		t.Errorf("the imported copy points at %q", got)
	}
	// A session with no counterpart, an import whose origin is not here, and
	// the same id under another harness are all left alone.
	for _, key := range []string{"claude:other", "claude:imported-elsewhere", "codex:dup-2"} {
		if got, ok := twins[key]; ok {
			t.Errorf("%s was paired with %q", key, got)
		}
	}
}
