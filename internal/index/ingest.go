package index

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func summarizeBuild(initial bool, sessions int, messages int, ss []model.Session) {
	LastBuild = buildSummary(initial, sessions, messages, ss)
}

func buildSummary(initial bool, sessions int, messages int, ss []model.Session) BuildSummary {
	counts := map[string]*HarnessCount{}
	order := []string{}
	parsed := sources.CountMessages(ss)
	for _, s := range ss {
		c := counts[s.Harness]
		if c == nil {
			c = &HarnessCount{Name: s.Harness}
			counts[s.Harness] = c
			order = append(order, s.Harness)
		}
		c.Sessions++
		c.Messages += sources.CountMessages([]model.Session{s})
	}
	sort.Strings(order)
	per := make([]HarnessCount, 0, len(order))
	for _, name := range order {
		per = append(per, *counts[name])
	}
	return BuildSummary{Initial: initial, Sessions: sessions, Messages: messages,
		Dropped: parsed - messages, Harnesses: len(order), PerHarness: per}
}

// IngestHealth returns the per-harness ingestion health persisted by the
// last indexing passes, or nil when the index has none recorded.
func IngestHealth(dir string) map[string]HarnessIngest {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	return maps.Clone(m.IngestHealth)
}

// IngestFilesReport returns the same story per file: which path each refused
// line, clipped message or unreadable store came from. The rollup says a line
// was skipped and doctor points at `--json` for more, which used to hold the
// rollup again (#2189). Sparse — only files with something to report.
func IngestFilesReport(dir string) map[string]FileIngest {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return nil
	}
	return m.IngestFiles
}

// mergeIngestDiag folds the sources side-channel counters into the manifest.
// The counts live per file, because that is what they are about: a pass that
// re-reads one transcript must not erase what a different one reported, and a
// file rewritten without its bad line must be able to clear its own count
// (#2015). The per-harness map every reader asks for is the sum.
func mergeIngestDiag(m *Manifest) {
	reasons := sources.DiagReasons()
	records := sources.DiagUnusableRecords()
	foldDeferred(m, sources.DiagNoZstdPaths())
	malformed, failed := sources.DiagSnapshot()
	for _, n := range malformed {
		skippedThisRun.Add(int64(n))
	}
	if m.IngestFiles == nil {
		m.IngestFiles = map[string]FileIngest{}
	}
	// Whatever this pass read, it read whole: its files start from nothing and
	// take what the parsers just reported. A database store read from its
	// watermark is the exception for the rows it skipped before: they are
	// still in the store, just older than the stamp (#4341).
	carried := map[string]FileIngest{}
	for p := range passParsed {
		e, ok := m.IngestFiles[p]
		if ok && len(e.Unusable) > 0 && passFromWatermark[p] {
			carried[p] = e
		}
		// The clip count for this pass was recorded during redaction, which
		// runs before this fold, so it is not something to start over.
		if ok && e.Clipped > 0 {
			m.IngestFiles[p] = FileIngest{Clipped: e.Clipped, ClippedSessions: e.ClippedSessions}
			continue
		}
		delete(m.IngestFiles, p)
	}
	for p, n := range malformed {
		e := m.IngestFiles[p]
		e.Malformed += n
		m.IngestFiles[p] = e
	}
	for p, r := range reasons {
		e := m.IngestFiles[p]
		e.Reason = r
		e.Unusable = records[p]
		m.IngestFiles[p] = e
	}
	for p, old := range carried {
		e := m.IngestFiles[p]
		merged := maps.Clone(e.Unusable)
		if merged == nil {
			merged = map[string]string{}
		}
		for id, r := range old.Unusable {
			if _, again := merged[id]; !again {
				merged[id] = r
				e.Malformed++
			}
		}
		e.Unusable = merged
		if e.Reason == "" {
			e.Reason = old.Reason
		}
		m.IngestFiles[p] = e
	}
	walk := sources.WalkFailures()
	for p, msg := range failed {
		e := m.IngestFiles[p]
		e.Error = msg
		e.Walk = walk[p] != ""
		m.IngestFiles[p] = e
	}
	// A file this pass read is a file that opens. Only the error goes: the bad
	// lines it counted are still in the part already indexed.
	for p := range passRead {
		if failed[p] != "" {
			continue
		}
		if e, ok := m.IngestFiles[p]; ok && e.Error != "" {
			e.Error, e.Walk = "", false
			if e.Malformed == 0 && e.Clipped == 0 {
				delete(m.IngestFiles, p)
			} else {
				m.IngestFiles[p] = e
			}
		}
	}
	// A path the walk refused before and reads now: a transcript unchanged
	// since it was indexed, or a directory that lists again (#5393). Only
	// the walk's own refusals: a parse error on an unchanged file stands. A
	// path that is gone has nothing left to report either way, and kept, it
	// failed every later `deja index` with exit status 3 (#5396).
	for p, e := range m.IngestFiles {
		if e.Error == "" || failed[p] != "" {
			continue
		}
		f, walked := passWalked[p]
		of, held := m.Files[p]
		if e.Walk && walked && held && sameFile(of, f) || !walked && passWalked != nil && (gone(p) || !held && dirListable(p)) {
			e.Error, e.Walk = "", false
			if e.Malformed == 0 && e.Clipped == 0 {
				delete(m.IngestFiles, p)
			} else {
				m.IngestFiles[p] = e
			}
		}
	}
	// A file deja no longer walks has nothing left to report. Kept for a file
	// that failed to open, which is exactly the file a walk may not see.
	for p, e := range m.IngestFiles {
		if e.Error != "" {
			continue
		}
		if _, ok := m.Files[p]; !ok {
			delete(m.IngestFiles, p)
		}
	}
	if len(m.IngestFiles) == 0 {
		m.IngestFiles = nil
	}
	m.IngestHealth = healthFromFiles(m.IngestFiles)
	// The set belongs to the pass that recorded it. Left standing, it deleted
	// those files' entries again on the next manifest write in the process —
	// and `deja sync` writes one, from Import, right after a pass.
	passParsed, passRead = nil, nil
}

// skippedThisRun counts the lines this process skipped in what it read, over
// every pass it ran. Lines skipped by an earlier run are in the manifest, not
// here: a run that read nothing new skipped nothing.
var skippedThisRun atomic.Int64

// SkippedThisRun returns how many lines or records this process's passes read
// and could not use.
func SkippedThisRun() int { return int(skippedThisRun.Load()) }

// healthFromFiles sums the per-file counts per harness.
//
// Paths in order, because LastError is one of them: taking whichever the map
// handed over last gave the same index a different error on every run, so a
// script diffing `doctor --json` saw a change where nothing changed (#2245).
// The first failing path is the one quoted; ingest_files holds them all.
func healthFromFiles(files map[string]FileIngest) map[string]HarnessIngest {
	out := map[string]HarnessIngest{}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		e := files[p]
		// The store, not the file kind: the run narrates "cline" and doctor
		// filed the same fact under "cline-sdk", while the documentation calls
		// this key a harness. Five stores have kinds by another name, so for
		// those a script keyed on the documented name found nothing (#2234).
		h := sources.HarnessForKind(harnessForPath(p))
		if h == "" {
			continue
		}
		cur := out[h]
		cur.MalformedLines += e.Malformed
		cur.ClippedMessages += e.Clipped
		if e.Error != "" {
			cur.FailedFiles++
			if cur.LastError == "" {
				cur.LastError = e.Error
			}
		}
		out[h] = cur
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// UpToDate reports whether Ensure would do nothing, and how many sessions the
// index holds. Only `deja index` asks: silence reads as "it did not run", but
// saying it on every search would be noise on a line nobody asked about (#824).
func UpToDate(dir string, harness string) (bool, int) {
	fresh, n, _ := CheckUpToDate(dir, harness)
	return fresh, n
}

// FreshCheck is the walk of the stores an UpToDate answer came from, and the
// manifest it was held against, decoded for this check alone.
type FreshCheck struct {
	dir, harness       string
	files              map[string]FileState
	prior              Manifest
	manifest, sessions os.FileInfo
	// walked is when the walk started listing the stores.
	walked time.Time
}

// CheckUpToDate is UpToDate that also returns its walk, for the pass the same
// command runs next to start from (EnsureChecked). Nil when there was none.
func CheckUpToDate(dir string, harness string) (bool, int, *FreshCheck) {
	if dir == "" {
		dir = DefaultDir()
	}
	c := &FreshCheck{dir: dir, harness: harness, manifest: manifestGeneration(dir), sessions: sessionsGeneration(dir)}
	// Not through the cache: a stale answer hands this copy to the pass that
	// follows, which changes it, and a fresh one hands it to the cache.
	prior, err := readManifest(dir)
	if err != nil {
		return false, 0, nil
	}
	c.walked = time.Now()
	want := currentFilesReusing(harness, priorFiles(prior, err))
	scope := ""
	if harness != "" {
		scope = harness
	}
	if !manifestFresh(prior, want, scope) || !recordsIntact(dir, prior) {
		c.files, c.prior = want, prior
		return false, len(prior.Sessions), c
	}
	if c.manifest != nil && c.sessions != nil {
		cacheManifest(dir, c.manifest, c.sessions, prior)
	}
	return true, len(prior.Sessions), nil
}

// EnsureChecked is Ensure for the command that has just asked CheckUpToDate:
// the pass starts from the walk that answer made, rather than walking every
// store a second time, 70 ms of `deja index` on 40,000 sessions (#5471). Not
// when another process wrote the index in between: its manifest holds files
// as they were after that walk, and the pass walks again. Nor when a
// transcript arrived, left or moved since the walk, including while the pass
// waited for the lock (#5503).
func EnsureChecked(c *FreshCheck, force bool, progress io.Writer) error {
	if c == nil {
		return errors.New("index: EnsureChecked without a check")
	}
	if autoUpdatesOff() {
		return manualCheck(c.dir)
	}
	unlock, err := lockDir(c.dir)
	if err != nil {
		return err
	}
	defer unlock()
	return ensureHeldFrom(c.dir, c.harness, force, progress, c)
}

// listingsUnchangedSince reports whether no directory holding a transcript of
// the walks, nor the ones above it up to the store, has changed since t, when
// the walk started. A transcript that arrived after it is not in the walk,
// and the prompt log naming its session is then read with the transcript on
// disk, so the log's line is skipped; a later pass does not undo that
// (#5503). The index's own files name the directories a store emptied since.
// A prompt log or a database store sits at a fixed path rather than in a
// listing: its directory is where the climb stops, as it does at the home
// directory, which change for other reasons. Codex files a rollout three
// levels down, by year, month and day. About 500 stats for 43,000
// transcripts.
func listingsUnchangedSince(t time.Time, walks ...map[string]FileState) bool {
	listed, fixed := map[string]bool{}, map[string]bool{}
	for _, files := range walks {
		for p := range files {
			// The directory first: asking the registry what a path is costs
			// more than the stat, and one transcript lists its directory.
			d := parentDir(p)
			if listed[d] {
				continue
			}
			if isCodexHistory(p) || storeHarness(p) != "" {
				fixed[d] = true
				continue
			}
			listed[d] = true
		}
	}
	// Up to the top of the store: the highest of the three directories above
	// that holds another listed one, and at least the one above.
	home, _ := os.UserHomeDir()
	above := func(d string) []string {
		var up []string
		for range 3 {
			u := filepath.Dir(d)
			if u == d || u == home || fixed[u] {
				break
			}
			up, d = append(up, u), u
		}
		return up
	}
	under := map[string]int{}
	for d := range listed {
		for _, u := range above(d) {
			under[u]++
		}
	}
	dirs := map[string]bool{}
	for d := range listed {
		dirs[d] = true
		up := above(d)
		top := min(1, len(up))
		for i, u := range up {
			if under[u] > 1 {
				top = i + 1
			}
		}
		for _, u := range up[:top] {
			dirs[u] = true
		}
	}
	for d := range dirs {
		fi, err := os.Stat(d)
		if os.IsNotExist(err) {
			// Gone, which the one above it shows.
			continue
		}
		if err != nil {
			return false
		}
		m := fi.ModTime()
		// Linux and Windows stamp a directory from a clock a tick behind
		// time.Now, and a file system that keeps whole seconds, or FAT's
		// two, cannot tell the walk's own seconds from those before.
		if !m.Before(t.Add(-listingSlack)) || m.Nanosecond() == 0 && m.Unix() >= t.Unix()-1 {
			return false
		}
	}
	return true
}

// listingSlack is how far before the walk a directory change still counts
// as after it.
const listingSlack = 50 * time.Millisecond

// parentDir is filepath.Dir for a clean path, without the copy Dir makes:
// 1.8 ms against 4.9 ms over 43,000 paths.
func parentDir(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 && filepath.Separator == '/' {
		return p[:i]
	}
	return filepath.Dir(p)
}

// handedManifest is the manifest a freshness check decoded, handed to the
// pass the same command runs: nothing else holds it, so the pass may change
// it rather than decode its own (#5471).
var handedManifest struct {
	mu                 sync.Mutex
	dir                string
	m                  Manifest
	manifest, sessions os.FileInfo
}

// handManifest hands m, decoded from the files mfi and sfi describe, to the
// next pass over dir, and returns what takes it back.
func handManifest(dir string, mfi, sfi os.FileInfo, m Manifest) func() {
	h := &handedManifest
	h.mu.Lock()
	h.dir, h.manifest, h.sessions, h.m = dir, mfi, sfi, m
	h.mu.Unlock()
	return func() { takeHanded(dir) }
}

func takeHanded(dir string) (Manifest, os.FileInfo, os.FileInfo, bool) {
	h := &handedManifest
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dir == "" || h.dir != dir {
		return Manifest{}, nil, nil, false
	}
	m, mfi, sfi := h.m, h.manifest, h.sessions
	h.dir, h.manifest, h.sessions, h.m = "", nil, nil, Manifest{}
	return m, mfi, sfi, true
}

// takeHandedManifest is readManifest, answered by the manifest handed to a
// pass over dir while the files are the ones it was decoded from.
func takeHandedManifest(dir string) (Manifest, error) {
	if m, mfi, sfi, ok := takeHanded(dir); ok && sameGeneration(mfi, manifestGeneration(dir)) && sameGeneration(sfi, sessionsGeneration(dir)) {
		return m, nil
	}
	return readManifest(dir)
}

// sweepStaleTmp deletes a build scratch dir left by a process that died
// mid-rebuild. Holding the dir lock means no live builder owns it. Without
// this only `index --rebuild` cleared it, so a crashed build left a full
// index worth of bytes on disk indefinitely, and `doctor` reported only the
// live index's size.
func sweepStaleTmp(dir string) {
	tmp := dir + ".tmp"
	if _, err := os.Stat(tmp); err == nil {
		_ = os.RemoveAll(tmp)
	}
}

// SweepStaleTmp is sweepStaleTmp for callers that do not already hold the dir
// lock — `deja index` decides the index is fresh and returns before Ensure
// ever runs, which is exactly the run that used to walk past the leftover.
// The same goes for what a pass killed after its swap leaves: the scrub of
// the records it replaced, and the parked copy of the index before it. The
// lock finishes both (recoverIndexDir), so they are a reason to take it too
// (#5450).
func SweepStaleTmp(dir string) {
	if dir == "" {
		dir = DefaultDir()
	}
	if !leftBehind(dir) {
		return
	}
	// Not waited for: a holder ran the same recovery when it took the lock,
	// and an up-to-date answer should not sit behind a whole pass.
	unlock, ok, err := tryLockDir(dir)
	if err != nil || !ok {
		return
	}
	defer unlock()
	sweepStaleTmp(dir)
}

// leftBehind reports whether a pass that did not finish left anything a lock
// taker clears: a staging dir, a scrub, a parked index.
func leftBehind(dir string) bool {
	if _, err := os.Stat(dir + ".tmp"); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, scrubPendingFile)); err == nil {
		return true
	}
	return len(parkedDirs(dir)) > 0
}

// Ensure brings the index in dir up to date with the transcripts on disk,
// holding the index lock while it does.
func Ensure(dir string, harness string, force bool, progress io.Writer) error {
	if dir == "" {
		dir = DefaultDir()
	}
	if autoUpdatesOff() {
		return manualCheck(dir)
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return err
	}
	defer unlock()
	return ensureHeld(dir, harness, force, progress)
}

func ensureHeld(dir string, harness string, force bool, progress io.Writer) error {
	return ensureHeldFrom(dir, harness, force, progress, nil)
}

func ensureHeldFrom(dir string, harness string, force bool, progress io.Writer, c *FreshCheck) error {
	sweepStaleTmp(dir)
	// The manifest is read before the walk so unchanged files can carry
	// their derived state forward instead of being re-read. Only read here:
	// the pass decodes its own copy to change.
	var (
		prior    Manifest
		priorErr error
		want     map[string]FileState
	)
	if c != nil && c.files != nil && c.harness == harness && sameGeneration(c.manifest, manifestGeneration(dir)) && sameGeneration(c.sessions, sessionsGeneration(dir)) {
		// Both are the check's alone, so the pass may change them.
		prior, want = c.prior, c.files
		c.prior, c.files = Manifest{}, nil
		defer handManifest(dir, c.manifest, c.sessions, prior)()
		if listingsUnchangedSince(c.walked, want, prior.Files) {
			// Said all the same: it is the phase the pass starts from.
			reportPhase("finding transcripts", 0)
		} else {
			// The manifest is still the one the check read; the walk is not.
			want = currentFilesReusing(harness, prior.Files)
		}
	} else {
		if c != nil {
			c.prior, c.files = Manifest{}, nil
		}
		prior, priorErr = readManifestCached(dir)
		want = currentFilesReusing(harness, priorFiles(prior, priorErr))
	}
	scope := ""
	if harness != "" {
		// A harness-scoped index is partial by construction; the manifest
		// records that so freshness checks and search callers know. The
		// parameter was silently ignored before — every "scoped" build
		// ingested the whole machine.
		scope = harness
	}
	m, err := prior, priorErr
	if !force && err == nil && zoneDrifted(m) {
		force = true
	}
	// A store skipped for a missing CLI has unchanged files, so the
	// incremental pass has nothing to revisit and the store stays out of the
	// index. Installing the tool is a change to what deja can read, not to the
	// transcripts, and only a full pass acts on it (#1760).
	if !force && err == nil && toolsChanged(m) {
		force = true
	}
	if !force && err == nil && manifestFresh(m, want, scope) && recordsIntact(dir, m) {
		return nil
	}
	return updateIndex(dir, harness, scope, want, force, progress)
}

// EnsureUnlessBusy is Ensure for a reader that can answer from the index on
// disk, such as `deja last` and `deja show`. When another deja holds the lock
// (a `deja index` or a hook refresh for its whole pass, or another reader) it
// reports busy at once instead of waiting, and the caller reads the snapshot.
// With the lock free it is Ensure(dir, "", false, progress). It still waits
// when the snapshot is not one to answer from: no index yet, one below the
// layout or redaction floor this build serves, or a record log shorter than
// the manifest says. A lock file that cannot be opened is not a held lock, so
// a read-only index fails the way Ensure fails it.
func EnsureUnlessBusy(dir string, progress io.Writer) (bool, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if autoUpdatesOff() {
		return false, manualCheck(dir)
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return false, err
	}
	if ok {
		defer unlock()
		return false, ensureHeld(dir, "", false, progress)
	}
	if f, err := os.OpenFile(dir+".lock", os.O_RDWR, 0); err == nil {
		_ = f.Close()
		if m, err := readManifestCached(dir); err == nil && !mustRebuildBeforeAnswering(m, version) && recordsReadable(dir, m) {
			return true, nil
		}
	}
	return false, Ensure(dir, "", false, progress)
}

// EnsureForSearch is Ensure before a search: an index it cannot write is
// served as it is rather than refused.
func EnsureForSearch(dir string, o query.Options, force bool, progress io.Writer) error {
	if dir == "" {
		dir = DefaultDir()
	}
	if autoUpdatesOff() {
		return manualCheck(dir)
	}
	unlock, err := lockDir(dir)
	if err != nil {
		// A read-only index — a container mount, a locked-down machine — can
		// still answer every question asked of it. Failing here made deja
		// unusable on those, while the hook path in the same situation simply
		// stays quiet. Serve what is on disk and skip the freshness check.
		if Unwritable(err) && HasManifest(dir) {
			return nil
		}
		return err
	}
	defer unlock()
	return ensureLocked(dir, o, force, progress)
}

// EnsureForSearchNoWait is EnsureForSearch for a caller that must answer inside
// somebody's tool call: it takes the lock or reports that another process holds
// it, in one attempt. Checking RebuildInProgress and then calling the blocking
// Ensure asked the same question twice, a lock acquisition apart, and a rebuild
// starting in that window was waited out inside the call (#1804).
func EnsureForSearchNoWait(dir string, o query.Options, progress io.Writer) (busy bool, err error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if autoUpdatesOff() {
		return false, manualCheck(dir)
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return false, err
	}
	if !ok {
		// tryLockDir reports "no lock" for two different things: someone else
		// holds it, and this machine cannot write the lock file at all. Only
		// the first is a refresh to wait for. A read-only index — a container
		// mount, a locked-down machine — answers every question asked of it,
		// and telling the caller to come back later would be a wait that never
		// ends.
		if lockUnwritable(dir) && HasManifest(dir) {
			return false, nil
		}
		return true, nil
	}
	defer unlock()
	return false, ensureLocked(dir, o, false, progress)
}

// lockUnwritable reports an index whose lock file cannot be created or opened
// for writing, which is how a read-only store presents itself.
func lockUnwritable(dir string) bool {
	f, err := openIndexFileWrite(dir+".lock", os.O_CREATE|os.O_RDWR)
	if err != nil {
		return Unwritable(err)
	}
	_ = f.Close()
	return false
}

// Unwritable reports an error that says the index cannot be written here at
// all: a denied permission, or a filesystem mounted read-only. The second
// comes back as EROFS, not as a permission error, so a real read-only mount
// failed every search while a chmod'ed directory answered from its snapshot.
func Unwritable(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS)
}

// ensureLocked is the body of an Ensure, with the lock already held.
func ensureLocked(dir string, o query.Options, force bool, progress io.Writer) error {
	sweepStaleTmp(dir)
	// Cached, and only read: the search that follows reads the same one, and
	// decoding it twice was most of the manifest's cost to a search (#5386).
	prior, priorErr := readManifestCached(dir)
	want := currentFilesReusing("", priorFiles(prior, priorErr))
	scope := ""
	m, err := prior, priorErr
	if !force && err == nil && zoneDrifted(m) {
		force = true
	}
	if !force && err == nil && manifestFresh(m, want, scope) && recordsIntact(dir, m) {
		return nil
	}
	if !force && err == nil && newerIndex(m) && recordsIntact(dir, m) {
		sayNewerIndex(progress, dir, m)
		return nil
	}
	damaged := !force && (priorErr != nil && !errors.Is(priorErr, fs.ErrNotExist) || priorErr == nil && !recordsIntact(dir, prior))
	if force || err != nil || m.Version != version || m.Scope != scope || !recordsIntact(dir, m) {
		if progress != nil {
			if !hasProgressSink() {
				if damaged {
					// A half-written index rebuilds itself, and the line for it
					// used to be the routine one — so a disk that keeps
					// corrupting the store looked like ordinary reindexing
					// every single time (#1110).
					fmt.Fprintf(progress, "deja: the index in %s could not be read and is being rebuilt ...\n", displayPath(dir))
				} else {
					fmt.Fprintf(progress, "deja: indexing sessions into %s ...\n", displayPath(dir))
				}
			}
		}
		// The same full build `deja index --force` runs. One of its own
		// skipped the deleted transcripts, the turns a harness compacted
		// away and the chats gone from a database store, so the first
		// search after an upgrade dropped what the index was keeping.
		return rebuild(dir, "", scope, want, progress)
	}
	if err := updateIndex(dir, o.Harness, scope, want, force, progress); err != nil {
		return fmt.Errorf("update: %w", err)
	}
	return nil
}

// EnsureForSearchStale is EnsureForSearch for latency-bound callers (the MCP
// server): cheap append-only increments run synchronously, but anything that
// would rewrite the index (full rebuild or a whole-file store change) is
// kicked to a detached warmup instead. The return value says whether the
// caller is serving a stale view so it can say so honestly.
func EnsureForSearchStale(dir string, o query.Options, progress io.Writer) (bool, error) {
	c, err := EnsureForSearchCoverage(dir, o, progress)
	return c.Stale, err
}

// EnsureForSearchCoverage is EnsureForSearchStale with what a stale answer
// covers, for the line that says so (#5390).
func EnsureForSearchCoverage(dir string, o query.Options, progress io.Writer) (Coverage, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	stale, pending, found, err := ensureForSearchStale(dir, o, progress)
	if err != nil || !stale {
		return Coverage{}, err
	}
	c := SnapshotCoverage(dir)
	c.Stale, c.Pending, c.Found = true, pending, found
	return c, nil
}

// ensureForSearchStale also returns, when it walked the stores before handing
// the work on, how many transcripts are new or changed since the snapshot and
// how many it found; zeros when it did not walk or could not tell.
func ensureForSearchStale(dir string, o query.Options, progress io.Writer) (bool, int, int, error) {
	if autoUpdatesOff() {
		return false, 0, 0, manualCheck(dir)
	}
	if k := keptFresh(dir); k != nil {
		// Only the manifest, no walk: the keeper does that (keepfresh.go). An
		// index that cannot answer yet still goes the long way below.
		if m, ok := servable(dir); ok {
			switch {
			case newerIndex(m):
				return false, 0, 0, nil
			case m.Version != version || zoneDrifted(m) || miningChanged(m):
				return true, 0, 0, nil
			}
			stale, pending, found := k.verdict()
			return stale, pending, found, nil
		}
	}
	return walkForSearchStale(dir, o, progress)
}

// walkForSearchStale is ensureForSearchStale with the walk of every store:
// what a query runs when nothing keeps the index fresh for it, and what the
// keeper runs, under the lock, on its timer.
func walkForSearchStale(dir string, o query.Options, progress io.Writer) (bool, int, int, error) {
	// The wait before an answer is made of three things — taking the lock,
	// walking the stores to see what changed, and ingesting whatever did — and
	// from the outside they are one number. A reader who sees seconds here can
	// say which one it was rather than guessing (#3021).
	mark := searchTrace()
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return false, 0, 0, err
	}
	if !ok {
		// A rebuild is already running; serve the current snapshot.
		return true, 0, 0, nil
	}
	defer unlock()
	mark("lock")
	return staleLocked(dir, o, progress, mark)
}

// staleLocked is the body of walkForSearchStale, with the lock held.
func staleLocked(dir string, o query.Options, progress io.Writer, mark func(string)) (bool, int, int, error) {
	// Read first, walk second: this is the path every search takes, and
	// re-deriving state for unchanged transcripts was costing 700 ms of the
	// second it takes to answer a query. Through the cache, since nothing here
	// writes to it: a long-lived server decoded every session's metadata on
	// every recall, 12 MB on a 25k-session store (#5386).
	m, err := readManifestCached(dir)
	mark("manifest")
	want := currentFilesReusing("", priorFiles(m, err))
	mark("walk stores")
	if err != nil || m.Scope != "" || !recordsIntact(dir, m) || mustRebuildBeforeAnswering(m, version) {
		// No usable index yet (or a rebuild-grade problem): the caller cannot
		// serve anything sensible stale, so build synchronously.
		return false, 0, 0, updateIndex(dir, o.Harness, "", want, false, progress)
	}
	if newerIndex(m) {
		// Left alone, and current as far as this build can tell: calling it
		// stale handed it to a warmup that would not touch it either.
		return false, 0, 0, nil
	}
	if m.Version != version {
		// A content-version bump that this build can still read: the store
		// answers under the old rules while the re-read runs behind it, the
		// way staleness is already handled. Blocking on it is what made the
		// first question after an upgrade wait for the whole pass — 13m54s on
		// a 520 MB store, and an agent that waited a minute gave up on the
		// tool (#3552).
		return true, 0, 0, nil
	}
	if zoneDrifted(m) || miningChanged(m) {
		// Regrouping the day buckets or rereading aider headers in this zone
		// is a full rebuild; hand it to the detached warmup and say the current answer is stale.
		return true, 0, 0, nil
	}
	if manifestFresh(m, want, "") {
		return false, 0, 0, nil
	}
	changed := map[string]FileState{}
	removedAny := false
	for p, f := range want {
		if of, ok := m.Files[p]; !ok || !sameFile(of, f) {
			changed[p] = f
		}
	}
	for p := range m.Files {
		if p == syncImportPath {
			continue
		}
		if _, ok := want[p]; !ok && !behindLockedDir(p) {
			removedAny = true
		}
	}
	// A file touched without being written is recorded, not read (#5324), so
	// it does not turn an append into a handoff either.
	dropTouched(changed, nil, m.Files)
	if !removedAny && (len(changed) == 0 || canAppendIncremental(changed, m.Files) && !promptLogTakeover(changed, m.Files, m.Sessions) && sharedTakeovers(dir, o.Harness, m, changed, want) == nil) {
		mark("decide append")
		// An append is cheap until it isn't. A live session that has been
		// writing all day — a Grok `updates.jsonl` in the tens of megabytes —
		// is appendable, so every search sat through its tail before
		// answering, including questions about last month's history in another
		// harness (#3021). Past the cap it is handed to the detached warmup
		// like rewrite-grade work: the answer comes from the snapshot with the
		// "as it was" line, and the tail lands before the next question.
		if tail := appendTailBytes(changed, m.Files); tail > inlineAppendMax {
			return true, len(changed), len(want), nil
		}
		err := updateIndex(dir, o.Harness, "", want, false, progress)
		mark("append")
		return false, 0, 0, err
	}
	// Caller detaches the rebuild (it owns the executable path).
	mark("hand to warmup")
	return true, len(changed), len(want), nil
}

// redactionFloor is the last content version whose bump was about what may be
// shown rather than about what deja derives. A store below it must not be
// quoted while it is re-read: 41 masks the argument-credential shapes already
// on disk, where eight of twenty-four planted secrets were stored in the clear,
// 42 the password given as a long flag at any length, 43 a secret whose value is
// not ASCII, 44 the rest of the flag family — `--passphrase`, `--secret`,
// `--token`, `--api-key` — and 46 the spellings of "api key" the gate left out
// plus the colon form under the key-value floor (#3535, #3572, #3587, #3596,
// #3614).
// Redaction runs at ingest, so text written before either is text this build
// would not write.
//
// It rises when, and only when, a bump is about what must not be shown. A bump
// about what deja derives — a role filed better, a title read from a different
// field — leaves the older answers correct, and there are three of those for
// every one of these.
const redactionFloor = 46

// mustRebuildBeforeAnswering reports whether an index has to be rebuilt before
// it may answer anything at all, rather than answering under its own older
// rules while the rebuild runs behind it.
//
// Two reasons, and only two: a layout this build cannot read answers nothing —
// that is what onDiskFormat is for — and text written before deja knew how to
// redact something must not be quoted while it is being re-read. An index a
// newer deja wrote in the same layout is neither: see newerIndex.
func mustRebuildBeforeAnswering(m Manifest, _ int) bool {
	return m.Format != onDiskFormat || m.Version < redactionFloor
}

// newerIndex reports an index a newer deja wrote in a layout this build reads:
// a binary rolled back, or two installs side by side. It answers as it is and
// is left alone. Rebuilding it down to this build's version had the two
// installs rebuild the whole store back and forth, each in turn, and the line
// printed for it said this build was the newer one. Only an explicit
// `deja index --rebuild` rebuilds it for this build.
func newerIndex(m Manifest) bool {
	return m.Format == onDiskFormat && m.Version > version
}

// sayNewerIndex is the line a pass prints when it leaves a newer index alone.
func sayNewerIndex(progress io.Writer, dir string, m Manifest) {
	if progress != nil {
		fmt.Fprintf(progress, "deja: the index in %s was written by a newer deja (version %d, this one writes %d) — answering from it as it is; `deja index --rebuild` rebuilds it for this deja\n", displayPath(dir), m.Version, version)
	}
}

// searchTrace returns a stage marker that prints when DEJA_TRACE=1, and costs a
// comparison otherwise. Same shape and same variable as the session-start hook,
// so one instruction covers both paths.
func searchTrace() func(string) {
	if os.Getenv("DEJA_TRACE") != "1" {
		return func(string) {}
	}
	last := time.Now()
	return func(stage string) {
		fmt.Fprintf(os.Stderr, "trace %-16s %6.1fms\n", stage, float64(time.Since(last).Microseconds())/1000)
		last = time.Now()
	}
}

func rebuild(dir string, harness string, scope string, files map[string]FileState, progress io.Writer) error {
	return rebuildWithTombstones(dir, harness, scope, files, progress, readTombstones())
}

func rebuildWithTombstones(dir string, harness string, scope string, files map[string]FileState, progress io.Writer, dead map[string]bool) error {
	defer readTo(files)()
	defer sources.PinZstd()()
	defer walkedThisPass(files)()
	defer tightenGCForBuild()()
	// This build's counts, not the process's: see writeSessionsWithSync (#1850).
	beginPass()
	emptied.Store(0)
	collisions.Store(nil)
	merged.Store(0)
	// A rebuild evicts nothing, but a number left by an earlier build must not
	// outlive it (#1861).
	evicted.Store(0)
	lastIngestFiles = len(files)
	parsedThisPass(files)
	// Capture before publishNewestFirst can atomically replace the live index
	// with a partial build. These packets are not derived index sidecars: they
	// are durable continuation state and must survive every replacement.
	compactions := compactionsForRebuild(dir, dead)
	initialBuild := !HasManifest(dir)
	writtenMessages := 0
	imported := importedSessions(dir)
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := mkdirIndexAll(filepath.Join(tmp, "buckets")); err != nil {
		return err
	}
	total := 0
	progressWeights = filesPerHarness(files)
	for _, n := range progressWeights {
		total += n
	}
	reportPhase("reading sessions", total)
	ss := sources.FilterSessions(filterTombstonedSet(loadProgress(harness, progress), dead))
	// A transcript its harness rewrote to a summary keeps the turns the last
	// build held for it (#4795).
	ss = carryCompactedAway(dir, ss)
	unread := map[string]bool{}
	for p := range sources.DiagFailedPaths() {
		unread[p] = true
	}
	ss = append(ss, sources.FilterSessions(unreadStoresCarry(dir, harness, unread, ss))...)
	forgetUnreadStores(files)
	// Imported sessions are filtered too: excluding a project must also drop
	// what a peer already pushed, not only what arrives next.
	ss = append(ss, sources.FilterSessions(imported.sessions)...)
	// And the sessions the client deleted the transcript of, which no source
	// can hand back (#3529). Read before the file rows are carried below, so
	// the progress weights above still count only what this pass parses.
	orphans := orphanedSessions(dir, harness, files)
	ss = append(ss, sources.FilterSessions(orphans.sessions)...)
	// And the chats a database store no longer holds while the store itself
	// is still there: Cursor's state.vscdb with a chat deleted, or moved
	// aside and started over. The incremental pass keeps them; so does this.
	vanished := sources.FilterSessions(vanishedFromStores(dir, harness, files, ss))
	ss = append(ss, vanished...)
	if progress != nil && len(vanished) > 0 {
		fmt.Fprintf(progress, "deja: %d session%s no longer in %s store — still searchable; `deja resume <id> --write-back` puts one back, `deja forget --session <id>` drops one for good\n",
			len(vanished), pluralS(len(vanished)), map[bool]string{true: "its", false: "their"}[len(vanished) == 1])
	}
	ss = filterTombstonedSet(ss, dead)
	ss = dropShadowedPrompts(ss, nil)
	keepOrphanFiles(ss, &orphans, files, progress)
	// Nothing to search until the whole corpus is written, and on a first
	// install that is the fourteen seconds a user decides in (#505). Publish
	// the newest slice first: it is a valid index of a few hundred sessions,
	// it lands in about a second, and the full one replaces it moments later.
	publishNewestFirst(dir, ss, progress)
	// A full build passes every session through the exclusion patterns, so
	// this is the one place the current set can be claimed as applied. An
	// incremental build carries the old stamp forward: it keeps records it
	// wrote under the previous patterns, which is why `deja index` has to ask
	// for a rebuild rather than quietly declaring the new list in force (#1307).
	m := Manifest{Version: version, KeptDated: true, Format: onDiskFormat, SearchOnly: searchOnlyFor(dir), Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), SourcesReadAt: time.Now(), ReadZone: zoneFingerprint(), Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope,
		ExportWatermarks: imported.watermarks, ExportBoundary: imported.boundary, ImportedRecords: imported.dedupe,
		Compactions:        compactions,
		ExcludeFingerprint: sources.ExclusionFingerprint(),
		AliasFingerprint:   sources.ProjectAliasFingerprint(),
		ToolFingerprint:    mergedToolFingerprint(priorToolFingerprint(dir))}
	recPath := filepath.Join(tmp, "records.bin")
	rf, err := createLog(recPath)
	if err != nil {
		return err
	}
	tbl := newRecordTables()
	rw, err := newRecordWriter(rf, tbl)
	if err != nil {
		_ = rf.Close()
		return err
	}
	preRedactSessions(&m, ss)
	seenMsgs := msgSeen{}
	keptTurns := keptTurnsOf(ss, orphans)
	// The file a turn of a live session is filed under when that is a deleted
	// copy's, which the command tables read as the record says.
	keptSource := func(key string, s model.Session, msg model.Message) string {
		if len(keptTurns) == 0 {
			return ""
		}
		if _, isKept := orphans.files[s.Path]; isKept {
			return ""
		}
		return keptTurns[seenKey(key, msg.Role, msg.Time, msg.Text)]
	}
	// What each session's row reads off its text, all at once: in the loop it
	// was one core's work for most of the phase.
	texts := sessionTexts(ss)
	for i, s := range ss {
		rebuildRow(m.Sessions, s, texts[i], orphans)
	}
	// The rows above take the files in the order the sources hand them; the
	// records, and what is mined from them, keep the order the log had.
	keepCopyOrder(ss, orphans)
	wrote := map[string]bool{}
	for _, s := range ss {
		if holdsText(s) {
			wrote[s.Harness+":"+s.ID] = true
		}
	}
	dropEmptySessions(&m, wrote)
	for key, ps := range orphans.bare {
		if row, ok := m.Sessions[key]; ok {
			row.Copies = withCopies(row.Path, row.Copies, ps...)
			m.Sessions[key] = row
			for _, p := range ps {
				st := orphans.bareFiles[p]
				st.Kept = true
				m.Files[p] = st
			}
		}
	}
	summary := buildSummary(initialBuild, len(m.Sessions), 0, ss)
	// The five sidecars walk every session before indexing starts so each
	// session's messages can be dropped as soon as its records and postings
	// are handed to the spiller (#5571).
	// Skipped whole on a search-only index (#5395).
	var failAcc *commandFailAcc
	if !m.SearchOnly {
		failAcc = mineRebuildSidecars(tmp, ss, m, keptSource)
	}
	// Counted in messages and reported when a batch has actually been
	// indexed, not when it was handed to the spiller: reporting per session
	// pushed sent the bar from 1% to 99% in one step and then held it at 99%
	// while the workers drained — a third of the phase (#3372).
	reportPhase("indexing messages", countMessages(ss))
	sp, err := newSpiller(tmp)
	if err != nil {
		_ = rw.Close()
		return err
	}
	defer sp.cleanup()
	// The command table's state, from the records as they are written: the
	// first pass after the build reads only what it changed.
	cmdState := newCommandsState()
	err = sp.run(func(push func(tokenJob)) error {
		for i := range ss {
			s := ss[i]
			key := s.Harness + ":" + s.ID
			for _, msg := range s.Messages {
				if seenMsgs.dup(key, msg.Role, msg.Time, msg.Text) {
					continue
				}
				src := s.Path
				if k := keptSource(key, s, msg); k != "" {
					src = k
				}
				// Already redacted (and length-capped) by preRedactSessions.
				text := msg.Text
				// A message that is nothing but harness plumbing strips to empty
				// (#551). Writing it would store a record with no content and give
				// it a posting.
				if strings.TrimSpace(text) == "" {
					continue
				}
				off, writeErr := rw.write(Record{Key: key, SourcePath: src, Role: msg.Role, Text: text, Time: msg.Time})
				if writeErr != nil {
					return writeErr
				}
				if msg.Role == roleCommand {
					cmdState.add(key, tbl.intern(src), text, msg.Time, off)
				}
				if sources.IsMessageRole(msg.Role) {
					writtenMessages++
				}
				push(tokenJob{text: tokenizedPart(msg.Role, text), offset: off, sid: m.Sessions[key].Ord, when: msg.Time, tool: isToolRole(msg.Role)})
			}
			ss[i].Messages = nil
		}
		return nil
	})
	if err != nil {
		_ = rw.Close()
		return err
	}
	if err := rw.Close(); err != nil {
		return err
	}
	if !m.SearchOnly {
		writeCommandsState(tmp, cmdState, m)
		writeCommandFailState(tmp, m.Generation, failAcc)
	}
	summary.Messages = writtenMessages
	summary.Dropped -= writtenMessages
	ss = nil
	cmdState, seenMsgs, keptTurns = nil, nil, nil
	reportPhase("writing index", sp.bucketCount())
	if err := sp.writeBuckets(filepath.Join(tmp, "buckets")); err != nil {
		return err
	}
	// Before the swap: whatever is left in tmp ships inside the index.
	sp.cleanup()
	setDatabaseStoreWatermarks(m.Files, m.Sessions)
	m.RecordStrings = tbl.strs
	if err := writeManifest(tmp, m); err != nil {
		return err
	}
	if err := swapIndexDir(dir, tmp); err != nil {
		return err
	}
	LastBuild = summary
	return nil
}

func load(h string) []model.Session { return loadProgress(h, nil) }

// safeLoad shields a cold rebuild from a panicking harness loader: one broken
// store costs that harness's sessions this pass, not the whole index.
func safeLoad(name string, load func() []model.Session, progress io.Writer) (ss []model.Session) {
	defer func() {
		if r := recover(); r != nil {
			ss = nil
			if progress != nil {
				fmt.Fprintf(progress, "deja: %s: parser crashed (%v) — skipping this harness for now\n", name, r)
			}
		}
	}()
	return load()
}

// countMessages is the unit the indexing phase works through. A message that
// strips to nothing is skipped by the loop, so the count is an upper bound and
// the bar finishes a little short rather than sitting at 99%.
func countMessages(ss []model.Session) int {
	n := 0
	for _, s := range ss {
		n += len(s.Messages)
	}
	return n
}

// progressWeights is how many files each store contributes, set by the caller
// that already walked the filesystem so the bar advances proportionally.
var progressWeights = map[string]int{}

// loadProgress narrates a full rebuild per harness: a cold pass over a large
// corpus takes seconds and used to look hung.
func loadProgress(h string, progress io.Writer) []model.Session {
	// Per file while the stores parse, and the remainder of each store's
	// weight when it lands. Advancing only per store left the bar at 0% for as
	// long as the largest one took — on a machine where Claude Code holds most
	// of the corpus, ten seconds of a thirty-second rebuild (#3372). A store
	// that parses no files through the pool (the SQLite ones) is unchanged: it
	// counts nothing here and its whole weight arrives at the end.
	var readMu sync.Mutex
	readPerHarness := map[string]int{}
	readTook := map[string]time.Duration{}
	restore := sources.SetFileProgress(func(path string) {
		name := harnessForPath(path)
		if store := sources.HarnessForKind(name); store != "" {
			name = store
		}
		readMu.Lock()
		counted := readPerHarness[name] < progressWeights[name]
		if counted {
			readPerHarness[name]++
		}
		readMu.Unlock()
		if counted {
			reportAdvance(1)
		}
	})
	defer restore()
	// Harness stores are independent files owned by different tools; parsing
	// them is CPU-bound JSON/regex work with no shared state, so the cold
	// build parses all stores concurrently. Results keep registry order so a
	// rebuild stays deterministic.
	type loaded struct {
		name string
		ss   []model.Session
	}
	// A store the reader has asked deja not to read is not walked at all.
	// Without this the only way to stop `needs-sqlite3` advice for a
	// harness they do not use was to install the package (#3499).
	reg := sources.StoresToRead()
	results := make([]loaded, len(reg))
	var wg sync.WaitGroup
	for i, hr := range reg {
		if h != "" && h != hr.Name {
			continue
		}
		wg.Add(1)
		go func(i int, name string, load func() []model.Session) {
			defer wg.Done()
			started := time.Now()
			stop := sayItIsStillReading(name, started, progress)
			ss := safeLoad(name, load, progress)
			stop()
			readMu.Lock()
			readTook[name] = time.Since(started)
			readMu.Unlock()
			results[i] = loaded{name: name, ss: ss}
			// Report as this store lands rather than after every store has,
			// so the bar moves during the parse instead of jumping at the end.
			reportHarness(name, len(ss), sources.CountMessages(ss))
			// Only what the per-file reports did not already cover, so a store
			// counts its weight once.
			readMu.Lock()
			rest := progressWeights[name] - readPerHarness[name]
			readPerHarness[name] = progressWeights[name]
			readMu.Unlock()
			if rest > 0 {
				reportAdvance(rest)
			}
		}(i, hr.Name, hr.Load)
	}
	wg.Wait()
	unreadable := malformedByHarness()
	refused := failedByHarness()
	stalled := stalledByHarness()
	var ss []model.Session
	for _, r := range results {
		if len(r.ss) == 0 {
			// A harness deja can see but could not read is worth a line: an
			// index run that narrates every store it read and stays silent
			// about the one it skipped makes an empty deja look like an empty
			// history (#794).
			//
			// Two ways to yield nothing, and the second had no line at all: a
			// store deja could not open has a skip reason, and one whose files
			// it read and refused has a count instead — computed just above and
			// then dropped, in the case where nothing else on screen mentions
			// the store (#2229).
			if progress != nil && !SuppressHarnessNarration {
				switch reason := sources.SkipReason(r.name); {
				case reason != "":
					fmt.Fprintf(progress, "deja: %s: skipped — %s\n", r.name, reason)
				case refused[r.name] > 0 && refused[r.name] == len(stalled[r.name]):
					fmt.Fprintf(progress, "deja: %s: skipped — %s\n", harnessLabel(r.name), stallNarration(stalled[r.name]))
				case unreadable[r.name] > 0 || refused[r.name] > 0:
					fmt.Fprintln(progress, nothingReadableNarration(r.name, unreadable[r.name], refused[r.name]))
				}
			}
			continue
		}
		ss = append(ss, r.ss...)
		if progress != nil && !SuppressHarnessNarration {
			readMu.Lock()
			took := readTook[r.name]
			readMu.Unlock()
			line := harnessNarration(r.name, r.ss, sources.SkipReason(r.name), unreadable[r.name], refused[r.name])
			if len(stalled[r.name]) > 0 {
				line += " — " + stallNarration(stalled[r.name])
			}
			fmt.Fprintln(progress, withReadTime(line, took))
		}
	}
	return ss
}

// readSlowAfter is when a store that is still being read says so, and the
// point above which its read time is reported when it lands. A cold pass over
// a large corpus is seconds per store; anything past this is a store worth
// naming while the wait is happening. readStillReadingEvery is how often it
// repeats after that, so a long wait stays visibly alive without filling the
// screen. Variables so a test can shorten them.
var (
	readSlowAfter         = 15 * time.Second
	readStillReadingEvery = 30 * time.Second
)

// withReadTime puts the time on a store's line once the wait was long enough
// to have been worth reporting. Every store on an ordinary pass reads in
// milliseconds, and stamping those would bury the one that did not.
func withReadTime(line string, took time.Duration) string {
	if took < readSlowAfter {
		return line
	}
	return line + " — the read took " + roundedSeconds(took)
}

// sayItIsStillReading names a store that is taking long enough for the run to
// look hung, and keeps saying so until the read lands. Returns the function
// that stops it.
//
// Without this a slow store is indistinguishable from a stuck one: an index
// run over a 520 MB opencode store printed `indexing sessions into …` and
// nothing else for thirteen minutes and fifty-four seconds, of which 0.75s was
// deja's own CPU, and `deja doctor` called the store healthy throughout
// (#3553, #3555). Which store it is, and that it is still moving, is the whole
// of what a person needs to decide between waiting and interrupting.
func sayItIsStillReading(name string, started time.Time, progress io.Writer) func() {
	if progress == nil || SuppressHarnessNarration {
		return func() {}
	}
	// The thresholds are read here rather than in the goroutine: they are
	// package variables so a test can shorten them, and reading them once
	// keeps the notice out of that race.
	after, every := readSlowAfter, readStillReadingEvery
	done := make(chan struct{})
	var once sync.Once
	go func() {
		timer := time.NewTimer(after)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-timer.C:
			}
			fmt.Fprintf(progress, "deja: %s: still reading (%s)\n", harnessLabel(name), roundedSeconds(time.Since(started)))
			timer.Reset(every)
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// harnessLabel is the store's name as a person reads it: "deja" is the notes
// pseudo-source and narrates as "notes".
func harnessLabel(name string) string {
	if name == "deja" {
		return "notes"
	}
	return name
}

// roundedSeconds drops the sub-second noise: "42s", "13m54s".
func roundedSeconds(d time.Duration) string { return d.Round(time.Second).String() }

// harnessNarration is the line an index run prints for one store. A store can
// be half-readable — cursor keeps CLI transcripts as JSONL and its IDE sessions
// in SQLite — and the count alone then reads as the whole story while half of
// it is missing from recall. The skip reason was printed only for a store that
// yielded nothing at all (#1758, the shape of #794).
func harnessNarration(name string, ss []model.Session, skipped string, unreadable, refused int) string {
	msgs := sources.CountMessages(ss)
	// "deja" is the notes pseudo-source; it narrates as "notes".
	label := name
	if label == "deja" {
		label = "notes"
	}
	line := fmt.Sprintf("deja: %s: %d session%s, %d message%s", label, len(ss), pluralS(len(ss)), msgs, pluralS(msgs))
	if unreadable > 0 {
		line += fmt.Sprintf(" — %d %s%s skipped, deja could not read %s", unreadable, sources.SkippedNoun(name), pluralS(unreadable), pluralThem(unreadable))
	}
	// A file deja could not read at all is the third fact of this kind, beside
	// the refused lines and the missing tool. Without it a store that gave up
	// ten sessions and lost three tasks read like a store with nothing wrong
	// (#2236).
	if refused > 0 {
		line += fmt.Sprintf(" — %d path%s could not be read at all", refused, pluralS(refused))
	}
	if skipped != "" {
		line += " — part of this store could not be read: " + skipped
	}
	return line
}

// nothingReadableNarration is the line for a store that yielded no session
// because deja could not read what it found. "0 sessions, 0 messages" is the
// ordinary line's shape and says the wrong thing here — there were sessions,
// and none of them survived the read (#2229).
func nothingReadableNarration(name string, unreadable, refused int) string {
	label := name
	if label == "deja" {
		label = "notes"
	}
	// Lines and whole files are different losses and the sentence says which:
	// a task deja could not parse is a path, and calling it a line said "1
	// line" about three thousand turns (#2232).
	var what []string
	if unreadable > 0 {
		what = append(what, fmt.Sprintf("%d %s%s", unreadable, sources.SkippedNoun(name), pluralS(unreadable)))
	}
	if refused > 0 {
		what = append(what, fmt.Sprintf("%d path%s", refused, pluralS(refused)))
	}
	return fmt.Sprintf("deja: %s: nothing indexed — %s could not be read", label, strings.Join(what, " and "))
}

// stalledByHarness is failedByHarness for the reads given up for making no
// progress, with the budget each ran out of.
func stalledByHarness() map[string][]time.Duration {
	out := map[string][]time.Duration{}
	for p, idle := range sources.DiagStalledPaths() {
		if h := sources.HarnessForKind(harnessForPath(p)); h != "" {
			out[h] = append(out[h], idle)
		}
	}
	return out
}

// stallNarration says that a read was given up and how to wait instead. Said
// like a store deja could not open, it read as a permission problem, and
// before that a store given up was not mentioned at all (#5517).
func stallNarration(idle []time.Duration) string {
	var budget time.Duration
	for _, d := range idle {
		budget = max(budget, d)
	}
	what := "a read in the same directory stopped moving"
	if budget > 0 {
		what = "no progress for " + budget.String()
	}
	if n := len(idle); n > 1 {
		what = fmt.Sprintf("%d reads given up, %s", n, what)
	}
	return what + "; DEJA_STORE_TIMEOUT=0 waits"
}

// failedByHarness is malformedByHarness for the paths that would not open or
// would not parse at all.
func failedByHarness() map[string]int {
	out := map[string]int{}
	for p := range sources.DiagFailedPaths() {
		if h := sources.HarnessForKind(harnessForPath(p)); h != "" {
			out[h]++
		}
	}
	return out
}

// totalMalformed is malformedByHarness summed: the incremental line names the
// pass, not a store.
func totalMalformed() int {
	n := 0
	for _, c := range malformedByHarness() {
		n += c
	}
	return n
}

// malformedByHarness folds the per-file malformed counts the parsers reported
// this run into per-store totals, without draining them: the manifest fold that
// doctor reads from runs later and takes the same numbers.
func malformedByHarness() map[string]int {
	out := map[string]int{}
	for p, n := range sources.DiagMalformedCounts() {
		// By the store's own name, not the file kind's: harnessForPath answers
		// "cline-sdk" where the run narrates "cline", so the count never
		// reached the line that would have said it (#2229).
		if h := sources.HarnessForKind(harnessForPath(p)); h != "" {
			out[h] += n
		}
	}
	return out
}

// ReportCollisions returns how many sessions had a transcript share their id
// with another since the last build, and clears the count. Silence was the worst part of #698:
// the indexer counted every session on disk while the manifest held fewer, and
// nothing connected the two numbers.
func ReportCollisions() int {
	return len(collisions.Swap(nil))
}

// ReportMerged is ReportCollisions plus the pairs deja merges without warning.
func ReportMerged() int {
	return int(merged.Swap(0))
}

// markShared records that a manifest row covers more than one conversation, so
// a later forget can say what it is about to take (#970).
func markShared(sessions map[string]SessionMeta, key string) {
	if meta, ok := sessions[key]; ok {
		meta.Shared = true
		sessions[key] = meta
	}
}

// forgetUnreadStores drops from the file table every path the loaders could
// not read this pass. Recorded with its size and mtime, a store locked past
// the sqlite timeout made the next pass call the index up to date, and its
// history stayed missing until someone rebuilt by hand. Left out, the next
// pass parses it again — and skips it again, aloud, while it stays closed
// (#3176). Both rebuild paths, since a damaged index reaches the search one
// directly.
func forgetUnreadStores(files map[string]FileState) {
	for p := range sources.DiagFailedPaths() {
		delete(files, p)
	}
}

// dropEmptySessions removes manifest rows that ended up with no records.
//
// A session whose every message strips to empty — harness plumbing, a prompt
// the user never sent — still got a row, so `deja last` printed a blank line
// for it, `show` printed a header with nothing under it, and the counters
// disagreed: brief and doctor read the manifest and stats reads the records
// (1159 against 1157 on my store) (#868). The build counts the empty
// transcripts as it reads them, not the rows dropped here: an empty transcript
// sharing an id with one that holds text leaves no empty row behind (#4213).
// The row is set aside in Empty, not lost: a transcript that takes the id in a
// later pass spans it (#5563).
func dropEmptySessions(m *Manifest, wrote map[string]bool) {
	for key := range m.Sessions {
		if !wrote[key] {
			setAsideEmpty(m, key)
		}
	}
}

// ReportEmptySessions returns how many transcripts held nothing to index in the
// last build, and clears the counter. The build zeroes it as it starts writing,
// so a caller reads that build's number whether or not anyone read the one
// before (#1850). The parse count and the indexed
// count differ by exactly this, and the run is where someone is looking at
// both numbers.
func ReportEmptySessions() int {
	return int(emptied.Swap(0))
}

// publishNewestFirst writes an index of the newest sessions and swaps it in,
// so a first build answers before it finishes. Best effort in every sense: any
// failure leaves the build to write the whole thing as it always did.
//
// Only on a first build, and only a big one — replacing an index that already
// answers with a smaller one would be a regression, and on a small corpus the
// full build is already fast enough that the extra pass is the slower path.
//
// The file states are deliberately not carried into it. They are the record of
// what has been parsed, and claiming the whole corpus for an index holding a
// slice of it would let the next incremental run skip files whose sessions
// were never written.
func publishNewestFirst(dir string, ss []model.Session, progress io.Writer) {
	if HasManifest(dir) || len(ss) < partialPublishFrom {
		return
	}
	newest := append([]model.Session(nil), ss...)
	sort.Slice(newest, func(i, j int) bool { return newest[i].Updated.After(newest[j].Updated) })
	newest = newest[:newestSlice(newest)]
	tmp := dir + ".part"
	_ = os.RemoveAll(tmp)
	if err := mkdirIndexAll(filepath.Join(tmp, "buckets")); err != nil {
		return
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	// The counters this pass moves belong to the build that follows it, so
	// they are put back where it left them: writeSessions zeroes and fills
	// them, and a line saying "3 empty transcripts" must count the whole
	// corpus rather than this slice of it.
	wasEmptied, wasCollisions, wasMerged := emptied.Load(), collisions.Load(), merged.Load()
	// The same goes for what the parsers reported: the manifest write folds
	// and drains it, and the full build has to fold it again — malformed
	// lines, failed reads, the rollouts waiting for zstd (#5110).
	diag, skipped := sources.DiagSave(), skippedThisRun.Load()
	err := writeSessionsWithSync(tmp, dir, newest, nil, "", importedState{compactions: compactionsForRebuild(dir, readTombstones()), partialOf: distinctSessions(ss)})
	sources.DiagRestore(diag)
	skippedThisRun.Store(skipped)
	emptied.Store(wasEmptied)
	collisions.Store(wasCollisions)
	merged.Store(wasMerged)
	if err != nil {
		return
	}
	if progress != nil {
		fmt.Fprintf(progress, "deja: searchable now with the %d most recent sessions while the rest is indexed\n", len(newest))
	}
}

// distinctSessions counts the sessions ss names, the way the build keys them:
// a transcript read twice is one session, not two.
func distinctSessions(ss []model.Session) int {
	seen := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		seen[s.Harness+":"+s.ID] = struct{}{}
	}
	return len(seen)
}

// partialPublishFrom is the corpus size at which the wait is worth a second
// pass, and partialPublishSessions how much of it lands first. A few hundred
// sessions is what a person has touched recently enough to ask about, and it
// writes in about a second where the whole corpus takes fourteen.
//
// partialPublishMessages bounds the slice by what it holds as well. The newest
// sessions are the long ones still being worked in, and 200 of them can hold
// more text than the rest of the store: on 3,871 sessions and 370k messages
// the slice took 28 s and the build 63 s, against 43 s with no slice at all
// (#4768).
const (
	partialPublishFrom     = 400
	partialPublishSessions = 200
	partialPublishMessages = 20000
)

// newestSlice is how many of the newest-first sessions go into the early
// index: up to partialPublishSessions, stopping once partialPublishMessages is
// reached. The newest session always goes in, whatever it holds.
func newestSlice(newest []model.Session) int {
	n, msgs := 0, 0
	for n < len(newest) && n < partialPublishSessions {
		if n > 0 && msgs+len(newest[n].Messages) > partialPublishMessages {
			break
		}
		msgs += len(newest[n].Messages)
		n++
	}
	return n
}

func writeSessions(tmp, dir string, ss []model.Session, files map[string]FileState, scope string) error {
	return writeSessionsWithSync(tmp, dir, ss, files, scope, importedState{compactions: compactionsForRebuild(dir, readTombstones())})
}

func writeSessionsWithSync(tmp, dir string, ss []model.Session, files map[string]FileState, scope string, imp importedState) error {
	ss = dropShadowedPrompts(ss, nil)
	// The counters belong to this build. Draining them only on read made
	// "since the last build" true only when the last read was the last build:
	// a second build in one process reported its own empty transcripts plus
	// whatever an earlier one left behind (#1850), and the collision counter
	// beside it did the same. One process is one build for the CLI, which is
	// why it showed in the test binary first.
	emptied.Store(0)
	collisions.Store(nil)
	merged.Store(0)
	initialBuild := !HasManifest(dir)
	writtenMessages := 0
	lastIngestFiles = len(files)
	parsedThisPass(files)
	m := Manifest{Version: version, KeptDated: true, Format: onDiskFormat, SearchOnly: searchOnlyFor(dir), Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), SourcesReadAt: time.Now(), ReadZone: zoneFingerprint(), Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope,
		ExportWatermarks: imp.watermarks, ExportBoundary: imp.boundary, ImportedRecords: imp.dedupe,
		Compactions:        imp.compactions,
		ExcludeFingerprint: sources.ExclusionFingerprint(),
		AliasFingerprint:   sources.ProjectAliasFingerprint(),
		ToolFingerprint:    mergedToolFingerprint(priorToolFingerprint(dir)),
		PartialOf:          imp.partialOf}
	recPath := filepath.Join(tmp, "records.bin")
	rf, err := createLog(recPath)
	if err != nil {
		return err
	}
	tbl := newRecordTables()
	rw, err := newRecordWriter(rf, tbl)
	if err != nil {
		_ = rf.Close()
		return err
	}
	// Redact in place before anything reads the sessions, so the record log,
	// the sidecars (cooccur, fixes, commands) and the per-session metadata all
	// see the same scrubbed text. Doing it per-message into the record only — as
	// this path used to — left ss raw, and buildFixes/buildCommands/buildCooccur
	// then mined
	// secrets straight out of the unredacted commands.
	preRedactSessions(&m, ss)
	seenMsgs := msgSeen{}
	// Counted in messages and reported when a batch has actually been
	// indexed, not when it was handed to the spiller: reporting per session
	// pushed sent the bar from 1% to 99% in one step and then held it at 99%
	// while the workers drained — a third of the phase (#3372).
	reportPhase("indexing messages", countMessages(ss))
	wrote := map[string]bool{}
	var wroteMu sync.Mutex
	sp, err := newSpiller(tmp)
	if err != nil {
		_ = rw.Close()
		return err
	}
	defer sp.cleanup()
	// What each session's row reads off its text, all at once: in the loop it
	// was one core's work for most of the phase.
	texts := sessionTexts(ss)
	err = sp.run(func(push func(tokenJob)) error {
		for i, s := range ss {
			key := s.Harness + ":" + s.ID
			ord := uint32(0)
			own := s
			if old, ok := m.Sessions[key]; ok {
				ord = old.Ord
				if s.Started.IsZero() || (!old.Started.IsZero() && old.Started.Before(s.Started)) {
					s.Started = old.Started
				}
				if old.Updated.After(s.Updated) {
					s.Updated = old.Updated
				}
				if s.Project == "history" && old.Project != "" && old.Project != "history" {
					s.Project = old.Project
				}
				if s.Title == "" {
					s.Title = old.Title
				}
			}
			if ord == 0 {
				ord = nextSessionOrd(m.Sessions)
			}
			prev := m.Sessions[key]
			owns, collided := claimSession(prev, s)
			if !holdsText(s) {
				emptied.Add(1)
			}
			if collided {
				collisions.Add(key)
			}
			if owns {
				m.Sessions[key] = ownerRow(prev, s, texts[i].row(s), ord, collided)
			} else {
				widenSpan(m.Sessions, key, own)
			}
			if collided {
				markShared(m.Sessions, key)
			}
			recordCopy(m.Sessions, key, prev, s)
			for _, msg := range s.Messages {
				if seenMsgs.dup(key, msg.Role, msg.Time, msg.Text) {
					continue
				}
				// Already redacted (and length-capped) by preRedactSessions.
				text := msg.Text
				// A message that is nothing but harness plumbing strips to empty
				// (#551). Writing it would store a record with no content and give
				// it a posting.
				if strings.TrimSpace(text) == "" {
					continue
				}
				off, writeErr := rw.write(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time})
				if writeErr != nil {
					return writeErr
				}
				wroteMu.Lock()
				wrote[key] = true
				wroteMu.Unlock()
				if sources.IsMessageRole(msg.Role) {
					writtenMessages++
				}
				push(tokenJob{text: tokenizedPart(msg.Role, text), offset: off, sid: m.Sessions[key].Ord, when: msg.Time, tool: isToolRole(msg.Role)})
			}
		}
		return nil
	})
	if err != nil {
		_ = rw.Close()
		return err
	}
	if err := rw.Close(); err != nil {
		return err
	}
	dropEmptySessions(&m, wrote)
	// The loop above redacted each message into a local for the record log but
	// left ss.Messages holding the raw text; cooccur reads ss directly, so a
	// secret repeated across cooccurMinDF sessions would land in cooccur.gob
	// unredacted. Scrub in place first — nil manifest, the loop already counted.
	// Gated on the same bounds as buildCooccur so a huge store is not redacted
	// twice for a sidecar it would skip anyway.
	if len(ss) >= cooccurMinDF && len(ss) <= cooccurMaxSessions {
		preRedactSessions(nil, ss)
	}
	// The sidecars walk every session again — what co-occurs, which command
	// followed which error, what was run and what failed, and what each session
	// touched and ran. On a real store that is eight seconds of a twenty-second
	// build, and it used to run under the previous phase's last percentage, so
	// the bar sat still through it (#3372).
	if !m.SearchOnly {
		reportPhase("mining fixes and commands", 5)
		// Beside the sidecars, which read the same sessions and write files
		// of their own, as the other full path runs them.
		var receipts sync.WaitGroup
		receipts.Go(func() { evidence.Build(tmp, ss, func(t string) string { r, _, _ := indexedText(t); return r }) })
		buildCooccur(tmp, ss)
		reportAdvance(1)
		cmds := commandSessions(ss, m, nil)
		buildFixes(tmp, cmds, func(s model.Session) string { return s.Harness + ":" + s.ID })
		reportAdvance(1)
		buildCommands(tmp, cmds)
		reportAdvance(1)
		// No state: ss may hold text the records were redacted from.
		buildCommandFails(tmp, "", cmds)
		reportAdvance(1)
		buildSessionFacts(tmp, cmds)
		reportAdvance(1)
		receipts.Wait()
	}
	reportPhase("writing index", sp.bucketCount())
	if err := sp.writeBuckets(filepath.Join(tmp, "buckets")); err != nil {
		return err
	}
	// Before the swap: whatever is left in tmp ships inside the index.
	sp.cleanup()
	setDatabaseStoreWatermarks(m.Files, m.Sessions)
	m.RecordStrings = tbl.strs
	if err := writeManifest(tmp, m); err != nil {
		return err
	}
	if err := swapIndexDir(dir, tmp); err != nil {
		return err
	}
	summarizeBuild(initialBuild, len(m.Sessions), writtenMessages, ss)
	return nil
}

// sortedKeys makes a map iteration reproducible.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// collidedIDs counts sessions that share harness:id with a transcript at a
// different path.
//
// Identity is harness:id and nothing guarantees it is unique: two files named
// session-1.jsonl in different projects produce one manifest row, and which
// transcript's project and title it carried used to depend on map order — so
// the same store described a conversation differently between two builds
// (#698). Both conversations stay searchable; what is at stake is which
// project they are filed under, and the trust policy, --project and the
// exclude patterns all key on that.
//
// Qualifying the key with the path was tried and reverted: records already on
// disk carry the old key, so an incremental pass that reassigned one dropped
// the session it renamed.
// collisions holds the session keys whose transcripts shared an id with
// another during the current build. A set rather than a counter: a key read
// from three files counted twice, and a pass that re-reads every copy of a
// shared id counted each copy, where the line it feeds says sessions. The two
// full-build paths index sessions in parallel, so it is locked.
var collisions keySet

type keySet struct {
	mu sync.Mutex
	m  map[string]bool
}

func (k *keySet) Add(key string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]bool{}
	}
	k.m[key] = true
}

// Swap replaces the set with keys and returns what it held.
func (k *keySet) Swap(keys map[string]bool) map[string]bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	was := k.m
	k.m = keys
	return was
}

func (k *keySet) Store(keys map[string]bool) { k.Swap(keys) }

func (k *keySet) Load() map[string]bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return maps.Clone(k.m)
}

// merged counts every pair of transcripts that became one row, including the
// ones deja does not warn about — a goose session present in both of that
// harness's stores is a migration, not a clash, but the per-harness lines
// still count it twice. The reconciling totals key on this, so they appear
// whenever the sums have parted rather than only when there is a warning
// (#1091, #2066).
var merged atomic.Int64
var emptied atomic.Int64

// evicted counts the indexed files that left because their store went away —
// a disk unmounted, a directory deleted. The command layer needs it to tell a
// machine deja has never seen history from ("nothing to index yet") from one
// whose history has just gone (#1762).
var evicted atomic.Int64

// ReportEvictedFiles returns how many indexed files were dropped for having
// disappeared since the last build, and clears the counter.
func ReportEvictedFiles() int {
	return int(evicted.Swap(0))
}
