package index

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// A rebuild is two renames, and between them the index directory does not
// exist. A reader that lands in that window — an agent recalling while
// `deja index --rebuild` runs in another terminal, which is the ordinary case —
// got a raw ENOENT naming a file it never chose: `open …/index.db/manifest.gob:
// no such file or directory` (#1317). Nothing is corrupted and no answer is
// wrong; what is wrong is the message.
//
// So a reader waits the window out instead of reporting it, and only while a
// swap is actually in flight. Everything else — a missing index, a deleted
// file, a typo in DEJA_INDEX_DIR — keeps failing at once with the message it
// already has.
const (
	swapWindowTries = 10
	swapWindowWait  = 20 * time.Millisecond
)

// openIndexFile is os.Open for a file inside the index directory.
func openIndexFile(path string) (*os.File, error) {
	f, err := os.Open(path)
	// errors.Is rather than os.IsNotExist: Windows reports a directory caught
	// mid-rename as a sharing violation, and the wrapped form is the one that
	// keeps matching as the standard library grows cases.
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return f, err
	}
	// Observe the state first, then try the file. Both orders were wrong the
	// other way round: checking after a failed open gave up on a swap that had
	// just finished, so the reader was handed the very ENOENT this exists to
	// absorb. Reading the state first makes the miss decidable — if no swap was
	// in flight when we looked and the file is still not there, it is not
	// coming.
	for i := 0; i < swapWindowTries; i++ {
		inFlight := swapInFlight(path)
		if f, oerr := os.Open(path); oerr == nil {
			return f, nil
		}
		if !inFlight {
			return nil, err
		}
		time.Sleep(swapWindowWait)
	}
	return nil, err
}

// statIndexFile is os.Stat for a file inside the index directory, waiting out a
// swap the same way openIndexFile does.
//
// "Is there an index" is asked by every hook before it serves anything, and
// during the swap the answer was no — measured at 894 of 18912 asks while
// rebuilds ran. The hook then asked for another rebuild and returned without
// recall, so a prompt submitted in that window lost its memory because a
// directory was missing for a millisecond (#1319).
func statIndexFile(path string) (os.FileInfo, error) {
	fi, err := os.Stat(path)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return fi, err
	}
	for i := 0; i < swapWindowTries; i++ {
		inFlight := swapInFlight(path)
		if fi, serr := os.Stat(path); serr == nil {
			return fi, nil
		}
		if !inFlight {
			return nil, err
		}
		time.Sleep(swapWindowWait)
	}
	return nil, err
}

// swapInFlight reports whether a directory on the way to path has been renamed
// aside and not yet replaced: gone from where it belongs, and sitting at its
// ".old" parking spot.
//
// Both halves are needed. The parking spot alone survives a crashed swap until
// the next build clears it, and waiting on that would tax every read on a
// machine whose index is otherwise fine. Two levels cover records.bin and
// manifest.gob directly in the index directory, and the bucket files one level
// below it.
func swapInFlight(path string) bool {
	d := filepath.Dir(path)
	for i := 0; i < 2; i++ {
		if _, err := os.Stat(d + ".old"); err == nil {
			if _, err := os.Stat(d); errors.Is(err, fs.ErrNotExist) {
				return true
			}
		}
		d = filepath.Dir(d)
	}
	return false
}
