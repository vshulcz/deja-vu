package index

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Every postings read starts with the bucket's directory, the list of the
// tokens in that file, and each read parsed it off disk again: on a
// 19,000-session store that was 18 MB of allocation and a few percent of the
// CPU of every recall, for files that change only when the index is written.
// A long-lived process keeps them here.
//
// An entry is the directory of one file, and is used only for that file: the
// reader opens the file it reads from anyway, and a stat of that handle that
// does not match the entry's sends it back to the disk. A rebuild renames
// every file it writes into place, so a new file is a new identity, and a word
// blanked in place changes the time. On top of that the whole cache is
// dropped whenever the manifest cache takes another generation of the index
// (manifest_cache.go): a publish here or in another process, so directories of
// files no generation names are not kept.
//
// The entries are shared: callers must not modify them.

// cachedBucketDir is one bucket file's directory and the file it was read
// from.
type cachedBucketDir struct {
	stat    os.FileInfo
	entries []bucketEntry
	// sorted says the entries are in token order, as writeBucket leaves them,
	// so a lookup can halve. A word blanked in place breaks the order.
	sorted bool
}

// find is the entry of tok.
func (d *cachedBucketDir) find(tok string) (bucketEntry, bool) {
	if d.sorted {
		i, ok := slices.BinarySearchFunc(d.entries, tok, func(e bucketEntry, t string) int { return strings.Compare(e.tok, t) })
		if ok {
			return d.entries[i], true
		}
		return bucketEntry{}, false
	}
	for _, e := range d.entries {
		if e.tok == tok {
			return e, true
		}
	}
	return bucketEntry{}, false
}

// bucketDirOf is the directory of the bucket file f, just opened from p: the
// cached one while f is the file it was read from, read off f otherwise.
func bucketDirOf(p string, f *indexBlobFile) (*cachedBucketDir, error) {
	dir := filepath.Dir(filepath.Dir(p))
	if err := ensureVaultUnlocked(dir); err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	norm := normSnapshotDir(dir)
	warmFileIdentity(fi)
	snapshots.mu.Lock()
	snap := snapshots.getOrCreateLocked(norm)
	if d := snap.byPath[p]; d != nil && sameFileVersion(d.stat, fi) {
		snapshots.touchLocked(norm)
		snapshots.mu.Unlock()
		return d, nil
	}
	wantEpoch := snap.epoch
	snapshots.mu.Unlock()
	entries, err := readBucketDir(f, fi)
	if err != nil {
		return nil, err
	}
	d := &cachedBucketDir{stat: fi, entries: entries, sorted: slices.IsSortedFunc(entries, func(a, b bucketEntry) int { return strings.Compare(a.tok, b.tok) })}
	storeBucketDirAtEpoch(dir, wantEpoch, p, d)
	return d, nil
}

// sameFileVersion reports whether two stats saw the same file, unchanged: the
// same file, not one of the same size, since a rebuild renames a new bucket in
// and a coarse clock can give it the old time.
func sameFileVersion(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// dropBucketDirs forgets the cached directories of the index in dir.
func dropBucketDirs(dir string) {
	norm := normSnapshotDir(dir)
	if norm == "" {
		return
	}
	snapshots.mu.Lock()
	snap := snapshots.getOrCreateLocked(norm)
	snap.byPath = nil
	snap.catalogOK, snap.catalogIdx, snap.catalogSig = false, nil, ""
	snap.cooccurStat, snap.cooccurMap = nil, nil
	snap.epoch = snapshots.nextEpochLocked()
	snapshots.touchLocked(norm)
	snapshots.mu.Unlock()
}
