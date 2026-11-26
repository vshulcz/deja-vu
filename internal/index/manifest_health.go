package index

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// hasBucketFile reports whether the postings directory holds at least one
// postings file. It reads bounded directory batches and stops on the first match
// rather than enumerating thousands of buckets.
func hasBucketFile(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	for {
		entries, readErr := f.ReadDir(32)
		for _, e := range entries {
			if !e.IsDir() && isPostingsFile(e.Name()) {
				return true
			}
		}
		if len(entries) == 0 || readErr != nil {
			return false
		}
	}
}

// recordsIntact reports whether records.bin still holds everything the manifest
// committed. A shorter file means a crash truncated the record log; the index
// must rebuild rather than silently return fewer messages.
func recordsIntact(dir string, m Manifest) bool {
	return recordsIntactSized(dir, m, false)
}

// recordsReadable is recordsIntact for a lock-free reader: it tolerates a
// records.bin LONGER than the manifest committed. An incremental append grows
// records.bin before it rewrites the manifest, and a reader that lands in that
// window would otherwise see the new size against the old stamp and call a
// perfectly healthy concurrent append "corrupt" — triggering a needless full
// rebuild on the MCP path, or a hard error on `deja files`. Only a SHORTER file
// is real truncation.
//
// Reading the longer file is safe not because the tail is never read — a
// keyless or regex query full-scans records.bin to EOF — but because a tail
// record for a not-yet-committed session resolves against the old manifest:
// scanRecords skips a record whose Key is absent from m.Sessions, and a
// half-flushed tail decodes to a short read that eachRecord treats as a clean
// end. So the extra bytes contribute nothing rather than a wrong answer.
//
// Not quite nothing: an incremental pass also appends records for sessions
// the manifest already holds (a live transcript that grew), and those keys do
// resolve. A keyless full scan during a pass can return them a moment before
// the manifest commits them.
func recordsReadable(dir string, m Manifest) bool {
	return recordsIntactSized(dir, m, true)
}

func recordsIntactSized(dir string, m Manifest, tolerateLonger bool) bool {
	return recordsDamage(dir, m, tolerateLonger) == ""
}

func recordsBaseDamage(dir string, m Manifest, tolerateLonger bool) string {
	if m.RecordsSize <= 0 {
		return "" // empty index, or one written before the size stamp existed
	}
	// Plain stat, deliberately: every caller reads the manifest first, and that
	// read waits the swap out — measured, removing the wait here changes
	// nothing (#1319).
	fi, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil {
		return "the record log is not there"
	}
	// Shorter: a crash truncated the log. Longer: a crash landed records the
	// manifest never committed, or an incremental append is in flight; either
	// way the tail is unreferenced, so a reader may treat it as a snapshot.
	if fi.Size() < m.RecordsSize {
		return "the record log is shorter than the manifest committed"
	}
	if !tolerateLonger && fi.Size() != m.RecordsSize {
		return "the record log is longer than the manifest committed"
	}
	// The postings live in buckets/, and losing that directory is not
	// hypothetical: a partial copy, an interrupted sync, a `find -delete` that
	// caught too much. The manifest survives, so the index reads as built and
	// up to date while every search answers with nothing — the one failure a
	// memory tool must not present as an empty result, because the reader
	// concludes it is useless rather than broken.
	//
	// This check is the detector. The wording of the empty-result message
	// never was: it said "no matches in 0 indexed sessions" on every ordinary
	// miss too, and since #637 it reports the manifest count instead.
	if len(m.Sessions) > 0 {
		buckets := filepath.Join(dir, "buckets")
		bi, err := os.Stat(buckets)
		if err != nil || !bi.IsDir() {
			return "the postings directory is not there"
		}
		// An empty directory is the same loss as a missing one, and it is what
		// `rm buckets/*.bin` and a copy that skipped the contents both leave
		// behind: every check passed, `doctor` said "built, up to date", and
		// search answered "no matches in 3 indexed sessions" about text that
		// was still in the record log (#946).
		if !hasBucketFile(buckets) {
			return "the postings directory is empty"
		}
	}
	return ""
}

// recordsDamage is recordsIntactSized with the answer said out loud: "" when
// the store holds what the manifest committed, and otherwise which file is not
// what it should be. One sentence covered all of these, and named records and
// postings for breakages that were neither (#2695).
func recordsDamage(dir string, m Manifest, tolerateLonger bool) string {
	if reason := recordsBaseDamage(dir, m, tolerateLonger); reason != "" {
		return reason
	}
	// Losing part of the directory is the same loss spread thinner, and it
	// is the shape a partial copy or an interrupted sync actually leaves.
	// Every token in a missing file answered "no matches in N indexed
	// sessions" while its text sat in the record log, and `doctor --deep`
	// called that index healthy (#1088). Fewer than committed only: a
	// larger count belongs to a build this manifest does not describe, and
	// the freshness check already covers that.
	if len(m.Sessions) > 0 && m.BucketFiles > 0 && countBucketFilesCached(filepath.Join(dir, "buckets")) < m.BucketFiles {
		return "some postings files are missing"
	}
	return ""
}

func dropBucketFileCount(dir string) {
	indexDir, _ := indexDirForBucketsPath(dir)
	if indexDir == "" {
		return
	}
	snapshots.mu.Lock()
	snap := snapshots.getOrCreateLocked(indexDir)
	snap.fileCountTarget, snap.fileCountMtime, snap.fileCount, snap.fileCountOK = "", 0, 0, false
	snap.epoch = snapshots.nextEpochLocked()
	snapshots.touchLocked(indexDir)
	snapshots.mu.Unlock()
}

func countBucketFilesCached(dir string) int {
	fi, err := os.Stat(dir)
	if err != nil {
		return 0
	}
	mtime := fi.ModTime().UnixNano()
	indexDir, target := indexDirForBucketsPath(dir)
	snapshots.mu.Lock()
	snap := snapshots.getOrCreateLocked(indexDir)
	if snap.fileCountOK && snap.fileCountTarget == target && snap.fileCountMtime == mtime {
		snapshots.touchLocked(indexDir)
		n := snap.fileCount
		snapshots.mu.Unlock()
		return n
	}
	wantEpoch := snap.epoch
	snapshots.mu.Unlock()

	n := countBucketFiles(dir)
	storeBucketFileCountAtEpoch(indexDir, wantEpoch, target, mtime, n)
	return n
}

// countBucketFiles counts the postings files a buckets directory holds:
// buckets and their overlays, for a lost overlay drops the postings it held
// as surely as a lost bucket (#5426).
func countBucketFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && isPostingsFile(e.Name()) {
			n++
		}
	}
	return n
}

// HookDamaged is the fast integrity check for hook paths. Like Damaged it
// catches a corrupt manifest, missing or truncated records.bin, and missing or
// empty buckets/, while tolerating a longer records.bin under an active build
// lock without enumerating thousands of bucket files.
func HookDamaged(dir string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		_, statErr := statIndexFile(filepath.Join(dir, "manifest.gob"))
		return statErr == nil
	}
	if recordsBaseDamage(dir, m, false) == "" {
		return false
	}
	if !RebuildInProgress(dir) {
		return true
	}
	// While a build holds the lock, a records.bin longer than m.RecordsSize is
	// an in-flight append (#4266); accept immediately without waiting out a swap.
	if recordsBaseDamage(dir, m, true) == "" {
		return false
	}
	waitOutSwapWindow()
	m2, err := readManifest(dir)
	if err != nil {
		return false
	}
	return recordsBaseDamage(dir, m2, true) != ""
}

// Damaged reports whether the store on disk still holds what the manifest
// committed — a truncated record log, or postings that went missing to a
// partial copy or an over-eager delete.
//
// The next search rebuilds when this is true, so nothing is lost permanently.
// What was wrong is the answer `doctor` gave: "built, up to date" about a store
// that could not return a single result (#735).
func Damaged(dir string) bool {
	return DamageReason(dir) != ""
}

// DamageReason is Damaged with the finding named: "" for a store that is
// whole, and otherwise the file that is not what the manifest says. The
// remedy is the same either way — the next search rebuilds — but a reader,
// and whoever reads the doctor output they paste into an issue, is sent after
// the right file (#2695).
func DamageReason(dir string) string {
	if dir == "" {
		dir = DefaultDir()
	}
	// Cached like IsCurrentVersion: same short-lived hook process, same store.
	// A manifest that fails to decode is not cached (readManifestCached returns
	// the error), so corruption is still detected here.
	m, err := readManifestCached(dir)
	if err != nil {
		// A manifest that is not there is "not built", which doctor reports
		// already. One that is there but will not decode is a different story:
		// doctor stats the file, calls the index built and up to date, and the
		// next search rebuilds it from scratch.
		if _, statErr := statIndexFile(filepath.Join(dir, "manifest.gob")); statErr == nil {
			// Which of the two: readManifest reads manifest.gob and then the
			// session table beside it, and either can be the one that will not
			// decode.
			var core manifestCore
			if readGob(filepath.Join(dir, "manifest.gob"), &core) != nil {
				return "the manifest will not decode"
			}
			return "the session table will not decode"
		}
		return ""
	}
	reason := recordsDamage(dir, m, false)
	if reason == "" {
		return ""
	}
	// The manifest and the record log were read a moment apart, and a rebuild
	// can land between them: the manifest is the old one, records.bin is the
	// new one, and their sizes disagree for reasons that have nothing to do
	// with damage. Measured on the hook predicate during eight rebuilds, 19 of
	// 13124 asks came back damaged, and each one served a prompt with no recall
	// (#1319).
	//
	// Only while a build actually holds the lock: a store that is short with
	// nothing running is short, and answering that immediately is what every
	// other caller depends on.
	// The reason from above, not a second look: asking twice does the stat and
	// the directory read again on every hook against a damaged store, and a
	// rebuild that lands between the two calls would answer "whole" for a store
	// this one already found short.
	if !RebuildInProgress(dir) {
		return reason
	}
	// During an incremental pass, records.bin grows before the manifest is
	// rewritten. Checking the cached manifest with tolerateLonger first avoids
	// sleeping waitOutSwapWindow() and uncached manifest decode (#4266).
	if reason == "the record log is longer than the manifest committed" && recordsDamage(dir, m, true) == "" {
		return ""
	}
	waitOutSwapWindow()
	m2, err := readManifest(dir)
	if err != nil {
		// The manifest went away under us, which is the swap itself rather than
		// damage; the next call sees the new one.
		return ""
	}
	// Longer is tolerated here and only here. An incremental pass appends to
	// records.bin under the lock and commits the manifest after, and a pass
	// outlasts the swap window: the strict recheck turned MCP recall away as
	// "indexing" for 17 of 60 calls made during one, over a complete snapshot
	// (#4266). Without the lock a longer log is a crash tail, which the check
	// above still reports, so the warmup repairs it rather than a tool call.
	return recordsDamage(dir, m2, true)
}

// RebuildInProgress reports whether another process holds the index lock. A
// rebuild recreates the directory, so a reader can land in a window where the
// manifest does not exist and get the syscall for it — a moment mistaken for a
// broken store (#822).
func RebuildInProgress(dir string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	return lockDirHeld(dir)
}

// StampedAhead reports a session stamped later than this machine's clock, and
// so unusable for placing it against the others. Anything ahead counts: a
// session three hours out is still ahead, which is what the brief has counted
// since #696 and what its test pins. One rule in one place, so the brief's
// count and the marker recall prints cannot disagree about the same session
// (#1753).
func StampedAhead(t, now time.Time) bool {
	return t.After(now)
}

// OverviewStats is what the brief needs about a whole store.
type OverviewStats struct {
	Sessions      int
	Harnesses     int
	SessionsToday int
	SessionsWeek  int
	// Oldest and Newest bound what the index holds. A store whose work is all
	// older than a week has nothing to say under "today" and "this week", and
	// two zero lines is a poor way to open the first screen someone sees.
	Oldest, Newest time.Time
	// Future counts sessions stamped after now. A wrong clock, a transcript
	// written in another timezone, a harness stamping UTC while the reader is
	// behind it — all produce them, and counting them as today's work made the
	// first screen's top line wrong upward and only upward (#696).
	Future int
}

// Overview summarizes the index from manifest metadata alone — no record
// reads — so the zero-argument brief stays instant.
func Overview(dir string) (OverviewStats, error) {
	return overview(dir, false)
}

// OverviewServable is Overview over the sessions recall can actually serve.
//
// The two differ by the ignore rule, and the difference is not cosmetic: on a
// store where that rule covers two thirds of the sessions, Overview reports
// three times the sessions the screens beside it show and dates the range to a
// day none of the servable work happened on. `doctor` wants the first number —
// it reports what the index holds. The brief and the "older than that window"
// line want the second, because both describe what a reader can be given
// (#2650).
func OverviewServable(dir string) (OverviewStats, error) {
	return overview(dir, true)
}

func overview(dir string, servable bool) (OverviewStats, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return OverviewStats{}, err
	}
	pol := policy.Load()
	var o OverviewStats
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	// now bounds both counters from above. Without it every session ahead of
	// the clock was today's, this week's, and the newest thing in the store —
	// permanently (#696). The bound is now rather than tomorrow's midnight on
	// purpose: work that has not happened yet is not today's work either.
	week := now.AddDate(0, 0, -7)
	hs := map[string]bool{}
	for _, meta := range m.Sessions {
		if servable && pol.Ignored(meta.Path, meta.Project) {
			continue
		}
		o.Sessions++
		hs[meta.Harness] = true
		if StampedAhead(meta.Updated, now) {
			o.Future++
		}
		if meta.Updated.After(day) && !meta.Updated.After(now) {
			o.SessionsToday++
		}
		// Started, not Updated: the earliest bound is when the oldest work
		// began. A session that ran from January to March last touched the
		// index in March, and taking that as the floor hid two months of its
		// own transcript (#786).
		from := meta.Started
		if from.IsZero() {
			from = meta.Updated
		}
		if o.Oldest.IsZero() || (!from.IsZero() && from.Before(o.Oldest)) {
			o.Oldest = from
		}
		if meta.Updated.After(o.Newest) {
			o.Newest = meta.Updated
		}
		if meta.Updated.After(week) && !meta.Updated.After(now) {
			o.SessionsWeek++
		}
	}
	o.Harnesses = len(hs)
	return o, nil
}

// AllMeta returns every session's metadata, no records read and no lock taken.
//
// The session-metadata constructors used elsewhere drop Touched, Asked and Hit
// (#633), so a caller that needs those has to see the manifest entries
// themselves. This is the read a status line can afford: one file, no fork.
func AllMeta(dir string) ([]SessionMeta, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(m.Sessions))
	for _, meta := range m.Sessions {
		out = append(out, meta)
	}
	return out, nil
}

// AllMetaShared is AllMeta without the copy: the metas of the cached manifest,
// one slice for every caller until the index is written again. Recall asks
// for them several times a query, and each copy was 10 MB on a 19,000-session
// store. The caller must not modify the slice or the metas in it.
func AllMetaShared(dir string) ([]SessionMeta, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	return viewsFor(m).allMeta(), nil
}

// SessionCount reports how many sessions the store holds, without reading a
// record. A caller reporting an empty result needs the size of the corpus, not
// the size of whatever it just loaded.
func SessionCount(dir string) (int, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if err := ensureVaultUnlocked(dir); err != nil {
		return 0, err
	}
	if _, err := statIndexFile(filepath.Join(dir, "manifest.gob")); err != nil {
		return 0, err
	}
	n, err := readSessionsBinCount(dir)
	if err == nil {
		return n, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	m, mErr := readManifestCached(dir)
	if mErr != nil {
		return 0, mErr
	}
	return len(m.Sessions), nil
}

// MetaForKey returns the manifest row for key without reading records.
func MetaForKey(dir, key string) (SessionMeta, bool) {
	if dir == "" {
		dir = DefaultDir()
	}
	if ensureVaultUnlocked(dir) != nil {
		return SessionMeta{}, false
	}
	if _, err := statIndexFile(filepath.Join(dir, "manifest.gob")); err != nil {
		return SessionMeta{}, false
	}
	meta, ok, err := readSessionsBinKey(dir, key)
	if err == nil {
		return meta, ok
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return SessionMeta{}, false
	}
	m, mErr := readManifestCached(dir)
	if mErr != nil {
		return SessionMeta{}, false
	}
	legacyMeta, legacyOK := m.Sessions[key]
	return legacyMeta, legacyOK
}
