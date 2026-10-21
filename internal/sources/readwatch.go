package sources

import (
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A transcript read can hang outright: a network mount that stopped answering,
// a synced folder holding a placeholder it will not fetch. The store went on
// printing "still reading" and the run never ended (#5396). Only SQLite stores
// had a bound. A read that keeps moving is left alone however long it takes;
// one that has moved no bytes for the store read budget (DEJA_STORE_TIMEOUT)
// is given up, named as unreadable, and the rest of the run goes on.
var (
	watchMu sync.Mutex
	// watched holds, per path, the progress marks of the reads in flight.
	watched = map[string]map[*int64]struct{}{}
	// stalledDirs counts, per directory, given-up reads still blocked there:
	// what stopped answering is usually the storage, and every further file
	// there would cost the whole budget again. Cleared as they return.
	stalledDirs = map[string]int{}
	// abandoned are paths whose given-up read may still finish; what it then
	// reports belongs to no pass.
	abandoned = map[string]int{}
	// watching counts the watches in flight, so a read outside any of them
	// takes no lock.
	watching atomic.Int64
)

// StalledRead is the error for a read given up for making no progress.
type StalledRead struct {
	Path string
	Idle time.Duration
}

func (e *StalledRead) Error() string {
	if e.Idle == 0 {
		return fmt.Sprintf("read %s: skipped, a read in the same directory stopped moving; DEJA_STORE_TIMEOUT=0 waits", e.Path)
	}
	return fmt.Sprintf("read %s: no progress for %s, skipped; DEJA_STORE_TIMEOUT=0 waits", e.Path, e.Idle)
}

// ReadWatched runs parse for path and gives up on it once its reads have made
// no progress for the store read budget, recording the path as unreadable.
func ReadWatched(path string, parse func() ([]model.Session, error)) ([]model.Session, error) {
	ss, err := Watch(path, parse)
	if _, ok := err.(*StalledRead); ok {
		diagFileError(path, err)
	}
	return ss, err
}

// Watch runs fn for path under the store read budget, counted from the last
// byte read through a watched reader. A read given up keeps its goroutine
// until the call it is blocked in returns; nothing waits for it, and what it
// reports after that is dropped. Until it returns, other reads in the same
// directory are given up at once.
func Watch[T any](path string, fn func() (T, error)) (T, error) {
	var zero T
	budget := storeReadBudget()
	if budget <= 0 {
		return fn()
	}
	dir := filepath.Dir(path)
	watchMu.Lock()
	if stalledDirs[dir] > 0 {
		watchMu.Unlock()
		return zero, &StalledRead{Path: path}
	}
	mark := new(int64)
	*mark = time.Now().UnixNano()
	if watched[path] == nil {
		watched[path] = map[*int64]struct{}{}
	}
	watched[path][mark] = struct{}{}
	watching.Add(1)
	watchMu.Unlock()
	unwatch := func() {
		watchMu.Lock()
		watching.Add(-1)
		delete(watched[path], mark)
		if len(watched[path]) == 0 {
			delete(watched, path)
		}
		watchMu.Unlock()
	}
	type result struct {
		v     T
		err   error
		panic any
	}
	done := make(chan result, 1)
	var gaveUp, finished bool // under watchMu
	go func() {
		var r result
		defer func() {
			if p := recover(); p != nil {
				r.panic = p
			}
			watchMu.Lock()
			finished = true
			if gaveUp {
				if abandoned[path]--; abandoned[path] <= 0 {
					delete(abandoned, path)
				}
				if stalledDirs[dir]--; stalledDirs[dir] <= 0 {
					delete(stalledDirs, dir)
				}
			}
			watchMu.Unlock()
			done <- r
		}()
		r.v, r.err = fn()
	}()
	tick := min(max(budget/10, 10*time.Millisecond), 5*time.Second)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case r := <-done:
			unwatch()
			if r.panic != nil {
				panic(r.panic)
			}
			return r.v, r.err
		case <-t.C:
			watchMu.Lock()
			idle := time.Since(time.Unix(0, *mark))
			// A read that returned just as the budget ran out is taken from
			// done on the next turn, not given up.
			if idle >= budget && !finished {
				gaveUp = true
				watching.Add(-1)
				abandoned[path]++
				stalledDirs[dir]++
				delete(watched[path], mark)
				if len(watched[path]) == 0 {
					delete(watched, path)
				}
				watchMu.Unlock()
				return zero, &StalledRead{Path: path, Idle: budget}
			}
			watchMu.Unlock()
		}
	}
}

// isAbandoned reports a path whose given-up read is still running.
func isAbandoned(path string) bool {
	watchMu.Lock()
	defer watchMu.Unlock()
	return abandoned[path] > 0
}

// watchedReader marks progress on every watch of path as bytes arrive.
func watchedReader(path string, r io.Reader) io.Reader {
	return progressReader{r: r, path: path}
}

type progressReader struct {
	r    io.Reader
	path string
}

func (p progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		markProgress(p.path)
	}
	return n, err
}

// markProgress moves every watch of path to now.
func markProgress(path string) {
	if watching.Load() == 0 {
		return
	}
	now := time.Now().UnixNano()
	watchMu.Lock()
	for m := range watched[path] {
		*m = now
	}
	watchMu.Unlock()
}
