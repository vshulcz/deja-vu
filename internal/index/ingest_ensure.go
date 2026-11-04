package index

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vshulcz/deja-vu/internal/query"
)

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
	if err == nil && m.Scope == "" && recordsIntact(dir, m) && !mustRebuildBeforeAnswering(m, version) {
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
	}
	want := currentFilesReusing("", priorFiles(m, err))
	mark("walk stores")
	if err != nil || m.Scope != "" || !recordsIntact(dir, m) || mustRebuildBeforeAnswering(m, version) {
		// No usable index yet (or a rebuild-grade problem): the caller cannot
		// serve anything sensible stale, so build synchronously.
		return false, 0, 0, updateIndex(dir, o.Harness, "", want, false, progress)
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
