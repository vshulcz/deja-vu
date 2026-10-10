package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
	LastBuild = BuildSummary{Initial: initial, Sessions: sessions, Messages: messages,
		Dropped: parsed - messages, Harnesses: len(order), PerHarness: per}
}

// IngestHealth returns the per-harness ingestion health persisted by the
// last indexing passes, or nil when the index has none recorded.
func IngestHealth(dir string) map[string]HarnessIngest {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return nil
	}
	return m.IngestHealth
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
	malformed, failed := sources.DiagSnapshot()
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
	for p, msg := range failed {
		e := m.IngestFiles[p]
		e.Error = msg
		m.IngestFiles[p] = e
	}
	// A file this pass read is a file that opens. Only the error goes: the bad
	// lines it counted are still in the part already indexed.
	for p := range passRead {
		if failed[p] != "" {
			continue
		}
		if e, ok := m.IngestFiles[p]; ok && e.Error != "" {
			e.Error = ""
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
	if dir == "" {
		dir = DefaultDir()
	}
	prior, err := readManifest(dir)
	if err != nil {
		return false, 0
	}
	want := currentFilesReusing(harness, priorFiles(prior, err))
	scope := ""
	if harness != "" {
		scope = harness
	}
	if !manifestFresh(prior, want, scope) || !recordsIntact(dir, prior) {
		return false, len(prior.Sessions)
	}
	return true, len(prior.Sessions)
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
func SweepStaleTmp(dir string) {
	if dir == "" {
		dir = DefaultDir()
	}
	if _, err := os.Stat(dir + ".tmp"); err != nil {
		return
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return
	}
	defer unlock()
	sweepStaleTmp(dir)
}

func Ensure(dir string, harness string, force bool, progress io.Writer) error {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return err
	}
	defer unlock()
	sweepStaleTmp(dir)
	// The manifest is read before the walk so unchanged files can carry
	// their derived state forward instead of being re-read.
	prior, priorErr := readManifest(dir)
	want := currentFilesReusing(harness, priorFiles(prior, priorErr))
	scope := ""
	if harness != "" {
		// A harness-scoped index is partial by construction; the manifest
		// records that so freshness checks and search callers know. The
		// parameter was silently ignored before — every "scoped" build
		// ingested the whole machine.
		scope = harness
	}
	m, err := prior, priorErr
	if !force && err == nil && notesZoneDrifted(m) {
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

func EnsureForSearch(dir string, o query.Options, force bool, progress io.Writer) error {
	if dir == "" {
		dir = DefaultDir()
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
	prior, priorErr := readManifest(dir)
	want := currentFilesReusing("", priorFiles(prior, priorErr))
	scope := ""
	m, err := prior, priorErr
	if !force && err == nil && notesZoneDrifted(m) {
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
	if dir == "" {
		dir = DefaultDir()
	}
	// The wait before an answer is made of three things — taking the lock,
	// walking the stores to see what changed, and ingesting whatever did — and
	// from the outside they are one number. A reader who sees seconds here can
	// say which one it was rather than guessing (#3021).
	mark := searchTrace()
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return false, err
	}
	if !ok {
		// A rebuild is already running; serve the current snapshot.
		return true, nil
	}
	defer unlock()
	mark("lock")
	// Read first, walk second: this is the path every search takes, and
	// re-deriving state for unchanged transcripts was costing 700 ms of the
	// second it takes to answer a query.
	m, err := readManifest(dir)
	mark("manifest")
	want := currentFilesReusing("", priorFiles(m, err))
	mark("walk stores")
	if err != nil || m.Scope != "" || !recordsIntact(dir, m) || mustRebuildBeforeAnswering(m, version) {
		// No usable index yet (or a rebuild-grade problem): the caller cannot
		// serve anything sensible stale, so build synchronously.
		return false, updateIndex(dir, o.Harness, "", want, false, progress)
	}
	if newerIndex(m) {
		// Left alone, and current as far as this build can tell: calling it
		// stale handed it to a warmup that would not touch it either.
		return false, nil
	}
	if m.Version != version {
		// A content-version bump that this build can still read: the store
		// answers under the old rules while the re-read runs behind it, the
		// way staleness is already handled. Blocking on it is what made the
		// first question after an upgrade wait for the whole pass — 13m54s on
		// a 520 MB store, and an agent that waited a minute gave up on the
		// tool (#3552).
		return true, nil
	}
	if notesZoneDrifted(m) {
		// Regrouping the day buckets is a full rebuild; hand it to the
		// detached warmup and say the current answer is stale.
		return true, nil
	}
	if manifestFresh(m, want, "") {
		return false, nil
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
		if _, ok := want[p]; !ok {
			removedAny = true
		}
	}
	if !removedAny && canAppendIncremental(changed, m.Files) {
		mark("decide append")
		// An append is cheap until it isn't. A live session that has been
		// writing all day — a Grok `updates.jsonl` in the tens of megabytes —
		// is appendable, so every search sat through its tail before
		// answering, including questions about last month's history in another
		// harness (#3021). Past the cap it is handed to the detached warmup
		// like rewrite-grade work: the answer comes from the snapshot with the
		// "as it was" line, and the tail lands before the next question.
		if tail := appendTailBytes(changed, m.Files); tail > inlineAppendMax {
			return true, nil
		}
		err := updateIndex(dir, o.Harness, "", want, false, progress)
		mark("append")
		return false, err
	}
	// Caller detaches the rebuild (it owns the executable path).
	mark("hand to warmup")
	return true, nil
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
	// This build's counts, not the process's: see writeSessionsWithSync (#1850).
	beginPass()
	emptied.Store(0)
	collisions.Store(0)
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
	for p, st := range orphans.files {
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
	m := Manifest{Version: version, Format: onDiskFormat, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), SourcesReadAt: time.Now(), Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope,
		ExportWatermarks: imported.watermarks, ExportBoundary: imported.boundary, ImportedRecords: imported.dedupe,
		Compactions:        compactions,
		ExcludeFingerprint: sources.ExclusionFingerprint(),
		ToolFingerprint:    mergedToolFingerprint(priorToolFingerprint(dir))}
	recPath := filepath.Join(tmp, "records.bin")
	rf, err := os.OpenFile(recPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
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
	err = sp.run(func(push func(tokenJob)) error {
		for _, s := range ss {
			key := s.Harness + ":" + s.ID
			ord := uint32(0)
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
			owns, collided := claimSession(m.Sessions[key], s)
			if !holdsText(s) {
				emptied.Add(1)
			}
			if collided {
				collisions.Add(1)
			}
			if owns {
				m.Sessions[key] = ownerRow(m.Sessions[key], s, ord, collided)
			} else {
				widenSpan(m.Sessions, key, s)
			}
			if collided {
				markShared(m.Sessions, key)
			}
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
	// The five sidecars walk every session again — what co-occurs, which
	// command followed which error, what was run and what failed. On a real
	// store that is eight seconds of a twenty-second build, and it used to run
	// under the previous phase's last percentage, so the bar sat still through
	// it (#3372).
	reportPhase("mining fixes and commands", 5)
	// Five passes over the same sessions, each writing its own file and reading
	// nothing the others write, so they run together rather than one after the
	// other. Profiled on a real store, they were 9.4 s (fixes) and 6.0 s
	// (co-occurrence) of a 51 s build, with the whole machine idle beside them.
	var sidecars sync.WaitGroup
	for _, build := range []func(){
		func() { buildCooccur(tmp, ss) },
		func() { buildFixes(tmp, ss, func(s model.Session) string { return s.Harness + ":" + s.ID }) },
		func() { buildCommands(tmp, ss) },
		func() { buildCommandFails(tmp, ss) },
		func() { buildSessionFacts(tmp, ss) },
	} {
		sidecars.Add(1)
		go func() {
			defer sidecars.Done()
			build()
			reportAdvance(1)
		}()
	}
	sidecars.Wait()
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
			s = &cp
			by[r.Key] = s
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
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
	if pairs := ReadFixes(dir); len(pairs) > 0 {
		dirty := false
		for i, p := range pairs {
			if keys[p.Key] {
				pairs[i].Project, dirty = sessions[p.Key].Project, true
			}
		}
		if dirty {
			_ = writeGobAtomic(fixesPath(dir), pairs)
		}
	}
	buildCommandsFromIndex(dir)
	buildCommandFailsFromIndex(dir, nil, nil)
	buildSessionFactsFromIndex(dir)
}

// orphanState is what a full rebuild has to carry: sessions whose transcript
// the client has deleted, and the file rows that keep them in the manifest.
type orphanState struct {
	sessions []model.Session
	files    map[string]FileState
	// unreadable counts the ones whose records this build cannot decode. A
	// record-layout bump — version 38 moved the role out of the compressed
	// body — leaves an older store's bytes unreadable here, and a transcript
	// that is gone from disk has nowhere else to come from. Measured against
	// released binaries: a store from 0.19.5 (content version 35) upgrades
	// with its deleted-transcript sessions unrecoverable, and saying so is the
	// difference between a loss and a silent one.
	unreadable int
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
	out := orphanState{files: map[string]FileState{}}
	m, err := readManifest(dir)
	if err != nil {
		return out
	}
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
	_ = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		if !want[r.SourcePath] {
			return
		}
		seen[r.SourcePath] = true
		// Only a path that still has records: `deja forget` drops the records
		// and leaves the row, and carrying that row put a transcript deja no
		// longer holds back into doctor's "still searchable" count.
		out.files[r.SourcePath] = m.Files[r.SourcePath]
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
	for _, key := range sortedKeys(by) {
		out.sessions = append(out.sessions, *by[key])
	}
	for p := range want {
		if !seen[p] {
			out.unreadable++
		}
	}
	return out
}

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
		if !msg.Time.IsZero() {
			s.LifecycleAt = msg.Time.Format("2006-01-02")
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
	reg := sources.Registry()
	results := make([]loaded, len(reg))
	var wg sync.WaitGroup
	skipStore := sources.ExcludedHarnesses()
	for i, hr := range reg {
		if h != "" && h != hr.Name {
			continue
		}
		// A store the reader has asked deja not to read is not walked at all.
		// Without this the only way to stop `needs-sqlite3` advice for a
		// harness they do not use was to install the package (#3499).
		if skipStore[hr.Name] {
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
			fmt.Fprintln(progress, withReadTime(
				harnessNarration(r.name, r.ss, sources.SkipReason(r.name), unreadable[r.name], refused[r.name]), took))
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

// ReportCollisions returns how many transcripts shared an id with another since
// the last build, and clears the counter. Silence was the worst part of #698:
// the indexer counted every session on disk while the manifest held fewer, and
// nothing connected the two numbers.
func ReportCollisions() int {
	return int(collisions.Swap(0))
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
func dropEmptySessions(m *Manifest, wrote map[string]bool) {
	for key := range m.Sessions {
		if !wrote[key] {
			delete(m.Sessions, key)
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
	err := writeSessions(tmp, dir, newest, nil, "")
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
	// The counters belong to this build. Draining them only on read made
	// "since the last build" true only when the last read was the last build:
	// a second build in one process reported its own empty transcripts plus
	// whatever an earlier one left behind (#1850), and the collision counter
	// beside it did the same. One process is one build for the CLI, which is
	// why it showed in the test binary first.
	emptied.Store(0)
	collisions.Store(0)
	merged.Store(0)
	initialBuild := !HasManifest(dir)
	writtenMessages := 0
	lastIngestFiles = len(files)
	parsedThisPass(files)
	m := Manifest{Version: version, Format: onDiskFormat, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), SourcesReadAt: time.Now(), Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope,
		ExportWatermarks: imp.watermarks, ExportBoundary: imp.boundary, ImportedRecords: imp.dedupe,
		Compactions:        imp.compactions,
		ExcludeFingerprint: sources.ExclusionFingerprint(),
		ToolFingerprint:    mergedToolFingerprint(priorToolFingerprint(dir))}
	recPath := filepath.Join(tmp, "records.bin")
	rf, err := os.OpenFile(recPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
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
	err = sp.run(func(push func(tokenJob)) error {
		for _, s := range ss {
			key := s.Harness + ":" + s.ID
			ord := uint32(0)
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
			owns, collided := claimSession(m.Sessions[key], s)
			if !holdsText(s) {
				emptied.Add(1)
			}
			if collided {
				collisions.Add(1)
			}
			if owns {
				m.Sessions[key] = ownerRow(m.Sessions[key], s, ord, collided)
			} else {
				widenSpan(m.Sessions, key, s)
			}
			if collided {
				markShared(m.Sessions, key)
			}
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
	reportPhase("mining fixes and commands", 5)
	buildCooccur(tmp, ss)
	reportAdvance(1)
	buildFixes(tmp, ss, func(s model.Session) string { return s.Harness + ":" + s.ID })
	reportAdvance(1)
	buildCommands(tmp, ss)
	reportAdvance(1)
	buildCommandFails(tmp, ss)
	reportAdvance(1)
	buildSessionFacts(tmp, ss)
	reportAdvance(1)
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
func dateTokens(when time.Time) []string {
	if when.IsZero() {
		return nil
	}
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

// eachIndexKey calls fn once per distinct token a message earns. Both the
// in-memory incremental path and the spilling full build go through it, so
// neither can drift from the other on what a message indexes to.
// eachIndexKey calls fn once per distinct key a record contributes. The keys
// are streamed rather than collected: a CJK message produces a bigram per rune
// pair, and building the slice only to walk it once cost a third of the keying
// time on a Han corpus (#492).
func eachIndexKey(text string, when time.Time, fn func(tok string)) {
	emit := dedupe(fn)
	textKeys(text, emit)
	for _, tok := range dateTokens(when) {
		emit(tok)
	}
}

// textKeys emits every key the text contributes, repeats included; the callers
// above put one dedupe in front of it rather than one per source.
func textKeys(text string, emit func(tok string)) {
	for _, tok := range tokens(text) {
		emit("t" + tok)
	}
	for _, part := range identifierParts(text) {
		emit("t" + part)
	}
	cjkIndexKeys(text, emit)
}

// dedupe wraps fn so it sees each key once. The three key sources overlap —
// a word is a token and can be an identifier part as well — and a posting list
// must not hold the same offset twice.
func dedupe(fn func(tok string)) func(tok string) {
	seen := make(map[string]bool, 64)
	return func(tok string) {
		if seen[tok] {
			return
		}
		seen[tok] = true
		fn(tok)
	}
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
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	k := key + "\x00" + role + "\x00" + ts.UTC().Format(time.RFC3339Nano) + "\x00" + fmt.Sprintf("%x", h.Sum64())
	if m[k] {
		return true
	}
	m[k] = true
	return false
}

func metaForSession(s model.Session) SessionMeta {
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
	return SessionMeta{ID: s.ID, Harness: s.Harness, Project: s.Project, Path: s.Path, Title: title, AgentTitle: agentTitle, Started: s.Started, Updated: s.Updated, Touched: touched, TouchHits: touchHits, Counted: len(s.Messages), LastMsg: last, Asked: askedHashes(s.Messages), Hit: frictionHashes(s.Messages), GaveUp: gaveUp(s.Messages), Words: sessionWords(s.Messages), NoText: !holdsText(s), Settled: sessionSettled(s), RanCommand: ranCommand(s.Messages),
		Kind: s.Kind, Parent: s.Parent, Agent: s.Agent, Opening: SessionOpening(s),
		OrigID: s.OrigID, RemoteID: s.RemoteID, From: s.From, Lifecycle: s.Lifecycle, LifecycleNote: s.LifecycleNote, LifecycleAt: s.LifecycleAt}
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
// collisions counts the transcripts that shared an id with another during the
// current build. The two full-build paths index sessions in parallel, so the
// counter is atomic.
var collisions atomic.Int64

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
// collided says whether s collided with it. The span of the files that share
// the id stays in the row, so reading the owner alone does not drop it
// (#4574): prev's own span when prev was another file sharing the id, a
// collision or a stub with nothing to index, and what prev kept when it was
// the same file. A row s supersedes, a ZCode snapshot restored into the
// database, gives it nothing.
func ownerRow(prev SessionMeta, s model.Session, ord uint32, collided bool) SessionMeta {
	meta := metaWithOrd(metaForSession(s), ord)
	switch {
	case prev.Path == "" || prev.Path == s.Path:
		meta.SharedStarted, meta.SharedUpdated = prev.SharedStarted, prev.SharedUpdated
	case collided || prev.NoText:
		meta.SharedStarted, meta.SharedUpdated = widerSpan(prev.SharedStarted, prev.SharedUpdated, prev.Started, prev.Updated)
	}
	meta.Started, meta.Updated = widerSpan(meta.Started, meta.Updated, meta.SharedStarted, meta.SharedUpdated)
	return meta
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
	// entry (#4180).
	if s.Harness == "codex" {
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

// isCodexHistory reports whether a path is Codex's prompt log rather than a
// rollout. Named, for the reason isGooseStore gives.
func isCodexHistory(path string) bool {
	return strings.EqualFold(filepath.Base(path), "history.jsonl")
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
	for h, db := range opencodeSchemaDBs {
		if p == db() {
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
func copyIngestFiles(old map[string]FileIngest, reread map[string]FileState) map[string]FileIngest {
	out := make(map[string]FileIngest, len(old))
	for p, e := range old {
		if _, ok := reread[p]; ok {
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
	sources.DiagSnapshot()
	passParsed = nil
	passRead = nil
	passWholeStores = nil
	passFromWatermark = nil
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
	defer readTo(files)()
	// Cleared here rather than beside the other two: this build counts what
	// went away further down, before the incremental paths reset theirs, so a
	// reset down there would zero the number this build is about to report
	// (#1861).
	evicted.Store(0)
	beginPass()
	old, err := readManifest(dir)
	if err == nil && !recordsIntact(dir, old) {
		force = true // records.bin lost its tail to a crash; only a rebuild is safe
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
	if err == nil && !force {
		if pairs := detectRenamedFiles(old.Files, files); len(pairs) > 0 {
			reprojected := applyRenamedFiles(&old, pairs)
			// Failing to record it costs the duplicate this exists to avoid,
			// not correctness: the pass below then treats the file as new.
			werr := writeManifestMeta(dir, old)
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
		}
		if !deletedFromLiveStore(p) {
			continue
		}
		if d := goneSessionDir(p); d != "" && arrivedDirs[filepath.Base(d)] {
			continue
		}
		// Kept for the sessions it holds, as a rebuild keeps it for its
		// records: a file whose sessions were all forgotten holds nothing.
		if held == nil {
			held = map[string]bool{}
			for _, meta := range old.Sessions {
				held[meta.Path] = true
			}
		}
		if !held[p] {
			continue
		}
		if of, ok := old.Files[p]; ok {
			if !of.Kept {
				newlyKept[p] = true
			}
			of.Kept = true
			files[p] = of
			delete(removed, p)
			kept[p] = true
		}
	}
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
	if len(changed) == 0 && len(removed) == 0 {
		sayKept()
		lastIngestFiles = 0
		// Nothing to read, but the kept mark has to land, or the next run
		// takes the same file for a fresh deletion.
		if len(newlyKept) > 0 {
			for p := range newlyKept {
				old.Files[p] = files[p]
			}
			return writeManifestOnly(dir, old)
		}
		return nil
	}
	if len(removed) == 0 && !rewritten && canAppendIncremental(changed, old.Files) {
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
		sayKept()
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
	rereadSharedCopies(removed, changed, files, old.Sessions)
	var replacements []model.Session
	// This pass's counts, like every other build path (#1850).
	emptied.Store(0)
	collisions.Store(0)
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
	skip := func(p string, err error) {
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
		ss = sources.FilterSessions(filterTombstoned(ss))
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
	for p, f := range changed {
		if harnessForPath(p) == "opencode-diff" {
			diffs = append(diffs, p)
			continue
		}
		ss, err := parseChangedFile(harness, p, old.Files[p])
		if err != nil {
			skip(p, err)
			continue
		}
		if harnessForPath(p) == "opencode" {
			for _, s := range ss {
				fromDB[s.ID] = true
			}
		}
		take(p, ss)
		files[p] = f
	}
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
	replaceKeys := map[string]bool{}
	for _, s := range replacements {
		replaceKeys[s.Harness+":"+s.ID] = true
	}
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
	arrivals := map[string][]int{}
	for i, r := range replacements {
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
			if (ownDir || filepath.Dir(r.Path) == filepath.Dir(meta.Path)) && coversKept(r, meta) {
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
		line := fmt.Sprintf("deja: %s %d changed transcript%s", replacementPassMarker, len(changed), pluralS(len(changed)))
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
	rf, err := os.OpenFile(filepath.Join(tmp, "records.bin"), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	tbl := newRecordTables()
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
	m := Manifest{Version: version, Format: onDiskFormat, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), SourcesReadAt: time.Now(),
		Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope,
		ExportWatermarks: old.ExportWatermarks, ExportBoundary: old.ExportBoundary, ImportedRecords: old.ImportedRecords,
		Compactions: cloneCompactions(old.Compactions),
		// Kept from the old index: this build reuses records written under
		// those patterns, so claiming today's set would be a lie the reader
		// cannot check.
		ExcludeFingerprint: old.ExcludeFingerprint,
		ToolFingerprint:    mergedToolFingerprint(priorToolFingerprint(dir)),
		// What deja could not read is about the files, not about the pass that
		// happened to notice: the lines are still there. Dropping the map meant
		// doctor forgot a store's unreadable file because an unrelated
		// transcript changed (#2015). A store this pass re-read starts over,
		// because a file rewritten clean has to be able to clear its count.
		IngestFiles: copyIngestFiles(old.IngestFiles, reread)}
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
		// Carried records were redacted when first written; re-running the
		// regex battery over the whole corpus made every incremental update
		// cost O(index), which is what a live cline/cursor store hits on
		// each change.
		off, err := rw.write(r)
		if err != nil {
			return err
		}
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
	var recErr error
	// The sessions that lost a record here, which the command failure walk
	// has to read again along with the ones re-read (#4288).
	dropped := map[string]bool{}
	if err := eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(old), func(r Record) {
		if recErr != nil {
			return
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
		if removed[r.SourcePath] || (changed[r.SourcePath].Path != "" && !fromStore && !leftItsFile(r, replaceKeys, old.Sessions[r.Key], aiderHeld) && !keptThroughCompaction(r, compacted)) || (fromStore && readWholeThisPass(r) && storeKeys[r.Key]) {
			dropped[r.Key] = true
			return
		}
		if r.SourcePath == "" {
			dropped[r.Key] = true // addRec does not carry it
		}
		recErr = addRec(r)
	}); err != nil {
		_ = rw.Close()
		return err
	}
	if recErr != nil {
		_ = rw.Close()
		return recErr
	}
	seenMsgs := msgSeen{}
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
	for _, s := range replacements {
		key := s.Harness + ":" + s.ID
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
			// The row may exist only in the manifest being replaced.
			held = old.Sessions[key]
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
			collisions.Add(1)
		}
		if owns {
			m.Sessions[key] = ownerRow(held, s, ord, collided)
		} else {
			if _, present := m.Sessions[key]; !present {
				m.Sessions[key] = held
			}
			widenSpan(m.Sessions, key, s)
		}
		if collided {
			markShared(m.Sessions, key)
		}
		for _, msg := range s.Messages {
			if seenMsgs.dup(key, msg.Role, msg.Time, msg.Text) {
				continue
			}
			// Already redacted (and length-capped) by preRedactSessions above.
			text := msg.Text
			if err := addRec(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time}); err != nil {
				_ = rw.Close()
				return err
			}
		}
	}
	if err := rw.Close(); err != nil {
		return err
	}
	if err := writeBucketsConcurrent(filepath.Join(tmp, "buckets"), buckets); err != nil {
		return err
	}
	setDatabaseStoreWatermarks(m.Files, m.Sessions)
	m.RecordStrings = tbl.strs
	if err := writeManifest(tmp, m); err != nil {
		return err
	}
	carrySidecars(dir, tmp)
	// After carrying, not instead of it: the fixes merge writes only when it
	// has something to say, and the carried file is what a quiet update leaves.
	// The command table is recomputed whole, and an empty one removes the
	// carried file, as a full build would not write it (#4441).
	mergeFixes(dir, tmp, replacements, replaceKeys)
	buildCommandsFromIndex(tmp)
	for key := range replaceKeys {
		dropped[key] = true
	}
	buildCommandFailsFromIndex(tmp, carriedCommandFailState(dir, old, tmp, m.Generation, dropped), dropped)
	buildSessionFactsFromIndex(tmp)
	return swapIndexDir(dir, tmp)
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
	for _, name := range []string{fixesFile, commandsFile, cooccurFile} {
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
	collisions.Store(0)
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
	loadBucket := func(tok string) (map[string][]posting, error) {
		b := bucket(tok)
		if data, ok := buckets[b]; ok {
			return data, nil
		}
		p := filepath.Join(dir, "buckets", b+".bin")
		data, err := readBucket(p)
		if os.IsNotExist(err) {
			data = map[string][]posting{}
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		buckets[b] = data
		return data, nil
	}
	m := old
	m.Version = version
	m.Format = onDiskFormat
	m.Scope = scope
	m.BuiltAt = time.Now()
	m.SourcesReadAt = m.BuiltAt
	m.Files = files
	m.Redacted = 0
	carryRedactions(&m, old, map[string]bool{})
	if m.Sessions == nil {
		m.Sessions = map[string]SessionMeta{}
	}
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
	for _, p := range sortedKeys(changed) {
		of, known := old.Files[p]
		ss, err := parseAppendedFile(harness, p, of, !known)
		if err != nil {
			if known {
				m.Files[p] = of // retry this file on the next pass
			} else {
				delete(m.Files, p)
			}
			continue
		}
		ss = sources.FilterSessions(filterTombstoned(ss))
		// Scrub before anything is derived from the text, as the other two paths
		// do. Redacting per message inside the write loop below left the derived
		// fields — the friction signatures especially — hashed from the raw
		// text, so the same session got different signatures depending on which
		// path had touched it last.
		preRedactSessions(&m, ss)
		// Kept for the sidecars below, after redaction so a pair cannot carry a
		// credential the records do not.
		appended = append(appended, ss...)
		filesTouched++
		for _, s := range ss {
			key := s.Harness + ":" + s.ID
			meta := m.Sessions[key]
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
			// A rename reaches this path as a new file holding a session deja
			// already has, and the path it has is the one that went away. The
			// replacement path asks the removed set; here that set is empty by
			// definition — the file that vanished was kept back as still
			// searchable — so ask the filesystem instead. A recorded path that
			// is not there cannot own the row, and without this the row kept
			// the dead path and was marked as sharing its id with it (#1086).
			// Not for an arrival with nothing to index: that is Gemini's resume
			// stub beside a deleted transcript, not the transcript moving (#4213).
			if meta.Path != "" && s.Path != "" && meta.Path != s.Path && holdsText(s) {
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
			if owns && meta.NoText && meta.Path != s.Path && !known {
				prev := meta
				meta = ownerRow(prev, s, prev.Ord, collided)
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
				collisions.Add(1)
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
			// first question that arrives later, as a rebuild names it.
			outranked := false
			if s.Title == "" && !titlePlaceholder(meta.Title) {
				t, agent := sessionTitleFrom(s)
				outranked = titleRankOfRow(t, agent) > titleRankOfRow(meta.Title, meta.AgentTitle)
			}
			if titlePlaceholder(meta.Title) || outranked {
				// The incremental fallback redacted nothing at all before; keep
				// sessionTitleFrom's correct fromAgent bit and redact before the
				// cut, as the full rebuild does.
				meta.Title, meta.AgentTitle = sessionTitleFrom(s)
				meta.Title, _ = redact.Text(meta.Title)
				meta.Title = truncateTitle(meta.Title, 60)
			} else if s.Title != "" {
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
	if err := writeBucketsConcurrent(filepath.Join(dir, "buckets"), buckets); err != nil {
		return filesTouched, messages, 0, err
	}
	setDatabaseStoreWatermarks(m.Files, m.Sessions)
	m.RecordStrings = tbl.strs
	// Read before writeManifest: the fold in there drains the counters, and the
	// caller prints its line after this returns (#2007).
	unreadable = totalMalformed()
	if err := writeManifest(dir, m); err != nil {
		return filesTouched, messages, unreadable, err
	}
	// The sidecars, from what this pass read and from the records already on
	// file. The replacement path does the same three things after it carries
	// them over; this path used to do none of them, which was survivable while
	// it only ever saw a tail and is not once a new conversation comes through
	// here (#3500). No `replaced` set: nothing was re-read, so every pair
	// already on file is still earned — the new ones merge in beside them, and
	// a candidate on file is what a second sighting needs to be promoted.
	mergeFixes(dir, dir, appended, map[string]bool{})
	// The two command tables are mined from every record the index holds, which
	// is a scan of records.bin — 2.9s of an 8.6s pass on a 191 MB store. Nothing
	// this pass added can change them unless it added a command or the output of
	// one, and most appends are speech, so ask first.
	if carriesWork(appended) {
		buildCommandsFromIndex(dir)
		// From the state the last update left: only the records this pass
		// appended are read (#4288).
		buildCommandFailsFromIndex(dir, readCommandFailState(dir), nil)
		buildSessionFactsFromIndex(dir)
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
			return w, true
		}
	}
	return model.Session{}, false
}

func sameFile(a, b FileState) bool {
	return a.Path == b.Path && a.Size == b.Size && a.MTime == b.MTime &&
		a.MetadataSize == b.MetadataSize && a.MetadataMTime == b.MetadataMTime &&
		a.CWDSize == b.CWDSize && a.CWDMTime == b.CWDMTime
}

// kindForPath returns the registry FileKind whose Match accepts p.
func kindForPath(p string) (sources.FileKind, bool) {
	for _, h := range sources.Registry() {
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
	return k.Parse(p, old.LastUpdated)
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
		return k.Parse(p, 0)
	}
	return k.ParseFrom(p, resumeOffset(old), old.LastUpdated)
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

func currentFilesWith(h string, old map[string]FileState) map[string]FileState {
	// Walking the stores is the first thing a build does and, on a network
	// volume, the slowest: it published nothing, so every "memory is on its
	// way" surface reported an ordinary quiet day until the walk finished
	// (#1021).
	reportPhase("finding transcripts", 0)
	paths := map[string]bool{}
	for _, hr := range sources.Registry() {
		if h != "" && h != hr.Name {
			continue
		}
		for _, p := range hr.Files() {
			paths[p] = true
		}
	}
	out := map[string]FileState{}
	for p := range paths {
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink == 0 && !fi.IsDir() {
			out[p] = walkFileState(p, fi, old)
		}
	}
	injectHermesPG(out, old)
	return out
}

// walkFileState is what the walk records for a transcript file.
func walkFileState(p string, fi os.FileInfo, old map[string]FileState) FileState {
	fs := FileState{Path: p, Size: fi.Size(), MTime: fi.ModTime().UnixNano()}
	if strings.HasSuffix(p, ".jsonl") {
		// Deriving these means reading the file: the tail for the last
		// complete line, the head for the prefix hash. When size and
		// mtime are unchanged the bytes are too, and on a large store
		// this is the difference between a stat and 650 ms of reading.
		if of, ok := old[p]; ok && of.Size == fs.Size && of.MTime == fs.MTime {
			fs.SafeSize, fs.PrefixHash = of.SafeSize, of.PrefixHash
			fs.PrefixSample = of.PrefixSample
		} else {
			fs.SafeSize = lastCompleteLineOffset(p, fi.Size())
			fs.PrefixSample = filePrefixSample(p, fs.SafeSize)
		}
	}
	if k, ok := kindForPath(p); ok && k.Sidecar != nil {
		fs.MetadataSize, fs.MetadataMTime = k.Sidecar(p)
	}
	if harnessForPath(p) == "grok" {
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
	if dsn == "" {
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
