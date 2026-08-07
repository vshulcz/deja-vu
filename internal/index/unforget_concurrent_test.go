package index

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// Two unforgets of different sessions at once used to lose one of them: each
// read the whole tombstone set before taking the index lock, dropped its own
// key from its own copy, and wrote that copy back in turn. Both printed
// "restored 1 session" and the loser's tombstone came back out of the stale
// copy — ten forgotten sessions, five parallel unforgets, nine tombstones left.
//
// The window is reproduced exactly: the lock is held while the second unforget
// starts, so it either reads the set now (stale) or after the lock is released
// (fresh), and the set changes in between.
func TestConcurrentUnforgetDoesNotResurrectATombstone(t *testing.T) {
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-p", "a.jsonl"),
		claudeLine("s1", "2026-01-02T03:04:05Z", "unfneedle one"))
	write(t, filepath.Join(root, "claude", "-tmp-p", "b.jsonl"),
		claudeLine("s2", "2026-01-02T03:05:05Z", "unfneedle two"))
	o := search.Options{Query: "unfneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "s2"} {
		if _, err := Forget(dir, ForgetOptions{Session: id}); err != nil {
			t.Fatal(err)
		}
	}
	if got := Tombstones(); !contains(got, "claude:s1") || !contains(got, "claude:s2") {
		t.Fatalf("tombstones = %v, want both sessions forgotten", got)
	}

	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Unforget(dir, "claude:s2", nil)
		done <- err
	}()
	// Long enough for the goroutine to reach its tombstone read; it cannot
	// get past the lock this test holds.
	time.Sleep(200 * time.Millisecond)

	// What a concurrent unforget of the other session commits, minus the
	// rebuild: s1 is no longer forgotten.
	if err := writeTombstones(map[string]bool{"claude:s2": true}); err != nil {
		t.Fatal(err)
	}
	if err := writeTombstoneMirrorAt(dir, []string{"claude:s2"}); err != nil {
		t.Fatal(err)
	}
	unlock()

	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := Tombstones(); contains(got, "claude:s1") || contains(got, "claude:s2") {
		t.Errorf("tombstones = %v after both sessions were unforgotten, want neither", got)
	}
}
