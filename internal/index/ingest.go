package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"hash/maphash"
	"io"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/redact"
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
	f, err := os.OpenFile(dir+".lock", os.O_CREATE|os.O_RDWR, 0o600)
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
func mustRebuildBeforeAnswering(m Manifest, build int) bool {
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
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o700); err != nil {
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
	// A kept file is kept for the sessions this build still writes from it.
	// One whose sessions were all forgotten or excluded above holds no
	// records once it is written, and the next rebuild no longer keeps it
	// (#5521).
	held := map[string]bool{}
	for _, s := range ss {
		held[s.Path] = true
	}
	for p := range orphans.files {
		if !held[p] {
			delete(orphans.files, p)
		}
	}
	for p, st := range orphans.files {
		if !st.Kept && orphans.dated {
			st.KeptAt = time.Now().Unix()
		}
		st.Kept = true
		files[p] = st
	}
	if progress != nil && len(orphans.files) > 0 {
		fmt.Fprintf(progress, "deja: %d transcript%s no longer on disk — still searchable; `deja resume <id> --write-back` puts one back, `deja forget --session <id>` drops one for good\n",
			len(orphans.files), pluralS(len(orphans.files)))
	}
	if progress != nil && orphans.unreadable > 0 {
		fmt.Fprintf(progress, "deja: %d transcript%s no longer on disk and written by an older deja — this build cannot read those records, so they go with this rebuild\n",
			orphans.unreadable, pluralS(orphans.unreadable))
	}
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
	// A turn a deleted transcript shares with one on disk stays filed under
	// the deleted one, as the incremental pass files it: under the live copy,
	// the next rebuild no longer found it kept, and a rewrite of that copy
	// took it from the index. Written where the live copy has it, so the
	// session keeps its order.
	keptTurns := map[seenK]string{}
	for _, s := range ss {
		if _, ok := orphans.files[s.Path]; ok {
			for _, msg := range s.Messages {
				if k := seenKey(s.Harness+":"+s.ID, msg.Role, msg.Time, msg.Text); keptTurns[k] == "" {
					keptTurns[k] = s.Path
				}
			}
		}
	}
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
	// The command table's state, from the records as they are written: the
	// first pass after the build reads only what it changed.
	cmdState := newCommandsState()
	// What each session's row reads off its text, all at once: in the loop it
	// was one core's work for most of the phase.
	texts := sessionTexts(ss)
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
			row := ownerRow(prev, s, texts[i].row(s), ord, collided)
			if kept, ok := orphans.rows[orphanKey(key, s.Path)]; ok {
				row = withKeptRow(row, kept, orphans.current)
			}
			m.Sessions[key] = row
		} else {
			widenSpan(m.Sessions, key, own)
		}
		if collided {
			markShared(m.Sessions, key)
		}
		recordCopy(m.Sessions, key, prev, s)
	}
	// The rows above take the files in the order the sources hand them; the
	// records, and what is mined from them, keep the order the log had.
	keepCopyOrder(ss, orphans)
	err = sp.run(func(push func(tokenJob)) error {
		for _, s := range ss {
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
				off, err := rw.write(Record{Key: key, SourcePath: src, Role: msg.Role, Text: text, Time: msg.Time})
				if err != nil {
					return err
				}
				if msg.Role == roleCommand {
					cmdState.add(key, tbl.intern(src), text, msg.Time, off)
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
	// The five sidecars walk every session again — what co-occurs, which
	// command followed which error, what was run and what failed. On a real
	// store that is eight seconds of a twenty-second build, and it used to run
	// under the previous phase's last percentage, so the bar sat still through
	// it (#3372).
	// Skipped whole on a search-only index (#5395).
	if !m.SearchOnly {
		reportPhase("mining fixes and commands", 5)
		// Five passes over the same sessions, each writing its own file and reading
		// nothing the others write, so they run together rather than one after the
		// other. Profiled on a real store, they were 9.4 s (fixes) and 6.0 s
		// (co-occurrence) of a 51 s build, with the whole machine idle beside them.
		var sidecars sync.WaitGroup
		cmds := commandSessions(ss, m, keptSource)
		for _, build := range []func(){
			func() { buildCooccur(tmp, ss) },
			func() { buildFixes(tmp, cmds, func(s model.Session) string { return s.Harness + ":" + s.ID }) },
			func() { buildCommands(tmp, cmds) },
			func() { buildCommandFails(tmp, m.Generation, cmds) },
			func() { buildSessionFacts(tmp, cmds) },
		} {
			sidecars.Add(1)
			go func() {
				defer sidecars.Done()
				build()
				reportAdvance(1)
			}()
		}
		sidecars.Wait()
		// After the facts, whose file it is stamped with.
		writeCommandsState(tmp, cmdState, m)
	}
	// Nothing below reads a session's text: counted here, the sessions can go
	// before the bucket files are assembled, rather than sitting beside the
	// postings as the larger half of the build's peak.
	summary := buildSummary(initialBuild, len(m.Sessions), writtenMessages, ss)
	ss, imported.sessions, orphans.sessions, vanished = nil, nil, nil, nil
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

// importedSessions preserves sync-imported data across full rebuilds: records
// with SourcePath deja-sync-import exist only in the index, not in any source.
func importedSessions(dir string) importedState {
	var out importedState
	m, err := readManifest(dir)
	if err != nil {
		return out
	}
	out.watermarks = m.ExportWatermarks
	out.boundary = m.ExportBoundary
	out.dedupe = m.ImportedRecords
	by := map[string]*model.Session{}
	_ = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		if r.SourcePath != syncImportPath {
			return
		}
		s := by[r.Key]
		if s == nil {
			meta, ok := m.Sessions[r.Key]
			if !ok {
				return
			}
			cp := sessionFromMeta(meta)
			cp.Path = syncImportPath
			retitleWrappedPaste(&cp)
			s = &cp
			by[r.Key] = s
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: carriedText(s.Harness, r.Role, r.Text), Time: r.Time})
	})
	for _, sess := range by {
		deriveImportedNoteState(sess)
		out.sessions = append(out.sessions, *sess)
	}
	return out
}

// detectRenamedFiles pairs a path that has gone with a path that has appeared
// holding the same bytes. Both sides carry a prefix fingerprint already — the
// scan computes one for every .jsonl it has not seen before — so the match is
// on content, not on a name or a timestamp: same size, same last-complete-line
// offset, same sample, same harness.
//
// A wrong pairing would attach one conversation's history to another file, so
// the conditions are deliberately narrow, and a row with no fingerprint (a
// store built before the sample existed) is left alone.
func detectRenamedFiles(oldFiles, files map[string]FileState) map[string]string {
	gone := map[string]FileState{}
	for _, p := range sortedKeys(oldFiles) {
		if p == syncImportPath {
			continue
		}
		of := oldFiles[p]
		if of.PrefixSample == 0 || of.SafeSize == 0 {
			continue
		}
		if _, still := files[p]; still {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			continue // there after all, just not in this pass's set
		}
		gone[p] = of
	}
	if len(gone) == 0 {
		return nil
	}
	out := map[string]string{}
	taken := map[string]bool{}
	for _, np := range sortedKeys(files) {
		if _, seen := oldFiles[np]; seen {
			continue
		}
		nf := files[np]
		if nf.PrefixSample == 0 || nf.SafeSize == 0 {
			continue
		}
		for _, op := range sortedKeys(gone) {
			if taken[op] {
				continue
			}
			of := gone[op]
			if harnessForPath(op) != harnessForPath(np) {
				continue
			}
			same := of.Size == nf.Size && of.SafeSize == nf.SafeSize && of.PrefixSample == nf.PrefixSample
			// The same file renamed and written to since — a resumed session
			// under a new name. The fingerprint covers the bytes deja had read,
			// so it still answers whether this is that file; the row it carries
			// then makes the pass read the tail rather than the whole log.
			grown := !same && nf.Size > of.Size && nf.SafeSize >= of.SafeSize &&
				filePrefixSample(np, of.SafeSize) == of.PrefixSample
			if !same && !grown {
				continue
			}
			out[np] = op
			taken[op] = true
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyRenamedFiles moves a manifest from the old names to the new ones. The
// records themselves do not move: their source path is interned, so one entry
// in that table is every record's path at once. It returns the sessions the
// move put in another project.
func applyRenamedFiles(m *Manifest, renamed map[string]string) map[string]bool {
	reprojected := map[string]bool{}
	for _, np := range sortedKeys(renamed) {
		op := renamed[np]
		of, ok := m.Files[op]
		if !ok {
			continue
		}
		of.Path = np
		m.Files[np] = of
		delete(m.Files, op)
		// A move to another directory can be a move to another project: a
		// renamed checkout is a renamed Claude project folder. The project is
		// what the reader says for the new path, as a rebuild would set it.
		var moved []model.Session
		if filepath.Dir(np) != filepath.Dir(op) {
			moved, _ = parseAppendedFile("", np, FileState{}, true)
		}
		for key, meta := range m.Sessions {
			if slices.Contains(meta.Copies, op) {
				renameCopy(&meta, op, np)
				m.Sessions[key] = meta
			}
			if meta.Path == op {
				meta.Path = np
				for _, s := range moved {
					if s.Harness+":"+s.ID == key && s.Project != "" && s.Project != meta.Project {
						meta.Project = s.Project
						reprojected[key] = true
					}
				}
				m.Sessions[key] = meta
			}
		}
		// A row set aside follows its file too, or the transcript that takes
		// its id later loses the span a rebuild gives it (#5563).
		for key, meta := range m.Empty {
			if meta.Path == op {
				meta.Path = np
				for _, s := range moved {
					if s.Harness+":"+s.ID == key && s.Project != "" {
						meta.Project = s.Project
					}
				}
				m.Empty[key] = meta
			}
		}
		for i, str := range m.RecordStrings {
			if str == op {
				m.RecordStrings[i] = np
			}
		}
		if m.IngestFiles != nil {
			if fi, ok := m.IngestFiles[op]; ok {
				m.IngestFiles[np] = fi
				delete(m.IngestFiles, op)
			}
		}
	}
	return reprojected
}

// reprojectSidecars files the tables a session feeds under the project a
// rename moved it to. Nothing was read, so the pairs keep their rows and take
// the new project; the command tables are mined again from the records.
func reprojectSidecars(dir string, sessions map[string]SessionMeta, keys map[string]bool) {
	if BuiltSearchOnly(dir) {
		return
	}
	if raw, ok := readFixRaw(dir); !ok {
		remineFixes(dir)
	} else if len(raw) > 0 {
		dirty := false
		for i, p := range raw {
			if keys[p.Key] {
				raw[i].Project, dirty = sessions[p.Key].Project, true
			}
		}
		if dirty {
			writeFixTables(dir, raw)
		}
	}
	// Nothing was read, so what the records say is what the states say; the
	// tables take the new project from the rows.
	if m, err := readManifest(dir); err == nil {
		buildCommandTables(dir, dir, m, len(m.DeadRecords), nil)
	}
	buildCommandFailsFromIndex(dir, readCommandFailState(dir), nil)
}

// orphanState is what a full rebuild has to carry: sessions whose transcript
// the client has deleted, and the file rows that keep them in the manifest.
type orphanState struct {
	sessions []model.Session
	files    map[string]FileState
	// rows is the row each kept file that owned one held, by orphanKey. The
	// records are not the file: a turn another copy holds too was filed under
	// that copy, and a repeated line was written once. Counted from them, the
	// row of a deleted transcript changed with every rebuild (#5380).
	rows map[string]SessionMeta
	// unreadable counts the ones whose records this build cannot decode. A
	// record-layout bump — version 38 moved the role out of the compressed
	// body — leaves an older store's bytes unreadable here, and a transcript
	// that is gone from disk has nowhere else to come from. Measured against
	// released binaries: a store from 0.19.5 (content version 35) upgrades
	// with its deleted-transcript sessions unrecoverable, and saying so is the
	// difference between a loss and a silent one.
	unreadable int
	// dated is the old manifest's KeptDated: a row first found gone here is
	// dated only when the store before this build dated its own.
	dated bool
	// current is whether this version wrote the old manifest.
	current bool
	// bare are the kept copies whose every turn was filed under another file
	// of the session, by key. They hold no records and stay the session's
	// copies, so a rewrite of the file holding their turns leaves them here.
	bare      map[string][]string
	bareFiles map[string]FileState
	// first is where each file of a session first shows in the old log, by
	// key and path: the order its copies are written in again.
	first map[string]map[string]int
}

// orphanedSessions preserves sessions the client's own housekeeping deleted.
// The incremental pass keeps them by never re-reading the file (#2970); a full
// rebuild reads the sources and writes what it read, so it wrote the store
// without them — 6 of 8 sessions gone on a store where the transcripts had
// been cleaned up, and a rebuild runs on a content-version bump, a changed
// exclude list and a damaged index, not only on `deja index --rebuild` (#3529).
//
// The rule is the incremental pass's: a file gone while the store directory
// around it is still there is the cleanup, and a tree gone whole is an
// uninstall or a disk that is not mounted, which is dropped as before.
func orphanedSessions(dir, harness string, files map[string]FileState) orphanState {
	out := orphanState{files: map[string]FileState{}, rows: map[string]SessionMeta{}}
	m, err := readManifest(dir)
	if err != nil {
		return out
	}
	out.dated = m.KeptDated
	out.current = m.Version == version
	want := map[string]bool{}
	var present map[string]bool
	moved := map[string]bool{}
	for _, op := range detectRenamedFiles(m.Files, files) {
		moved[op] = true
	}
	for _, p := range sortedKeys(m.Files) {
		if p == syncImportPath {
			continue // importedSessions carries these
		}
		if _, ok := files[p]; ok {
			continue
		}
		if harness != "" {
			name := harnessForPath(p)
			if store := sources.HarnessForKind(name); store != "" {
				name = store
			}
			if name != harness {
				continue
			}
		}
		if _, err := os.Lstat(p); err == nil {
			continue // on disk after all, just not in this pass's set
		}
		// Renamed or moved to another folder: read from where it is now,
		// as the incremental pass follows it.
		if moved[p] {
			continue
		}
		if !deletedFromLiveStore(p) {
			continue
		}
		// Moved with its directory, not deleted: the session is read from
		// where it is now (#4195).
		if d := goneSessionDir(p); d != "" {
			if present == nil {
				present = sessionDirsUnder(files)
			}
			if present[filepath.Base(d)] {
				continue
			}
		}
		want[p] = true
	}
	if len(want) == 0 {
		return out
	}
	by := map[string]*model.Session{}
	seen := map[string]bool{}
	spans := map[string]bool{}
	// Only the sessions of a kept file can be ordered by it.
	out.first = map[string]map[string]int{}
	for key, meta := range m.Sessions {
		for _, p := range sessionFiles(meta) {
			if want[p] {
				out.first[key] = map[string]int{}
				break
			}
		}
	}
	n := 0
	_ = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		n++
		if seenAt, ok := out.first[r.Key]; ok {
			if _, ok := seenAt[r.SourcePath]; !ok {
				seenAt[r.SourcePath] = n
			}
		}
		if !want[r.SourcePath] {
			return
		}
		seen[r.SourcePath] = true
		// Only a path that still has records: `deja forget` drops the records
		// and leaves the row, and carrying that row put a transcript deja no
		// longer holds back into doctor's "still searchable" count.
		out.files[r.SourcePath] = m.Files[r.SourcePath]
		// One session per file: two kept files under one id are two copies,
		// as they were while on disk (#5380). The one that owns the row is the
		// row; another spans its own records, since the row's span would hand
		// it the owner's.
		at := orphanKey(r.Key, r.SourcePath)
		s := by[at]
		if s == nil {
			meta, ok := m.Sessions[r.Key]
			if !ok {
				return
			}
			cp := sessionFromMeta(meta)
			cp.Path = r.SourcePath
			retitleWrappedPaste(&cp)
			if meta.Path == r.SourcePath {
				out.rows[at] = meta
			}
			// A row one file holds spans that file, as it did when the file
			// was read; beside copies, a file spans its own records.
			alone := meta.Path == r.SourcePath && len(meta.Copies) == 0 && !meta.Shared
			if !alone {
				cp.Started, cp.Updated = time.Time{}, time.Time{}
				spans[at] = true
			}
			s = &cp
			by[at] = s
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: carriedText(s.Harness, r.Role, r.Text), Time: r.Time})
		if spans[at] {
			s.Started, s.Updated = widerSpan(s.Started, s.Updated, r.Time, r.Time)
		}
	})
	for _, key := range sortedKeys(by) {
		out.sessions = append(out.sessions, *by[key])
	}
	heldBy := keysByFile(m.Sessions)
	for _, p := range sortedKeys(want) {
		if seen[p] {
			continue
		}
		if keys := heldBy[p]; len(keys) > 0 {
			if out.bare == nil {
				out.bare, out.bareFiles = map[string][]string{}, map[string]FileState{}
			}
			for _, key := range keys {
				out.bare[key] = append(out.bare[key], p)
			}
			out.bareFiles[p] = m.Files[p]
			continue
		}
		out.unreadable++
	}
	return out
}

// withKeptRow is row with what the pass read off a deleted transcript when it
// was on disk. Its records are not the file: a turn another copy holds was
// filed under one of them, and the log holds them in the order passes wrote
// them, so what is read off them in order (the conclusion, the friction lines
// and questions up to their caps) came back other than the pass had it
// (#5535). Those are taken only from a row this version wrote: a version bump
// that changes how they are read, or what is redacted from the conclusion,
// recomputes them from the records.
func withKeptRow(row, kept SessionMeta, sameVersion bool) SessionMeta {
	row.Counted, row.Words, row.LastMsg, row.Opening = kept.Counted, kept.Words, kept.LastMsg, kept.Opening
	if sameVersion {
		row.Settled, row.RanCommand, row.Asked, row.Hit, row.GaveUp = kept.Settled, kept.RanCommand, kept.Asked, kept.Hit, kept.GaveUp
		row.Touched, row.TouchHits = kept.Touched, kept.TouchHits
	}
	return row
}

// keepCopyOrder puts the copies of a session that has a kept file in the
// order the old log held them, for the records and what is mined from them.
// A transcript rewritten and moved in one pass is no rename: the pass keeps
// the old file's records where they were and appends the new file's, while
// the rebuild wrote every file on disk before the kept ones, so the session
// read back in another order and its facts, fixes and friction texts differed
// from the pass's. A file the log never held goes last, as a pass appends it.
func keepCopyOrder(ss []model.Session, orphans orphanState) {
	if len(orphans.files) == 0 {
		return
	}
	kept := map[string]bool{}
	for _, s := range ss {
		if _, ok := orphans.files[s.Path]; ok {
			kept[s.Harness+":"+s.ID] = true
		}
	}
	slots := map[string][]int{}
	for i, s := range ss {
		if key := s.Harness + ":" + s.ID; kept[key] {
			slots[key] = append(slots[key], i)
		}
	}
	for key, at := range slots {
		if len(at) < 2 {
			continue
		}
		first := orphans.first[key]
		pos := func(s model.Session) int {
			if n, ok := first[s.Path]; ok {
				return n
			}
			return math.MaxInt
		}
		group := make([]model.Session, len(at))
		for j, i := range at {
			group[j] = ss[i]
		}
		sort.SliceStable(group, func(a, b int) bool { return pos(group[a]) < pos(group[b]) })
		for j, i := range at {
			ss[i] = group[j]
		}
	}
}

// orphanKey names a kept file's session: its row's key and the file.
func orphanKey(key, path string) string { return key + "\x00" + path }

// vanishedFromStores returns the sessions a database-backed store held at the
// last build and no longer hands back, for a store that is still on disk. One
// file there holds every session, so the file-level carry in orphanedSessions
// never sees a single one of them go: the file is still there. The incremental
// pass reads such a store from a cursor and keeps what it had; a rebuild reads
// it whole and, without this, kept only what is in it now.
//
// A store is database-backed when the last build stamped its LastUpdated —
// setStoreLastUpdated does that for those alone. fresh is what this build
// already has, sources and carries both, keyed as harness:id.
func vanishedFromStores(dir, harness string, files map[string]FileState, fresh []model.Session) []model.Session {
	m, err := readManifest(dir)
	if err != nil {
		return nil
	}
	stores := map[string]bool{}
	for p, st := range m.Files {
		// An aider history is a file and still one store of many sessions: a
		// session that left it — the file deleted and started again — is kept
		// by the incremental pass, and a rebuild has to keep it too (#4332).
		if st.LastUpdated <= 0 && harnessForPath(p) != "aider" {
			continue
		}
		if _, ok := files[p]; !ok {
			continue // gone whole: orphanedSessions answers for that
		}
		if harness != "" {
			name := harnessForPath(p)
			if store := sources.HarnessForKind(name); store != "" {
				name = store
			}
			if name != harness {
				continue
			}
		}
		stores[p] = true
	}
	return carryFromStores(dir, m, stores, fresh)
}

// unreadStoresCarry returns what the last build held from the stores this
// pass could not read: a read that failed or was given up for making no
// progress. Read whole, a rebuild wrote only what it read, so a Hermes store
// that stalled took every session it held out of the index (#5517). A store
// skipped is unread, not emptied; its file row is left out, so the next pass
// reads it again.
func unreadStoresCarry(dir, harness string, unread map[string]bool, fresh []model.Session) []model.Session {
	if len(unread) == 0 {
		return nil
	}
	m, err := readManifest(dir)
	if err != nil {
		return nil
	}
	stores := map[string]bool{}
	for p := range unread {
		if _, ok := m.Files[p]; !ok {
			continue
		}
		// Gone since the walk: orphanedSessions answers for that.
		if !sources.IsHermesPGStore(p) {
			if _, err := os.Lstat(p); err != nil {
				continue
			}
		}
		if harness != "" {
			name := harnessForPath(p)
			if store := sources.HarnessForKind(name); store != "" {
				name = store
			}
			if name != harness {
				continue
			}
		}
		stores[p] = true
	}
	return carryFromStores(dir, m, stores, fresh)
}

// carryFromStores returns the sessions the manifest m holds from stores that
// fresh does not, rebuilt from their records.
func carryFromStores(dir string, m Manifest, stores map[string]bool, fresh []model.Session) []model.Session {
	if len(stores) == 0 {
		return nil
	}
	have := make(map[string]bool, len(fresh))
	for _, s := range fresh {
		have[s.Harness+":"+s.ID] = true
	}
	// An OpenCode-schema session records its project directory as its path,
	// not the database (#2033), so the path alone carried none of them and a
	// rebuild dropped what the incremental pass keeps (#4447). The harness
	// names the store there, as sessionInStore has it.
	schemaDB := map[string]bool{}
	for h, db := range opencodeSchemaDBs {
		if stores[db()] {
			schemaDB[h] = true
		}
	}
	inStore := func(r Record) bool {
		if stores[r.SourcePath] {
			return true
		}
		h, _, _ := strings.Cut(r.Key, ":")
		return schemaDB[h] && inOpencodeSchemaDB(h, r.SourcePath)
	}
	by := map[string]*model.Session{}
	_ = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		if have[r.Key] || !inStore(r) {
			return
		}
		s := by[r.Key]
		if s == nil {
			meta, ok := m.Sessions[r.Key]
			if !ok {
				return
			}
			cp := sessionFromMeta(meta)
			cp.Path = r.SourcePath
			s = &cp
			by[r.Key] = s
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	})
	// An aider session the file still holds under another id — the ordinal
	// ids before #4332 — has not left it, and carrying it would index it twice.
	started := aiderStarts(fresh)
	out := make([]model.Session, 0, len(by))
	for _, key := range sortedKeys(by) {
		s := by[key]
		if s.Harness == "aider" && started[aiderStart(s.Path, s.Started)] {
			continue
		}
		out = append(out, *s)
	}
	return out
}

// deriveImportedNoteState recovers the state of an imported promoted note from
// the note text. #984 started recording that state on the manifest row without
// bumping the index format, so a store that imported a batch before it holds a
// row with no state at all and nothing re-derives it — the batch is deduped,
// so re-importing adds 0 records and the decision the other machine retracted
// reads as accepted here (#1049).
func deriveImportedNoteState(s *model.Session) {
	if s.Harness != "deja" || s.Lifecycle != "" {
		return
	}
	for _, msg := range s.Messages {
		st, note, ok := noteStateFromText(msg.Text)
		if !ok {
			continue
		}
		s.Lifecycle, s.LifecycleNote = st, note
		// The UTC day, as import stores it: records come back in this
		// machine's zone, and the rebuild must not move the date (#5053).
		if !msg.Time.IsZero() {
			s.LifecycleAt = msg.Time.UTC().Format(lifecycleDay)
		}
		return
	}
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
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o700); err != nil {
		return
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	// The counters this pass moves belong to the build that follows it, so
	// they are put back where it left them: writeSessions zeroes and fills
	// them, and a line saying "3 empty transcripts" must count the whole
	// corpus rather than this slice of it.
	wasEmptied, wasCollisions, wasMerged := emptied.Load(), collisions.Load(), merged.Load()
	err := writeSessionsWithSync(tmp, dir, newest, nil, "", importedState{compactions: compactionsForRebuild(dir, readTombstones()), partialOf: distinctSessions(ss)})
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
				off, err := rw.write(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time})
				if err != nil {
					return err
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

// indexTextParallel hands the feed a push callback and moves jobs to the
// workers in batches: one channel send per message caused enough scheduler
// wakeups to show up as ~20% of a cold rebuild profile.
func indexTextParallel(feed func(push func(tokenJob)) error) (bucketPostings, error) {
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	const batchSize = 512
	jobs := make(chan []tokenJob, workers*4)
	partials := make([]bucketPostings, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		i := i
		partials[i] = bucketPostings{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				for _, job := range batch {
					addIndexKeys(partials[i], job.text, job.offset, job.sid, job.when, job.tool)
				}
			}
		}()
	}
	batch := make([]tokenJob, 0, batchSize)
	push := func(j tokenJob) {
		batch = append(batch, j)
		if len(batch) == batchSize {
			jobs <- batch
			batch = make([]tokenJob, 0, batchSize)
		}
	}
	err := feed(push)
	if len(batch) > 0 {
		jobs <- batch
	}
	close(jobs)
	wg.Wait()
	if err != nil {
		return nil, err
	}
	merged := bucketPostings{}
	for _, part := range partials {
		for b, toks := range part {
			if merged[b] == nil {
				merged[b] = map[string][]posting{}
			}
			for tok, offsets := range toks {
				merged[b][tok] = append(merged[b][tok], offsets...)
			}
		}
	}
	return merged, nil
}

// dateTokens makes a message findable by when it happened: the month name,
// the year, and year-month land in the postings like ordinary words, so
// "deja \"what did we do in may\"" matches May sessions structurally.
//
// The month is UTC's. A record read back from the store carries the local zone
// and a parsed one mostly UTC, so near a month boundary the incremental pass
// and a rebuild gave the same record different months, and west of UTC-10 the
// two indexes disagreed.
func dateTokens(when time.Time) []string {
	if when.IsZero() {
		return nil
	}
	when = when.UTC()
	return []string{
		"t" + strings.ToLower(when.Month().String()),
		"t" + when.Format("2006"),
		"t" + when.Format("2006-01"),
	}
}

// tokenizedPart is what of a record earns postings. For most records that is
// the whole text; for a replaced span it is only the path on the first line.
// Nobody searches for the body of a span — `deja restore` finds it by path —
// and indexing 1 MB of source code puts every `func` and `return` in it into
// the postings, which cost the median query 0.5 ms for nothing.
//
// A written side is the same shape and the argument is stronger: its body is
// 200,000 lines of hex on this machine, and no query will ever be a hash.
func tokenizedPart(role, text string) string {
	switch role {
	case roleEdit, roleWrote:
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			return text[:i]
		}
		return text
	case roleToolOutput:
		return signalLines(text)
	}
	return text
}

// signalTail is how much of the end of an unmatched output is kept beside the
// head. Half the head: measured on a 1637-output store it adds 1.6 MB to the
// postings, and the end of a long output is where a failure states itself.
const signalTail = signalFloor / 2

// signalLines keeps the part of a command's output anyone would search for.
//
// A build log is mostly progress: files compiled, tests named, packages
// downloaded. Measured over 147,575 lines of real output, 3% carry an error or
// a warning and they are 7% of the bytes — the rest is noise that nobody
// queries and that doubles the sessions a common word matches. Indexing all of
// it took the commonest word in a 1150-session store from 22 ms to 59 ms.
//
// The full text is still stored and still served: this decides what earns
// postings, exactly as a replaced span stores its body and indexes its path.
func signalLines(text string) string {
	if len(text) < signalFloor {
		return text
	}
	var b strings.Builder
	b.Grow(len(text) / 8)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !signalLine(line) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
		// The line after a verdict is usually what the verdict was about — the
		// assertion, the missing symbol, the path. Keeping the marker and
		// dropping the explanation made a test name unfindable while its
		// "--- FAIL" line was indexed.
		if i+1 < len(lines) && !signalLine(lines[i+1]) {
			b.WriteString(lines[i+1])
			b.WriteByte('\n')
		}
	}
	if b.Len() == 0 {
		// Nothing matched, which is the common case rather than the safety net:
		// a contributor measured 234 of 533 filtered records on their store
		// matching none of the substrings, and found a vendor error — `Model
		// name is not valid`, present verbatim in 8 rollouts — unrecoverable
		// because it sat past the head and "not valid" is not "not found"
		// (#614). The list of substrings is the limit, not its contents, so
		// the fallback keeps both ends: errors cluster at the end of a long
		// output, and the head alone is the least informative part of exactly
		// the records that matched nothing.
		if len(text) > signalFloor {
			// Rune-safe: this text is indexed and served back through recall,
			// so a character split here is a broken byte in an answer (#1319).
			head := text[:runeBoundary(text, signalFloor)]
			if len(text) <= signalFloor+signalTail {
				return text
			}
			// Both ends: the tail is cut from the other side, and a character
			// split there is the same broken byte in the same answer.
			tail := text[len(text)-signalTail:]
			for len(tail) > 0 && !utf8.ValidString(tail) {
				tail = tail[1:]
			}
			return head + "\n" + tail
		}
		return text
	}
	return b.String()
}

// signalFloor is the length below which output is indexed whole. A short
// output is usually the answer to something — a test run, a failed build, a
// one-line error — and filtering it costs recall on exactly the queries this
// data exists to serve. Only the long logs get filtered: measured on a real
// store, outputs above this threshold are 7% of the records and 74% of the
// bytes, and they are where the posting explosion comes from.
const signalFloor = 8192

// runeBoundary is n, or the largest offset below it that does not split a
// character.
func runeBoundary(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && !utf8.ValidString(s[:n]) {
		n--
	}
	return n
}

func signalLine(l string) bool {
	for _, p := range []string{"FAIL", "fail", "Error", "error", "panic:", "fatal",
		"Traceback", "not found", "undefined", "cannot", "refused", "denied",
		"timeout", "exit status", "warning", "WARN", "Exception", "No such"} {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

// eachIndexKey calls fn once per distinct key a record contributes. Both the
// in-memory incremental path and the spilling full build key through a keyer,
// so neither can drift from the other on what a message indexes to. The keys
// are streamed rather than collected: a CJK message produces a bigram per rune
// pair, and building the slice only to walk it once cost a third of the keying
// time on a Han corpus (#492).
func eachIndexKey(text string, when time.Time, fn func(tok string)) {
	var k keyer
	// The three key sources overlap — a word is a token and can be an
	// identifier part as well — and a posting list must not hold the same
	// offset twice.
	seen := make(map[string]bool, 64)
	k.keys(text, when, func(b []byte) {
		if seen[string(b)] {
			return
		}
		tok := string(b)
		seen[tok] = true
		fn(tok)
	})
}

// textKeys emits every key the text contributes, repeats included.
func textKeys(text string, emit func(tok string)) {
	var k keyer
	k.textKeys(text, func(b []byte) { emit(string(b)) })
}

func addIndexKeys(buckets bucketPostings, text string, off int64, sid uint32, when time.Time, tool bool) {
	eachIndexKey(text, when, func(tok string) {
		b := bucket(tok)
		if buckets[b] == nil {
			buckets[b] = map[string][]posting{}
		}
		buckets[b][tok] = append(buckets[b][tok], posting{Off: off, Sid: sid, Tool: tool})
	})
}

func writeBucketsConcurrent(dir string, buckets bucketPostings) error {
	if len(buckets) == 0 {
		return nil
	}
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}
	type bucketWrite struct {
		name string
		data map[string][]posting
	}
	jobs := make(chan bucketWrite)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				reportAdvance(1)
				if err := writeBucket(filepath.Join(dir, job.name+".bin"), job.data); err != nil {
					select {
					case errCh <- err:
					default:
					}
				}
			}
		}()
	}
	for b, data := range buckets {
		jobs <- bucketWrite{name: b, data: data}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (m msgSeen) dup(key, role string, ts time.Time, text string) bool {
	k := seenKey(key, role, ts, text)
	if m[k] {
		return true
	}
	m[k] = true
	return false
}

// seenK is a message as msgSeen tells it apart: session, role, time and text,
// hashed to 128 bits. The joined string it was, time formatted and hash in
// hex, was an allocation per message and 430 MB live at the peak of a cold
// build over 3.7M messages; this is 16 bytes inline.
type seenK [2]uint64

var seenSeeds = [2]maphash.Seed{maphash.MakeSeed(), maphash.MakeSeed()}

func seenKey(key, role string, ts time.Time, text string) seenK {
	var k seenK
	var stamp [12]byte
	binary.LittleEndian.PutUint64(stamp[:8], uint64(ts.Unix()))
	binary.LittleEndian.PutUint32(stamp[8:], uint32(ts.Nanosecond()))
	for i, seed := range seenSeeds {
		var h maphash.Hash
		h.SetSeed(seed)
		_, _ = h.WriteString(key)
		_ = h.WriteByte(0)
		_, _ = h.WriteString(role)
		_ = h.WriteByte(0)
		_, _ = h.Write(stamp[:])
		_, _ = h.WriteString(text)
		k[i] = h.Sum64()
	}
	return k
}

// repeatKey is a message as the record dedupe sees it: role, time and text.
type repeatKey struct {
	role string
	at   int64
	text uint64
}

func repeatKeyOf(role string, at time.Time, text string) repeatKey {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	return repeatKey{role, at.UnixNano(), h.Sum64()}
}

// dropRepeatedMessages drops a message that repeats an earlier one of the
// session verbatim, time included, before anything is derived from it. The
// record log already kept one copy, and the counts and sidecars took both, so
// a line a store wrote twice counted as two turns in a rebuild and as however
// many passes it was split across in an update. A message without a time or
// text is kept: two of those are not evidence of a duplicate.
func dropRepeatedMessages(ms []model.Message) []model.Message {
	var seen map[repeatKey]bool
	out := ms[:0]
	for _, m := range ms {
		if !m.Time.IsZero() && strings.TrimSpace(m.Text) != "" {
			if seen == nil {
				seen = make(map[repeatKey]bool, len(ms))
			}
			k := repeatKeyOf(m.Role, m.Time, m.Text)
			if seen[k] {
				continue
			}
			seen[k] = true
		}
		out = append(out, m)
	}
	return out
}

// dropStoredRepeats is dropRepeatedMessages for a tail: it drops the messages
// of s the index already holds from the same file, and marks every stored
// message of the session in seen so its records are not written twice. Only a
// message no newer than the row can repeat one, so a tail that is all new
// reads nothing.
// files says which copies are kept, and from is where the tail was read from
// in a file the last pass held, nil for one read whole.
func dropStoredRepeats(rw *recordWriter, dir string, s *model.Session, row SessionMeta, seen msgSeen, files map[string]FileState, harness string, from *FileState) {
	if row.ID == "" {
		return
	}
	older := false
	for _, m := range s.Messages {
		if !m.Time.IsZero() && !m.Time.After(row.Updated) && strings.TrimSpace(m.Text) != "" {
			older = true
			break
		}
	}
	if !older || rw.w.Flush() != nil {
		return
	}
	key := s.Harness + ":" + s.ID
	stored := map[repeatKey]bool{}
	elsewhere := map[repeatKey]bool{}
	if err := eachRecordForKeys(filepath.Join(dir, "records.bin"), rw.tables, map[string]bool{key: true}, func(r Record) {
		seen.dup(key, r.Role, r.Time, r.Text)
		if r.SourcePath == s.Path {
			stored[repeatKeyOf(r.Role, r.Time, r.Text)] = true
		} else if files[r.SourcePath].Kept {
			elsewhere[repeatKeyOf(r.Role, r.Time, r.Text)] = true
		}
	}); err != nil {
		return
	}
	if from != nil {
		heldBefore(s, stored, elsewhere, harness, *from)
	}
	out := s.Messages[:0]
	for _, m := range s.Messages {
		if !m.Time.IsZero() && stored[repeatKeyOf(m.Role, m.Time, m.Text)] {
			continue
		}
		out = append(out, m)
	}
	s.Messages = out
}

// heldBefore adds to stored the messages of the tail s the file already held
// before it whose record is filed under a deleted copy of the session: a turn a
// deleted transcript shares with one on disk stays filed under the deleted
// one, so the file's own records do not show the live file held it, and its
// repeat in the tail was counted as a new turn where a rebuild of the file
// counts it once (#5544). Only then is the file read again, whole and from
// where the tail starts, to tell such a repeat from a turn of the other copy
// the file takes up for the first time: what the file holds beyond the tail
// is what it held before. Keyed on the redacted text, as the tail and the
// records are.
func heldBefore(s *model.Session, stored, elsewhere map[repeatKey]bool, harness string, from FileState) {
	want := map[repeatKey]bool{}
	for _, m := range s.Messages {
		if k := repeatKeyOf(m.Role, m.Time, m.Text); elsewhere[k] && !stored[k] && !m.Time.IsZero() {
			want[k] = true
		}
	}
	if len(want) == 0 {
		return
	}
	count := func(ss []model.Session, sign int, into map[repeatKey]int) {
		for _, w := range ss {
			if w.Harness != s.Harness || w.ID != s.ID {
				continue
			}
			for _, m := range w.Messages {
				text, _, _ := indexedText(m.Text)
				if k := repeatKeyOf(m.Role, m.Time, text); want[k] {
					into[k] += sign
				}
			}
		}
	}
	whole, err := parseAppendedFile(harness, s.Path, FileState{}, true)
	if err != nil {
		return
	}
	tail, err := parseAppendedFile(harness, s.Path, from, false)
	if err != nil {
		return
	}
	before := map[repeatKey]int{}
	count(whole, 1, before)
	count(tail, -1, before)
	for k, n := range before {
		if n > 0 {
			stored[k] = true
		}
	}
}

func metaForSession(s model.Session) SessionMeta { return textOf(s).row(s) }

// sessionText is the part of a session's row read off its messages: the
// derived title and every field mined from the text. It is the expensive part,
// mostly redaction, so a build computes it for every session in parallel ahead
// of the write loop, which then only lays the session's own fields over it.
// title is the s.Title it was computed for: the loop can still hand a session
// the title of an earlier row with its id.
type sessionText struct {
	meta  SessionMeta
	title string
}

// row is the session's row: its own fields over what its text gave.
func (t sessionText) row(s model.Session) SessionMeta {
	meta := t.meta
	if s.Title != t.title {
		meta.Title, meta.AgentTitle = sessionRowTitle(s)
	}
	meta.ID, meta.Harness, meta.Project, meta.Path = s.ID, s.Harness, s.Project, s.Path
	meta.Started, meta.Updated = s.Started, s.Updated
	meta.Kind, meta.Parent, meta.Agent = s.Kind, s.Parent, s.Agent
	meta.OrigID, meta.RemoteID, meta.From = s.OrigID, s.RemoteID, s.From
	meta.Lifecycle, meta.LifecycleNote, meta.LifecycleAt = s.Lifecycle, s.LifecycleNote, s.LifecycleAt
	return meta
}

// sessionTexts is textOf for every session, spread over the cores.
func sessionTexts(ss []model.Session) []sessionText {
	out := make([]sessionText, len(ss))
	workers := min(runtime.GOMAXPROCS(0), len(ss))
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(ss) {
					return
				}
				out[i] = textOf(ss[i])
			}
		}()
	}
	wg.Wait()
	return out
}

// sessionRowTitle is the title a session's row carries, and whether it is the
// agent's words rather than the user's.
func sessionRowTitle(s model.Session) (string, bool) {
	title := s.Title
	agentTitle := s.AgentTitle
	if title == "" {
		// A session with no user turn borrows the assistant's opening line
		// (#692), and one that is only tool output is named after its first
		// output; the listing needs to say so rather than print it where the
		// reader's own question goes (#1100). sessionTitleFrom carries the
		// right fromAgent bit — a computed one calls tool-only sessions
		// agent-titled.
		title, agentTitle = sessionTitleFrom(s)
		// Redact before the cut: slicing a secret in half leaves a prefix no
		// pattern matches, and it survives into sessions.gob (G/#…).
		title, _ = redact.Text(title)
		title = truncateTitle(title, 60)
	} else {
		// Titles come from unredacted places — an agent-generated summary, a
		// composer name, the first user message — and are persisted in
		// sessions.gob, so they need the same scrubbing as record text.
		title, _ = redact.Text(title)
		// And the same bound. A title the source authored went to the
		// one-line surfaces whole: measured at 384 characters with the
		// newlines still in it, so one session printed several rows of
		// `deja last` and the text after the break began "[claude · …",
		// which is deja's own listing format (#1090 covers the escape bytes;
		// this is the line break). Derived titles have been collapsed and cut
		// since they existed.
		title = widenThinSourceTitle(s, boundSourceTitle(s.Harness, title))
	}
	return title, agentTitle
}

// textOf is the row fields s's messages give.
func textOf(s model.Session) sessionText {
	title, agentTitle := sessionRowTitle(s)
	// The import fields travel with the session, not with the transcript: a
	// rebuild reloads imported sessions out of the index itself, and rebuilding
	// the row from scratch dropped what only the import knew — the note's state
	// and the id it had on the machine it came from (#1049).
	touched, touchHits := topTouchedCounted(s.Messages)
	// Counted, so an append that arrives in the same pass folds only what this
	// did not already see. Without it a session new to the index was derived
	// from here and merged again below, doubling its Words (#1304).
	last := uint64(0)
	if len(s.Messages) > 0 {
		last = messageFingerprint(s.Messages[len(s.Messages)-1])
	}
	return sessionText{title: s.Title, meta: SessionMeta{ID: s.ID, Harness: s.Harness, Project: s.Project, Path: s.Path, Title: title, AgentTitle: agentTitle, Started: s.Started, Updated: s.Updated, Touched: touched, TouchHits: touchHits, Counted: len(s.Messages), LastMsg: last, Asked: askedHashes(s.Messages), Hit: frictionHashes(s.Messages), GaveUp: gaveUp(s.Messages), Words: sessionWords(s.Messages), NoText: !holdsText(s), Settled: sessionSettled(s), RanCommand: ranCommand(s.Messages),
		Kind: s.Kind, Parent: s.Parent, Agent: s.Agent, Opening: SessionOpening(s),
		OrigID: s.OrigID, RemoteID: s.RemoteID, From: s.From, Lifecycle: s.Lifecycle, LifecycleNote: s.LifecycleNote, LifecycleAt: s.LifecycleAt}}
}

// sessionSettled is what this session concluded, taken from its tail.
//
// The same extraction the point-of-action hook used to do per action, moved to
// where the session is already in hand. Measured on a real store: doing it at
// the moment of the action cost 133 ms a call against 21 ms when the hook said
// nothing, because it had to rank candidates and then load whole sessions to
// ask which had run the command — and almost every action asks about a command
// the session has not run before, so nothing was ever warm (#3001, #3605).
//
// The tail and the budget are the hook's own, so the text is the text it would
// have produced. Empty when the session settled nothing, which is the same
// answer the hook gave then.
func sessionSettled(s model.Session) string {
	// Only a session that ran a command can ever be asked: the command table
	// names the session, and nothing else reads this field. Extracting for the
	// rest cost 21s of a 51s rebuild on a 2.0 GB corpus for an answer no caller
	// could reach.
	if !ranCommand(s.Messages) {
		return ""
	}
	return settledFrom(s)
}

func ranCommand(ms []model.Message) bool {
	for _, m := range ms {
		if m.Role == roleCommand {
			return true
		}
	}
	return false
}

// settledFrom is sessionSettled without the command gate.
func settledFrom(s model.Session) string {
	const tail, budget = 150, 200
	if len(s.Messages) > tail {
		cp := s
		cp.Messages = s.Messages[len(s.Messages)-tail:]
		s = cp
	}
	cs := digest.Conclusions(s, budget, 1)
	if len(cs) == 0 {
		return ""
	}
	return strings.TrimSpace(cs[0])
}

// extendDerived folds messages appended to an already-indexed session into the
// fields derived from its text.
//
// The append path used to update only what it could read off the new messages
// directly — timestamps, project, title — and left everything derived frozen at
// whatever the session held when it was first seen. A transcript is written to
// while the work happens, so that is the ordinary case, not an edge one: an
// error hit later in a session never entered Hit and so `deja error` could not
// match it by signature; files touched later never entered Touched, so blame
// did not know about them; a session that gave up after its first pass was
// still ranked as one that had not.
//
// Merged rather than recomputed, because this path only ever holds the new
// messages: the file is read from the last watermark, not from the start.
// extendDerived folds a session's new messages into the fields derived from its
// text. ms is the whole session as the source hands it over; meta.Counted says
// how much of it is already in, so re-delivering a live session — which goose
// does on every pass, and which is how a session new to this file arrives — is
// idempotent rather than additive (#1304).
func extendDerived(meta *SessionMeta, ms []model.Message) {
	tail := newMessages(meta, ms)
	if len(tail) == 0 {
		return
	}
	meta.Counted += len(tail)
	if meta.Opening == 0 {
		meta.Opening = SessionOpening(model.Session{Harness: meta.Harness, Messages: tail})
	}
	if meta.NoText && holdsText(model.Session{Messages: tail}) {
		meta.NoText = false
	}
	meta.LastMsg = messageFingerprint(ms[len(ms)-1])
	meta.Words += sessionWords(tail)
	// Capped like the full build caps: a plain union grows on every append,
	// and the manifest holding it is read on every search.
	meta.Asked = mergeCappedU64(meta.Asked, askedHashes(tail), askedQuestionCap)
	meta.Hit = mergeCappedU64(meta.Hit, frictionHashes(tail), frictionSessionCap)
	// Giving up is a state a session reaches, never one it leaves: the phrases
	// that set it are reversals of work already done.
	if gaveUp(tail) {
		meta.GaveUp = true
	}
	if paths, hits := topTouchedCounted(tail); len(paths) > 0 {
		meta.Touched, meta.TouchHits = mergeTouchedCounted(meta.Touched, meta.TouchHits, paths, hits)
	}
	// The line is picked from the session's last 150 messages, and reading a
	// live session whole again on every append costs more than the line is
	// worth. The old line stands in for the messages before the tail: picked
	// against the tail by the same rules, a concluding line already held
	// outranks a newer one that only reports.
	meta.RanCommand = meta.RanCommand || ranCommand(tail)
	if meta.RanCommand {
		win := tail
		if meta.Settled != "" {
			win = append([]model.Message{{Role: "assistant", Text: meta.Settled}}, tail...)
		}
		if s := settledFrom(model.Session{Harness: meta.Harness, Messages: win}); s != "" {
			meta.Settled = s
		}
	}
}

// newMessages is the part of a delivery the derived fields have not seen. The
// fingerprint is matched from the end: a session that repeats a line verbatim
// would otherwise resume from the first copy and fold the rest twice.
func newMessages(meta *SessionMeta, ms []model.Message) []model.Message {
	if len(ms) == 0 {
		return nil
	}
	if meta.LastMsg == 0 {
		return ms
	}
	for i := len(ms) - 1; i >= 0; i-- {
		if messageFingerprint(ms[i]) == meta.LastMsg {
			return ms[i+1:]
		}
	}
	return ms
}

// messageFingerprint identifies one message well enough to find it again in a
// later delivery of the same session. Role, time and text: two adjacent
// messages with the same text differ by time, and a store that rewrites times
// re-folds a tail, which costs a little accuracy and no correctness.
func messageFingerprint(m model.Message) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(m.Role))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.FormatInt(m.Time.UnixNano(), 10)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(m.Text))
	// Zero means "nothing counted yet", so a message that hashes to it takes
	// the neighbouring value rather than resetting the session's state.
	if sum := h.Sum64(); sum != 0 {
		return sum
	}
	return 1
}

// mergeTouchedCounted adds the tail's touches to the counts already held and
// re-ranks. Position alone was all the old merge had: six paths touched once in
// an early batch filled the list, and the file the session actually worked on
// hardest never appeared (#1333, and its real cause #1304).
// A path with no count behind it — the import path derives Touched from records
// and carries none — counts as one, so it keeps its place without outranking a
// file this session actually worked on repeatedly.
func mergeTouchedCounted(have []string, hits []int, add []string, addHits []int) ([]string, []int) {
	count := map[string]int{}
	for i, p := range have {
		n := 1
		if i < len(hits) {
			n = hits[i]
		}
		count[p] += n
	}
	for i, p := range add {
		n := 1
		if i < len(addHits) {
			n = addHits[i]
		}
		count[p] += n
	}
	return rankTouchedCounted(count)
}

func mergeTouched(have, add []string) []string {
	seen := make(map[string]bool, len(have)+len(add))
	out := make([]string, 0, touchedFileCap)
	keep := func(p string) bool {
		if p == "" || seen[p] {
			return true
		}
		seen[p] = true
		out = append(out, p)
		return len(out) < touchedFileCap
	}
	for i := 0; i < len(have) || i < len(add); i++ {
		if i < len(have) && !keep(have[i]) {
			return out
		}
		if i < len(add) && !keep(add[i]) {
			return out
		}
	}
	return out
}

// agentOwnedFile drops the agent's own working files. They are touched
// constantly while a subject is being worked on, so left in they take the top
// slots from the source that was actually being changed — measured: the six
// stored paths held no repository file at all for a session whose work was
// entirely in one.
func agentOwnedFile(p string) bool {
	// A worktree Claude Code made for an isolated agent sits under .claude/
	// and holds the repository's own source: the edits in it are the work,
	// not the agent's bookkeeping. Only the .claude/ segment is forgiven;
	// every other rule still applies inside it (#4164).
	p = strings.ReplaceAll(p, "/.claude/worktrees/", "/")
	for _, seg := range []string{"/scratchpad/", "/tasks/", "/.claude/", "/.cache/", "/claude-501/", "/node_modules/", "/.git/"} {
		if strings.Contains(p, seg) {
			return true
		}
	}
	return strings.HasSuffix(p, ".log") || strings.HasSuffix(p, ".output")
}

// askedHashOf returns the stem hash for one user turn, or false when the text
// is not a question worth tracking. Shared by the message and record paths so
// they agree on what counts as an asking.
func askedHashOf(text string) (uint64, bool) {
	if notAsked(text) || !looksLikeQuestion(text) {
		return 0, false
	}
	stem := questionStem(text)
	if stem == "" {
		return 0, false
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(stem))
	return h.Sum64(), true
}

// askedHashes fingerprints the substantial things a person asked in a session.
// Short turns are excluded the way stats excludes them: "ok" and "continue"
// repeat in every session and mean nothing.
func askedHashes(ms []model.Message) []uint64 {
	var out []uint64
	seen := map[uint64]bool{}
	for _, m := range ms {
		if m.Role != "user" {
			continue
		}
		v, ok := askedHashOf(m.Text)
		if !ok || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
		if len(out) >= askedQuestionCap {
			break
		}
	}
	return out
}

// notAsked rejects the text a harness writes under the user role: hook
// envelopes, interruption notices, resume preambles, the compaction summary.
// It repeats across sessions by construction, so without this the most
// "repeated question" in any store is a piece of plumbing — measured: the first
// candidate this produced was "The following tool was executed by the user",
// spanning April to July.
func notAsked(text string) bool {
	// "no visible output" is a shell record's phrase and not an opening, so it
	// is asked about the whole turn here; a title candidate asks only
	// harnessPreamble, because a person can write those words in a question
	// ("why does the button have no visible output when clicked?") and that
	// question is a perfectly good name for a session (review of #3328).
	return harnessPreamble(text) || strings.Contains(strings.TrimSpace(text), "no visible output")
}

// harnessPreamble reports whether a turn opens with something the harness
// wrote: an envelope, an interruption notice, a resume preamble, the
// compaction caveat.
func harnessPreamble(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range []string{
		"<local-command", "<command-", "<task-notification", "<teammate-message",
		"<bash-", "<system-reminder", "<deja-recall", "Caveat:",
		"[Request interrupted", "The following tool was executed",
		"This session is being continued", "Continue from where you left off",
	} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return compactionSummary(t)
}

// compactionSummary reports the summary a harness writes when it compacts a
// conversation and hands the rest back to itself.
//
// The preamble that introduced it — "This session is being continued…" — is
// stripped as an injected line before anything reads the turn, so what is left
// starts at the summary and no longer looks like the harness's. 23 of the last
// 800 sessions on a real store were named after it: "Summary: 1. Primary
// Request and Intent: …" reached `deja last`, the recall block and the
// repeated-question counter as if a person had typed it.
//
// Matched on the template rather than on the word: someone writing "Summary: we
// moved the retry budget" is naming their own session, and the headings below
// are the ones a compaction writes.
func compactionSummary(t string) bool {
	if !strings.HasPrefix(strings.ToLower(t), "summary:") {
		return false
	}
	head := t
	if len(head) > 600 {
		head = head[:600]
	}
	for _, heading := range []string{
		"Primary Request", "Key Technical Concepts", "Files and Code Sections",
		"Pending Tasks", "Current Work",
	} {
		if strings.Contains(head, heading) {
			return true
		}
	}
	return false
}

// looksLikeQuestion keeps this to things a person actually asked. Without it
// the candidates a real store produces are overwhelmingly instructions to an
// agent — "Use the X tool", "Call Y with scope all", "Reply with only the raw
// JSON" — which repeat verbatim by construction and say nothing about the work.
//
// A question mark, or an interrogative opening in either language deja sees
// most. This under-includes on purpose: a missed repeat costs a line on one
// screen, a wrong one costs the reader's trust in the screen.
func looksLikeQuestion(text string) bool {
	t := strings.TrimSpace(text)
	// A person's question is short. A pasted report that happens to contain a
	// question mark two paragraphs in is not one, and shown truncated on a
	// screen it reads as noise — measured: the first candidate to survive the
	// earlier version was a 900-character critique of some charts.
	if len([]rune(t)) > askedMaxRunes {
		return false
	}
	if strings.HasSuffix(t, "?") || strings.HasSuffix(t, "？") {
		return true
	}
	fields := strings.Fields(t)
	if len(fields) == 0 {
		return false
	}
	first := strings.Trim(strings.ToLower(fields[0]), ",.:;!\"'")
	switch first {
	case "what", "why", "how", "when", "where", "which", "who", "whose",
		"did", "does", "do", "is", "are", "was", "were", "can", "should", "would",
		"что", "чем", "почему", "зачем", "как", "какой", "какая", "какие", "когда",
		"где", "куда", "откуда", "сколько", "кто", "чей", "можно", "нужно", "надо":
		return true
	}
	return false
}

// questionStem folds a message to the form two askings of the same question
// share: lowercase, letters and digits only. Fewer than five words is not a
// question worth matching on.
func questionStem(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	fields := strings.Fields(b.String())
	if len(fields) < 5 && cjkfold.CountCJK(text) < 5 {
		// Chinese, Japanese and Korean write no separator between words, so a
		// question in those scripts is a single field however much it asks, and
		// the five-word bar dropped every one of them: a person could ask the
		// same thing weekly and deja never noticed (#1346). Their characters are
		// the words.
		return ""
	}
	return strings.Join(fields, " ")
}

// topTouchedFiles returns the files a session worked on most, busiest first.
func topTouchedFiles(ms []model.Message) []string {
	count := map[string]int{}
	for _, m := range ms {
		if m.Role != roleFiles {
			continue
		}
		countTouchedPaths(count, m.Text)
	}
	return rankTouched(count)
}

// countTouchedPaths tallies the file paths in one `files` record's text, one
// per line, skipping deja's own injected artifacts.
func countTouchedPaths(count map[string]int, text string) {
	for _, p := range strings.Split(text, "\n") {
		if p = strings.TrimSpace(p); p != "" && !agentOwnedFile(p) {
			count[p]++
		}
	}
}

// rankTouchedCounted returns the counts behind the ranking, which the
// incremental merge needs: two ranked lists cannot be fused into a correct one
// from position alone (#1304).
func rankTouchedCounted(count map[string]int) ([]string, []int) {
	paths := rankTouched(count)
	hits := make([]int, len(paths))
	for i, p := range paths {
		hits[i] = count[p]
	}
	return paths, hits
}

// topTouchedCounted is topTouchedFiles with those counts.
func topTouchedCounted(ms []model.Message) ([]string, []int) {
	count := map[string]int{}
	for _, m := range ms {
		if m.Role != roleFiles {
			continue
		}
		countTouchedPaths(count, m.Text)
	}
	return rankTouchedCounted(count)
}

// rankTouched orders touched paths by recurrence and caps the list, the shape
// SessionMeta.Touched holds.
func rankTouched(count map[string]int) []string {
	if len(count) == 0 {
		return nil
	}
	out := make([]string, 0, len(count))
	for p := range count {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if count[out[i]] != count[out[j]] {
			return count[out[i]] > count[out[j]]
		}
		return out[i] < out[j]
	})
	if len(out) > touchedFileCap {
		out = out[:touchedFileCap]
	}
	return out
}

func metaWithOrd(meta SessionMeta, ord uint32) SessionMeta {
	meta.Ord = ord
	return meta
}

func nextSessionOrd(sessions map[string]SessionMeta) uint32 {
	var maxOrd uint32
	for _, meta := range sessions {
		if meta.Ord > maxOrd {
			maxOrd = meta.Ord
		}
	}
	return maxOrd + 1
}

// sessionWords counts the words of a whole session, which is the document
// length BM25 is supposed to normalise by. Runs of letters, digits and the
// characters identifiers are made of, matching how the ranking side counts.
func sessionWords(ms []model.Message) int {
	n := 0
	for _, m := range ms {
		inWord := false
		for _, r := range m.Text {
			word := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
			if word && !inWord {
				n++
			}
			inWord = word
		}
	}
	return n
}

// sessionFromMeta is the one place a manifest entry becomes a session. It
// carries Touched, which retrieval used to copy by hand in a second, nearly
// identical constructor — so a caller reading it off `Recent` got an empty
// slice on every session and no error. Silence is the failure mode, and it
// already cost one wrong measurement: reading Touched from Recent reported 0
// of 1153 sessions carrying files while the manifest held them (#633).
//
// SessionMeta.Asked and SessionMeta.Hit have no counterpart on model.Session
// and are read from the manifest directly; they are not dropped here.
func sessionFromMeta(meta SessionMeta) model.Session {
	return model.Session{
		ID: meta.ID, Harness: meta.Harness, Project: meta.Project, Path: meta.Path,
		Title: meta.Title, AgentTitle: meta.AgentTitle, Started: meta.Started, Updated: meta.Updated, Touched: meta.Touched,
		GaveUp: meta.GaveUp,
		Words:  meta.Words,
		Kind:   meta.Kind, Parent: meta.Parent, Agent: meta.Agent,
		OrigID: meta.OrigID, RemoteID: meta.RemoteID, From: meta.From, Lifecycle: meta.Lifecycle, LifecycleNote: meta.LifecycleNote, LifecycleAt: meta.LifecycleAt,
	}
}

// carriedText is a stored or synced turn's text as this version's reader
// hands it back. A rebuild re-reads every transcript it still can, but a
// session whose transcript is gone, compacted away or imported is rebuilt from
// records.bin, which holds what an older reader stored, and a peer on an older
// build sends the same — for Claude Code, a prompt still inside its
// <pasted_content> wrapper.
func carriedText(harness, role, text string) string {
	if harness == "claude" && role == "user" {
		return sources.UnwrapPastedContent(text)
	}
	return text
}

// retitleWrappedPaste clears a title an older reader cut from a prompt still
// inside its paste wrapper, so metaForSession derives it again from the
// unwrapped turns.
func retitleWrappedPaste(s *model.Session) {
	if s.Harness == "claude" && strings.Contains(s.Title, "<pasted_content id=") {
		s.Title = ""
		s.AgentTitle = false
	}
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

// widenSpan takes the span of a file that shares key's id and does not own
// its row into that row. The owner merges the earlier files' span in when it
// is read second, so without this the row's Started and Updated followed the
// file names' order, where an update takes every file's span (#4253).
func widenSpan(sessions map[string]SessionMeta, key string, s model.Session) {
	meta, ok := sessions[key]
	if !ok {
		return
	}
	meta.Started, meta.Updated = widerSpan(meta.Started, meta.Updated, s.Started, s.Updated)
	meta.SharedStarted, meta.SharedUpdated = widerSpan(meta.SharedStarted, meta.SharedUpdated, s.Started, s.Updated)
	sessions[key] = meta
}

// ownerRow is the row of s, which owns it. prev is the row held before, and
// collided says whether s collided with it, and meta is s's own row,
// metaForSession's. The span of the files that share
// the id stays in the row, so reading the owner alone does not drop it
// (#4574): prev's own span when prev was another file sharing the id, a
// collision or a stub with nothing to index, and what prev kept when it was
// the same file. A row s supersedes, a ZCode snapshot restored into the
// database, gives it nothing.
func ownerRow(prev SessionMeta, s model.Session, meta SessionMeta, ord uint32, collided bool) SessionMeta {
	meta = metaWithOrd(meta, ord)
	switch {
	case prev.Path == "" || prev.Path == s.Path:
		meta.SharedStarted, meta.SharedUpdated = prev.SharedStarted, prev.SharedUpdated
	case collided || prev.NoText:
		meta.SharedStarted, meta.SharedUpdated = widerSpan(prev.SharedStarted, prev.SharedUpdated, prev.Started, prev.Updated)
	}
	meta.Started, meta.Updated = widerSpan(meta.Started, meta.Updated, meta.SharedStarted, meta.SharedUpdated)
	return meta
}

// rowRetaken is a row about to be taken again from its files: the span of the
// copies beside its owner goes, and a deleted owner gives up the row until its
// records are read back in, as a rebuild reads them after the files on disk.
// So does an owner read again whole, which takes the row back if it still
// holds the session; held, it kept a file that had moved on to another id.
func rowRetaken(row SessionMeta, keptOwner string, reread map[string]FileState) SessionMeta {
	row.SharedStarted, row.SharedUpdated = time.Time{}, time.Time{}
	_, again := reread[row.Path]
	if keptOwner != "" && row.Path == keptOwner || again && copyable(row.Path) {
		row.Path, row.Shared = "", false
	}
	return row
}

// widerSpan is the span covering both.
func widerSpan(started, updated, s2, u2 time.Time) (time.Time, time.Time) {
	if !s2.IsZero() && (started.IsZero() || s2.Before(started)) {
		started = s2
	}
	if u2.After(updated) {
		updated = u2
	}
	return started, updated
}

// claimSession is attributeSession for a session read this pass. A transcript
// with nothing to index is not a second conversation: Gemini CLI's resume
// leaves a file holding only the preamble deja strips, under the id of the
// transcript it appends to. Sort order handed that file the row, and with no
// records under it the session was dropped from the index (#4213). Either
// side can be the empty one, since either can be read first. Only where the
// pair would be reported as a clash: the store pairs attributeSession knows
// (goose, opencode, codex) keep their own rule.
func claimSession(held SessionMeta, s model.Session) (owns, collided bool) {
	owns, collided = attributeSession(held, s)
	if collided {
		if !holdsText(s) {
			return false, false
		}
		if held.NoText {
			return true, false
		}
	}
	return owns, collided
}

// attributeSession decides which of two transcripts sharing an id owns the
// manifest row, and whether they collided at all. Lexicographically smallest
// path wins, so the answer does not depend on which file was read first.
func attributeSession(held SessionMeta, s model.Session) (owns, collided bool) {
	if held.Path == "" || s.Path == "" || held.Path == s.Path {
		return true, false
	}
	merged.Add(1)
	// goose 1.10 moved its sessions into sessions.db and left the JSONL files
	// where they were, so after a migration the same conversation is in both
	// stores under one id — the db row keeps the id the file was named after.
	// That is one conversation across two storage generations, not two
	// transcripts clashing, and sort order decided it: `<stamp>.jsonl` sorts
	// below `sessions.db`, so every migrated session was filed under the
	// superseded copy and counted as a collision the reader can do nothing
	// about (#2066). The live store owns the row and the pair is not reported.
	if s.Harness == "goose" {
		if newIsDB, heldIsDB := isGooseStore(s.Path), isGooseStore(held.Path); newIsDB != heldIsDB {
			return newIsDB, false
		}
	}
	// opencode writes a per-session diff file beside its database and both
	// carry the same session id — one conversation in two stores, the same
	// shape as the goose migration above. The database row owns the session;
	// the diff is that session's account of what it changed, and the messages
	// merge either way. Reporting the pair told a reader with 84 such diffs
	// that 84 of their sessions were clashing, which is a data problem they do
	// not have (#3791).
	if s.Harness == "opencode" {
		if newIsDiff, heldIsDiff := isOpencodeDiff(s.Path), isOpencodeDiff(held.Path); newIsDiff != heldIsDiff {
			return !newIsDiff, false
		}
	}
	// Codex writes an interactive session to its rollout and a line of it to
	// history.jsonl under the same id. The rollout holds the conversation and
	// the directory it ran in; the history line holds the prompt and the
	// project "history". Sort order gave the row to history.jsonl, so a TUI
	// session was filed outside its project and resume refused it as an exec
	// entry (#4180). TRAE writes the same pair, and Claude Code's prompt log
	// sorts ahead of its projects folder the same way (#5046).
	if s.Harness == "codex" || s.Harness == "trae" || s.Harness == "claude" {
		if newIsHist, heldIsHist := isCodexHistory(s.Path), isCodexHistory(held.Path); newIsHist != heldIsHist {
			return !newIsHist, false
		}
	}
	// A ZCode snapshot restored into the CLI database is the same
	// conversation there under the same id, and the snapshot stays on disk
	// unchanged, so an incremental pass never re-reads it to skip it. The
	// database owns the row (#4432).
	if s.Harness == "zcode" {
		if newIsSnap, heldIsSnap := isZCodeSnapshot(s.Path), isZCodeSnapshot(held.Path); newIsSnap != heldIsSnap {
			return !newIsSnap, false
		}
	}
	return s.Path < held.Path, true
}

// coversKept reports whether an arrival holds what the kept row indexed, which
// a moved transcript does and a different file under the same id does not. The
// test is the row's last message: a move carries it, and so does a move that
// grew on the way. Gemini CLI's resume stub gains turns of its own after the
// transcript it was resumed from is deleted, and taking it for the move threw
// away that transcript's records for good (#4213). A count alone would not do:
// the stub can outgrow a short transcript. A row from before LastMsg was kept
// has nothing to compare, so it falls back to the count.
func coversKept(r model.Session, meta SessionMeta) bool {
	if meta.LastMsg == 0 {
		return len(r.Messages) >= meta.Counted
	}
	for i := len(r.Messages) - 1; i >= 0; i-- {
		m := r.Messages[i]
		// Fingerprinted as stored: the row's was taken after redaction, and
		// the arrival has not been redacted yet.
		m.Text, _, _ = indexedText(m.Text)
		if messageFingerprint(m) == meta.LastMsg {
			return true
		}
		// The command may have been indexed before its result landed; the
		// re-read adds the exit status to the same message. Codex compressing a
		// rollout in place is still the move, so match it without the suffix.
		if base := exitSuffix.ReplaceAllString(m.Text, ""); base != m.Text {
			m.Text = base
			if messageFingerprint(m) == meta.LastMsg {
				return true
			}
		}
	}
	return false
}

// holdsKeptRecords reports whether r holds every turn filed under the kept
// file at path. A copy of the session that only shares its last turn is not
// the file moved, and taking it for one dropped the turns only the deleted
// file held. Asked only once coversKept has matched, which is rare.
func holdsKeptRecords(dir string, m Manifest, r model.Session, path string) bool {
	recs, err := recordsForKey(filepath.Join(dir, "records.bin"), tablesFromManifest(m), r.Harness+":"+r.ID)
	if err != nil {
		return false
	}
	have := map[uint64]bool{}
	for _, msg := range r.Messages {
		msg.Text, _, _ = indexedText(msg.Text)
		have[messageFingerprint(msg)] = true
		if base := exitSuffix.ReplaceAllString(msg.Text, ""); base != msg.Text {
			msg.Text = base
			have[messageFingerprint(msg)] = true
		}
	}
	for _, rec := range recs {
		if rec.SourcePath != path {
			continue
		}
		msg := model.Message{Role: rec.Role, Text: carriedText(r.Harness, rec.Role, rec.Text), Time: rec.Time}
		if have[messageFingerprint(msg)] {
			continue
		}
		if base := exitSuffix.ReplaceAllString(msg.Text, ""); base != msg.Text {
			msg.Text = base
			if have[messageFingerprint(msg)] {
				continue
			}
		}
		return false
	}
	return true
}

// exitSuffix is the status the parsers append to a command once its result is
// read: "  → exit N".
var exitSuffix = regexp.MustCompile(`  → exit -?\d+$`)

// holdsText reports whether any message of s has text left to index once
// plumbing is stripped. Stripping is idempotent, so it answers the same before
// and after preRedactSessions.
func holdsText(s model.Session) bool {
	for _, m := range s.Messages {
		if strings.TrimSpace(stripSelfRecall(m.Text)) != "" {
			return true
		}
	}
	return false
}

// isCodexHistory reports whether a path is a prompt log rather than a
// transcript: Codex's, TRAE's or Claude Code's history.jsonl. Named, for the
// reason isGooseStore gives.
func isCodexHistory(path string) bool {
	return sources.IsPromptLog(path)
}

// promptLogTakeover reports whether a file new to this pass is the transcript
// of a session the index holds from a prompt log alone. Claude Code writes the
// prompt to history.jsonl before the transcript line, and deja's own prompt
// hook runs a pass in between, so a new session's first prompt is indexed from
// the log and its transcript lands a pass later. The append path can only add
// to what is stored; the replacement path drops the log's copy, which is what a
// rebuild, reading the transcript and skipping the line, gives. A Codex rollout
// after its history line is the same pair. The id is in the file name: the
// whole stem for Claude, the trailing UUID of a rollout.
func promptLogTakeover(changed, oldFiles map[string]FileState, sessions map[string]SessionMeta) bool {
	logged := map[string]bool{}
	for _, meta := range sessions {
		if isCodexHistory(meta.Path) {
			logged[meta.ID] = true
		}
	}
	if len(logged) == 0 {
		return false
	}
	for p := range changed {
		if _, known := oldFiles[p]; known || isCodexHistory(p) {
			continue
		}
		stem := filepath.Base(fileStem(p))
		if logged[stem] || (len(stem) > 36 && logged[stem[len(stem)-36:]]) {
			return true
		}
	}
	return false
}

// promptLogTakeovers names the sessions of this batch whose row came from a
// prompt log and that a transcript now holds.
func promptLogTakeovers(ss []model.Session, held map[string]SessionMeta) map[string]bool {
	out := map[string]bool{}
	for _, s := range ss {
		key := s.Harness + ":" + s.ID
		if meta, ok := held[key]; ok && isCodexHistory(meta.Path) && !isCodexHistory(s.Path) {
			out[key] = true
		}
	}
	return out
}

// freshPromptWait is how long a prompt-log line with no transcript beside it
// waits before it is taken for a session whose transcript is gone. Claude Code
// writes the transcript line a moment after the prompt; a pass in between
// would index the prompt twice and leave the takeover to a full rewrite.
const freshPromptWait = 5 * time.Minute

// waitsForTranscript reports whether a prompt log's tail holds a session the
// index does not know whose last prompt is younger than freshPromptWait. The
// file is then left for the next pass to read from the same offset, by which
// time the transcript is on disk and the parser skips the line.
func waitsForTranscript(ss []model.Session, held map[string]SessionMeta, now time.Time) bool {
	for _, s := range ss {
		if _, ok := held[s.Harness+":"+s.ID]; ok {
			continue
		}
		if !s.Updated.IsZero() && now.Sub(s.Updated) < freshPromptWait {
			return true
		}
	}
	return false
}

// dropShadowedPrompts drops a session read from a prompt log when a transcript
// holds the same id, in this batch or in the rows the index already has. A
// prompt log is read for sessions whose transcript is not on disk, and one deja
// kept after the client deleted it is exactly that: its prompts merged into
// the kept conversation a second time, at the log's own instant, and on the
// append path the log took the row because the kept path was gone (#5046). A
// rebuild sees the kept transcript in its batch, an incremental pass in held,
// so both drop the same lines.
func dropShadowedPrompts(ss []model.Session, held map[string]SessionMeta) []model.Session {
	transcript := map[string]bool{}
	logs := false
	for _, s := range ss {
		if isCodexHistory(s.Path) {
			logs = true
		} else {
			transcript[s.Harness+":"+s.ID] = true
		}
	}
	if !logs {
		return ss
	}
	out := make([]model.Session, 0, len(ss))
	for _, s := range ss {
		if isCodexHistory(s.Path) {
			key := s.Harness + ":" + s.ID
			if transcript[key] {
				continue
			}
			if meta, ok := held[key]; ok && meta.Path != "" && !isCodexHistory(meta.Path) {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

// isZCodeSnapshot reports whether a ZCode session's path is a snapshot an
// older ZCode left: the CLI database's sessions carry their directory and the
// transcripts end in .jsonl.
func isZCodeSnapshot(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".json")
}

// isOpencodeDiff reports whether a path is one of opencode's per-session diff
// files rather than its database. Named, not compared against
// sources.OpencodeDiffDir(), for the reason isGooseStore gives: the row deja
// already holds was written by an earlier pass that may have read a
// differently configured root.
func isOpencodeDiff(path string) bool {
	if !strings.EqualFold(filepath.Ext(path), ".json") {
		return false
	}
	return filepath.Base(filepath.Dir(path)) == "session_diff"
}

// isGooseStore reports whether a path is goose's database rather than one of
// the JSONL files beside it. Named, not compared against sources.GooseDB(),
// because the row deja already holds was written by an earlier pass that may
// have read a differently configured root.
func isGooseStore(path string) bool {
	return strings.EqualFold(filepath.Base(path), "sessions.db")
}

// replacementPassMarker opens the line the replacement-grade pass prints, and
// is what the tests that care which path ran match on. They used to match
// "incremental index", which is why that phrase survived in a line a user
// reads: an assertion on prose keeps the prose from being fixed.
const replacementPassMarker = "re-reading"

// pluralS keeps "1 sessions" off the first line anyone sees from deja (#737).
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// pluralThem is pluralS for the pronoun that follows it, so a run never says
// "1 line skipped, deja could not read them".
func pluralThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

func sessionTitle(s model.Session) string {
	t, _ := sessionTitleFrom(s)
	return t
}

// sessionTitleFrom derives a session's title and reports whether it is the
// agent's own words rather than the reader's.
//
// A user turn always wins; the assistant's opening line fills in when there is
// none — a session the agent opened itself, or one whose prompts are all
// harness plumbing. The alternative was the blank line these printed in `deja
// last` and on the first screen (#692). A session of nothing but tool output
// has neither, and printed an empty bracket in `last` against a dash in
// `stats`; it is named after its first output instead.
func sessionTitleFrom(s model.Session) (title string, fromAgent bool) {
	if t := earliestTitle(s.Messages, "user"); t != "" {
		if thinTitle(t) {
			// A session that opens with a greeting was named after it: on a real
			// 800-session store, 17 rows read "hi" and 3 "привет", with the turn
			// that says what the work was one line below (#790). No word list —
			// any of those is language-bound, and this store has two languages
			// in the sample already. A turn too short to name anything gives way
			// to the next one that can, and stands on its own when there is
			// none.
			if next := nextSubstantialTitle(s.Messages, t); next != "" {
				return next, false
			}
		}
		return t, false
	}
	if t := earliestTitle(s.Messages, "assistant"); t != "" {
		return t, true
	}
	if t := earliestTitle(s.Messages, roleToolOutput); t != "" {
		return toolOutputTitle(t), false
	}
	// Nothing here is worth a title on its own — every turn is harness
	// plumbing, a CLI's own stdout or a notification the runtime spliced in.
	// #1100 named the tool-only session so it would stop listing as an empty
	// bracket; this is the same session one step further down. The surfaces
	// that used to recover a title read .Messages, and they are all fed by
	// Recent, which returns metadata alone, so the recovery never ran (#2548).
	if t := earliestAnyText(s.Messages); t != "" {
		return harnessOutputTitle(t), false
	}
	return "", false
}

// harnessOutputTitlePrefix marks a title borrowed from what the harness itself
// wrote, the way toolOutputTitlePrefix marks one borrowed from tool output: it
// rides in the text so every surface says the same thing.
const harnessOutputTitlePrefix = "harness output: "

// TitleIsBorrowed reports that a title is not what a person typed: the ingest
// borrows one from what the harness or a tool wrote when a session opens with
// plumbing, and marks it in the text. A surface that picks a phrase out of
// titles has to skip those, or it suggests a fleet's paperwork (#3714).
func TitleIsBorrowed(title string) bool {
	t := strings.TrimSpace(title)
	return strings.HasPrefix(t, harnessOutputTitlePrefix) || strings.HasPrefix(t, toolOutputTitlePrefix)
}

// titlePlaceholder reports that a title is only standing in until the session
// says something of its own. The incremental path fills a title in when it is
// empty; a session that opens with a slash command would otherwise keep the
// stand-in forever, with the reader's own first question one line below it.
func titlePlaceholder(t string) bool {
	return t == "" || strings.HasPrefix(t, harnessOutputTitlePrefix)
}

// titleRankOfRow is where a row's title sits in sessionTitleFrom's order: a
// question (4), a turn too thin to name anything (3), the agent's words (2),
// tool output (1), plumbing or nothing (0). A pass that sees only part of a
// session keeps a title unless what it read outranks it, as a rebuild of the
// whole session would: a thin opening gives way to the first real question.
func titleRankOfRow(title string, fromAgent bool) int {
	switch {
	case titlePlaceholder(title):
		return 0
	case strings.HasPrefix(title, toolOutputTitlePrefix):
		return 1
	case fromAgent:
		return 2
	case thinTitle(title):
		return 3
	}
	return 4
}

func harnessOutputTitle(t string) string {
	return harnessOutputTitlePrefix + truncateTitle(stripPlumbingTag(t), 60-len([]rune(harnessOutputTitlePrefix)))
}

// stripPlumbingTag unwraps the element a harness wraps its own output in —
// <local-command-stdout>…</local-command-stdout> and its neighbours — so the
// row reads as the sentence rather than as markup.
func stripPlumbingTag(t string) string {
	t = strings.TrimSpace(t)
	if !strings.HasPrefix(t, "<") {
		return t
	}
	end := strings.Index(t, ">")
	if end < 0 {
		return t
	}
	name := t[1:end]
	if i := strings.IndexAny(name, " \t"); i >= 0 {
		name = name[:i]
	}
	rest := strings.TrimSpace(t[end+1:])
	rest = strings.TrimSuffix(rest, "</"+name+">")
	return strings.TrimSpace(rest)
}

// earliestAnyText is the first thing a session says, whoever said it, for the
// last resort above.
func earliestAnyText(ms []model.Message) string {
	best := ""
	var bestAt time.Time
	for _, msg := range ms {
		// Work records are not something anyone said — a file list, an
		// invocation, a replaced span — and a session named after one reads as
		// a sentence it never contained.
		if msg.Role == roleFiles || msg.Role == roleCommand || msg.Role == roleEdit {
			continue
		}
		t := strings.TrimSpace(msg.Text)
		if t == "" {
			continue
		}
		switch {
		case best == "":
		case bestAt.IsZero(), msg.Time.IsZero():
			continue
		case !msg.Time.Before(bestAt):
			continue
		}
		best, bestAt = t, msg.Time
	}
	return best
}

// toolOutputTitlePrefix marks a title borrowed from tool output. It rides in
// the title text rather than in a flag beside it so that every surface —
// `last`, `stats`, the MCP listing, a synced peer — says the same thing
// without a second field having to travel with it.
const toolOutputTitlePrefix = "tool output: "

func toolOutputTitle(t string) string {
	return toolOutputTitlePrefix + truncateTitle(t, 60-len([]rune(toolOutputTitlePrefix)))
}

// earliestTitle picks the first turn of a role by the clock, falling back to
// file order for records that carry no time.
//
// Taking whichever line sat first in the file disagreed with the import path,
// which grew this fallback in #692 and orders by time — so one store titled
// locally and the same store imported elsewhere could disagree (#769).
func earliestTitle(ms []model.Message, role string) string {
	best := ""
	var bestAt time.Time
	for _, msg := range ms {
		if msg.Role != role {
			continue
		}
		t := strings.TrimSpace(msg.Text)
		if !titleWorthy(t) {
			continue
		}
		switch {
		case best == "":
		case bestAt.IsZero(), msg.Time.IsZero():
			// Nothing to compare: the first one found stands.
			continue
		case !msg.Time.Before(bestAt):
			continue
		}
		best, bestAt = t, msg.Time
	}
	return best
}

// thinTitle reports that a turn is too short to name a session by.
//
// Two words and twelve runes: "hi", "привет", "say ok", "reply ok" are inside
// it, and the short instructions worth keeping — "fix the build", "run the
// tests" — are not. A length rule rather than a vocabulary is the only version
// of this that works in every language the store holds.
func thinTitle(t string) bool {
	return titleWords(t) <= 2 && len([]rune(t)) <= 12
}

// titleWords counts what a reader would call words. Whitespace separates them
// in most scripts and in none of the CJK ones, where a whole sentence is one
// field and a rune-length rule alone called it a greeting: 为什么测试失败了 is
// eight runes and says why the test failed (review of #3328). Thai is spaced
// the same way, which is why Unspaced covers both.
func titleWords(t string) int {
	words, inWord := 0, false
	for _, r := range t {
		switch {
		// Hangul is in cjkfold's CJK set because that is what folds, but Korean
		// writes its words apart like Latin does: counting each syllable as a
		// word made "고마워" — one word, three syllables — look substantial and
		// left it as a session's name (review of #3328).
		case cjkfold.Unspaced(r) && !unicode.Is(unicode.Hangul, r):
			words++
			inWord = false
		case unicode.IsSpace(r):
			inWord = false
		default:
			if !inWord {
				words++
				inWord = true
			}
		}
	}
	return words
}

// nextSubstantialTitle is the first later user turn that can name the session.
// Ordered by the clock like earliestTitle, so a store titled locally and the
// same store imported elsewhere agree.
func nextSubstantialTitle(ms []model.Message, skip string) string {
	best := ""
	var bestAt time.Time
	for _, msg := range ms {
		if msg.Role != "user" {
			continue
		}
		t := strings.TrimSpace(msg.Text)
		if t == skip || !titleWorthy(t) || thinTitle(t) {
			continue
		}
		switch {
		case best == "":
		case bestAt.IsZero(), msg.Time.IsZero():
			continue
		case !msg.Time.Before(bestAt):
			continue
		}
		best, bestAt = t, msg.Time
	}
	return best
}

// titleWorthy reports whether a user turn is the sentence a person would
// recognise the session by. Harness plumbing arrives with the user role — a
// slash command's expansion, a task notification, the compaction caveat — and
// naming a session after one of those is how the titles in #636 happened.
func titleWorthy(t string) bool {
	// The same list the repeat-question counter rejects: the two were written
	// apart and drifted, so a session whose store title was thin could be
	// renamed after a resume preamble — "This session is being continued from a
	// previous conversation…" — which notAsked has rejected all along (review
	// of #3328).
	return strings.TrimSpace(t) != "" && !harnessPreamble(t)
}

// widenThinSourceTitle gives a name too short to name anything way to the
// question under it, the way a derived title has since #790: dsh's title model
// answered "ok" and "47" for sessions whose user turn is a whole sentence — 39
// of the 43 on this machine's store (#3328). Notes name themselves.
func widenThinSourceTitle(s model.Session, title string) string {
	if s.Harness == "deja" || !thinTitle(title) {
		return title
	}
	next := nextSubstantialTitle(s.Messages, title)
	if next == "" {
		return title
	}
	next, _ = redact.Text(next)
	return truncateTitle(next, 60)
}

// boundSourceTitle collapses and bounds a title the source authored, the way
// a derived one has always been.
//
// Notes are exempt. A promoted note's title ends in its state — "… [rejected]"
// — and the state is what every one-line surface reads it for, so cutting the
// tail would drop exactly the part that matters and, worse, make a state
// change invisible to the comparison that decides whether to rewrite the row
// (#R11).
//
// What makes the exemption safe is not that the text is deja's own — the notes
// file is edited by hand, so a note title is whatever someone typed (#2063) —
// but that every reader clips it for its own layout: `last`, `stats`, the view
// page and the MCP payloads through SafeNoteTitle, hook-context through
// trimBriefTitle. TestEverySurfaceThatPrintsANoteTitleBoundsIt holds that, and
// a new surface has to join it (#2092).
func boundSourceTitle(harness, title string) string {
	if harness == "deja" {
		return title
	}
	return truncateTitle(title, 60)
}

func truncateTitle(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// recordsForKey collects one session's records. It peeks the key field and
// skips the body of everything else: decoding all of them to keep a few
// hundred cost 300 ms per session on an 80 MB log, paid twice on every bare
// `deja` by the two lines that recover text from a hash (#625).
func recordsForKey(path string, t *recordTables, key string) ([]Record, error) {
	var out []Record
	err := eachRecordForKeys(path, t, map[string]bool{key: true}, func(r Record) {
		out = append(out, r)
	})
	return out, err
}

func redactForIngest(m *Manifest, sourcePath, text string) string {
	// Drop deja's own injected recall before anything else looks at the text,
	// so it is never counted, tokenized or stored.
	text = stripSelfRecall(text)
	// Canonicalise accented text to NFC so an "é" stored decomposed (base + a
	// combining mark, as some editors and macOS filesystems emit) matches a
	// query typed precomposed and the reverse. NFC is lossless — it names the
	// same characters — so unlike a fold it is safe to store, and it makes every
	// downstream surface (postings, snippet, digest) compare like against like
	// (#1098).
	text = nfcfold.Compose(text)
	// Redact the full text before capping: a secret straddling the cap
	// boundary would otherwise lose its closing marker and store raw.
	redacted, counts := redact.Text(text)
	if len(redacted) > maxIndexedText {
		// Cut on a rune boundary so a multibyte rune straddling the cap is not
		// split, leaving an invalid tail byte in the stored text.
		cut := maxIndexedText
		for cut > 0 && !utf8.RuneStart(redacted[cut]) {
			cut--
		}
		redacted = redacted[:cut]
		countClipped(m, sourcePath, "", 1)
	}
	n := counts.Total()
	if n == 0 || m == nil {
		return redacted
	}
	m.Redacted += n
	if m.RedactionRules == nil {
		m.RedactionRules = map[string]int{}
	}
	// The store, not the file kind: `deja stats` prints these as headings, and
	// "cline-sdk" is a word no other screen uses (#2238, the shape #2234 fixed
	// for the ingest counters).
	h := sources.HarnessForKind(harnessForPath(sourcePath))
	if h == "" {
		if _, ok := m.Files[sources.OpencodeDB()]; ok {
			h = "opencode"
		}
	}
	for rule, count := range counts {
		m.RedactionRules[h+":"+rule] += count
	}
	if sourcePath != "" && m.Files != nil {
		if fs, ok := m.Files[sourcePath]; ok {
			fs.Redactions += n
			m.Files[sourcePath] = fs
		} else if db := sources.OpencodeDB(); sourcePath != db {
			// opencode sessions carry their project dir as Path; the store
			// on record is the database file. Attribute stats there so
			// `deja sources` reports them.
			if fs, ok := m.Files[db]; ok {
				fs.Redactions += n
				m.Files[db] = fs
			}
		}
	}
	return redacted
}

func carryRedactions(m *Manifest, old Manifest, skip map[string]bool) {
	if m.RedactionRules == nil {
		m.RedactionRules = map[string]int{}
	}
	for p, f := range old.Files {
		if skip[p] || f.Redactions == 0 || m.Files == nil {
			continue
		}
		cur, ok := m.Files[p]
		if !ok {
			continue
		}
		cur.Redactions = f.Redactions
		m.Files[p] = cur
		m.Redacted += f.Redactions
	}
	skipHarness := map[string]bool{}
	for path, skipped := range skip {
		if !skipped {
			continue
		}
		// The store, matching the key the rules are filed under since #2238:
		// asking for the file kind here left "cline-sdk" against a "cline" key,
		// so nothing matched and every incremental pass carried the old counts
		// on top of the fresh ones (#2240).
		h := sources.HarnessForKind(harnessForPath(path))
		if h == "" && path == sources.OpencodeDB() {
			h = "opencode"
		}
		skipHarness[h] = true
	}
	for key, count := range old.RedactionRules {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		// Folded before the lookup: an index written before #2238 files these
		// by file kind, and a pass since then drops by store — so a stale
		// "cline-sdk" count survived its file being re-read and was added to
		// the fresh "cline" one when the report folded them (#2240).
		name := parts[0]
		if store := sources.HarnessForKind(name); store != "" {
			name = store
		}
		if !skipHarness[name] {
			m.RedactionRules[key] = count
		}
	}
}

// fromDatabase reports whether a record came out of a shared store rather than
// a file of its own, which is what exempts it from the changed-file rule: the
// database changes whenever any session in it does, and dropping every record
// that names it would take the untouched sessions along.
//
// The path settles it for goose and cursor, which name the database as the
// session's path. opencode names the project directory, so nothing about its
// path says "database" — the key names the harness (#2033). The same holds for
// every store read through OpenCode's schema (opencodeSchemaDBs).
func fromDatabase(r Record) bool {
	// The key first: asking the path first got this wrong for an opencode
	// project that lives inside another harness's root — a versioned ~/.claude,
	// say — where harnessForPath answers with that harness's kind.
	if h, _, ok := strings.Cut(r.Key, ":"); ok && inOpencodeSchemaDB(h, r.SourcePath) {
		return true
	}
	// A path that names a per-file kind settles it: two transcripts in
	// different projects can share a filename-derived id, and judging those by
	// key erased the sibling that was never re-read (#699).
	return storeHarness(r.SourcePath) != ""
}

// leftItsFile reports a session that a re-read file no longer holds, in a
// harness that keeps many sessions in one file. aider appends every launch to
// one history, and people delete it because it grows forever: the next launch
// starts a new file at the same path, and dropping by path took every session
// the deleted file held, which a deleted transcript keeps (#2970, #4332).
//
// A session the file still holds under another id has not left it: ids before
// #4332 were ordinals, and two launches in one second are told apart by order.
// held is aiderStarts of what the pass read.
func leftItsFile(r Record, reread map[string]bool, meta SessionMeta, held map[string]bool) bool {
	harness, _, _ := strings.Cut(r.Key, ":")
	return harness == "aider" && !reread[r.Key] && !held[aiderStart(r.SourcePath, meta.Started)]
}

// aiderStarts is the history path and start time of every aider session in
// ss, the one thing that names an aider session across a change of its id.
func aiderStarts(ss []model.Session) map[string]bool {
	out := map[string]bool{}
	for _, s := range ss {
		if s.Harness == "aider" {
			out[aiderStart(s.Path, s.Started)] = true
		}
	}
	return out
}

func aiderStart(path string, started time.Time) string {
	return path + "\x00" + started.UTC().String()
}

// readWholeThisPass reports whether the pass re-read this record's store in
// full. Only then may an old record be dropped because its key came back: a
// store read from a watermark hands back the new turns alone, and dropping the
// rest by key would take the earlier turns of a continued session with them.
//
// By store rather than by harness where the record names one: cursor keeps a
// database per workspace as well as the global one, and a first sight of a new
// workspace — opening a project — has no watermark, so a harness-wide flag let
// that pass replace sessions in the store it had only read the tail of.
func readWholeThisPass(r Record) bool {
	if len(passWholeStores) == 0 {
		return false
	}
	// opencode's records name a project directory or, from older passes, a
	// diff file; either way the session is the store's, read whole (#4207).
	// Kilo's and ZCode's are the same store shape (#4396); a record from their
	// transcript files is not a store's and never reaches this.
	if h, _, ok := strings.Cut(r.Key, ":"); ok && opencodeSchemaDBs[h] != nil {
		return passWholeStores[h]
	}
	if storeHarness(r.SourcePath) != "" {
		return passWholeStores[r.SourcePath]
	}
	harness, _, ok := strings.Cut(r.Key, ":")
	return ok && passWholeStores[harness]
}

// passWholeStores names the database-backed stores this pass read whole, by
// harness. Package state for the same reason passParsed is: a pass holds the
// directory lock.
var passWholeStores map[string]bool

// passFromWatermark names the database stores this pass read from their
// watermark, so only rows changed since came back. passWholeStores cannot say
// it: zed is in there on every pass, because the threads it does hand back
// come whole (#4341).
var passFromWatermark map[string]bool

// wholeStoresThisPass records them, under both the store path and the harness:
// a record names the first where it can and the second otherwise.
//
// Every store setDatabaseStoreWatermarks stamps belongs here, and storeHarness
// is the one list of them: a stamped store is parsed from its watermark, so its
// records must survive a pass that did not hand them back, and only a pass that
// read the store whole may drop one because its key came back.
func wholeStoresThisPass(changed, old map[string]FileState) {
	passWholeStores = map[string]bool{}
	passFromWatermark = map[string]bool{}
	// Rebuilt here rather than kept: a store that appeared since the last pass
	// is one the walk has to see.
	passStores = resolveStorePaths()
	passStoreHarness = new(sync.Map)
	passSchemaDBs.Store(nil)
	for p := range changed {
		harness := storeHarness(p)
		if harness == "" {
			continue
		}
		if old[p].LastUpdated != 0 {
			passFromWatermark[p] = true
		}
		if old[p].LastUpdated == 0 || rereadsWholeSessions(p) {
			passWholeStores[harness] = true
			passWholeStores[p] = true
		}
	}
}

// rereadsWholeSessions marks a store whose cursor selects sessions rather than
// messages: goose asks for every message of any session touched since the
// stamp, so a continued session hands back turns already counted, and adding
// them again grew the count on every pass. Starting the file over is the lesser
// wrong — it loses what an untouched session held, which is #2025 again for
// this one store, rather than a number that only climbs. Narrowing the clause
// instead costs the session its earlier turns (#2033), so the re-reading stays.
func rereadsWholeSessions(p string) bool {
	if harnessForPath(p) == "goose-db" {
		return true
	}
	switch storeHarness(p) {
	case "opencode", "kilocode", "zcode":
		// Read by session since #4207: a reply's text lands in a part created
		// before the last pass, so only a touched session read whole carries
		// it, and adding that to what the index held doubled the turns. Its
		// counts start over with each pass, the goose trade above.
		return true
	case "grok", "zed", "hermes", "openclaw", "crush":
		// The same shape: each asks for the sessions touched since the stamp
		// and hands them back whole, so what comes back replaces what the
		// index holds for that key rather than adding to it (#2075). The
		// clauses are session-scoped for exactly this reason — a message-scoped
		// one returns the newest turn alone, and replacing a session with it
		// loses the rest.
		return true
	case "cursor":
		// Since #4450 and #4451 a touched composer comes back with all its
		// bubbles, so a rename is read and the derived fields count every turn.
		return true
	}
	return false
}

// storeHarness names the harness whose shared store lives at p, and "" for
// anything else.
//
// harnessForPath answers with a kind, and three of the six stores share their
// kind name with the harness's own transcripts — grok and hermes both have
// files as well as a database, and a kind name cannot tell them apart. The
// store paths can, and a record that names one belongs to a store read from a
// watermark: it must not be dropped because the database changed, and it may be
// replaced when its key comes back.
func storeHarness(p string) string {
	if p == "" {
		return ""
	}
	// Per path, not per record: every record of a goose or cursor store names
	// the same database, and asking the registry again for each one was a
	// third of a one-message pass over 5,000 sessions (#4272).
	if h, ok := passStoreHarness.Load(p); ok {
		return h.(string)
	}
	h := resolveStoreHarness(p)
	passStoreHarness.Store(p, h)
	return h
}

// passStoreHarness memoises storeHarness for one pass; wholeStoresThisPass
// starts it over with passStores.
var passStoreHarness = new(sync.Map)

func resolveStoreHarness(p string) string {
	if h, ok := passStorePaths()[p]; ok {
		return h
	}
	if sources.IsHermesPGStore(p) {
		return "hermes"
	}
	for h, db := range passSchemaDBPaths() {
		if p == db {
			return h
		}
	}
	switch harnessForPath(p) {
	case "opencode-diff":
		// A diff file is read as its session from the database (#4207), so it
		// is that store's as much as the database file is.
		return "opencode"
	case "cursor-db":
		return "cursor"
	case "goose-db":
		return "goose"
	case "openclaw-db":
		return "openclaw"
	}
	return ""
}

// passStorePaths is the store-path half of storeHarness, resolved once.
//
// fromDatabase asks per record, and the answer needs the store paths, two of
// which are found by walking a directory. Measured at ~11 µs a record with the
// walk on the hot path, which is a second per hundred thousand records on every
// pass. A pass holds the directory lock, so the map lives beside
// passWholeStores and is built where that one is.
func passStorePaths() map[string]string {
	// wholeStoresThisPass builds it at the top of a pass, before anything asks.
	// The fallback is for a caller outside a pass — a search recovering a
	// damaged index reads records without one.
	if passStores == nil {
		passStores = resolveStorePaths()
	}
	return passStores
}

var passStores map[string]string

// passSchemaDBPaths is opencodeSchemaDBs resolved once a pass. Resolving
// opencode's stats two files when XDG_DATA_HOME is set, and storeHarness asks
// for every path it meets for the first time: on 40,000 transcripts that was
// a second of a pass that replaced 20 of them.
func passSchemaDBPaths() map[string]string {
	if m := passSchemaDBs.Load(); m != nil {
		return *m
	}
	m := make(map[string]string, len(opencodeSchemaDBs))
	for h, db := range opencodeSchemaDBs {
		m[h] = db()
	}
	// Only inside a pass: outside one nothing says when the stores move.
	if passesRunning.Load() > 0 {
		passSchemaDBs.Store(&m)
	}
	return m
}

var (
	passSchemaDBs atomic.Pointer[map[string]string]
	passesRunning atomic.Int32
)

// opencodeSchemaDBs are the databases read through OpenCode's schema, by
// harness. A session from one names its project directory as its path, not the
// database, so only the harness in its key says where it came from. Kilo CLI
// and ZCode vendor that schema, and knowing opencode's alone left their
// sessions re-read whole on every write to the database and appended to the
// records already held: a Kilo session doubled on each pass (#4396).
var opencodeSchemaDBs = map[string]func() string{
	"opencode": sources.OpencodeDB,
	"kilocode": sources.KiloDB,
	"zcode":    sources.ZCodeDB,
}

// inOpencodeSchemaDB reports whether a session of harness h whose path is p
// came out of that harness's OpenCode-schema database.
func inOpencodeSchemaDB(h, p string) bool {
	db, ok := opencodeSchemaDBs[h]
	if !ok {
		return false
	}
	// opencode's diff files, Kilo's task files and ZCode's transcripts and
	// snapshots carry the same harness name; anything else is a project directory. A diff
	// record still counts as the database's through storeHarness, which files
	// the diff path under that store.
	//
	// Asked before the database path: resolving it can stat two files (with
	// XDG_DATA_HOME set, opencode's), and fromDatabase asks this of every
	// record an incremental pass holds.
	return !opencodeSchemaOwnFile(h, p) || p == db()
}

// opencodeSchemaOwnFile reports whether p is one of harness h's own files
// rather than its database or a project directory. Named, for the reason
// isGooseStore gives, and because fromDatabase asks it of every record held:
// asking the registry what a project directory was cost ~68 µs a record.
func opencodeSchemaOwnFile(h, p string) bool {
	switch h {
	case "opencode":
		return isOpencodeDiff(p)
	case "kilocode":
		return strings.EqualFold(filepath.Base(p), "api_conversation_history.json")
	case "zcode":
		// Its transcripts, and the snapshots an older ZCode left, each
		// rewritten whole (#4432).
		return strings.EqualFold(filepath.Ext(p), ".jsonl") || isZCodeSnapshot(p)
	}
	return false
}

func resolveStorePaths() map[string]string {
	out := map[string]string{sources.GrokDB(): "grok", sources.ZedDB(): "zed"}
	for _, db := range sources.HermesDBs() {
		out[db] = "hermes"
	}
	// One store per Crush project, each read from its own watermark (#4381).
	for _, db := range sources.CrushDBs() {
		out[db] = "crush"
	}
	return out
}

// fullyReadFiles drops the files a pass reads only part of. LastUpdated is
// stamped for database-backed stores alone (setStoreLastUpdated), and it is
// exactly what makes their parse partial: parseChangedFile hands it to the kind
// as the since cursor.
func fullyReadFiles(changed, old map[string]FileState) map[string]FileState {
	out := make(map[string]FileState, len(changed))
	for p, f := range changed {
		if old[p].LastUpdated > 0 && !rereadsWholeSessions(p) {
			continue
		}
		out[p] = f
	}
	return out
}

// copyIngestFiles hands the new manifest its own map: the old one belongs to
// the manifest this build read, which is still in use while the build runs. The
// files this pass will re-read arrive with their clip count zeroed — the pass
// records its own as it goes, and adding it to what the last pass found counted
// one long message twice.
func copyIngestFiles(old map[string]FileIngest, reread map[string]FileState, logTails map[string]map[string]bool) map[string]FileIngest {
	out := make(map[string]FileIngest, len(old))
	for p, e := range old {
		if ids, ok := logTails[p]; ok {
			e = clipsOutsideTail(e, ids)
		} else if _, ok := reread[p]; ok {
			e.Clipped = 0
			e.ClippedSessions = nil
		} else {
			e.ClippedSessions = maps.Clone(e.ClippedSessions)
		}
		out[p] = e
	}
	return out
}

// beginPass clears the parsers' skip counters, which belong to the pass that
// parsed. They used to be cleared only by the manifest fold, so a pass that died
// before writing left its count for the next one to report: one bad line on
// disk, "2 lines skipped" on screen, with the manifest agreeing (#2010).
//
// Called at every place a pass parses: this one and rebuildWithTombstones,
// which a recall reaches directly once an index is found damaged, without
// passing through updateIndex at all, and which forget and unforget call for
// themselves.
func beginPass() {
	sources.DiagBeginPass()
	passParsed = nil
	passRead = nil
	passWholeStores = nil
	passFromWatermark = nil
	passSchemaDBs.Store(nil)
}

// passParsed is the set of files the pass in progress re-read. Package state
// for the same reason lastIngestFiles is: a pass holds the directory lock, so
// only one is ever in flight.
var passParsed map[string]bool

// passRead is the files a pass read without the open failing. The append path
// cannot use parsedThisPass — it reads a tail, so its counts add — but a file
// it read is a file that opens, and an error recorded when it did not has to
// go, or a permission blip stayed in doctor until a forced rebuild (#2015).
var passRead map[string]bool

// passWalked is the walk the pass in progress started from, copied before the
// pass drops the files it could not read from that same map. Deferred needs
// the state the walk saw for exactly those files (#5105). Nil outside a pass,
// so a manifest write that is not one (sync, forget) keeps Deferred as is.
var passWalked map[string]FileState

// walkedThisPass records the walk a pass starts from, until the returned func
// runs. A rebuild inside a pass keeps the outer walk, taken before the outer
// pass changed anything.
func walkedThisPass(files map[string]FileState) func() {
	if passWalked != nil {
		return func() {}
	}
	passWalked = maps.Clone(files)
	return func() { passWalked = nil }
}

// foldDeferred updates the transcripts held as waiting for zstd. A file this
// pass left unread for that reason is held with the state the walk saw; one
// the index now holds, or the walk no longer lists, is let go. A file the pass
// did not try again keeps its entry: nothing about it has changed.
func foldDeferred(m *Manifest, noZstd map[string]bool) {
	if passWalked == nil {
		return
	}
	// A new map: m.Deferred may be shared with the cached manifest.
	out := map[string]FileState{}
	for p, f := range m.Deferred {
		_, walked := passWalked[p]
		_, held := m.Files[p]
		if walked && !held {
			out[p] = f
		}
	}
	for p := range noZstd {
		f, walked := passWalked[p]
		if _, held := m.Files[p]; walked && !held {
			out[p] = f
		}
	}
	if len(out) == 0 {
		out = nil
	}
	m.Deferred = out
}

// readThisPass records a file a pass opened and parsed.
func readThisPass(files map[string]FileState) {
	if passRead == nil {
		passRead = map[string]bool{}
	}
	for p := range files {
		passRead[p] = true
	}
}

// parsedThisPass records which files a pass read, so the fold can start their
// counts over rather than adding to what an earlier pass left (#2015).
func parsedThisPass(files map[string]FileState) {
	if passParsed == nil {
		passParsed = map[string]bool{}
	}
	for p := range files {
		passParsed[p] = true
	}
}

func updateIndex(dir, harness, scope string, files map[string]FileState, force bool, progress io.Writer) error {
	err := updateIndexOnce(dir, harness, scope, files, force, progress)
	// A pass that met a hole in the record log stops there rather than
	// committing what it carried before it, and the store is rebuilt from the
	// sources instead. Carrying on dropped every record after the hole and
	// wrote a manifest that called the loss clean.
	if IsCorrupt(err) {
		if progress != nil {
			fmt.Fprintf(progress, "deja: %s (%v), rebuilding ...\n", damagedOrOutdated(err), err)
		}
		return rebuild(dir, harness, scope, files, progress)
	}
	return err
}

func updateIndexOnce(dir, harness, scope string, files map[string]FileState, force bool, progress io.Writer) error {
	return updateIndexPass(dir, harness, scope, files, force, true, progress)
}

// updateIndexPass is one pass. split lets it append what it can and hand the
// rest to a second pass; that second pass replaces without splitting again, so
// a file the append path keeps retrying cannot loop it.
func updateIndexPass(dir, harness, scope string, files map[string]FileState, force, split bool, progress io.Writer) error {
	// The walk as it came in: the pass below writes the kept files into files,
	// and a second pass handed that map would take them for files put back.
	var walked map[string]FileState
	if split {
		walked = maps.Clone(files)
	}
	defer readTo(files)()
	defer sources.PinZstd()()
	defer walkedThisPass(files)()
	// Cleared here rather than beside the other two: this build counts what
	// went away further down, before the incremental paths reset theirs, so a
	// reset down there would zero the number this build is about to report
	// (#1861).
	evicted.Store(0)
	beginPass()
	passesRunning.Add(1)
	defer func() {
		if passesRunning.Add(-1) == 0 {
			passSchemaDBs.Store(nil)
		}
	}()
	old, err := takeHandedManifest(dir)
	if err == nil && !recordsIntact(dir, old) {
		force = true // records.bin lost its tail to a crash; only a rebuild is safe
	}
	if err == nil && miningChanged(old) {
		force = true // search-only turned on or off: the sidecars go or come back whole
	}
	if err == nil && !force && newerIndex(old) {
		sayNewerIndex(progress, dir, old)
		return nil
	}
	// A transcript under a new name is not a new transcript. Settled before the
	// diff below, so the file is neither read again nor left behind as a row
	// pointing at a path that is gone: twenty renames of one 4 KB log left the
	// store holding twenty-one copies of it and twenty dead rows, with search
	// answering once and nothing on any screen saying so (#3546).
	// A renamed file another transcript shares a session with is read again
	// with its copies when the rename moves it past another of the session's
	// files: the row's owner is the smallest path, and a rebuild reads them in
	// path order. Followed by name alone, the row kept the moved file as its
	// owner where a rebuild hands it to a copy. A rename that keeps the order
	// changes nothing a rebuild would, and reading the session again sent it
	// down the replacing path: 20 s for 20 renames at 40,000 sessions (#5466).
	var regather []string
	if err == nil && !force {
		if pairs := detectRenamedFiles(old.Files, files); len(pairs) > 0 {
			reprojected := applyRenamedFiles(&old, pairs)
			heldBy := keysByFile(old.Sessions)
			for np := range pairs {
				for _, key := range heldBy[np] {
					if renameReorders(sessionFiles(old.Sessions[key]), pairs) {
						regather = append(regather, np)
						break
					}
				}
			}
			// Failing to record it costs the duplicate this exists to avoid,
			// not correctness: the pass below then treats the file as new.
			// A rename read again is recorded by the pass that reads it, which
			// writes the manifest anyway; one that fails leaves the rename to
			// be found again.
			var werr error
			if len(regather) == 0 || len(reprojected) > 0 {
				werr = writeManifestMeta(dir, old)
			}
			if werr == nil && len(reprojected) > 0 {
				reprojectSidecars(dir, old.Sessions, reprojected)
			}
			if werr == nil && progress != nil {
				fmt.Fprintf(progress, "deja: %d transcript%s renamed — the index followed the new name\n", len(pairs), pluralS(len(pairs)))
			}
		}
	}
	// The format is checked beside the version because the two answer different
	// questions: a store whose content rules are stale still reads, a store
	// whose layout this build cannot read does not. Every format bump so far
	// carried a version bump with it, so this has never fired — and that is the
	// assumption it stops the next one from depending on.
	if force || err != nil || old.Version != version || old.Format != onDiskFormat || old.Scope != scope {
		if progress != nil {
			// A store built by an earlier release re-reads its sources whole,
			// which on a large one is the longest deja ever makes anyone wait
			// — and it said only "indexing sessions", the same line a first
			// install prints. Every other reason for a full pass names itself:
			// damage says it is damage, a changed exclude list says so, and
			// `--rebuild` was asked for (#3500).
			if err == nil && !force && old.Version < version && old.Version != 0 {
				fmt.Fprintf(progress, "deja: this build reads a newer index than the one on disk (%d, was %d) — re-reading your sources once\n", version, old.Version)
			}
			if !hasProgressSink() {
				fmt.Fprintf(progress, "deja: indexing sessions into %s ...\n", displayPath(dir))
			}
		}
		return rebuild(dir, harness, scope, files, progress)
	}
	changed := map[string]FileState{}
	removed := map[string]bool{}
	for p, f := range files {
		if of, ok := old.Files[p]; !ok || !sameFile(of, f) || of.Kept { // a kept one put back is read again
			changed[p] = f
		}
	}
	// Recorded with its new mtime through files, never read (#5324).
	touched := dropTouched(changed, files, old.Files)
	for p := range old.Files {
		if p == syncImportPath {
			continue
		}
		if _, ok := files[p]; !ok {
			removed[p] = true
		}
	}
	// A store on a disk that is not mounted looks exactly like a store whose
	// files were deleted, and the sessions it held leave the index — after
	// which every surface reads "no agent history was found on this machine".
	// The files are still there, on a disk that is not, and reconnecting it
	// restores them; that is what this says (#900).
	//
	// Saying it once was not enough: the eviction still happened, so from the
	// next run on there was nothing left to compare against and the warning
	// stopped too. Records that came off a mount point stay in the index while
	// the volume is away, and the line repeats until it is back.
	gone := missingTrees(removed)
	for i := range gone {
		gone[i].renamed = renamedMount(gone[i].dir)
		if !gone[i].mount && gone[i].renamed == "" {
			continue
		}
		for p := range removed {
			if !strings.HasPrefix(p, gone[i].dir+string(filepath.Separator)) {
				continue
			}
			if of, ok := old.Files[p]; ok {
				files[p] = of
				delete(removed, p)
			}
		}
	}
	// A file that is gone while the directory around it is still there is
	// not a store that went away: it is Claude Code's own cleanup, which
	// deletes a transcript once it is older than cleanupPeriodDays (30 by
	// default), or a deletion by hand. Either way the index keeps what it
	// read — outlasting the client's housekeeping is the one thing a memory
	// over transcripts can offer against a 30-day default, and `deja forget`
	// is the deliberate path for a session that must go (#2970). A tree that
	// is gone whole is an uninstall or a move, and is dropped as before.
	// Cursor, Copilot CLI and Kimi keep each session in a directory of its
	// own, so deleting one takes the directory with the file. A missing tree
	// named after a session, under a parent that is still there, is that
	// deletion and not a store that went away (#4195).
	stores := gone[:0]
	for _, g := range gone {
		if g.mount || g.renamed != "" || !deletedSessionDir(g.dir) {
			stores = append(stores, g)
		}
	}
	gone = stores
	// A session directory that turns up under another parent in the same pass
	// moved rather than went: its id is its name. Keeping the old copy then
	// made the session its own second copy, and the append path below, which
	// takes over once nothing is removed, has no pairing to catch it.
	arrivedDirs := sessionDirsUnder(changed)
	superseded := supersededLogs(removed, files)
	kept := map[string]bool{}
	// Kept while still on disk: a store this run cannot see, so not deleted.
	unseen := map[string]bool{}
	// Kept for the first time in this pass: the mark that lets the next run
	// read the index as fresh has to be written even when nothing else is.
	newlyKept := map[string]bool{}
	var view *listedView
	var held map[string]bool
	// Read before the loop and set after it: what a store written before
	// deja dated deletions lost is undated, and from here on it is dated.
	dated := old.KeptDated
	for p := range removed {
		if superseded[p] {
			continue
		}
		// A file still on disk that the listing left out was not deleted. When
		// this pass read the session it hangs off — a file of the same store
		// one or two directories up — the store was in view and a setting left
		// the file out: DEJA_INCLUDE_SUBAGENTS turned off. It goes, as a
		// rebuild would drop it; the incremental pass kept 32 children and
		// called them "no longer on disk" (#4739). Otherwise its store is
		// outside this run's view — a hook started with a stripped environment
		// (#4738), a root no longer set — and that is no reason to drop the
		// store's sessions, so they stay.
		if _, err := os.Lstat(p); err == nil {
			if view == nil {
				view = newListedView(files, old.Sessions)
			}
			if view.leftOutBySetting(p) {
				continue
			}
			if of, ok := old.Files[p]; ok {
				files[p] = of
				delete(removed, p)
				kept[p] = true
				unseen[p] = true
			}
			continue
		} else if os.IsPermission(err) {
			// Behind a directory this run may not enter: not deleted, and
			// not outside the stores read either. The unreadable line names
			// the directory (#5393).
			if of, ok := old.Files[p]; ok {
				files[p] = of
				delete(removed, p)
			}
			continue
		}
		if !deletedFromLiveStore(p) {
			continue
		}
		if d := goneSessionDir(p); d != "" && arrivedDirs[filepath.Base(d)] {
			continue
		}
		// Kept for the sessions it holds, as a rebuild keeps it for its
		// records: a file whose sessions were all forgotten holds nothing.
		// A copy beside another file's row holds them too, and dropping it
		// took the turns only it had.
		if held == nil {
			held = map[string]bool{}
			for _, meta := range old.Sessions {
				for _, q := range sessionFiles(meta) {
					held[q] = true
				}
			}
		}
		if !held[p] {
			continue
		}
		if of, ok := old.Files[p]; ok {
			if !of.Kept {
				newlyKept[p] = true
				if dated {
					of.KeptAt = time.Now().Unix()
				}
			}
			of.Kept = true
			files[p] = of
			delete(removed, p)
			kept[p] = true
		}
	}
	old.KeptDated = true
	// Said once per pass, and only once the rename filter below has had its
	// say — except when nothing else changed, in which case there is no
	// rename to filter and the pass returns early.
	sayKept := func() {
		if progress == nil {
			return
		}
		out := 0
		for p := range kept {
			if unseen[p] {
				out++
			}
		}
		if n := len(kept) - out; n > 0 {
			fmt.Fprintf(progress, "deja: %d transcript%s no longer on disk — still searchable; `deja resume <id> --write-back` puts one back, `deja forget --session <id>` drops one for good\n", n, pluralS(n))
		}
		if out > 0 {
			fmt.Fprintf(progress, "deja: %d transcript%s outside the stores this run reads — still searchable\n", out, pluralS(out))
		}
	}
	// Counted after the keep-backs above: records that came off an unmounted
	// volume, or out of a file the client cleaned up, are still in the index,
	// so they did not go away.
	evicted.Add(int64(len(removed)))
	if progress != nil {
		for _, g := range gone {
			verb := "is"
			if g.files != 1 {
				verb = "are"
			}
			switch {
			case g.renamed != "":
				fmt.Fprintf(progress, "deja: %s is mounted as %s now — its %d indexed file%s %s still searchable; point deja at the new path\n",
					g.dir, g.renamed, g.files, pluralFiles(g.files), verb)
			case g.mount:
				fmt.Fprintf(progress, "deja: %s is not mounted — its %d indexed file%s %s still searchable; reconnect the disk to pick up anything new\n",
					g.dir, g.files, pluralFiles(g.files), verb)
			default:
				fmt.Fprintf(progress, "deja: %s is gone, and %d indexed file%s with it — if that disk is simply not mounted, reconnect it and run `deja index`\n",
					g.dir, g.files, pluralFiles(g.files))
			}
		}
	}
	rewritten := rewrittenInPlace(kept, changed, files, old.Files, old.Sessions)
	// A transcript deleted this pass is handed over the way a rebuild hands
	// it over, which only the replacement pass can do: the row it owned beside
	// other copies is taken again from them and from its records, and a file
	// arriving in the same pass is paired with it by the rename rule there
	// rather than taking a dead path's row in the append path (#5380, #1086).
	// A file new to the index is the only one that can be the deleted one
	// moved; a live transcript growing beside an unrelated deletion still
	// appends.
	sharedBy := keysByFile(old.Sessions)
	arrived := false
	for p := range changed {
		if _, ok := old.Files[p]; !ok {
			arrived = true
			break
		}
	}
	handover := false
	for p := range newlyKept {
		if arrived || len(sharedBy[p]) > 0 {
			handover = true
			break
		}
	}
	for _, p := range regather {
		if f, ok := files[p]; ok {
			changed[p] = f
			handover = true
		}
	}
	if len(changed) == 0 && len(removed) == 0 && !handover {
		sayKept()
		lastIngestFiles = 0
		// Nothing to read, but the kept mark has to land, or the next run
		// takes the same file for a fresh deletion.
		if len(newlyKept) > 0 || !dated || len(touched) > 0 || !unreadableCurrent(old, files) {
			for p := range newlyKept {
				old.Files[p] = files[p]
			}
			for p := range touched {
				old.Files[p] = files[p]
			}
			if !unreadableCurrent(old, files) {
				mergeIngestDiag(&old)
			}
			return writeManifestOnly(dir, old)
		}
		return nil
	}
	appendPass := func(files, changed map[string]FileState, last bool) error {
		filesTouched, messages, unreadable, err := appendIncremental(dir, harness, scope, old, files, changed)
		if IsCorrupt(err) {
			if progress != nil {
				fmt.Fprintf(progress, "deja: %s (%v), rebuilding ...\n", damagedOrOutdated(err), err)
			}
			return rebuild(dir, harness, scope, files, progress)
		}
		if err != nil {
			return fmt.Errorf("append: %w", err)
		}
		if last {
			sayKept()
		}
		if progress != nil {
			line := fmt.Sprintf("deja: updated %d file%s (%d new message%s)", filesTouched, pluralS(filesTouched), messages, pluralS(messages))
			// After the first build every run a person sees is this one, so a
			// clause that lives only in the full pass is a clause nobody reads
			// (#2007). Same counters, summed across the stores this pass
			// touched — the line is about the pass rather than one store.
			if unreadable > 0 {
				line += fmt.Sprintf(" — %d line%s skipped, deja could not read %s", unreadable, pluralS(unreadable), pluralThem(unreadable))
			}
			fmt.Fprintln(progress, line)
		}
		return nil
	}
	takenFrom := sharedTakeovers(dir, harness, old, changed, files)
	if len(removed) == 0 && !rewritten && !handover && !promptLogTakeover(changed, old.Files, old.Sessions) && takenFrom == nil {
		if canAppendIncremental(changed, old.Files) {
			return appendPass(files, changed, true)
		}
		// Decided per file: the appendable ones are appended, and the pass
		// after it, over the same walk, replaces only the rest (#5324).
		if appendable, rest := splitAppendable(changed, old.Files, old.Sessions); split && len(appendable) > 0 && len(rest) > 0 {
			if err := appendPass(withoutRest(files, rest, old.Files), appendable, false); err != nil {
				return err
			}
			return updateIndexPass(dir, harness, scope, walked, false, false, progress)
		}
	}
	rereadSharedCopies(removed, changed, files, old.Sessions)
	takenRead := 0
	for p := range takenFrom {
		if _, again := changed[p]; !again {
			changed[p] = files[p]
			takenRead++
		}
	}
	rereadPromptLogs(removed, changed, files, old.Files, old.Sessions)
	var replacements []model.Session
	// This pass's counts, like every other build path (#1850).
	emptied.Store(0)
	collisions.Store(nil)
	merged.Store(0)
	lastIngestFiles = len(changed)
	// The sessions a shared store handed back. Only those may drop a store's
	// records by key: a per-file transcript under the same id — goose's JSONL
	// beside its database — says nothing about what the store still holds.
	// Judged by path after wholeStoresThisPass, which resolves the store paths.
	storeKeys := map[string]bool{}
	keysFrom := map[string][]string{}
	// A live-locked or half-written store (Cursor holds its sqlite under WAL)
	// must not fail every search. Keep the old records and the old FileState
	// so the next run retries this file.
	logTails := promptLogTails(changed, old.Files, old.IngestFiles)
	skip := func(p string, err error) {
		delete(logTails, p)
		if progress != nil {
			fmt.Fprintf(progress, "deja: skipping %s this pass: %v\n", filepath.Base(p), err)
		}
		delete(changed, p)
		if of, ok := old.Files[p]; ok {
			files[p] = of
		} else {
			delete(files, p)
		}
	}
	take := func(p string, ss []model.Session) {
		ss = dropShadowedPrompts(sources.FilterSessions(filterTombstoned(ss)), old.Sessions)
		for _, s := range ss {
			keysFrom[p] = append(keysFrom[p], s.Harness+":"+s.ID)
		}
		replacements = append(replacements, ss...)
	}
	// opencode's diff files are read after its database: each is read as its
	// session from the database, which the since read may already have handed
	// back with the diff folded in (#4207).
	var diffs []string
	fromDB := map[string]bool{}
	read := func(p string, f FileState) {
		if harnessForPath(p) == "opencode-diff" {
			diffs = append(diffs, p)
			return
		}
		ss, f, again, err := readSettled(p, f, func() ([]model.Session, error) { return parseChangedFile(harness, p, old.Files[p]) })
		if err != nil {
			skip(p, err)
			return
		}
		if _, ok := changed[p]; ok {
			changed[p] = f
		}
		if again {
			// Read whole: the tail was named from the walk's size.
			delete(logTails, p)
		}
		if ids, ok := logTails[p]; ok {
			ss = sessionsNamed(ss, widenLogTail(ids, p, ss, old.Sessions))
		}
		if harnessForPath(p) == "opencode" {
			for _, s := range ss {
				fromDB[s.ID] = true
			}
		}
		take(p, ss)
		files[p] = f
	}
	for p, f := range changed {
		read(p, f)
	}
	// A session this pass replaces is read from every transcript that holds
	// it, as a rebuild reads it: the ones that changed, and the copies beside
	// them that did not (#5358). What those copies hold can name a session
	// another file holds in turn, so it runs until nothing new is read.
	copiesRead := takenRead
	if heldBy := keysByFile(old.Sessions); len(heldBy) > 0 {
		var pending []string
		for p := range changed {
			pending = append(pending, heldBy[p]...)
			pending = append(pending, keysFrom[p]...)
		}
		for p := range removed {
			pending = append(pending, heldBy[p]...)
		}
		for p := range newlyKept {
			pending = append(pending, heldBy[p]...)
		}
		seen, tried := map[string]bool{}, map[string]bool{}
		for len(pending) > 0 {
			key := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if seen[key] {
				continue
			}
			seen[key] = true
			meta, ok := old.Sessions[key]
			if !ok || len(meta.Copies) == 0 {
				continue
			}
			for _, q := range sessionFiles(meta) {
				// A written-back file holds less than its records do, and
				// those are carried instead (#4617).
				f, live := files[q]
				if _, again := changed[q]; again || tried[q] || !live || kept[q] || f.Kept || f.WrittenBack || !copyable(q) {
					continue
				}
				tried[q] = true
				copiesRead++
				changed[q] = f
				read(q, f)
				pending = append(pending, heldBy[q]...)
				pending = append(pending, keysFrom[q]...)
			}
		}
	}
	sortCopies(replacements)
	var pending []string
	for _, p := range diffs {
		if !fromDB[strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))] {
			pending = append(pending, p)
		}
		files[p] = changed[p]
	}
	if len(pending) > 0 {
		// Read together: one query rather than one a file, each paying for
		// every session's parent and title again.
		ss, err := sources.ParseOpencodeDiffSessions(pending)
		if err != nil {
			for _, p := range pending {
				skip(p, err)
			}
		} else {
			at := map[string]string{}
			for _, p := range pending {
				at[strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))] = p
			}
			for _, s := range ss {
				take(at[s.ID], []model.Session{s})
			}
		}
	}
	// First, so everything below reads the same list of stores.
	wholeStoresThisPass(changed, old.Files)
	for p, keys := range keysFrom {
		if storeHarness(p) == "" {
			continue
		}
		for _, k := range keys {
			storeKeys[k] = true
		}
	}
	// After the loop, because a file whose parse failed is dropped from
	// `changed` there and keeps what it already held — starting it over would
	// throw the counts away on the one pass that could not read it.
	//
	// A database-backed store reads only what is newer than the cursor the last
	// pass stamped, so this pass cannot speak for the rest of it either: it adds
	// to what the store holds, the way an append does (#2025). The trade is the
	// append path's — a db's counts can only grow until a full rebuild.
	reread := fullyReadFiles(changed, old.Files)
	parsedThisPass(reread)
	// A file the pass read is a file that opens, whether or not it read all of
	// it, so an error recorded when it did not has to go.
	readThisPass(changed)
	// Each file's read drops what the index holds from a transcript; this
	// drops what a transcript in the same batch holds, as a rebuild does. A
	// log read before its session's transcript kept the log's prompt (#5503).
	replacements = dropShadowedPrompts(replacements, old.Sessions)
	replaceKeys := map[string]bool{}
	for _, s := range replacements {
		replaceKeys[s.Harness+":"+s.ID] = true
	}
	// A transcript arriving for a session held from a prompt log alone: the
	// log's records for it go, and the row starts over from the transcript.
	takeovers := promptLogTakeovers(replacements, old.Sessions)
	aiderHeld := aiderStarts(replacements)
	// A kept file whose session arrived again from another path is a rename,
	// not a cleanup: the client moved the transcript, and keeping the old copy
	// would make the session its own second copy (#1086). Those go back to
	// being removed, so the records under the old path are dropped below.
	// Only when the arrival sits in the same directory: two projects can
	// share a filename-derived id (#699), and that is a collision, not the
	// kept session moving.
	// An arrival with nothing to index is not the kept session moving: Gemini
	// CLI's resume stub lands beside the transcript it shares an id with, and
	// taking it for a rename dropped the deleted transcript's records (#4213).
	// Only a file new to the index arrives: a copy read again beside a deleted
	// owner holds its last turn too, and taking it for the move dropped the
	// deleted transcript's records (#5380).
	arrivals := map[string][]int{}
	for i, r := range replacements {
		if _, known := old.Files[r.Path]; known && !removed[r.Path] {
			continue
		}
		if holdsText(r) {
			key := r.Harness + ":" + r.ID
			arrivals[key] = append(arrivals[key], i)
		}
	}
	// A session kept in a directory of its own moves with the directory, so
	// the arrival is never beside the old path; its id is a UUID, which two
	// projects do not share by accident, so the id alone says it moved.
	for key, meta := range old.Sessions {
		if !kept[meta.Path] {
			continue
		}
		ownDir := sessionDirName.MatchString(filepath.Base(filepath.Dir(meta.Path)))
		moved := false
		for _, i := range arrivals[key] {
			r := replacements[i]
			if (ownDir || filepath.Dir(r.Path) == filepath.Dir(meta.Path)) && coversKept(r, meta) && holdsKeptRecords(dir, old, r, meta.Path) {
				moved = true
				break
			}
		}
		if moved {
			removed[meta.Path] = true
			delete(files, meta.Path)
			delete(kept, meta.Path)
		}
	}
	sayKept()
	if progress != nil {
		// A sentence, like every other line this prints. It used to read
		// "incremental index changed_files=306 removed_files=0 sessions=1789",
		// which is a log line for whoever wrote it, on a path every user hits:
		// the first `deja blame` or search after a day of work prints it above
		// the answer. Zero counts are left out rather than shown as zero.
		n := len(changed) - copiesRead
		line := fmt.Sprintf("deja: %s %d changed transcript%s", replacementPassMarker, n, pluralS(n))
		if copiesRead > 0 {
			line += fmt.Sprintf(", %d cop%s read again", copiesRead, map[bool]string{true: "y", false: "ies"}[copiesRead == 1])
		}
		if len(replaceKeys) > 0 {
			line += fmt.Sprintf(", %d session%s replaced", len(replaceKeys), pluralS(len(replaceKeys)))
		}
		if len(removed) > 0 {
			line += fmt.Sprintf(", %d gone", len(removed))
		}
		fmt.Fprintln(progress, line)
	}
	tmp := dir + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o700); err != nil {
		return err
	}
	// Appended to the log the old index holds, or the log written anew: see
	// append_replace.go (#5387).
	appending := appendsInPlace(dir, tmp, old)
	swapped := false
	// Appending, a record that is not carried stays where it is and is listed
	// as dead, and is overwritten once the swap commits.
	var dead []int64
	var deadBytes int64
	if appending {
		defer func() {
			if !swapped {
				undoAppend(dir, old.RecordsSize)
				// Its log is a link to the live one.
				_ = os.RemoveAll(tmp)
			}
		}()
	}
	swap := func(m Manifest) error {
		if appending && beforeAppendSwap != nil {
			if err := beforeAppendSwap(); err != nil {
				return err
			}
		}
		if appending && len(dead) > 0 {
			if err := markScrubPending(tmp); err != nil {
				return err
			}
		}
		err := swapIndexDir(dir, tmp)
		swapped = err == nil
		if swapped && len(dead) > 0 {
			scrubSwapped(dir, m, dead)
		}
		return err
	}
	var rf *os.File
	tbl := newRecordTables()
	if appending {
		rf, err = os.OpenFile(filepath.Join(tmp, "records.bin"), os.O_RDWR, 0o600)
		// Ids continue where the log's records left off; clipped, so the
		// table this pass grows is not the old manifest's backing array.
		tbl = tablesFromStrings(slices.Clip(old.RecordStrings))
	} else {
		rf, err = createLog(filepath.Join(tmp, "records.bin"))
	}
	if err != nil {
		return err
	}
	rw, err := newRecordWriter(rf, tbl)
	if err != nil {
		_ = rf.Close()
		return err
	}
	// A new generation, not the old one: this path opens records.bin with
	// O_TRUNC and writes every surviving record again, so an offset from before
	// it means nothing afterwards. Carrying the stamp forward told anything
	// keyed on it — the embedding sidecar — that the file it measured was still
	// there (#1357). Only appendIncremental, which appends in place, may keep a
	// generation.
	m := Manifest{Version: version, KeptDated: true, Format: onDiskFormat, SearchOnly: searchOnlyAfter(old), Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), SourcesReadAt: time.Now(), ReadZone: zoneFingerprint(),
		Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope,
		ExportWatermarks: old.ExportWatermarks, ExportBoundary: old.ExportBoundary, ImportedRecords: old.ImportedRecords,
		Compactions: cloneCompactions(old.Compactions),
		// Kept from the old index: this build reuses records written under
		// those patterns, so claiming today's set would be a lie the reader
		// cannot check.
		ExcludeFingerprint: old.ExcludeFingerprint,
		// What the index on disk recorded: old is that manifest, or what the
		// followed renames wrote of it.
		ToolFingerprint: mergedToolFingerprint(old.ToolFingerprint),
		// What deja could not read is about the files, not about the pass that
		// happened to notice: the lines are still there. Dropping the map meant
		// doctor forgot a store's unreadable file because an unrelated
		// transcript changed (#2015). A store this pass re-read starts over,
		// because a file rewritten clean has to be able to clear its count.
		IngestFiles: copyIngestFiles(old.IngestFiles, reread, logTails)}
	if appending && old.Generation != "" {
		// Nothing moved: an offset from before still names the same record.
		m.Generation = old.Generation
	}
	skipRedactions := map[string]bool{}
	for p := range changed {
		skipRedactions[p] = true
	}
	for p := range removed {
		skipRedactions[p] = true
	}
	carryRedactions(&m, old, skipRedactions)
	// Scrub once, up front, the way both full builds do — everything below reads
	// these sessions. Redacting per message inside the record loop instead left
	// the sessions themselves raw, so whatever was mined from them afterwards
	// was raw too: a credential in an error line reached fixes.gob whole while a
	// rebuild of the same corpus produced a clean one. It also left
	// metaForSession hashing unredacted text, so the friction signatures a
	// session carried depended on which build path had touched it last.
	preRedactSessions(&m, replacements)
	// After the redaction, so the texts compare with what the records hold.
	compacted := compactedInPlace(replacements, false)
	buckets := bucketPostings{}
	// Every record's offset in the new log, as it is written: the table the
	// commit would otherwise build by walking the whole log again (#5402).
	offsets := &passOffsets{byKey: map[string][]int64{}}
	freshKeys := map[string]bool{}
	addRec := func(r Record) error {
		if r.SourcePath == "" {
			return nil
		}
		meta, ok := old.Sessions[r.Key]
		if !ok {
			meta, ok = m.Sessions[r.Key]
		}
		if !ok {
			return nil
		}
		if appending && !freshKeys[r.Key] {
			freshKeys[r.Key] = true
			tbl.fresh(r.Key)
		}
		off, err := rw.write(r)
		if err != nil {
			return err
		}
		offsets.add(r.Key, off)
		passEncoded.Add(1)
		passKeyed.Add(1)
		// The same part and the same tool bit a full build gives the record.
		// Tokenizing the whole text here put every hash of a written side,
		// every replaced span's body and whole build logs into the postings
		// of each session this pass carried, and dropped the bit the
		// per-session read bound uses to spend its budget on speech first:
		// a live index held twice the tokens a rebuild of it did.
		addIndexKeys(buckets, tokenizedPart(r.Role, r.Text), off, meta.Ord, r.Time, isToolRole(r.Role))
		if _, exists := m.Sessions[r.Key]; exists {
			return nil
		}
		m.Sessions[r.Key] = meta
		return nil
	}
	// The sessions that lost a record here, which the command failure walk
	// has to read again along with the ones re-read (#4288).
	dropped := map[string]bool{}
	// Seeded with the records carried for a session this pass replaces, so a
	// turn a kept or skipped copy already holds is not written again.
	seenMsgs := msgSeen{}
	// Carried records were redacted, encoded and keyed when first written.
	// Each is copied as it is stored, and its postings are moved over below:
	// doing it all again made every replacement pass cost the whole store, on
	// one core (#5383). Only a session this pass replaces, or one a compaction
	// may have kept, has its body read.
	oldTables := tablesFromManifest(old)
	carried := &carriedRecords{}
	// The files beside a session this pass replaces that it does not read
	// again: a kept transcript, a written-back one, one skipped. A rebuild
	// reads them from their records after the files on disk, and so does this
	// pass, or a copy that left kept its span in the row (#5380).
	// Those rows are taken again too where a copy left them this pass and
	// nothing was read again, and a deleted owner is read from its records
	// like the rest: the span it held was its own and its copies', some of
	// which have gone since.
	rederive := maps.Clone(replaceKeys)
	for _, set := range []map[string]FileState{changed} {
		for p := range set {
			for _, key := range sharedBy[p] {
				rederive[key] = true
			}
		}
	}
	for _, set := range []map[string]bool{removed, newlyKept} {
		for p := range set {
			for _, key := range sharedBy[p] {
				rederive[key] = true
			}
		}
	}
	unread, unreadMsgs := map[string]bool{}, map[string][]model.Message{}
	keptOwner := map[string]string{}
	for key := range rederive {
		row := old.Sessions[key]
		for _, q := range sessionFiles(row) {
			f, ok := files[q]
			if _, again := changed[q]; again || !ok || q == row.Path && !f.Kept {
				continue
			}
			unread[orphanKey(key, q)] = true
			if q == row.Path {
				keptOwner[key] = q
			}
		}
	}
	// A turn a deleted copy shared with a live one was filed once, under
	// whichever was read first. When that live file is read again without
	// it, the turn goes to the deleted copy rather than out of the index:
	// the deleted transcript can no longer be read to say which turns it
	// held, so it keeps every one the session loses here.
	keptCopy := map[string]string{}
	for key := range rederive {
		for _, q := range sessionFiles(old.Sessions[key]) {
			if _, again := changed[q]; !again && files[q].Kept && (keptCopy[key] == "" || q < keptCopy[key]) {
				keptCopy[key] = q
			}
		}
	}
	readAgain := msgSeen{}
	for _, s := range replacements {
		if key := s.Harness + ":" + s.ID; keptCopy[key] != "" {
			for _, msg := range s.Messages {
				readAgain.dup(key, msg.Role, msg.Time, msg.Text)
				// A command whose result landed since is the same turn.
				readAgain.dup(key, msg.Role, msg.Time, exitSuffix.ReplaceAllString(msg.Text, ""))
			}
		}
	}
	// Only a turn from before the copy was last written can be one it held:
	// a later one, rewound or rewritten away, was never in it.
	couldHold := func(r Record, k string) bool {
		f := files[k]
		return r.Time.IsZero() || f.MTime == 0 || r.Time.UnixNano() <= f.MTime
	}
	gotAgain := func(r Record) bool {
		return readAgain[seenKey(r.Key, r.Role, r.Time, r.Text)] || readAgain[seenKey(r.Key, r.Role, r.Time, exitSuffix.ReplaceAllString(r.Text, ""))]
	}
	compactedKeys := make(map[string]bool, len(compacted))
	for key := range compacted {
		compactedKeys[key] = true
	}
	carry := func(oldOff int64, payload []byte) error {
		kill := func() {
			if appending {
				dead = append(dead, oldOff)
				deadBytes += 4 + int64(len(payload))
			}
		}
		kid, pid, rid, rest, ok := recordPrefix(payload)
		if !ok {
			return errShortRecord
		}
		r := Record{Key: oldTables.lookup(kid), SourcePath: oldTables.lookup(pid), Role: oldTables.lookup(rid)}
		if _, ok := compacted[r.Key]; ok || rederive[r.Key] {
			full, err := decodeRecord(payload, oldTables)
			if err != nil {
				return err
			}
			r = full
		}
		// Shared-store harnesses (opencode, cursor) are parsed since a
		// watermark, so their untouched sessions are NOT re-emitted on a
		// change — they must be retained, not dropped, or they vanish.
		// Superseded sessions are handled by storeKeys.
		fromStore := fromDatabase(r)
		// storeKeys is scoped to shared stores. A shared store is parsed since
		// a watermark, so a superseded session's old record is not re-read and
		// clause two never reaches it — storeKeys is what drops it. For a
		// per-file harness, a removed or changed file's old records are already
		// dropped by the two clauses above, so applying a key there only
		// hurts: two transcripts in different projects can share a filename-derived
		// id, and dropping by key alone erased the sibling that was never re-read
		// (#699). The record's own SourcePath decides its fate for those.
		//
		// And only when the pass read that store whole: a store read from its
		// watermark hands back the new turns alone, so dropping the rest by key
		// would take the earlier turns of every continued session (#2033).
		if removed[r.SourcePath] || (changed[r.SourcePath].Path != "" && !fromStore && !keptFromLogTail(r, logTails) && !leftItsFile(r, replaceKeys, old.Sessions[r.Key], aiderHeld) && !keptThroughCompaction(r, compacted)) || (fromStore && readWholeThisPass(r) && storeKeys[r.Key]) ||
			(takeovers[r.Key] && isCodexHistory(r.SourcePath)) {
			if k := keptCopy[r.Key]; k != "" && !removed[r.SourcePath] && copyable(r.SourcePath) && couldHold(r, k) && !gotAgain(r) {
				kill()
				r.SourcePath = k
				if at := orphanKey(r.Key, k); unread[at] {
					h, _, _ := strings.Cut(r.Key, ":")
					unreadMsgs[at] = append(unreadMsgs[at], model.Message{Role: r.Role, Text: carriedText(h, r.Role, r.Text), Time: r.Time})
				}
				return addRec(r)
			}
			dropped[r.Key] = true
			kill()
			return nil
		}
		if r.SourcePath == "" {
			dropped[r.Key] = true // not carried
			kill()
			return nil
		}
		if replaceKeys[r.Key] {
			seenMsgs.dup(r.Key, r.Role, r.Time, r.Text)
		}
		if len(unread) > 0 && unread[orphanKey(r.Key, r.SourcePath)] {
			at := orphanKey(r.Key, r.SourcePath)
			h, _, _ := strings.Cut(r.Key, ":")
			unreadMsgs[at] = append(unreadMsgs[at], model.Message{Role: r.Role, Text: carriedText(h, r.Role, r.Text), Time: r.Time})
		}
		meta, ok := old.Sessions[r.Key]
		if !ok {
			kill()
			return nil
		}
		if appending {
			offsets.add(r.Key, oldOff)
			if _, exists := m.Sessions[r.Key]; !exists {
				m.Sessions[r.Key] = meta
			}
			return nil
		}
		off, err := rw.writeStored(r.Key, r.SourcePath, r.Role, rest)
		if err != nil {
			return err
		}
		carried.add(oldOff, off, meta.Ord, isToolRole(r.Role))
		offsets.add(r.Key, off)
		if _, exists := m.Sessions[r.Key]; !exists {
			m.Sessions[r.Key] = meta
		}
		return nil
	}
	if err := carryStoredRecords(dir, old, oldTables, appending, changed, removed, carry, func(key string, offs []int64) {
		for _, off := range offs {
			offsets.add(key, off)
		}
		if _, exists := m.Sessions[key]; !exists {
			m.Sessions[key] = old.Sessions[key]
		}
	}, rederive, storeKeys, takeovers, compactedKeys); err != nil {
		_ = rw.Close()
		return err
	}
	// Ord is the posting's session id, so two sessions sharing one merges
	// their postings. A replacement reclaims its Ord from the OLD manifest,
	// which the new map has not seen yet — so a new session picked from
	// max(new)+1 could collide with an Ord an existing session was about to
	// take back. Reserve both sides before handing any out.
	nextOrd := uint32(0)
	for _, meta := range m.Sessions {
		if meta.Ord > nextOrd {
			nextOrd = meta.Ord
		}
	}
	for _, meta := range old.Sessions {
		if meta.Ord > nextOrd {
			nextOrd = meta.Ord
		}
	}
	rederived := map[string]bool{}
	// What each session's row reads off its text, all at once: in the loop it
	// was one core's work for most of the phase.
	texts := sessionTexts(replacements)
	// The sessions read this pass: one with no record under it when the pass
	// is done goes to Empty, as a rebuild sets it aside (#5563).
	readKeys := map[string]bool{}
	for i, s := range replacements {
		key := s.Harness + ":" + s.ID
		readKeys[key] = true
		ord := uint32(0)
		if om, ok := old.Sessions[key]; ok {
			ord = om.Ord
		} else if cur, ok := m.Sessions[key]; ok {
			ord = cur.Ord
		}
		if ord == 0 {
			nextOrd++
			ord = nextOrd
		}
		held, ok := m.Sessions[key]
		if !ok {
			// The row may exist only in the manifest being replaced, or be set
			// aside there with nothing to index (#5563).
			var listed bool
			if held, listed = old.Sessions[key]; !listed {
				// Set aside without an Ord; the one this pass gives it.
				if row, ok := old.Empty[key]; ok {
					held = metaWithOrd(row, ord)
				}
			}
			// The file that owned it was read again whole and none of its
			// records were carried: the row starts over from what this pass
			// reads, as a rebuild starts it. Held, it kept the span and the
			// owner of a file that may no longer hold the session (#5358).
			_, again := reread[held.Path]
			if takeovers[key] || again && copyable(held.Path) {
				held = SessionMeta{}
			}
		}
		if !rederived[key] {
			// The span of the files beside the owner is taken again, from the
			// ones read now and the ones that were not, as a rebuild takes it:
			// held, it kept the span of a copy that has since left (#5380).
			rederived[key] = true
			held = rowRetaken(held, keptOwner[key], reread)
			if ok {
				m.Sessions[key] = held
			}
		}
		// A renamed transcript arrives as one removed path and one added path
		// in the same pass, so the path deja holds is the one that went away.
		// Comparing them called it a collision with a file that no longer
		// exists: the row kept the dead path until a full rebuild, `--json`
		// handed callers that path, and `forget` warned that dropping the
		// session would take a second conversation with it (#1086).
		if removed[held.Path] {
			held.Path = ""
		}
		owns, collided := claimSession(held, s)
		if collided {
			collisions.Add(key)
		}
		if owns {
			m.Sessions[key] = ownerRow(held, s, texts[i].row(s), ord, collided)
		} else {
			if _, present := m.Sessions[key]; !present {
				m.Sessions[key] = held
			}
			widenSpan(m.Sessions, key, s)
		}
		if collided {
			markShared(m.Sessions, key)
		}
		recordCopy(m.Sessions, key, held, s)
		for _, msg := range s.Messages {
			// Already redacted (and length-capped) by preRedactSessions above.
			text := msg.Text
			// Plumbing alone strips to empty and is not written, as a rebuild
			// does not write it (#551, #5555).
			if strings.TrimSpace(text) == "" {
				continue
			}
			if seenMsgs.dup(key, msg.Role, msg.Time, text) {
				continue
			}
			if err := addRec(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time}); err != nil {
				_ = rw.Close()
				return err
			}
		}
	}
	for key := range rederive {
		if row, ok := m.Sessions[key]; ok && !rederived[key] {
			m.Sessions[key] = rowRetaken(row, keptOwner[key], reread)
		}
	}
	for _, at := range sortedKeys(unread) {
		key, q, _ := strings.Cut(at, "\x00")
		row, ok := m.Sessions[key]
		if !ok || len(unreadMsgs[at]) == 0 {
			continue
		}
		cp := sessionFromMeta(old.Sessions[key])
		retitleWrappedPaste(&cp)
		cp.Path, cp.Messages, cp.Started, cp.Updated = q, unreadMsgs[at], time.Time{}, time.Time{}
		for _, msg := range cp.Messages {
			cp.Started, cp.Updated = widerSpan(cp.Started, cp.Updated, msg.Time, msg.Time)
		}
		owns, collided := claimSession(row, cp)
		if owns {
			next := ownerRow(row, cp, metaForSession(cp), row.Ord, collided)
			// What the records cannot count, as the rebuild carries it.
			if q == keptOwner[key] {
				next = withKeptRow(next, old.Sessions[key], true)
			}
			m.Sessions[key] = next
		} else {
			widenSpan(m.Sessions, key, cp)
		}
		if collided {
			markShared(m.Sessions, key)
		}
		recordCopy(m.Sessions, key, row, cp)
	}
	m.Empty = keepEmpty(old.Empty, m.Sessions, m.Files)
	// A file read again whole says what it holds now: a row set aside for it
	// that this pass did not read again names an id the file no longer has.
	for key, row := range m.Empty {
		// A prompt log read from its tail does not say what the rest holds.
		if _, again := reread[row.Path]; again && !readKeys[key] && logTails[row.Path] == nil {
			delete(m.Empty, key)
		}
	}
	for key := range readKeys {
		if len(offsets.byKey[key]) == 0 {
			setAsideEmpty(&m, key)
		}
	}
	if err := rw.Close(); err != nil {
		return err
	}
	// The neighbour map's words an index written anew has no postings for,
	// which the carried map loses (see cooccurUnindexed). Appending, the scrub
	// after the swap takes them out instead.
	var unposted map[string]bool
	if appending {
		// The kept records' postings are the old ones, linked in; this pass's
		// go into overlays, and the dead records' are dropped on read.
		m.DeadRecords = mergeDead(old.DeadRecords, dead)
		m.DeadBytes = old.DeadBytes + deadBytes
		if _, err := addOverlays(filepath.Join(dir, "buckets"), filepath.Join(tmp, "buckets"), buckets, m.DeadRecords); err != nil {
			return err
		}
	} else {
		moved, err := carriedPostings(dir, old, carried)
		if err != nil {
			return err
		}
		mergePostings(moved, buckets)
		if err := writeBucketsConcurrent(filepath.Join(tmp, "buckets"), moved); err != nil {
			return err
		}
		if !m.SearchOnly {
			unposted = cooccurUnindexed(dir, moved)
		}
	}
	settleCopies(&m, old.Sessions, changed, replacements)
	setDatabaseStoreWatermarks(m.Files, m.Sessions)
	m.RecordStrings = tbl.strs
	_ = offsets.write(tmp)
	// The sessions this pass read again or dropped a record of. Everything
	// else was copied as stored, in its order, so what was derived from its
	// records still holds.
	for key := range replaceKeys {
		dropped[key] = true
	}
	// Ahead of the commit, which then reads only the sessions that changed
	// rather than parsing every session that carries a wall again.
	carryFrictionTexts(dir, tmp, old, dropped)
	// Nothing below changes m, so the commands, the swap and the CLI's checks
	// after the pass read the manifest from memory.
	if err := writeManifestShared(tmp, m); err != nil {
		return err
	}
	if m.SearchOnly {
		return swap(m)
	}
	carrySidecars(dir, tmp)
	if len(unposted) > 0 {
		_ = dropCooccurWords(tmp, func(w string) bool { return unposted[w] })
	}
	// After carrying, not instead of it: the fixes merge writes only when it
	// has something to say, and the carried file is what a quiet update leaves.
	// The command table is recomputed from the sessions this pass changed and
	// the state the last one left, and an empty one removes the carried file,
	// as a full build would not write it (#4441).
	fixSessions, fixKeys := fixSessionsForPass(tmp, old, m, replacements, replaceKeys)
	mergeFixes(dir, tmp, sessionKeys(m), fixSessions, fixKeys)
	buildCommandTables(dir, tmp, m, len(old.DeadRecords), dropped)
	buildCommandFailsFromIndex(tmp, carriedCommandFailState(dir, old, tmp, m.Generation, dropped), dropped)
	return swap(m)
}

// carrySidecars copies the mined sidecars from the live index into the
// incremental build, because the swap replaces the whole directory and this
// path does not mine them again — it deliberately never holds the whole corpus.
//
// Without this, one incremental update deleted all three. `fix` went silent
// outright, since it has no other source; ranking lost its query expansion;
// commands survived only because they have a fallback path. And an incremental
// update is not a rare event — it is what a new session in any store triggers,
// so the sidecars were being destroyed during ordinary use and only came back
// on the next full rebuild.
//
// They are carried, not rebuilt: pairs mined from sessions that arrived in this
// update are missing until a full build, which is the ordinary staleness of a
// derived file and not the same thing as having none.
func carrySidecars(dir, tmp string) {
	for _, name := range []string{fixesFile, fixRawFile, commandsFile, cooccurFile} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(tmp, name), b, 0o600)
	}
}

// appendableKind reports whether a kind can be read from where the last pass
// stopped. Named by kind rather than by path so the guard and the test that
// keeps it honest ask the same question.
func appendableKind(kind string) bool {
	if kind == "" {
		return false
	}
	resumableKindsOnce.Do(func() {
		resumableKinds = map[string]bool{}
		for _, k := range sources.KindsWithOffsetParsers() {
			resumableKinds[k] = true
		}
	})
	return resumableKinds[kind]
}

// The registry is fixed for the life of the process, and this is asked once
// per changed file: walking it each time cost 4.3 KB of garbage a file.
var (
	resumableKinds     map[string]bool
	resumableKindsOnce sync.Once
)

// inlineAppendMax is how many new bytes a search will read before answering.
// Eight megabytes is about a second of parsing on the machine #3021 was
// measured on, and larger than any ordinary turn: the sessions that pass it
// are the ones being written while the question is asked. A variable so a test
// can put the boundary where its fixtures are.
var inlineAppendMax int64 = 8 << 20

// appendTailBytes is how much has been added to the files an append would
// read, which is what the reader waits on.
func appendTailBytes(changed map[string]FileState, old map[string]FileState) int64 {
	var n int64
	for p, f := range changed {
		of, ok := old[p]
		if !ok {
			// A file deja has not read before is new from its first byte, and
			// the append path reads all of it.
			n += f.Size
			continue
		}
		if f.Size > of.Size {
			n += f.Size - of.Size
		}
	}
	return n
}

func canAppendIncremental(changed map[string]FileState, old map[string]FileState) bool {
	if len(changed) == 0 {
		return false
	}
	for p, f := range changed {
		of, ok := old[p]
		if !ok {
			// A transcript deja has never read is new from its first byte, which
			// is what this path writes: parse from offset zero, every session in
			// it new, old records untouched. Refusing it sent every new
			// conversation — each one is a new file — down the replacement path,
			// which rewrites and re-tokenizes the whole store: 4.76s against
			// 0.30s on a 171 MB index, and growing with the store rather than
			// with the file (#3500).
			//
			// Whether the kind can *resume* a parse does not matter here, only
			// whether deja can parse it at all: a new file is read from its
			// first byte either way. Requiring an offset parser refused the
			// whole batch over one such file, and an inline caller hands
			// rewrite-grade work to a detached warmup rather than doing it —
			// so the live index on the machine this was found on had never
			// read 1,013 of the files its own loaders list: five senpi
			// transcripts, five Copilot Chat ones, and a thousand opencode
			// session diffs, a kind that gains a file per session (#3747).
			if _, ok := kindForPath(p); !ok {
				return false
			}
			// Except a new opencode diff file: it is read as its session, whole,
			// and that session is already in the index (#4207).
			if storeHarness(p) == "opencode" {
				return false
			}
			continue
		}
		if f.Size <= of.Size {
			return false
		}
		// A deleted transcript put back is read whole: the row held for it
		// while it was gone kept the span of copies that have left since, and
		// only a read from the start gives the row a rebuild gives (#5380).
		if of.Kept {
			return false
		}
		// A store that hands back touched sessions whole has nothing to append:
		// adding them to what the index holds is the doubling the replacement
		// path exists to avoid. Defensive — sqlite rewrites its page count in
		// the header on growth, so the prefix check below rarely passes (#4207).
		if rereadsWholeSessions(p) {
			return false
		}
		// A prior pass that saw bytes past the last newline leaves SafeSize
		// short of Size. The readers index a last line that parses even with no
		// newline yet, so resuming from SafeSize read that line a second time
		// once the writer finished it; and a torn first line with SafeSize 0
		// would be re-read mid-line. Either way the tail is ambiguous, so these
		// files go through the full re-index path instead (#appendloss).
		if of.SafeSize < of.Size {
			return false
		}
		// Growth is not proof that the earlier bytes are untouched: a rewind
		// that truncates and regrows past the old length looks exactly like
		// an append, and appending onto it leaves the rewritten prefix in the
		// index with its old text. Compare the prefix before trusting it.
		switch {
		case of.PrefixSample != 0:
			if filePrefixSample(p, of.SafeSize) != of.PrefixSample {
				return false
			}
		case of.PrefixHash != 0:
			// A manifest from before the sample existed. Verified the old way
			// this once; the walk above has already recorded a sample, so the
			// next call is bounded.
			if filePrefixHash(p, of.SafeSize) != of.PrefixHash {
				return false
			}
		}
		// Whether the kind can resume a parse, not whether its name is on a
		// list: the list grew one harness at a time and six kinds that declare
		// an offset parser were never added, so their files were re-read whole
		// on every pass (#2870).
		if !appendableKind(harnessForPath(p)) {
			return false
		}
		// The kind can resume, but this tail may rewrite a record already
		// stored: a Cherry Studio reply still streaming when the last pass ran
		// (#4346).
		if k, ok := kindForPath(p); ok && k.Resumes != nil && !k.Resumes(p, resumeOffset(of)) {
			return false
		}
	}
	return true
}

// readTo holds this pass's transcript reads to the sizes its walk recorded,
// which is where the next pass resumes. A line the client wrote after the walk
// was read here and again there, and the copies stayed until a rebuild
// (#4442).
func readTo(files map[string]FileState) func() {
	ends := make(map[string]int64, len(files))
	for p, f := range files {
		if strings.HasSuffix(p, ".jsonl") {
			ends[p] = f.Size
		}
	}
	return sources.LimitReads(ends)
}

// errChangedWhileRead is a file rewritten after the walk stated it: what the
// read found is not what the state the pass would record describes.
var errChangedWhileRead = errors.New("changed while deja read it")

// readAsWalked reports whether p, just read, still holds what f, its walk
// state, describes. A transcript rewritten after the walk was read as it is
// now and recorded as it was, and the pass after it could not tell what it
// held: a rewrite between `deja index`'s check and its pass lost the
// session's commands (#5503). Growth alone is fine: the read stops at the
// walk's size (readTo). A database store is read from its watermark, which
// moves with what was read, not with the walk.
func readAsWalked(p string, f FileState) bool {
	if storeHarness(p) != "" {
		return true
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return false
	}
	if fi.Size() == f.Size && fi.ModTime().UnixNano() == f.MTime {
		return true
	}
	return strings.HasSuffix(p, ".jsonl") && f.PrefixSample != 0 && fi.Size() >= f.Size && filePrefixSample(p, f.SafeSize) == f.PrefixSample
}

// readSettled runs parse for p, read at f, its walk state, and returns what
// it read with the state it was read at. A file rewritten after the walk is
// stated and read again, twice at most, and then left for the next pass.
// again reports a read at a state other than f.
func readSettled(p string, f FileState, parse func() ([]model.Session, error)) (ss []model.Session, at FileState, again bool, err error) {
	for try := 0; ; try++ {
		ss, err = parse()
		if err != nil || readAsWalked(p, f) {
			return ss, f, try > 0, err
		}
		fi, serr := os.Lstat(p)
		if try == 2 || serr != nil || !fi.Mode().IsRegular() {
			return nil, f, false, errChangedWhileRead
		}
		f = walkFileStateIn(sources.Registry(), p, fi, nil)
		if strings.HasSuffix(p, ".jsonl") {
			defer sources.LimitReads(map[string]int64{p: f.Size})()
		}
	}
}

// resumeOffset is where an appended read of a known file starts: the end of
// the last complete line indexed, or the old size when none was recorded.
func resumeOffset(old FileState) int64 {
	if old.SafeSize == 0 || old.SafeSize > old.Size {
		return old.Size
	}
	return old.SafeSize
}

func appendIncremental(dir, harness, scope string, old Manifest, files map[string]FileState, changed map[string]FileState) (filesTouched, messages, unreadable int, err error) {
	// This pass's counts, like the two full paths: an incremental that nobody
	// read between builds otherwise reported its own colliding ids plus the
	// ones before it (#1850).
	emptied.Store(0)
	collisions.Store(nil)
	merged.Store(0)
	lastIngestFiles = len(changed)
	readThisPass(changed)
	// Deliberately not parsedThisPass: this path reads the appended tail, not
	// the file, so what it finds adds to the file's count instead of replacing
	// it. Marking the file re-read dropped every bad line in the part already
	// indexed — which is every live session.
	rf, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, 0, 0, err
	}
	// The log is being APPENDED to, so ids must continue where the existing
	// records left off. Starting a fresh table would hand id 0 to a new
	// string and silently repoint every record that already uses it.
	tbl := tablesFromManifest(old)
	rw, err := newRecordWriter(rf, tbl)
	if err != nil {
		_ = rf.Close()
		return 0, 0, 0, err
	}
	defer func() { _ = rw.Close() }()
	buckets := bucketPostings{}
	// The same rule the two full builds apply: a message repeated verbatim in
	// one pass is written once. Without it the same rollout held one record or
	// two depending on whether it was appended or rebuilt, so an index compared
	// against a rebuilt copy of itself came out unequal (#3934).
	seenMsgs := msgSeen{}
	// Every session this pass read, for the sidecars at the end.
	var appended []model.Session
	var rowMoved map[string]bool
	// Only what this pass adds: it goes into an overlay beside each bucket
	// rather than into the bucket written whole again (#5387).
	loadBucket := func(tok string) (map[string][]posting, error) {
		b := bucket(tok)
		data, ok := buckets[b]
		if !ok {
			data = map[string][]posting{}
			buckets[b] = data
		}
		return data, nil
	}
	m := old
	m.Version = version
	m.Format = onDiskFormat
	m.Scope = scope
	m.BuiltAt = time.Now()
	m.SourcesReadAt = m.BuiltAt
	m.ReadZone = zoneFingerprint()
	m.Files = files
	m.Redacted = 0
	carryRedactions(&m, old, map[string]bool{})
	if m.Sessions == nil {
		m.Sessions = map[string]SessionMeta{}
	}
	m.Empty = keepEmpty(old.Empty, m.Sessions, files)
	// The rows this pass made or took back from Empty, and the ones it wrote a
	// record under: one with none goes back to Empty (#5563).
	emptyBefore, wroteUnder := map[string]bool{}, map[string]bool{}
	// Sorted, not map order: two sessions can claim the same harness:id, and
	// which one wins decided the project a whole conversation was filed under
	// — differently on every run (#698).
	// A file deja has never read is read whole here, not resumed, so its counts
	// start over the way the full paths start them. Leaving it out of the
	// parsed set added this pass's bad lines to a count the file never had.
	newFiles := map[string]FileState{}
	for p, f := range changed {
		if _, ok := old.Files[p]; !ok {
			newFiles[p] = f
		}
	}
	parsedThisPass(newFiles)
	// Prompt logs after the transcripts: a session whose transcript this pass
	// reads is then held from it, and its log line dropped as a rebuild drops
	// it (#5503).
	order := sortedKeys(changed)
	sort.SliceStable(order, func(i, j int) bool { return !isCodexHistory(order[i]) && isCodexHistory(order[j]) })
	for _, p := range order {
		of, known := old.Files[p]
		var ss []model.Session
		var err error
		if known {
			// Appended to from where the last pass stopped, which a rewrite
			// moves: the next pass decides how to read it.
			ss, err = parseAppendedFile(harness, p, of, false)
			if err == nil && !readAsWalked(p, changed[p]) {
				err = errChangedWhileRead
			}
		} else {
			var f FileState
			ss, f, _, err = readSettled(p, changed[p], func() ([]model.Session, error) { return parseAppendedFile(harness, p, of, true) })
			if err == nil {
				m.Files[p], changed[p] = f, f
			}
		}
		if err != nil {
			if known {
				m.Files[p] = of // retry this file on the next pass
			} else {
				delete(m.Files, p)
			}
			continue
		}
		ss = dropShadowedPrompts(sources.FilterSessions(filterTombstoned(ss)), m.Sessions)
		// A prompt whose transcript has not landed yet is read again next pass,
		// from the same offset, rather than indexed from the log now and taken
		// over by a full rewrite when the transcript arrives.
		if isCodexHistory(p) && waitsForTranscript(ss, m.Sessions, time.Now()) {
			if known {
				m.Files[p] = of
			} else {
				delete(m.Files, p)
			}
			continue
		}
		// Scrub before anything is derived from the text, as the other two paths
		// do. Redacting per message inside the write loop below left the derived
		// fields — the friction signatures especially — hashed from the raw
		// text, so the same session got different signatures depending on which
		// path had touched it last.
		preRedactSessions(&m, ss)
		for i := range ss {
			var from *FileState
			if known {
				from = &of
			}
			dropStoredRepeats(rw, dir, &ss[i], m.Sessions[ss[i].Harness+":"+ss[i].ID], seenMsgs, m.Files, harness, from)
		}
		// Kept for the sidecars below, after redaction so a pair cannot carry a
		// credential the records do not.
		appended = append(appended, ss...)
		filesTouched++
		for _, s := range ss {
			key := s.Harness + ":" + s.ID
			meta, listed := m.Sessions[key]
			if row, ok := m.Empty[key]; ok && !listed {
				meta = metaWithOrd(row, nextSessionOrd(m.Sessions))
				delete(m.Empty, key)
			}
			if !listed {
				emptyBefore[key] = true
			}
			before := meta
			named := meta.ID != ""
			if !named {
				meta = metaWithOrd(metaForSession(s), nextSessionOrd(m.Sessions))
			}
			if meta.Started.IsZero() || (!s.Started.IsZero() && s.Started.Before(meta.Started)) {
				meta.Started = s.Started
			}
			if s.Updated.After(meta.Updated) {
				meta.Updated = s.Updated
			}
			// A recorded path that is not there cannot own the row, and
			// without this the row kept the dead path and was marked as sharing
			// its id with it (#1086). A rename in one pass no longer reaches
			// here: a file deleted this pass sends the pass to the replacement
			// path, whose rename rule pairs the two. A transcript deleted in an
			// earlier pass and kept is a copy, as a rebuild reads it, and owns
			// the row by the same rule as any other (#5380). This is what is
			// left: a path gone without being kept.
			// Not for an arrival with nothing to index: that is Gemini's resume
			// stub beside a deleted transcript, not the transcript moving (#4213).
			if meta.Path != "" && s.Path != "" && meta.Path != s.Path && holdsText(s) && !m.Files[meta.Path].Kept {
				if _, err := os.Lstat(meta.Path); err != nil {
					meta.Path = ""
				}
			}
			owns, collided := claimSession(meta, s)
			// A row with nothing to index that changes hands describes the
			// stub, not the transcript taking it over: folding the transcript
			// in on top counted the stub's preamble too, one more message than
			// a rebuild of the same files gives (#4213). Start the row over
			// from the session that owns it, as the full build does.
			// Only for a file read whole: a known file hands over its tail,
			// which is not the session. The span stays what both files cover,
			// as the full build keeps it.
			// A new file that sorts ahead of the one holding the row takes it
			// the same way: a rebuild derives the row from its owner alone.
			if owns && (meta.NoText || collided) && meta.Path != s.Path && !known {
				// The span the row had before s widened it: s's own span is
				// not one the files beside it cover.
				prev := meta
				if named {
					prev.Started, prev.Updated = before.Started, before.Updated
				}
				meta = ownerRow(prev, s, metaForSession(s), prev.Ord, collided)
				if !prev.Started.IsZero() && (meta.Started.IsZero() || prev.Started.Before(meta.Started)) {
					meta.Started = prev.Started
				}
				if prev.Updated.After(meta.Updated) {
					meta.Updated = prev.Updated
				}
			}
			if !owns {
				meta.SharedStarted, meta.SharedUpdated = widerSpan(meta.SharedStarted, meta.SharedUpdated, s.Started, s.Updated)
			}
			if collided {
				collisions.Add(key)
				meta.Shared = true
			}
			// A tail with no files of its own names only the directory the
			// session started in; the row already holds what the whole file
			// said, which a rebuild also takes.
			if s.Project != "" && s.Project != "-" && owns && (!known || !named || !s.ProjectFromDir) {
				meta.Project = s.Project
			}
			if s.Path != "" && owns {
				meta.Path = s.Path
			}
			// A row named from the agent's words or tool output takes the
			// first question that arrives later, as a rebuild names it. Only
			// the owner names it: a copy's tail renamed the session after its
			// own newest turn (#5358).
			outranked := false
			if owns && s.Title == "" && !titlePlaceholder(meta.Title) {
				t, agent := sessionTitleFrom(s)
				outranked = titleRankOfRow(t, agent) > titleRankOfRow(meta.Title, meta.AgentTitle)
			}
			if owns && (titlePlaceholder(meta.Title) || outranked) {
				// The incremental fallback redacted nothing at all before; keep
				// sessionTitleFrom's correct fromAgent bit and redact before the
				// cut, as the full rebuild does.
				meta.Title, meta.AgentTitle = sessionTitleFrom(s)
				meta.Title, _ = redact.Text(meta.Title)
				meta.Title = truncateTitle(meta.Title, 60)
			} else if owns && s.Title != "" {
				// A title the source authored is a live value, not a one-time
				// naming: a promoted note's carries its state, and the loader
				// rewrites it on every correction. Only the full rebuild took
				// the new one, so `promote --state rejected` left every
				// one-line surface — `deja last`, the digest, the citation the
				// hook hands the agent to say aloud — reading "[accepted]"
				// until an unrelated rebuild happened to run (#R11).
				// The same widening the first naming does, or a session that
				// gets its thin title later — dsh and opencode both retitle
				// after the fact — would keep it until an unrelated rebuild.
				//
				// A thin title is widened from the session's first substantial
				// turn, and a tail does not hold it: one appended turn renamed
				// the session after itself. A row already named from the whole
				// session keeps that name (#4452). Unless the sidecar the title
				// lives in changed too: that is a rename, the row holds the old
				// name, and only the whole session can widen the new one
				// (#4592).
				t, _ := redact.Text(s.Title)
				t = boundSourceTitle(s.Harness, t)
				fromTail := known && named && s.Harness != "deja" && thinTitle(t) && !thinTitle(meta.Title)
				w := widenThinSourceTitle(s, t)
				if fromTail && sidecarChanged(of, changed[p]) {
					if whole, ok := wholeSession(p, s); ok {
						w, fromTail = widenThinSourceTitle(whole, t), false
					}
				}
				if !fromTail && w != meta.Title {
					meta.Title = w
					meta.AgentTitle = s.AgentTitle
				}
			}
			// Only the session that owns this row. The full build writes these
			// fields under the same condition; here the merge ran regardless,
			// so growing the loser of an id collision moved the owner's Words,
			// flipped its GaveUp and put the loser's files in its Touched —
			// the wrong conversation's files surfacing in blame (#1304).
			if owns {
				ran := meta.RanCommand
				extendDerived(&meta, s.Messages)
				// The session's first command: its line can come from what it
				// said before this tail, which the fold never saw. Once per
				// session, so the whole read is affordable here.
				if !ran && meta.RanCommand && known {
					if whole, ok := wholeSession(p, s); ok {
						meta.Settled = sessionSettled(whole)
					}
				}
				// The bridge record can land in any append, and a later one
				// read from the watermark does not carry it again.
				if s.RemoteID != "" {
					meta.RemoteID = s.RemoteID
				}
			}
			meta.Copies = withCopies(meta.Path, before.Copies, before.Path, s.Path)
			// Which copy's commands the tables count follows the row's file
			// (foreignPath): one that moved has its runs mined again.
			if named && (before.Shared != meta.Shared || meta.Shared && filepath.Dir(before.Path) != filepath.Dir(meta.Path)) {
				if rowMoved == nil {
					rowMoved = map[string]bool{}
				}
				rowMoved[key] = true
			}
			m.Sessions[key] = meta
			for _, msg := range s.Messages {
				// Already redacted (and length-capped) by preRedactSessions above.
				text := msg.Text
				// A message that is nothing but harness plumbing strips to empty
				// (#551). Writing it would store a record with no content and give
				// it a posting.
				if strings.TrimSpace(text) == "" {
					continue
				}
				if seenMsgs.dup(key, msg.Role, msg.Time, text) {
					continue
				}
				off, err := rw.write(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time})
				if err != nil {
					return filesTouched, messages, 0, err
				}
				messages++
				wroteUnder[key] = true
				// Keyed the way a rebuild keys it: only the part that earns
				// postings, its date keys, and the tool bit.
				var keyErr error
				tool := isToolRole(msg.Role)
				eachIndexKey(tokenizedPart(msg.Role, text), msg.Time, func(tok string) {
					if keyErr != nil {
						return
					}
					data, err := loadBucket(tok)
					if err != nil {
						keyErr = err
						return
					}
					data[tok] = append(data[tok], posting{Off: off, Sid: meta.Ord, Tool: tool})
				})
				if keyErr != nil {
					return filesTouched, messages, 0, keyErr
				}
			}
		}
	}
	if err := rw.Close(); err != nil {
		return filesTouched, messages, 0, err
	}
	for key := range emptyBefore {
		if !wroteUnder[key] {
			setAsideEmpty(&m, key)
		}
	}
	stale, err := addOverlays(filepath.Join(dir, "buckets"), filepath.Join(dir, "buckets"), buckets, m.DeadRecords)
	if err != nil {
		return filesTouched, messages, 0, err
	}
	m.bucketsLeaving = staleCount(stale)
	setDatabaseStoreWatermarks(m.Files, m.Sessions)
	m.RecordStrings = tbl.strs
	// Read before writeManifest: the fold in there drains the counters, and the
	// caller prints its line after this returns (#2007).
	unreadable = totalMalformed()
	// A pass that recorded files is past a first build's early slice; what
	// is still missing is what the next walk finds unread.
	m.PartialOf = 0
	if err := writeManifest(dir, m); err != nil {
		return filesTouched, messages, unreadable, err
	}
	dropStale(stale)
	m.bucketsLeaving = 0
	// The sidecars, from what this pass read and from the records already on
	// file. The replacement path does the same three things after it carries
	// them over; this path used to do none of them, which was survivable while
	// it only ever saw a tail and is not once a new conversation comes through
	// here (#3500). A session whose tail brought a tool record is mined again
	// whole, so a pair that starts before the tail is found; the rest of the
	// table is still earned, and a candidate on file is what a second sighting
	// needs to be promoted.
	if m.SearchOnly {
		return filesTouched, messages, unreadable, nil
	}
	whole, rewhole := wholeSessionsForFixes(dir, m, appended)
	mergeFixes(dir, dir, nil, whole, rewhole)
	// The two command tables are mined from every record the index holds, which
	// is a scan of records.bin — 2.9s of an 8.6s pass on a 191 MB store. Nothing
	// this pass added can change them unless it added a command or the output of
	// one, and most appends are speech, so ask first.
	// A pass killed after its commit and before the tables left them behind
	// the log, which the next pass heals whatever it appended.
	if carriesWork(appended) || commandTablesBehind(dir, m) || len(rowMoved) > 0 {
		// From the states the last update left: only the sessions this pass
		// appended to are read (#4288). The command state goes last, so it
		// stays behind until both tables are written.
		buildCommandFailsFromIndex(dir, readCommandFailState(dir), maps.Clone(rowMoved))
		buildCommandTables(dir, dir, m, len(m.DeadRecords), maps.Clone(rowMoved))
	}
	return filesTouched, messages, unreadable, nil
}

// rereadSharedCopies reads again each held transcript that shares a removed
// one's file name, where that name is the id of a row two transcripts share:
// the filename-derived id two projects can share (#699). A turn both copies
// held was written once, under the copy read first; dropping that copy's
// records by path took the survivor's commands and outputs with them, and the
// row stayed on the deleted file until a rebuild (#4310).
func rereadSharedCopies(removed map[string]bool, changed, files map[string]FileState, sessions map[string]SessionMeta) {
	shared := map[string]bool{}
	for _, meta := range sessions {
		if meta.Shared {
			shared[meta.ID] = true
		}
	}
	names := map[string]bool{}
	for p := range removed {
		base := filepath.Base(p)
		if storeHarness(p) == "" && shared[strings.TrimSuffix(base, filepath.Ext(base))] {
			names[base] = true
		}
	}
	if len(names) == 0 {
		return
	}
	for p, f := range files {
		if _, ok := changed[p]; !ok && !removed[p] && names[filepath.Base(p)] && storeHarness(p) == "" {
			changed[p] = f
		}
	}
}

// rewrittenInPlace reports whether a kept file has another beside it under
// the same name in another form: Codex compressing x.jsonl to x.jsonl.zst,
// Gemini rewriting x.json as x.jsonl. That is the file moving, which only the
// replacement path's rename rule pairs up; the append path wrote the new
// file's records beside the old ones and kept the dead path (#4252). A pass
// that ran between zstd writing the .zst and removing the .jsonl held both,
// the row marked shared; the .zst is read again so the rule can pair them.
func rewrittenInPlace(kept map[string]bool, changed, files, held map[string]FileState, sessions map[string]SessionMeta) bool {
	if len(kept) == 0 {
		return false
	}
	stems := map[string]bool{}
	for p := range kept {
		stems[fileStem(p)] = true
	}
	shared := map[string]bool{}
	for _, meta := range sessions {
		if meta.Shared && kept[meta.Path] {
			shared[fileStem(meta.Path)] = true
		}
	}
	found := false
	for p, f := range files {
		if kept[p] || !stems[fileStem(p)] {
			continue
		}
		if _, ok := changed[p]; ok {
			if _, known := held[p]; !known {
				found = true
			}
		} else if shared[fileStem(p)] {
			changed[p] = f
			found = true
		}
	}
	return found
}

// fileStem is p without its compression suffix and its extension.
func fileStem(p string) string {
	p = strings.TrimSuffix(p, ".zst")
	return strings.TrimSuffix(p, filepath.Ext(p))
}

// carriesWork reports whether any of these sessions holds a command or the
// output of one, which is all the two command tables are mined from.
func carriesWork(ss []model.Session) bool {
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == sources.RoleCommand || m.Role == sources.RoleToolOutput {
				return true
			}
		}
	}
	return false
}

// sidecarChanged reports whether the metadata file read with a transcript
// (Kimi's state.json, say) changed between two walks.
func sidecarChanged(a, b FileState) bool {
	return a.MetadataSize != b.MetadataSize || a.MetadataMTime != b.MetadataMTime
}

// wholeSession reads s's file from its first byte and returns s as the full
// build sees it, for what an appended tail cannot tell.
func wholeSession(p string, s model.Session) (model.Session, bool) {
	ss, err := parseAppendedFile("", p, FileState{}, true)
	if err != nil {
		return model.Session{}, false
	}
	for _, w := range ss {
		if w.Harness == s.Harness && w.ID == s.ID {
			w.Messages = dropRepeatedMessages(w.Messages)
			return w, true
		}
	}
	return model.Session{}, false
}

func sameFile(a, b FileState) bool {
	return a.Path == b.Path && a.Size == b.Size && a.MTime == b.MTime &&
		a.MetadataSize == b.MetadataSize && a.MetadataMTime == b.MetadataMTime &&
		a.CWDSize == b.CWDSize && a.CWDMTime == b.CWDMTime &&
		a.WALSize == b.WALSize && a.WALMTime == b.WALMTime
}

// walStat is the size and mtime of a SQLite store's write-ahead log, zero for
// a store without one and for anything that is not a database. Hermes
// checkpoints every 50 writes of one process, so a few short sessions live in
// state.db-wal alone, and a walk that statted state.db called them unchanged
// (#5140).
func walStat(p string) (size, stamp int64) {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".db", ".sqlite", ".sqlite3", ".vscdb":
	default:
		return 0, 0
	}
	fi, err := os.Lstat(p + "-wal")
	if err != nil || !fi.Mode().IsRegular() {
		return 0, 0
	}
	return fi.Size(), fi.ModTime().UnixNano()
}

// kindForPath returns the registry FileKind whose Match accepts p.
func kindForPath(p string) (sources.FileKind, bool) {
	return kindIn(sources.Registry(), p)
}

// kindIn is kindForPath against a registry already built.
func kindIn(reg []sources.Harness, p string) (sources.FileKind, bool) {
	for _, h := range reg {
		for _, k := range h.Kinds {
			if k.Match(p) {
				return k, true
			}
		}
	}
	return sources.FileKind{}, false
}

func parseChangedFile(harness, p string, old FileState) ([]model.Session, error) {
	k, ok := kindForPath(p)
	if !ok {
		return nil, nil
	}
	return sources.ReadWatched(p, func() ([]model.Session, error) { return k.Parse(p, old.LastUpdated) })
}

func parseAppendedFile(harness, p string, old FileState, isNew bool) (ss []model.Session, err error) {
	defer func() {
		if r := recover(); r != nil {
			ss, err = nil, fmt.Errorf("parser panic on %s: %v", p, r)
		}
	}()
	k, ok := kindForPath(p)
	if !ok {
		return nil, nil
	}
	if k.ParseFrom == nil {
		// Resuming is what this kind cannot do, and a file deja has never read
		// needs no resuming: read it whole from its first byte, which is the
		// same work the replacement path would do for it (#3747).
		if !isNew || k.Parse == nil {
			return nil, nil
		}
		return sources.ReadWatched(p, func() ([]model.Session, error) { return k.Parse(p, 0) })
	}
	return sources.ReadWatched(p, func() ([]model.Session, error) { return k.ParseFrom(p, resumeOffset(old), old.LastUpdated) })
}

// harnessForPath reports the fine-grained source kind for a path (claude,
// codex-history, cursor-db, ...) via the sources registry, or "" if none.
// harnessForPath maps an ingest-diagnostic path to the harness it belongs to.
// A directory has no transcript extension, so the matchers reject it outright;
// asking what a transcript inside it would be is the same question the walk
// was already answering (#818).
func harnessForPath(p string) string {
	if h := sources.KindForPath(p); h != "" {
		return h
	}
	if filepath.Ext(p) != "" {
		return ""
	}
	for _, name := range []string{"probe.jsonl", "probe.json", "probe.md"} {
		if h := sources.KindForPath(filepath.Join(p, name)); h != "" {
			return h
		}
	}
	return ""
}

// setDatabaseStoreWatermarks stamps every store deja reads through SQLite.
//
// Each of the six has a since-the-watermark parser, and dbParse only calls it
// once the store carries a watermark — so the three that were never stamped
// (grok, hermes, zed) read their store whole on every pass, for the life of
// the index (#2075).
func setDatabaseStoreWatermarks(files map[string]FileState, sessions map[string]SessionMeta) {
	for h, db := range opencodeSchemaDBs {
		setStoreLastUpdated(files, sessions, h, db())
	}
	setStoreLastUpdated(files, sessions, "goose", sources.GooseDB())
	// grok's database had a since-the-watermark parser in the registry and
	// nothing ever stamped it, so every pass read the store whole — the pass
	// with one new line cost what reading everything costs (#2075).
	//
	// Safe to stamp because ParseGrokDBSince selects messages rather than
	// sessions and normalises both sides with a millisecond backoff (#2150),
	// which is the cursor shape. hermes compares whole seconds
	// with a strict >, and zed returns whole threads; each needs its own fix
	// before it can be stamped, so neither is here.
	setStoreLastUpdated(files, sessions, "grok", sources.GrokDB())
	// hermes, once its reader stopped dropping the watermark's own second
	// (#2075). Several stores on one machine: current builds keep one at the
	// root and older ones shard per profile, and each is stamped with its own
	// newest — stamping them alike would give a quiet profile the busy one's
	// time (#2071).
	for _, db := range sources.HermesDBs() {
		setStoreLastUpdated(files, sessions, "hermes", db)
	}
	// zed, whose cursor selects threads rather than messages: a continued
	// thread comes back whole, so it joins rereadsWholeSessions below in the
	// same change — the goose shape, not the cursor one (#2075).
	setStoreLastUpdated(files, sessions, "zed", sources.ZedDB())
	for _, db := range sources.CursorDBs() {
		setStoreLastUpdated(files, sessions, "cursor", db)
	}
	for _, db := range sources.HermesDBs() {
		setStoreLastUpdated(files, sessions, "hermes", db)
	}
	for _, db := range sources.OpenClawAgentDBs() {
		setStoreLastUpdated(files, sessions, "openclaw", db)
	}
	// crush keeps one store per project and was never stamped, so one new
	// turn re-read every session in it: 3000 replaced, 8 s (#4381). Its
	// cursor selects sessions, so it is in rereadsWholeSessions too.
	for _, db := range sources.CrushDBs() {
		setStoreLastUpdated(files, sessions, "crush", db)
	}
}

// sessionInStore reports whether a row came from the store being stamped.
//
// Cursor keeps one database per workspace, and both it and goose record the
// store path in Path, so the row says which one it came from. opencode records
// the project directory instead (#2033) — and has a single database, so there
// the harness is the store, less any transcript files of its own (#4396).
func sessionInStore(s SessionMeta, harness, db string) bool {
	if _, ok := opencodeSchemaDBs[harness]; ok {
		// Not a row the diff files gave: it carries a file's mtime, which
		// says nothing about how far the database has been read (#4207).
		return inOpencodeSchemaDB(harness, s.Path)
	}
	return s.Path == db
}

// setStoreLastUpdated stamps a database-backed store with the newest session
// time so incremental passes can query only newer content.
//
// The newest session in THAT store: taking the newest across the harness
// stamped a quiet Cursor workspace with the busy one's time, and a turn the
// quiet store gained below that line was never asked for again (#2071).
func setStoreLastUpdated(files map[string]FileState, sessions map[string]SessionMeta, harness, db string) {
	f, ok := files[db]
	if !ok {
		return
	}
	var latest int64
	for _, s := range sessions {
		if s.Harness != harness || !sessionInStore(s, harness, db) {
			continue
		}
		if s.Updated.UnixNano() > latest {
			latest = s.Updated.UnixNano()
		}
	}
	// Never past the clock: one session stamped ahead of it (a skewed
	// machine, a store synced from one) put the mark a day out, and every
	// turn written in the meantime sat below it unread.
	if now := time.Now().UnixNano(); latest > now {
		latest = now
	}
	f.LastUpdated = latest
	files[db] = f
}

// currentFilesReusing carries derived state forward for files whose size and
// mtime are unchanged. Computing SafeSize means reading each file's tail and
// PrefixHash means reading its head, and on a large store that is most of what
// a search spends its time on — 650 ms against 13 ms of actual searching,
// every invocation, to conclude nothing moved. If size and mtime match, the
// bytes behind those numbers match too: that assumption already decides
// whether a file is reindexed at all.
func currentFilesReusing(h string, old map[string]FileState) map[string]FileState {
	return currentFilesWith(h, old)
}

// priorFiles is the previous walk's state, or nothing when the manifest could
// not be read — in which case every file is re-derived, as before.
func priorFiles(m Manifest, err error) map[string]FileState {
	if err != nil {
		return nil
	}
	return m.Files
}

// missingTree is a directory that vanished along with everything under it.
type missingTree struct {
	dir   string
	files int
	// mount is set when dir is a mount point rather than an ordinary
	// directory: an unplugged disk, not a deletion.
	mount bool
	// renamed is where the same volume turned up under a numbered name.
	renamed string
}

// renamedMount finds the path a missing one moved to when its volume came
// back under a different name: macOS mounts a disk as "/Volumes/Disk 1"
// when something already sits on "/Volumes/Disk", and every path naming the
// old mount point starts failing while the files are right there. Telling
// the user to reconnect a disk that is already connected helps nobody.
func renamedMount(path string) string {
	sep := string(filepath.Separator)
	for _, m := range mountParents {
		if !strings.HasPrefix(path, m+sep) {
			continue
		}
		rest := strings.TrimPrefix(path, m+sep)
		name, sub, _ := strings.Cut(rest, sep)
		if name == "" {
			continue
		}
		for n := 1; n <= 9; n++ {
			cand := filepath.Join(m, fmt.Sprintf("%s %d", name, n), sub)
			if _, err := os.Stat(cand); err == nil {
				return cand
			}
		}
	}
	return ""
}

// mountParents are the directories whose direct children are mount points.
// A test cannot create one for real — /Volumes is not writable — so it
// points this at a temporary directory instead.
var mountParents = defaultMountParents()

func defaultMountParents() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Volumes"}
	case "windows":
		return nil
	default:
		return []string{"/mnt", "/media"}
	}
}

// mountRoot reports whether dir is where a removable volume gets mounted.
// A directory the user deleted is a deletion and its sessions leave the
// index; a mount point that is empty means the disk is elsewhere, and the
// sessions it holds are not gone.
func mountRoot(dir string) bool {
	dir = filepath.Clean(dir)
	parent := filepath.Dir(dir)
	if parent == dir {
		return false
	}
	if runtime.GOOS == "windows" {
		return filepath.VolumeName(dir) == dir || parent == filepath.VolumeName(dir)+`\`
	}
	for _, m := range mountParents {
		if parent == m {
			return true
		}
	}
	// /run/media/<user>/<volume> is what udisks2 uses.
	return runtime.GOOS == "linux" && filepath.Dir(parent) == "/run/media"
}

// missingTrees groups files that disappeared by the outermost directory that
// is no longer there. One deleted file has an existing parent and is reported
// as nothing; a whole store that went away with its disk is one line.
func missingTrees(removed map[string]bool) []missingTree {
	byDir := map[string]int{}
	for p := range removed {
		dir := filepath.Dir(p)
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			continue
		}
		for {
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			if _, err := os.Stat(parent); !os.IsNotExist(err) {
				break
			}
			dir = parent
		}
		byDir[dir]++
	}
	out := make([]missingTree, 0, len(byDir))
	for dir, n := range byDir {
		out = append(out, missingTree{dir: dir, files: n, mount: mountRoot(dir)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dir < out[j].dir })
	return out
}

// sessionDirName is a directory named for one session: the id itself
// (Cursor, Copilot CLI, Antigravity, a Claude transcript's sidecar), Kimi's
// session_<id>, DeepSeek's session-<id>, Kiro's sess_<id>, and Cline CLI's
// <unix ms>_<suffix>, which `cline history delete` removes whole. Anchored,
// because an encoded working directory under a store root carries a UUID
// whenever the directory did — a temp dir, a sandbox — and that folder is a
// project, not a session.
var sessionDirName = regexp.MustCompile(`^(?:(?:session[_-]|sess_)?[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9]{13}_[0-9a-z]{5})$`)

// deletedFromLiveStore reports whether a file no longer on disk was deleted
// from a store that is still there — the client's cleanup or a deletion by
// hand, which the index keeps (#2970) — rather than with a store that went
// away whole: its directory is still there, or the directory that went with
// it was one session's (#4195).
func deletedFromLiveStore(p string) bool {
	if _, err := os.Stat(filepath.Dir(p)); err == nil {
		return true
	}
	return goneSessionDir(p) != ""
}

// supersededLogs are the removed paths a listed file replaced: an older dsh
// log generation, still on disk beside the one dsh migrated it into. Keeping
// it back as a deleted transcript held the session twice and sent the new log
// down the append path on top of the old records (#4600); left removed, the
// replacement path drops the old file's records and reads the new one whole.
func supersededLogs(removed map[string]bool, files map[string]FileState) map[string]bool {
	out := map[string]bool{}
	if len(removed) == 0 {
		return out
	}
	byDir := map[string][]string{}
	for p := range removed {
		byDir[filepath.Dir(p)] = nil
	}
	for p := range files {
		d := filepath.Dir(p)
		if _, ok := byDir[d]; ok {
			byDir[d] = append(byDir[d], p)
		}
	}
	for p := range removed {
		for _, q := range byDir[filepath.Dir(p)] {
			if sources.DeepSeekLogSupersedes(q, p) {
				out[p] = true
				break
			}
		}
	}
	return out
}

// sessionDirsUnder names the session directories the given files sit in.
func sessionDirsUnder(files map[string]FileState) map[string]bool {
	out := map[string]bool{}
	for p := range files {
		for d, i := filepath.Dir(p), 0; i < 4; d, i = filepath.Dir(d), i+1 {
			if b := filepath.Base(d); sessionDirName.MatchString(b) {
				out[b] = true
			}
		}
	}
	return out
}

// goneSessionDir is the session directory that went with a file, when the
// topmost directory missing above it is one session's under a parent that is
// still there; "" otherwise.
func goneSessionDir(p string) string {
	dir := filepath.Dir(p)
	if _, err := os.Stat(dir); err == nil {
		return ""
	}
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		if _, err := os.Stat(parent); err == nil {
			if deletedSessionDir(dir) {
				return dir
			}
			return ""
		}
		dir = parent
	}
}

// deletedSessionDir reports whether a missing directory is one session's,
// deleted from a store that is still there.
func deletedSessionDir(dir string) bool {
	if !sessionDirName.MatchString(filepath.Base(dir)) {
		return false
	}
	_, err := os.Stat(filepath.Dir(dir))
	return err == nil
}

func pluralFiles(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func currentFiles(h string) map[string]FileState {
	return currentFilesWith(h, nil)
}

// storeWalks counts the walks of the stores, for the test that keeps `deja
// index` to one.
var storeWalks atomic.Int64

func currentFilesWith(h string, old map[string]FileState) map[string]FileState {
	storeWalks.Add(1)
	// Walking the stores is the first thing a build does and, on a network
	// volume, the slowest: it published nothing, so every "memory is on its
	// way" surface reported an ordinary quiet day until the walk finished
	// (#1021).
	reportPhase("finding transcripts", 0)
	sources.BeginWalk()
	defer func() { setLastWalkFailed(sources.WalkFailures()) }()
	paths := map[string]bool{}
	// The stores a rebuild reads, not every store: an excluded harness was
	// walked and indexed here on every incremental pass (#5394). Its rows in
	// the manifest are kept as a store outside this run's view, so what is
	// already indexed stays until a rebuild, as with a project pattern.
	for _, hr := range sources.StoresToRead() {
		if h != "" && h != hr.Name {
			continue
		}
		for _, p := range hr.Files() {
			paths[p] = true
		}
	}
	out := statFiles(paths, old)
	injectHermesPG(out, old)
	noteLockedAway(h, out, old)
	return out
}

// noteLockedAway names an indexed transcript the walk did not list because a
// directory above it now refuses this run. A store that lists its files with
// a glob drops the refusal on the floor, and the transcript then read as gone
// (#5393). Once per directory the walk already named. One Lstat per directory:
// whether a file can be statted is decided by the directories above it.
func noteLockedAway(h string, out, old map[string]FileState) {
	if h != "" {
		return
	}
	var failed map[string]string
	denied := map[string]bool{}
	for p, f := range old {
		if _, ok := out[p]; ok || f.Kept || p == syncImportPath || sources.OutsideStoresRead(p) {
			continue
		}
		d := filepath.Dir(p)
		locked, seen := denied[d]
		if !seen {
			locked = behindLockedDir(p)
			denied[d] = locked
		}
		if !locked {
			continue
		}
		if failed == nil {
			failed = sources.WalkFailures()
		}
		named := false
		for ; d != filepath.Dir(d); d = filepath.Dir(d) {
			if failed[d] != "" {
				named = true
				break
			}
		}
		if !named {
			sources.NoteUnreadable(p)
		}
	}
}

// lastWalkFailed is what the latest store walk could not read: directories it
// could not list and transcripts whose mode refuses a read.
var (
	lastWalkMu     sync.Mutex
	lastWalkFailed map[string]string
)

func setLastWalkFailed(m map[string]string) {
	lastWalkMu.Lock()
	lastWalkFailed = m
	lastWalkMu.Unlock()
}

func walkFailed() map[string]string {
	lastWalkMu.Lock()
	defer lastWalkMu.Unlock()
	return lastWalkFailed
}

// unreadableCurrent reports whether the manifest's unreadable paths are the
// ones the latest walk saw. A chmod changes no size or mtime, and a directory
// that cannot be listed adds no file, so the walk alone called the index up to
// date and nothing ever said part of the history was not being read (#5393).
// Only paths with a recorded error are looked at, so the walk costs no more.
func unreadableCurrent(m Manifest, files map[string]FileState) bool {
	failed := walkFailed()
	for p := range failed {
		if m.IngestFiles[p].Error == "" {
			return false
		}
	}
	for p, e := range m.IngestFiles {
		if e.Error == "" || failed[p] != "" {
			continue
		}
		if f, ok := files[p]; ok {
			if of, held := m.Files[p]; e.Walk && held && sameFile(of, f) {
				return false
			}
			continue
		}
		if gone(p) {
			return false
		}
		if _, held := m.Files[p]; !held && dirListable(p) {
			return false
		}
	}
	return true
}

// behindLockedDir reports an indexed file the walk did not list because a
// directory above it refuses this run, rather than because it went away.
func behindLockedDir(p string) bool {
	_, err := os.Lstat(p)
	return os.IsPermission(err)
}

// gone reports a path that is no longer there at all: a transcript the walk
// once could not read, deleted since, has nothing left to report.
func gone(p string) bool {
	_, err := os.Lstat(p)
	return errors.Is(err, fs.ErrNotExist)
}

// dirListable reports a directory that lists now. Only a directory is
// opened: the path may be a transcript that failed to read.
func dirListable(p string) bool {
	if fi, err := os.Lstat(p); err != nil || !fi.IsDir() {
		return false
	}
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil || !fi.IsDir() {
		return false
	}
	_, err = f.Readdirnames(1)
	return err == nil || errors.Is(err, io.EOF)
}

// statFiles is the walk's FileState for each path that is a regular file.
//
// Every search stats each transcript to learn nothing moved: 12k files on the
// store in #5386. The files are independent, so a few workers share them; the
// result is the same map either way.
func statFiles(paths map[string]bool, old map[string]FileState) map[string]FileState {
	list := make([]string, 0, len(paths))
	for p := range paths {
		list = append(list, p)
	}
	states := make([]FileState, len(list))
	isFile := make([]bool, len(list))
	reg := sources.Registry()
	stat := func(i int) {
		p := list[i]
		fi, err := os.Lstat(p)
		if os.IsPermission(err) {
			// Listed by a directory this run may read but not enter.
			sources.NoteUnreadable(p)
			return
		}
		if err == nil && fi.Mode()&os.ModeSymlink == 0 && !fi.IsDir() {
			// Listed all the same: a file this run may not read is not a
			// deleted one, and the sessions already read from it stay.
			if sources.ModeUnreadable(p, fi) {
				sources.NoteUnreadable(p)
			}
			states[i], isFile[i] = walkFileStateIn(reg, p, fi, old), true
		}
	}
	workers := min(runtime.GOMAXPROCS(0), statWorkers, len(list)/statPerWorker)
	if workers <= 1 {
		for i := range list {
			stat(i)
		}
	} else {
		var next atomic.Int64
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(next.Add(1)) - 1
					if i >= len(list) {
						return
					}
					stat(i)
				}
			}()
		}
		wg.Wait()
	}
	out := make(map[string]FileState, len(list))
	for i, p := range list {
		if isFile[i] {
			out[p] = states[i]
		}
	}
	return out
}

// statWorkers caps the walk's workers: on 12k files four took the walk from
// 55 ms to 28 ms and eight did no better. statPerWorker is the fewest files
// worth starting one for.
const (
	statWorkers   = 4
	statPerWorker = 256
)

// walkFileState is what the walk records for a transcript file.
func walkFileState(p string, fi os.FileInfo, old map[string]FileState) FileState {
	return walkFileStateIn(sources.Registry(), p, fi, old)
}

// walkFileStateIn is walkFileState against a registry the caller built once.
// Building it is most of what the walk spent per file, twice over — once to
// find the file's kind, once to name its harness — and a walk visits every
// transcript on the machine (#5386).
func walkFileStateIn(reg []sources.Harness, p string, fi os.FileInfo, old map[string]FileState) FileState {
	fs := FileState{Path: p, Size: fi.Size(), MTime: fi.ModTime().UnixNano()}
	if strings.HasSuffix(p, ".jsonl") {
		// Deriving these means reading the file: the tail for the last
		// complete line, the head for the prefix hash. When size and
		// mtime are unchanged the bytes are too, and on a large store
		// this is the difference between a stat and 650 ms of reading.
		if of, ok := old[p]; ok && of.Size == fs.Size && of.MTime == fs.MTime {
			fs.SafeSize, fs.PrefixHash = of.SafeSize, of.PrefixHash
			fs.PrefixSample = of.PrefixSample
			fs.WrittenBack = of.WrittenBack
		} else {
			// Under the store read budget: the walk reads the tail and the
			// head of a changed transcript, and a file on a mount that stopped
			// answering held the run in "finding transcripts" (#5396).
			type ends struct {
				safe   int64
				sample uint64
			}
			e, err := sources.Watch(p, func() (ends, error) {
				safe := lastCompleteLineOffset(p, fi.Size())
				return ends{safe, filePrefixSample(p, safe)}, nil
			})
			if err != nil {
				// 0 is "not known": the next pass reads the file whole
				// rather than resuming from a guess.
				sources.NoteWalkError(p, err)
				e = ends{}
			}
			fs.SafeSize, fs.PrefixSample = e.safe, e.sample
		}
	}
	k, ok := kindIn(reg, p)
	if ok && k.Sidecar != nil {
		fs.MetadataSize, fs.MetadataMTime = k.Sidecar(p)
	}
	fs.WALSize, fs.WALMTime = walStat(p)
	// The kind's name is what harnessForPath answers for a path it matched;
	// only a path no kind claims goes on to its probe names.
	harness := k.Name
	if !ok {
		harness = harnessForPath(p)
	}
	if harness == "grok" {
		if cwd, err := os.Lstat(filepath.Join(filepath.Dir(filepath.Dir(p)), ".cwd")); err == nil && cwd.Mode()&os.ModeSymlink == 0 && !cwd.IsDir() {
			fs.CWDSize = cwd.Size()
			fs.CWDMTime = cwd.ModTime().UnixNano()
		}
	}
	return fs
}

// injectHermesPG adds the Postgres-backed Hermes store, which has no inode to
// stat. Its fingerprint is a query: count(*) as the size, max(timestamp) as the
// mtime, so the ordinary size/mtime change check drives a re-read (#1018). A
// store deja cannot reach this instant, or a DSN turned off for one run, keeps
// its old state — an unset env is an unmounted disk, not a deletion, and its
// sessions must not be dropped as if forgotten.
func injectHermesPG(out, old map[string]FileState) {
	dsn := sources.HermesPGDSN()
	if dsn == "" || !sources.StoreRead("hermes") {
		for p, of := range old {
			if sources.IsHermesPGStore(p) {
				out[p] = of
			}
		}
		return
	}
	token := sources.HermesPGStorePath(dsn)
	rows, newest, err := sources.HermesPGFingerprint(dsn)
	if err != nil {
		if of, ok := old[token]; ok {
			out[token] = of
		}
		return
	}
	out[token] = FileState{Path: token, Size: rows, MTime: newest, LastUpdated: newest}
}

// lastCompleteLineOffset finds the offset just past the final newline, so an
// append can resume without re-reading or losing a torn tail line. Reads at
// most the last 64KB; a longer unterminated tail falls back to full size.
func lastCompleteLineOffset(p string, size int64) int64 {
	if size == 0 {
		return 0
	}
	f, err := os.Open(p)
	if err != nil {
		return size
	}
	defer func() { _ = f.Close() }()
	// Walk backwards window by window: a torn line longer than one window
	// (a fat tool result caught mid-write) must not fool us into treating
	// the whole file as complete, or its message is lost after completion.
	const window = 64 * 1024
	end := size
	for end > 0 {
		start := end - window
		if start < 0 {
			start = 0
		}
		buf := make([]byte, end-start)
		if _, err := f.ReadAt(buf, start); err != nil {
			return size
		}
		for i := len(buf) - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				return start + int64(i) + 1
			}
		}
		end = start
	}
	return 0
}

// indexedText is a message's text as the index stores it: plumbing stripped,
// NFC-canonicalised (this path does not go through redactForIngest, #1098),
// redacted, then cut to maxIndexedText on a rune boundary.
func indexedText(text string) (string, redact.Counts, bool) {
	redacted, counts := redact.Text(nfcfold.Compose(stripSelfRecall(text)))
	if len(redacted) <= maxIndexedText {
		return redacted, counts, false
	}
	cut := maxIndexedText
	for cut > 0 && !utf8.RuneStart(redacted[cut]) {
		cut--
	}
	return redacted[:cut], counts, true
}

// preRedactSessions redacts every message concurrently before the write
// loop. Redaction is regex-heavy and was the serial bottleneck of a cold
// build; the write loop stays sequential (append-only log), but by the time
// it runs every text is already clean. Counters land in the manifest exactly
// as the serial path recorded them.
func preRedactSessions(m *Manifest, ss []model.Session) {
	var mu sync.Mutex
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := runtime.GOMAXPROCS(0)
	if workers > len(ss) {
		workers = len(ss)
	}
	if workers < 1 {
		return
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for si := range jobs {
				s := &ss[si]
				for mi := range s.Messages {
					redacted, counts, clipped := indexedText(s.Messages[mi].Text)
					if clipped {
						mu.Lock()
						countClipped(m, s.Path, s.ID, 1)
						mu.Unlock()
					}
					s.Messages[mi].Text = redacted
					if n := counts.Total(); n > 0 && m != nil {
						mu.Lock()
						m.Redacted += n
						if m.RedactionRules == nil {
							m.RedactionRules = map[string]int{}
						}
						// The store, as above (#2238).
						h := sources.HarnessForKind(harnessForPath(s.Path))
						if h == "" {
							if _, ok := m.Files[sources.OpencodeDB()]; ok {
								h = "opencode"
							}
						}
						for rule, c := range counts {
							m.RedactionRules[h+":"+rule] += c
						}
						if s.Path != "" && m.Files != nil {
							if fs, ok := m.Files[s.Path]; ok {
								fs.Redactions += n
								m.Files[s.Path] = fs
							}
						}
						mu.Unlock()
					}
				}
				s.Messages = dropRepeatedMessages(s.Messages)
			}
		}()
	}
	for si := range ss {
		jobs <- si
	}
	close(jobs)
	wg.Wait()
}

// prefixSampleWindow is how much is read at each end. Large enough that a
// rewrite cannot plausibly reproduce it byte for byte, small enough that the
// cost does not depend on how long the session has been running.
const prefixSampleWindow = 512 << 10

// filePrefixSample fingerprints the first n bytes by reading at most two
// windows of them: the head and the bytes ending at n. n is mixed in, so a
// file that grew and one that was rewritten to the same content at a
// different length do not collide.
func filePrefixSample(path string, n int64) uint64 {
	if n <= 0 {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	h := fnv.New64a()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(n))
	_, _ = h.Write(b[:])
	head := min(n, prefixSampleWindow)
	if _, err := io.Copy(h, io.LimitReader(f, head)); err != nil {
		return 0
	}
	if n > head {
		start := max(head, n-prefixSampleWindow)
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return 0
		}
		if _, err := io.Copy(h, io.LimitReader(f, n-start)); err != nil {
			return 0
		}
	}
	return h.Sum64()
}

// filePrefixHash fingerprints the first n bytes of a file. Only used to decide
// whether an append is safe, so a fast non-cryptographic hash is the right
// tool: a collision costs one unnecessary full reparse, never a wrong index.
//
// Kept for manifests written before filePrefixSample: those store a hash of
// every byte, and it takes one more read to verify them before the walk
// records a sample instead.
func filePrefixHash(path string, n int64) uint64 {
	if n <= 0 {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	h := fnv.New64a()
	if _, err := io.Copy(h, io.LimitReader(f, n)); err != nil {
		return 0
	}
	return h.Sum64()
}

// countClipped records messages stored short of the transcript, against the
// file that holds them. The caller holds the lock where one is needed;
// redactForIngest runs single-threaded.
func countClipped(m *Manifest, sourcePath, sessionID string, n int) {
	if m == nil || n == 0 {
		return
	}
	p := sourcePath
	if harnessForPath(p) == "" {
		// opencode sessions carry their project dir as Path; the store on
		// record is the database file.
		if db := sources.OpencodeDB(); db != "" {
			if _, ok := m.Files[db]; ok {
				p = db
			}
		}
	}
	if harnessForPath(p) == "" {
		return
	}
	if m.IngestFiles == nil {
		m.IngestFiles = map[string]FileIngest{}
	}
	e := m.IngestFiles[p]
	e.Clipped += n
	if sessionID != "" {
		if e.ClippedSessions == nil {
			e.ClippedSessions = map[string]int{}
		}
		e.ClippedSessions[sessionID] += n
	}
	m.IngestFiles[p] = e
}
