package main

import (
	"bytes"
	"errors"
	"os"
	"sync"
)

// A client can save its config while deja is editing it: Claude Code rewrites
// ~/.claude.json while it runs, and `deja install` is often run from inside
// it. writeIfChanged builds the new file from what the writer read and renames
// it into place, so a save that landed in between was put back by the rename,
// silently (#4561). The install lock only orders deja against deja, and a lock
// file the client does not honour orders nothing.
//
// So the file is read again just before it is replaced. If it is no longer what
// the writer read, nothing is written and the target is edited again from the
// client's version.

// readBytes is what readConfig last read of each path during an install or
// uninstall run, as it was on disk; nil for a file that was not there. Outside
// a run — doctor, the MCP server — nothing is kept.
var (
	readBytes   map[string][]byte
	readBytesMu sync.Mutex
)

func rememberRead(path string, b []byte, absent bool) {
	readBytesMu.Lock()
	defer readBytesMu.Unlock()
	if readBytes == nil {
		return
	}
	if absent {
		readBytes[path] = nil
		return
	}
	readBytes[path] = append([]byte{}, b...)
}

func forgetRead(path string) {
	readBytesMu.Lock()
	defer readBytesMu.Unlock()
	delete(readBytes, path)
}

// errConfigChanged is the target-level signal to edit again.
var errConfigChanged = errors.New("changed while deja was editing it")

type configChangedError struct{ path string }

func (e configChangedError) Error() string {
	return e.path + " changed while deja was editing it, and deja left the new version alone — run the command again"
}

func (e configChangedError) Is(target error) bool { return target == errConfigChanged }

// changedSinceRead reports whether path is no longer what readConfig read,
// when that read is what the write was built from. A writer that read the file
// some other way, or a path never read, has nothing to compare against.
func changedSinceRead(path string, old []byte) error {
	readBytesMu.Lock()
	was, ok := readBytes[path]
	readBytesMu.Unlock()
	if !ok || !bytes.Equal(bytes.TrimPrefix(was, utf8BOM), old) {
		return nil
	}
	now, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if was == nil {
			return nil
		}
	case err != nil:
		return nil //nolint:nilerr // an unreadable file has nothing to compare against
	case was != nil && bytes.Equal(now, was):
		return nil
	}
	return configChangedError{path}
}

// configRaceRetries is how many times a target is edited again after a client
// saved under it. A client that saves on every keystroke wins in the end, and
// the run says so.
const configRaceRetries = 3

// beforeConfigReplace runs just before writeIfChanged puts its file in place.
// Tests use it to stand in for a client saving the same config at that moment.
var beforeConfigReplace = func(string) {}
