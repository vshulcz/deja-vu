package peers

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	// lockWait is how long a sync waits for another one to finish writing the
	// list. The file is small and written once per exchange, so a wait this
	// long only happens when something is wrong.
	lockWait = 2 * time.Second
	// lockStale is when a lock is treated as left behind by a process that
	// died. Nothing holds this lock across a network call — it is taken around
	// a read, an edit and a write of one small file.
	lockStale = 30 * time.Second
	// lockPoll is how often the wait looks again.
	lockPoll = 5 * time.Millisecond
)

// withLock serialises a read-modify-write of the peers file.
//
// Record reads the whole list, edits it and writes it back, so two syncs
// finishing at once kept only the last writer's row — and a lost row is a
// machine deja stops syncing with, silently (#1883).
//
// A sync is never failed for want of the lock: waiting is bounded, a lock left
// by a dead process is taken over, and a directory that cannot hold one at all
// (a read-only config mount) runs the write unlocked, which is what deja did
// before this existed.
func withLock(fn func() error) error {
	if Path() == "" {
		// Nowhere to keep the list means nowhere to keep its lock, and
		// creating the directory anyway is what put a `.config` in whatever
		// checkout the command ran in (#2790).
		return errors.New("no home directory, so there is nowhere to keep the peer list")
	}
	path := Path() + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fn()
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			defer func() { _ = os.Remove(path) }()
			return fn()
		}
		if !errors.Is(err, fs.ErrExist) {
			return fn()
		}
		if fi, serr := os.Stat(path); serr == nil && time.Since(fi.ModTime()) > lockStale {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			// Better a write that races than a sync that refuses to record what
			// it just did: the exchange has already happened by the time this
			// runs.
			return fn()
		}
		time.Sleep(lockPoll)
	}
}
