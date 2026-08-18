// Package atomicfile replaces a file's contents in one step, for the writers
// that are not behind a lock.
//
// Writing a temp file and renaming it is the shape deja already uses
// everywhere, and it holds as long as one writer is doing it. The temp name was
// derived from the destination, so two writers shared it: the second truncates
// what the first is still writing, the first renames a half-written file into
// place, and a reader gets a record that parses as nothing. Measured on the
// warmup status file, which two agents starting together both write: 180 of
// 18049 reads came back unparseable, and every one of those told the agent no
// build was running while one was (#1319).
//
// The index's own writers are excluded from this by `lockDir`, which is why
// they can keep their fixed temp names.
package atomicfile

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// publish renames the temp file into place, retrying briefly.
//
// Windows refuses a rename onto a file another handle has open, and a reader is
// exactly what these files have — the warmup status is polled by every surface
// while a build writes it. Measured on windows CI: three of four concurrent
// writers were denied. Unix succeeds on the first attempt and pays nothing.
func publish(tmp, path string) error {
	err := os.Rename(tmp, path)
	for i := 0; err != nil && i < renameTries; i++ {
		time.Sleep(renameWait)
		err = os.Rename(tmp, path)
	}
	return err
}

const (
	renameTries = 20
	renameWait  = 5 * time.Millisecond
)

// sweptDirs remembers the directories this process has already tidied, so the
// sweep below costs one listing per directory per run rather than one per
// write — the warmup status is written four times a second.
var sweptDirs sync.Map

// sweepStale removes temp files this package left behind. A fixed temp name was
// reused by the next writer and so cleaned itself up; a unique one is not, and
// a process killed between creating it and renaming it leaves a file nothing
// would ever look at again. An hour is well past any write: these are one
// buffer and one rename apart.
func sweepStale(dir, base string) {
	if _, done := sweptDirs.LoadOrStore(dir+"\x00"+base, true); done {
		return
	}
	matches, err := filepath.Glob(filepath.Join(dir, "."+base+".tmp-*"))
	if err != nil {
		return
	}
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && time.Since(fi.ModTime()) > time.Hour {
			_ = os.Remove(m)
		}
	}
}

// WriteStream is Write for content too large to hold twice: the embedding
// sidecar is megabytes of vectors, and buffering it to hand over as one slice
// would double that for no gain. fn writes the whole file; if it returns an
// error nothing is published.
func WriteStream(path string, perm os.FileMode, fn func(io.Writer) error) error {
	defer sweepStale(filepath.Dir(path), filepath.Base(path))
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := fn(f); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := publish(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Write replaces path with b. The temp file is created beside it, so the rename
// stays on one filesystem, and its name is unique, so concurrent writers do not
// share it. On any failure the temp file is removed rather than left on a disk
// that may have just filled up.
func Write(path string, b []byte, perm os.FileMode) error {
	defer sweepStale(filepath.Dir(path), filepath.Base(path))
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := publish(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
