package index

import (
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/query"
)

func keepFreshStore(t *testing.T) (dir, path string) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir = filepath.Join(home, "idx")
	path = filepath.Join(claude, "project", "s.jsonl")
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "heron lantern"))
	if err := EnsureForSearch(dir, query.Options{All: true}, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// indexedBytes is how much of the transcripts the manifest says it has read.
func indexedBytes(t *testing.T, dir string) int64 {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, f := range m.Files {
		n += f.Size
	}
	return n
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (k *freshKeeper) checkedOnce() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return !k.checked.IsZero()
}

func kickKeeper(k *freshKeeper) {
	k.mu.Lock()
	before := k.checked
	k.mu.Unlock()
	k.kick <- struct{}{}
	for {
		k.mu.Lock()
		done := k.checked.After(before)
		k.mu.Unlock()
		if done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// With a keeper running, a query reads the index as it is: the append waits
// for the keeper, which absorbs it on its own goroutine.
func TestKeepFreshTakesTheWalkOffTheQuery(t *testing.T) {
	dir, path := keepFreshStore(t)
	var stale atomic.Int32
	stop := KeepFresh(dir, time.Hour, func() { stale.Add(1) }, io.Discard)
	defer stop()
	k := keptFresh(dir)
	if k == nil {
		t.Fatal("no keeper registered for the index")
	}
	waitUntil(t, "the first check", k.checkedOnce)

	before := indexedBytes(t, dir)
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "heron lantern"), claudeLine("s1", "2026-01-01T00:02:00Z", "quartz meadow"))
	if s, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil || s {
		t.Fatalf("query: stale=%v err=%v", s, err)
	}
	if got := indexedBytes(t, dir); got != before {
		t.Fatalf("the query read the append itself: %d -> %d bytes", before, got)
	}

	kickKeeper(k)
	if got := indexedBytes(t, dir); got <= before {
		t.Fatalf("the keeper did not absorb the append: %d -> %d bytes", before, got)
	}
	if n := stale.Load(); n != 0 {
		t.Fatalf("an append was handed to a detached build %d times", n)
	}
}

// Work the keeper will not do inline is handed on once, and queries say the
// answer is stale until the index is written again.
func TestKeepFreshHandsOnRewritesAndForgetsThemOnPublish(t *testing.T) {
	dir, path := keepFreshStore(t)
	var handed atomic.Int32
	stop := KeepFresh(dir, time.Hour, func() { handed.Add(1) }, io.Discard)
	defer stop()
	k := keptFresh(dir)
	waitUntil(t, "the first check", k.checkedOnce)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	kickKeeper(k)
	if n := handed.Load(); n != 1 {
		t.Fatalf("a removed transcript was handed on %d times, want 1", n)
	}
	c, err := EnsureForSearchCoverage(dir, query.Options{All: true}, io.Discard)
	if err != nil || !c.Stale {
		t.Fatalf("query after a removal: %+v err=%v", c, err)
	}

	// The build the hand-off asked for, run here in place of a detached one.
	if err := EnsureForSearch(dir, query.Options{All: true}, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if s, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil || s {
		t.Fatalf("query after the rebuild published: stale=%v err=%v", s, err)
	}
}

// One keeper per process, and only for its own index; stop lets go of it.
func TestKeepFreshIsOnePerProcess(t *testing.T) {
	dir, _ := keepFreshStore(t)
	stop := KeepFresh(dir, time.Hour, nil, io.Discard)
	k := keptFresh(dir)
	if k == nil {
		t.Fatal("no keeper")
	}
	KeepFresh(dir+"-other", time.Hour, nil, io.Discard)()
	if keptFresh(dir) != k || keptFresh(dir+"-other") != nil {
		t.Fatal("a second keeper replaced the first")
	}
	stop()
	stop()
	if keptFresh(dir) != nil {
		t.Fatal("the keeper outlived stop")
	}
}

// A query that finds the verdict old (the keeper idled, the machine slept)
// wakes the keeper rather than walking itself.
func TestKeepFreshOldVerdictWakesTheKeeper(t *testing.T) {
	dir, path := keepFreshStore(t)
	stop := KeepFresh(dir, 20*time.Millisecond, nil, io.Discard)
	defer stop()
	k := keptFresh(dir)
	waitUntil(t, "the first check", k.checkedOnce)
	// Idle: no query yet, so the ticker does not walk.
	before := indexedBytes(t, dir)
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "heron lantern"), claudeLine("s1", "2026-01-01T00:02:00Z", "quartz meadow"))
	k.queried.Store(time.Now().Add(-2 * keepIdleAfter).UnixNano())
	time.Sleep(100 * time.Millisecond)
	if got := indexedBytes(t, dir); got != before {
		t.Fatalf("an idle keeper walked: %d -> %d bytes", before, got)
	}
	if _, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the woken keeper", func() bool { return indexedBytes(t, dir) > before })
}

// A search in the same process holds the lock while it reads. The keeper
// waits it out rather than taking it for a build and calling the index stale.
func TestKeepFreshWaitsOutASearchHoldingTheLock(t *testing.T) {
	dir, _ := keepFreshStore(t)
	var handed atomic.Int32
	stop := KeepFresh(dir, time.Hour, func() { handed.Add(1) }, io.Discard)
	defer stop()
	k := keptFresh(dir)
	waitUntil(t, "the first check", k.checkedOnce)

	unlock, ok, err := tryLockDir(dir)
	if err != nil || !ok {
		t.Fatalf("lock: ok=%v err=%v", ok, err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		unlock()
	}()
	kickKeeper(k)
	if s, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil || s {
		t.Fatalf("a held lock read as a stale index: stale=%v err=%v", s, err)
	}
	if n := handed.Load(); n != 0 {
		t.Fatalf("a held lock was handed to a build %d times", n)
	}

	// Held past the keeper's patience, by a build in another process: the
	// last verdict stands and nothing more is asked for.
	unlock2, ok2, err2 := tryLockDir(dir)
	if err2 != nil || !ok2 {
		t.Fatalf("lock: ok=%v err=%v", ok2, err2)
	}
	k.check(func() { handed.Add(1) }, io.Discard)
	unlock2()
	if s, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil || s {
		t.Fatalf("a build in flight left a stale verdict behind: stale=%v err=%v", s, err)
	}
	if n := handed.Load(); n != 0 {
		t.Fatalf("a build in flight was asked for again %d times", n)
	}
}
