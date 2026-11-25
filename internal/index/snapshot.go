package index

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// maxCachedSnapshots bounds how many distinct index directories stay warm in memory.
const maxCachedSnapshots = 4

// indexSnapshot holds all cached manifest and derived state for one index
// directory at a single monotonic epoch.
type indexSnapshot struct {
	epoch uint64

	fi         os.FileInfo
	sessions   os.FileInfo
	m          Manifest
	manifestOK bool
	views      *metaViews

	catalogSig string
	catalogIdx *tokenIndex
	catalogOK  bool

	byPath map[string]*cachedBucketDir

	cooccurStat os.FileInfo
	cooccurMap  map[string][]string

	fixesFI    os.FileInfo
	fixesPairs []FixPair
	fixesOK    bool

	fileCountTarget string
	fileCountMtime  int64
	fileCount       int
	fileCountOK     bool
}

func (s *indexSnapshot) clearDerived() {
	s.catalogSig, s.catalogIdx, s.catalogOK = "", nil, false
	s.byPath = nil
	s.cooccurStat, s.cooccurMap = nil, nil
	s.fixesFI, s.fixesPairs, s.fixesOK = nil, nil, false
	s.fileCountTarget, s.fileCountMtime, s.fileCount, s.fileCountOK = "", 0, 0, false
}

func (s *indexSnapshot) clearAll() {
	s.fi, s.sessions, s.m, s.manifestOK, s.views = nil, nil, Manifest{}, false, nil
	s.clearDerived()
}

func (s *indexSnapshot) hasDerived() bool {
	return s.catalogOK ||
		len(s.byPath) > 0 ||
		s.cooccurStat != nil ||
		s.cooccurMap != nil ||
		s.fixesOK ||
		s.fileCountOK
}

// snapshotStore manages per-directory indexSnapshot entries and monotonic epochs.
type snapshotStore struct {
	mu    sync.Mutex
	seq   uint64
	order []string
	byDir map[string]*indexSnapshot
}

// snapshots is the unified per-directory cache store for internal/index.
var snapshots = snapshotStore{byDir: map[string]*indexSnapshot{}}

func normSnapshotDir(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Clean(dir)
}

func indexDirForBucketsPath(p string) (indexDir, target string) {
	clean := normSnapshotDir(p)
	if clean == "" {
		return "", ""
	}
	if filepath.Base(clean) == "buckets" {
		return filepath.Dir(clean), clean
	}
	return clean, clean
}

func (s *snapshotStore) nextEpochLocked() uint64 {
	s.seq++
	return s.seq
}

func (s *snapshotStore) touchLocked(norm string) {
	if norm == "" {
		return
	}
	for i, d := range s.order {
		if d == norm {
			if i == len(s.order)-1 {
				return
			}
			copy(s.order[i:], s.order[i+1:])
			s.order[len(s.order)-1] = norm
			return
		}
	}
	s.order = append(s.order, norm)
	for len(s.order) > maxCachedSnapshots {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.byDir, oldest)
	}
}

func (s *snapshotStore) getOrCreateLocked(norm string) *indexSnapshot {
	if norm == "" {
		return &indexSnapshot{epoch: s.nextEpochLocked()}
	}
	if snap := s.byDir[norm]; snap != nil {
		return snap
	}
	snap := &indexSnapshot{epoch: s.nextEpochLocked()}
	s.byDir[norm] = snap
	s.touchLocked(norm)
	return snap
}

func snapshotEpoch(dir string) uint64 {
	norm := normSnapshotDir(dir)
	if norm == "" {
		return 0
	}
	snapshots.mu.Lock()
	defer snapshots.mu.Unlock()
	snap := snapshots.byDir[norm]
	if snap == nil {
		return 0
	}
	return snap.epoch
}

func invalidateSnapshot(dir string) {
	norm := normSnapshotDir(dir)
	if norm == "" {
		return
	}
	snapshots.mu.Lock()
	snap := snapshots.getOrCreateLocked(norm)
	snap.clearAll()
	snap.epoch = snapshots.nextEpochLocked()
	snapshots.touchLocked(norm)
	snapshots.mu.Unlock()
}

func invalidateDerivedSnapshot(dir string) {
	norm := normSnapshotDir(dir)
	if norm == "" {
		return
	}
	snapshots.mu.Lock()
	snap := snapshots.getOrCreateLocked(norm)
	snap.clearDerived()
	snap.epoch = snapshots.nextEpochLocked()
	snapshots.touchLocked(norm)
	snapshots.mu.Unlock()
}

func moveSnapshot(tmp, dir string) {
	src := normSnapshotDir(tmp)
	dst := normSnapshotDir(dir)
	snapshots.mu.Lock()
	defer snapshots.mu.Unlock()
	var srcSnap *indexSnapshot
	if src != "" {
		srcSnap = snapshots.byDir[src]
		delete(snapshots.byDir, src)
		if i := slices.Index(snapshots.order, src); i >= 0 {
			snapshots.order = slices.Delete(snapshots.order, i, i+1)
		}
	}
	if dst == "" {
		return
	}
	dstSnap := snapshots.getOrCreateLocked(dst)
	dstSnap.clearAll()
	if srcSnap != nil && srcSnap.manifestOK {
		dstSnap.fi = srcSnap.fi
		dstSnap.sessions = srcSnap.sessions
		dstSnap.m = srcSnap.m
		dstSnap.manifestOK = true
		dstSnap.views = srcSnap.views
	}
	dstSnap.epoch = snapshots.nextEpochLocked()
	snapshots.touchLocked(dst)
}

func invalidateFixesFor(dir string) {
	norm := normSnapshotDir(dir)
	if norm == "" {
		return
	}
	snapshots.mu.Lock()
	snap := snapshots.getOrCreateLocked(norm)
	snap.fixesFI, snap.fixesPairs, snap.fixesOK = nil, nil, false
	snap.epoch = snapshots.nextEpochLocked()
	snapshots.touchLocked(norm)
	snapshots.mu.Unlock()
}

func invalidateAllFixes() {
	snapshots.mu.Lock()
	for _, snap := range snapshots.byDir {
		snap.fixesFI, snap.fixesPairs, snap.fixesOK = nil, nil, false
		snap.epoch = snapshots.nextEpochLocked()
	}
	snapshots.mu.Unlock()
}

func resetAllSnapshots() {
	snapshots.mu.Lock()
	snapshots.order = nil
	snapshots.byDir = map[string]*indexSnapshot{}
	snapshots.mu.Unlock()
}

func resetAllSnapshotsForTest() {
	resetAllSnapshots()
}

func cachedViewsFor(m Manifest) *metaViews {
	snapshots.mu.Lock()
	for _, d := range slices.Backward(snapshots.order) {
		snap := snapshots.byDir[d]
		if snap != nil && snap.manifestOK && snap.views != nil && sameTable(snap.views.sessions, m.Sessions) {
			v := snap.views
			snapshots.mu.Unlock()
			return v
		}
	}
	snapshots.mu.Unlock()
	return &metaViews{sessions: m.Sessions}
}

func cachedBucketDirs(dir string) map[string]*cachedBucketDir {
	norm := normSnapshotDir(dir)
	if norm == "" {
		return nil
	}
	snapshots.mu.Lock()
	defer snapshots.mu.Unlock()
	snap := snapshots.byDir[norm]
	if snap == nil || len(snap.byPath) == 0 {
		return nil
	}
	return maps.Clone(snap.byPath)
}

// warmFileIdentity resolves lazy OS file identity outside snapshots.mu so
// os.SameFile comparisons under the lock never issue syscalls (on Windows,
// os.Stat defers CreateFileW until the first os.SameFile call).
func warmFileIdentity(fi os.FileInfo) {
	if fi != nil {
		_ = os.SameFile(fi, fi)
	}
}

func storeManifestAtEpoch(dir string, wantEpoch uint64, fi, si os.FileInfo, m Manifest) bool {
	norm := normSnapshotDir(dir)
	if norm == "" {
		return false
	}
	warmFileIdentity(fi)
	warmFileIdentity(si)
	snapshots.mu.Lock()
	defer snapshots.mu.Unlock()
	snap := snapshots.byDir[norm]
	if snap == nil || snap.epoch != wantEpoch {
		return false
	}
	if snap.manifestOK && (!sameGeneration(snap.fi, fi) || !sameGeneration(snap.sessions, si)) {
		snap.clearDerived()
		snap.epoch = snapshots.nextEpochLocked()
	}
	snap.fi, snap.sessions = fi, si
	snap.m, snap.manifestOK = m, true
	snap.views = &metaViews{sessions: m.Sessions}
	snapshots.touchLocked(norm)
	return true
}

func storeCatalogAtEpoch(dir string, wantEpoch uint64, sig string, idx *tokenIndex) bool {
	norm := normSnapshotDir(dir)
	if norm == "" {
		return false
	}
	snapshots.mu.Lock()
	defer snapshots.mu.Unlock()
	snap := snapshots.byDir[norm]
	if snap == nil || snap.epoch != wantEpoch {
		return false
	}
	snap.catalogSig, snap.catalogIdx, snap.catalogOK = sig, idx, true
	snapshots.touchLocked(norm)
	return true
}

func storeBucketDirAtEpoch(dir string, wantEpoch uint64, p string, d *cachedBucketDir) bool {
	norm := normSnapshotDir(dir)
	if norm == "" {
		return false
	}
	if d != nil {
		warmFileIdentity(d.stat)
	}
	snapshots.mu.Lock()
	defer snapshots.mu.Unlock()
	snap := snapshots.byDir[norm]
	if snap == nil || snap.epoch != wantEpoch {
		return false
	}
	if snap.byPath == nil {
		snap.byPath = map[string]*cachedBucketDir{}
	}
	snap.byPath[p] = d
	snapshots.touchLocked(norm)
	return true
}

func storeCooccurAtEpoch(dir string, wantEpoch uint64, fi os.FileInfo, m map[string][]string) bool {
	norm := normSnapshotDir(dir)
	if norm == "" {
		return false
	}
	warmFileIdentity(fi)
	snapshots.mu.Lock()
	defer snapshots.mu.Unlock()
	snap := snapshots.byDir[norm]
	if snap == nil || snap.epoch != wantEpoch {
		return false
	}
	snap.cooccurStat, snap.cooccurMap = fi, m
	snapshots.touchLocked(norm)
	return true
}

func storeFixesAtEpoch(dir string, wantEpoch uint64, fi os.FileInfo, pairs []FixPair) bool {
	norm := normSnapshotDir(dir)
	if norm == "" {
		return false
	}
	warmFileIdentity(fi)
	snapshots.mu.Lock()
	defer snapshots.mu.Unlock()
	snap := snapshots.byDir[norm]
	if snap == nil || snap.epoch != wantEpoch {
		return false
	}
	snap.fixesFI, snap.fixesPairs, snap.fixesOK = fi, pairs, true
	snapshots.touchLocked(norm)
	return true
}

func storeBucketFileCountAtEpoch(dir string, wantEpoch uint64, target string, mtime int64, n int) bool {
	norm := normSnapshotDir(dir)
	if norm == "" {
		return false
	}
	snapshots.mu.Lock()
	defer snapshots.mu.Unlock()
	snap := snapshots.byDir[norm]
	if snap == nil || snap.epoch != wantEpoch {
		return false
	}
	snap.fileCountTarget, snap.fileCountMtime, snap.fileCount, snap.fileCountOK = target, mtime, n, true
	snapshots.touchLocked(norm)
	return true
}
