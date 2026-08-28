package usage

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	// logLockWait is how long a writer waits for another one to finish
	// rewriting a log. Both rewrites — a rotation and a forget sweep — are one
	// pass over a file bounded to a few hundred kilobytes, so a wait this long
	// only happens when something is wrong.
	logLockWait = time.Second
	// logLockStale is when a lock counts as left behind by a process that
	// died. Nothing holds it across anything that can block.
	logLockStale = 10 * time.Second
	// logLockPoll is how often the wait looks again.
	logLockPoll = 2 * time.Millisecond
)

// withLogLock serialises one process's write of a log against another's
// rewrite of the same file.
//
// The logs are append-only precisely so that agents can write to them at once,
// and the two operations that are not appends — the rotation and the forget
// sweep — read the whole file and write it back. An injection appended in that
// window was written over: a record of memory an agent had already received,
// gone with nothing to show it (#2413).
//
// A write is never refused for want of the lock. Waiting is bounded, a lock
// left by a dead process is taken over, and a directory that cannot hold one
// runs the write unlocked, which is what deja did before this existed — a
// racing write beats a lost injection.
func withLogLock(path string, fn func()) {
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		fn()
		return
	}
	deadline := time.Now().Add(logLockWait)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			defer func() { _ = os.Remove(lock) }()
			fn()
			return
		}
		if !errors.Is(err, fs.ErrExist) {
			fn()
			return
		}
		if fi, serr := os.Stat(lock); serr == nil && time.Since(fi.ModTime()) > logLockStale {
			_ = os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			fn()
			return
		}
		time.Sleep(logLockPoll)
	}
}
