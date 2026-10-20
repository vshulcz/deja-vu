package index

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// A pass that replaces a few sessions wrote the whole store again: on 25,000
// sessions, 9 changed transcripts cost 570 MB of records.bin, 208 MB of
// postings, 2.5 GB of memory and every search's disk for the length of it
// (#5387). It stages its index in a new directory as before, but the new
// directory starts as hard links to the old files: the replaced sessions'
// records are appended to the log both directories share, the records they
// replace are listed as dead in the new manifest, and the new postings go into
// overlays. Each session the pass writes is filed under a key id new to the
// string table, so a reader still holding the old manifest resolves the
// appended tail to no session and passes over it; the swap is the commit, as
// it always was.
//
// The log is written whole again, without its dead records, when they come to
// a quarter of it, and on any pass where a hard link is refused.

// appendPendingFile marks a live index whose log a pass is appending to. It
// holds the size the log had before, which an interrupted pass leaves it
// longer than; the next lock taker cuts it back rather than rebuilding.
const appendPendingFile = "append.pending"

// deadShareCompact is the share of the log in dead records past which the next
// replacing pass writes the log again.
const deadShareCompact = 4

// appendReplacing turns the append off, for a test's control.
var appendReplacing = true

// beforeAppendSwap, when set, runs before an appending pass swaps its index
// in; a test fails the pass there.
var beforeAppendSwap func() error

// appendsInPlace stages tmp from dir's files and reports whether the pass may
// append to the log. False leaves tmp empty, and the pass writes the log anew.
func appendsInPlace(dir, tmp string, old Manifest) bool {
	if !appendReplacing || old.RecordsSize <= 0 || old.DeadBytes*deadShareCompact > old.RecordsSize {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil || fi.Size() != old.RecordsSize {
		return false
	}
	undo := func() bool {
		_ = os.RemoveAll(tmp)
		_ = os.MkdirAll(filepath.Join(tmp, "buckets"), 0o700)
		return false
	}
	if os.Link(filepath.Join(dir, "records.bin"), filepath.Join(tmp, "records.bin")) != nil {
		return undo()
	}
	// Before the first byte is appended: a pass killed after it leaves the
	// mark behind.
	if writeFileAtomic(filepath.Join(dir, appendPendingFile), []byte(strconv.FormatInt(old.RecordsSize, 10))) != nil {
		return undo()
	}
	return true
}

// createLog creates a record log at p that is a file of its own. Truncating
// whatever is there would write through a hard link a pass that never finished
// left in its staging directory, onto the live log.
func createLog(p string) (*os.File, error) {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}

// undoAppend cuts the log in dir back to what its manifest committed, after a
// pass that appended to it did not swap its index in.
func undoAppend(dir string, size int64) {
	p := filepath.Join(dir, "records.bin")
	if fi, err := os.Stat(p); err == nil && fi.Size() > size {
		_ = os.Truncate(p, size)
	}
	_ = os.Remove(filepath.Join(dir, appendPendingFile))
}

// recoverAppendedTail undoes what a pass killed while appending left in dir.
// Only a lock holder calls it, and the mark means no swap followed the
// append, so the bytes past the mark's size are that pass's and nobody's
// else: any other writer takes the lock first and runs this before it.
func recoverAppendedTail(dir string) {
	b, err := os.ReadFile(filepath.Join(dir, appendPendingFile))
	if err != nil {
		return
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || size <= 0 {
		_ = os.Remove(filepath.Join(dir, appendPendingFile))
		return
	}
	undoAppend(dir, size)
}

// mergeDead joins two ascending offset lists that share none.
func mergeDead(a, b []int64) []int64 {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	out := make([]int64, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] < b[j] {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	return append(out, b[j:]...)
}
