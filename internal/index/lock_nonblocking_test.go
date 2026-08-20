package index

import (
	"testing"
	"time"
)

// Read paths must never wait out a rebuild. The session-start hook goes
// through these, and an index-format upgrade puts every user behind exactly
// this rebuild on their first session — twelve seconds of an agent that looks
// hung. A blocking lock here is a regression even though nothing fails.
func TestReadsDoNotBlockWhileTheIndexIsLocked(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Logf("build: %v", err) // an empty machine is fine: the lock is what matters
	}
	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	defer unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = FindByPrefix(dir, "nothing")
		_, _, _, _, _ = ProjectRelevant(dir, []string{"p"}, []string{"term"}, 3)
		_, _ = RecentProject(dir, "p", 3)
		_, _, _ = FirstMatch(dir, []string{"term"}, 3)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a read waited for the index lock: on a real corpus that is the length of a rebuild")
	}
}
