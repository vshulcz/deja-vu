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
	if !swapInFlight(path) {
		return nil, err
	}
	for i := 0; i < swapWindowTries; i++ {
		time.Sleep(swapWindowWait)
		if f, oerr := os.Open(path); oerr == nil {
			return f, nil
		}
		// The swap finished between that open and this check, so the file is
		// there now and the open above just missed it. Giving up here handed
		// back the ENOENT this function exists to avoid — caught by CI, where
		// the slower runner landed in that gap most times.
		if !swapInFlight(path) {
			return os.Open(path)
		}
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
