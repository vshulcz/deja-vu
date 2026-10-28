package index

import (
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A long-lived reader (the MCP server) keeps its index fresh on a timer
// instead of before every query. Seeing what changed means a stat of every
// transcript on the machine, and that walk was 21% of a recall's CPU: 93 ms a
// query on a 19k-session store, growing with the store. With a keeper running,
// a query reads the index as it is and takes the last verdict the keeper
// reached; the walk, the inline append and the hand-off to a detached build
// all happen on the keeper's goroutine.

// keepIdleAfter is how long after the last query the keeper keeps walking. An
// idle server does no work; the first query after a long quiet spell wakes it.
const keepIdleAfter = 10 * time.Minute

// keepLockPatience is how long a check waits for the index lock before it
// takes the holder for a build and calls the index stale, as a query does.
const keepLockPatience = 3 * time.Second

type freshKeeper struct {
	dir     string
	every   time.Duration
	kick    chan struct{}
	queried atomic.Int64 // unix nanos of the last query served

	mu      sync.Mutex
	checked time.Time
	stale   bool
	pending int
	found   int
	stamp   string // manifest stamp the stale verdict was reached against
}

var keeper atomic.Pointer[freshKeeper]

// KeepFresh moves the freshness check for dir off the query path for as long
// as it runs: it checks once now and then every interval while queries keep
// coming, and calls onStale when the index needs a build the keeper should not
// run itself (anything beyond a short append). The returned stop waits for the
// keeper to finish. One keeper per process; a second call while one runs
// returns a no-op stop.
func KeepFresh(dir string, every time.Duration, onStale func(), progress io.Writer) (stop func()) {
	if dir == "" {
		dir = DefaultDir()
	}
	k := &freshKeeper{dir: dir, every: every, kick: make(chan struct{}, 1)}
	if !keeper.CompareAndSwap(nil, k) {
		return func() {}
	}
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		k.check(onStale, progress)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
				if time.Since(time.Unix(0, k.queried.Load())) < keepIdleAfter {
					k.check(onStale, progress)
				}
			case <-k.kick:
				k.check(onStale, progress)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(quit)
			<-done
			keeper.CompareAndSwap(k, nil)
		})
	}
}

func (k *freshKeeper) check(onStale func(), progress io.Writer) {
	// No index yet, or one that must be rebuilt before it answers: the first
	// query builds it, as it did before there was a keeper.
	if _, ok := servable(k.dir); autoUpdatesOff() || !ok {
		return
	}
	// Patient with the lock: a search in this same process holds it while it
	// reads. Held past that, a build is running; the last verdict stands and
	// the next tick looks again. Calling it stale here outlived the build and
	// asked for another one once it was done.
	unlock, ok, err := tryLockDirFor(k.dir, keepLockPatience)
	if err != nil || !ok {
		return
	}
	stale, pending, found, err := staleLocked(k.dir, query.Options{}, progress, func(string) {})
	stamp := ""
	if err != nil {
		stale, pending, found = false, 0, 0
	} else if stale {
		stamp = Stamp(k.dir)
	}
	unlock()
	k.mu.Lock()
	k.checked, k.stale, k.pending, k.found, k.stamp = time.Now(), stale, pending, found, stamp
	k.mu.Unlock()
	if stale && onStale != nil {
		onStale()
	}
}

// verdict is what a query takes instead of walking: whether the index was
// behind the stores at the last check. A stale verdict lasts until the index
// is next written, so a build that finished is not asked for again. A query
// that finds the verdict older than two intervals (the machine slept, or the
// keeper idled) wakes the keeper and answers with what it has. No verdict yet
// means the first check is still running, or the index could not answer then
// and the query built it.
func (k *freshKeeper) verdict() (bool, int, int) {
	k.queried.Store(time.Now().UnixNano())
	k.mu.Lock()
	checked, stale, pending, found, stamp := k.checked, k.stale, k.pending, k.found, k.stamp
	k.mu.Unlock()
	if !checked.IsZero() && time.Since(checked) > 2*k.every {
		select {
		case k.kick <- struct{}{}:
		default:
		}
	}
	if stale && Stamp(k.dir) != stamp {
		return false, 0, 0
	}
	return stale, pending, found
}

// tryLockDirFor is tryLockDir, tried again every 50 ms for up to patience.
func tryLockDirFor(dir string, patience time.Duration) (func(), bool, error) {
	deadline := time.Now().Add(patience)
	for {
		unlock, ok, err := tryLockDir(dir)
		if ok || err != nil || !time.Now().Before(deadline) || lockUnwritable(dir) {
			return unlock, ok, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// servable reads the manifest of an index that can answer as it is.
func servable(dir string) (Manifest, bool) {
	m, err := readManifestCached(dir)
	return m, err == nil && m.Scope == "" && recordsIntact(dir, m) && !mustRebuildBeforeAnswering(m, version)
}

// keptFresh returns the keeper for dir, or nil when queries on it must walk.
func keptFresh(dir string) *freshKeeper {
	if k := keeper.Load(); k != nil && k.dir == dir {
		return k
	}
	return nil
}
