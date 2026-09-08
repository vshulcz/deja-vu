package main

import (
	"os"
	"testing"
	"time"
)

// The per-prompt hook asks the dedupe file four questions and used to read and
// split the whole file for each — 4 ms of a 24 ms call on a 648 KB list
// (#3379). It reads once now, and the four standalone helpers answer from the
// same code, so the two cannot drift apart.
func TestTheSeenListAnswersTheSameFromOneRead(t *testing.T) {
	dir := t.TempDir() + "/index"
	key := askKey([]string{"zebraquux", "fetcher"})
	rememberInjectedIDs(dir, "sess-1", "block-aaa")
	rememberInjectedIDsFor(dir, "sess-1", "proj", []string{"s-old", "s-new"})
	rememberInjectedIDsFor(dir, "sess-1", "proj", []string{key})
	rememberInjectedIDsFor(dir, "sess-2", "proj", []string{"s-other"})

	sl := loadSeen(dir)
	if got, want := sl.injected("sess-1"), alreadyInjected(dir, "sess-1"); !sameSet(got, want) {
		t.Errorf("injected: %v vs %v", got, want)
	}
	if got, want := sl.recent("sess-1", 2), recentlyInjected(dir, "sess-1", 2); !sameSet(got, want) {
		t.Errorf("recent: %v vs %v", got, want)
	}
	if got, want := sl.recentInProject("proj", 3), recentlyInjectedInProject(dir, "proj", 3); !sameSet(got, want) {
		t.Errorf("recentInProject: %v vs %v", got, want)
	}
	if got, want := sl.asked("sess-1", key, time.Hour), askedRecently(dir, "sess-1", key, time.Hour); got != want {
		t.Errorf("asked: %v vs %v", got, want)
	}
	// And a missing file answers what it always did.
	if err := os.Remove(dir + ".hookseen"); err != nil {
		t.Fatal(err)
	}
	empty := loadSeen(dir)
	if len(empty.injected("sess-1")) != 0 || empty.asked("sess-1", key, time.Hour) {
		t.Error("a missing dedupe file is no longer empty")
	}
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
