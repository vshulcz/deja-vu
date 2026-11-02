package index

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// steps leaves a recoverable copy instead of nothing (#181).
func swapIndexDir(dir, tmp string) error {
	old := dir + ".old"
	// A copy left parked survived the removal — a leftover from an
	// interrupted swap whose permissions stop deja from clearing it. The bare
	// `rename …: file exists` names two paths nobody chose and gives nothing to
	// do about either (#1009). Decided before the wait: no amount of waiting
	// clears it, and the reader has to act on the message either way.
	//
	// Unless what holds it is a reader. The live log is a hard link of the one
	// in the parked index (append_replace.go), so on Windows any search
	// running as the last swap ended kept that copy from going, and this pass
	// failed on it. That clears once the reader lets go, so the pass parks
	// one name further instead, and the next swap or lock taker removes both.
	if stuck, err := clearParked(dir); stuck != "" {
		if !renameHeldOpen(err) {
			return fmt.Errorf("an earlier index swap left %s behind and deja cannot replace it — remove that directory and run `deja index` again", stuck)
		}
		if old = nextParking(dir); old == "" {
			return fmt.Errorf("earlier index swaps left %s and more behind and deja cannot replace them — remove them and run `deja index` again", stuck)
		}
	}
	carryTombstoneMirror(dir, tmp)
	// The parking step can afford to wait: the index is still where readers
	// look until it succeeds, so nobody is waiting on it, and this is the
	// rename a pass that spent seconds building tmp would otherwise give up on
	// for the sake of a search that held a handle for a quarter of a second.
	if err := renameWaiting(dir, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// From here the index is not where readers look, so this rename and the
	// restore that may follow it share one deadline: the window a reader waits
	// out, spent once between them rather than twice.
	deadline := swapNow().Add(swapRenameWait)
	carried := carryIndexState(old, tmp)
	if err := renameWaitingUntil(tmp, dir, deadline); err != nil {
		for _, name := range carried {
			_ = os.Rename(filepath.Join(tmp, name), filepath.Join(old, name))
		}
		// Put the previous index back rather than leaving nothing, with
		// whatever is left of that window — this runs exactly when renames on
		// this directory are being refused.
		floor := swapNow().Add(restoreRenameFloor)
		if floor.After(deadline) {
			// A little past the window rather than none of it: the restore is
			// the one rename that must not fail, and a second rename that
			// spent the whole budget would otherwise leave it none.
			deadline = floor
		}
		_ = renameWaitingUntil(old, dir, deadline)
		return err
	}
	moveManifestCache(tmp, dir)
	_, _ = clearParked(dir)
	return nil
}

// maxParked bounds the parking spots a swap tries past dir.old.
const maxParked = 16

// removeParked removes a parked index; a test stands where Windows refuses.
var removeParked = os.RemoveAll

// parkedDirs is every parked copy of dir on disk, dir.old first and then
// dir.old.N in the order they were parked.
func parkedDirs(dir string) []string {
	var out []string
	if _, err := os.Lstat(dir + ".old"); err == nil {
		out = append(out, dir+".old")
	}
	for _, n := range parkedNumbers(dir) {
		out = append(out, dir+".old."+strconv.Itoa(n))
	}
	return out
}

// parkedNumbers is the N of every dir.old.N, ascending.
func parkedNumbers(dir string) []int {
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		return nil
	}
	prefix := filepath.Base(dir) + ".old."
	var out []int
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(rest); err == nil && n > 0 && strconv.Itoa(n) == rest {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// clearParked removes every parked copy of dir. A copy still there comes back
// with the reason it stayed, a refusal that is not a reader's first; the
// next parking spot is past it, so the newest copy is always the last.
func clearParked(dir string) (stuck string, _ error) {
	var why error
	for _, p := range parkedDirs(dir) {
		err := removeParked(p)
		if _, statErr := os.Lstat(p); statErr != nil {
			continue
		}
		if err == nil {
			err = fs.ErrExist
		}
		if stuck == "" || (renameHeldOpen(why) && !renameHeldOpen(err)) {
			stuck, why = p, err
		}
	}
	return stuck, why
}

// nextParking is the spot past every parked copy still there, "" when there
// are too many to add one.
func nextParking(dir string) string {
	n := 1
	if ns := parkedNumbers(dir); len(ns) > 0 {
		n = ns[len(ns)-1] + 1
	}
	if n > maxParked {
		return ""
	}
	return dir + ".old." + strconv.Itoa(n)
}

// newestParked is the copy the last swap parked: the highest dir.old.N, as
// a swap parks past every copy still there.
func newestParked(dir string) string {
	ps := parkedDirs(dir)
	if len(ps) == 0 {
		return ""
	}
	return ps[len(ps)-1]
}

// indexState is what lives inside the index directory without being built
// from the transcripts: the hook text still waiting for its session
// (cmd/deja's deferred/). No pass writes it into a new index, so every swap
// deleted it along with the old one (#5052).
var indexState = []string{"deferred"}

// carryIndexState moves indexState from the parked index into the new one and
// returns the names it moved, so a failed swap can put them back. A rename,
// not a copy: the parked directory goes a moment later either way.
func carryIndexState(old, tmp string) []string {
	var moved []string
	for _, name := range indexState {
		if _, err := os.Lstat(filepath.Join(tmp, name)); err == nil {
			continue
		}
		if os.Rename(filepath.Join(old, name), filepath.Join(tmp, name)) == nil {
			moved = append(moved, name)
		}
	}
	return moved
}

// carryTombstoneMirror writes the tombstone mirror into the index about to
// replace dir. Only forget and unforget wrote it, so the first rebuild after a
// forget deleted it, and with the config directory gone too the next rebuild
// brought the session back (#5052). Both copies go in, so a mirror lost
// earlier comes back from the config copy. Best effort, as in writeTombstones:
// an index that cannot hold it still gets swapped.
func carryTombstoneMirror(dir, tmp string) {
	set := readTombstoneFile(tombstonePath())
	for key := range readTombstoneFile(tombstoneMirrorPath(dir)) {
		set[key] = true
	}
	if len(set) == 0 {
		return
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	_ = writeTombstoneMirrorAt(tmp, keys)
}

// renameWaiting is os.Rename with a short wait for a rename another pass is
// holding the directory open against.
//
// Windows refuses to rename a directory while any handle inside it is open, so
// two ordinary passes at once — a hook's warmup and a `deja index` from a shell
// — left the loser unable to swap, and the store a session short until
// something rebuilt it. On Unix the loser renames over the winner and both end
// up consistent, which is why this went unmeasured (#2228). The reader holding
// those handles is finishing a read, not doing work, so the wait is short and
// bounded; a rename still refused at the end is reported rather than retried
// forever, and the caller puts the previous index back.
func renameWaiting(from, to string) error {
	return renameWaitingUntil(from, to, swapNow().Add(parkRenameWait))
}

// renameWaitingUntil is renameWaiting against a deadline the caller keeps, so a
// swap and the restore that follows it share one window rather than each
// getting a fresh one — two full budgets in a row is the index away for twice
// as long as a reader will wait (#2228).
func renameWaitingUntil(from, to string, deadline time.Time) error {
	err := renameFile(from, to)
	for err != nil && renameHeldOpen(err) && swapNow().Before(deadline) {
		swapSleep(swapRenameStep)
		err = renameFile(from, to)
	}
	return err
}

// renameHeldOpen reports whether a refusal is the kind that clears on its own.
// A handle in the directory is; a read-only filesystem, a path that is a file,
// a parking spot deja cannot replace (#1009) are not, and waiting two seconds
// to say so delays a message the reader has to act on either way.
//
// By errno rather than by fs.ErrPermission, which does not discriminate here:
// Windows reports both a sharing refusal and a real ACL denial as
// ERROR_ACCESS_DENIED.
func renameHeldOpen(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch uintptr(errno) {
	case 5, 32, 33: // ACCESS_DENIED, SHARING_VIOLATION, LOCK_VIOLATION
		// Those numbers mean something else on Unix, where the rename does not
		// fail this way at all, so the wait is Windows' alone. A variable
		// rather than runtime.GOOS so a test can stand where Windows is.
		return renameOS == "windows"
	}
	return false
}

// renameOS is runtime.GOOS, indirected for the tests that inject a refusal.
var renameOS = runtime.GOOS

const (
	// swapRenameWait is how long a swap waits out a reader, and it is bounded
	// by what a reader will wait for it: swapWindowTries × swapWindowWait is
	// 200ms, after which a reader stops looking and gets the bare ENOENT that
	// swapInFlight exists to absorb. Waiting longer than that turns one pass
	// losing its swap into every reader losing (#2228).
	//
	// The same refusal atomicfile.publish waits out, measured on windows CI at
	// 20 × 5ms; this is the same order, in the steps the reader side uses.
	swapRenameWait = swapWindowTries * swapWindowWait
	swapRenameStep = swapWindowWait
	// parkRenameWait is the other side of that: the parking step and the
	// crash-recovery restore happen while the index is still (or already)
	// where readers look, so nothing is waiting on them and the only cost of
	// waiting is the pass's own. Long enough to outlast a search holding a
	// handle, short enough that a directory nobody will ever release still
	// reports inside a scheduled run.
	parkRenameWait = 2 * time.Second
	// restoreRenameFloor is what the restore gets when the rename before it
	// spent the shared window. Three attempts, which is what the refusal this
	// waits out clears in.
	restoreRenameFloor = 3 * swapRenameStep
)

// renameFile is os.Rename, indirected so a test can refuse a rename the way
// Windows does without needing Windows.
var renameFile = os.Rename

// swapNow and swapSleep are the swap's clock, indirected so a test can hold
// the waits to what the swap asks for: measured on a shared runner, the same
// swap was 240ms one run and 306ms the next (#4160).
var (
	swapNow   = time.Now
	swapSleep = time.Sleep
)

// recoverIndexDir finishes an interrupted swapIndexDir: if the index dir is
// missing but its .old sibling survives, restore it. A log a killed pass was
// appending to is cut back to what the manifest committed.
func recoverIndexDir(dir string) {
	if dir == "" {
		return
	}
	if _, err := os.Stat(dir); err == nil {
		_, _ = clearParked(dir)
		recoverAppendedTail(dir)
		recoverScrub(dir)
		return
	}
	if p := newestParked(dir); p != "" {
		// The same wait: this is the crash-recovery path, and a refusal here
		// leaves the index missing for the whole run.
		_ = renameWaiting(p, dir)
	}
	recoverAppendedTail(dir)
	recoverScrub(dir)
}
