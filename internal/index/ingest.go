package index

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func summarizeBuild(initial bool, sessions int, messages int, ss []model.Session) {
	counts := map[string]*HarnessCount{}
	order := []string{}
	for _, s := range ss {
		c := counts[s.Harness]
		if c == nil {
			c = &HarnessCount{Name: s.Harness}
			counts[s.Harness] = c
			order = append(order, s.Harness)
		}
		c.Sessions++
		c.Messages += len(s.Messages)
	}
	sort.Strings(order)
	per := make([]HarnessCount, 0, len(order))
	for _, name := range order {
		per = append(per, *counts[name])
	}
	LastBuild = BuildSummary{Initial: initial, Sessions: sessions, Messages: messages, Harnesses: len(order), PerHarness: per}
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

// mergeIngestDiag folds the sources side-channel counters into the manifest,
// keyed by harness. Harnesses untouched this pass keep their previous entry.
func mergeIngestDiag(m *Manifest) {
	malformed, failed := sources.DiagSnapshot()
	if len(malformed) == 0 && len(failed) == 0 {
		return
	}
	if m.IngestHealth == nil {
		m.IngestHealth = map[string]HarnessIngest{}
	}
	touched := map[string]bool{}
	for p := range malformed {
		touched[harnessForPath(p)] = true
	}
	for p := range failed {
		touched[harnessForPath(p)] = true
	}
	for h := range touched {
		if h == "" {
			continue
		}
		// The clip count for this pass is recorded during redaction, which
		// runs before this fold; resetting the whole entry threw it away.
		m.IngestHealth[h] = HarnessIngest{ClippedMessages: m.IngestHealth[h].ClippedMessages}
	}
	for p, n := range malformed {
		h := harnessForPath(p)
		if h == "" {
			continue
		}
		e := m.IngestHealth[h]
		e.MalformedLines += n
		m.IngestHealth[h] = e
	}
	for p, msg := range failed {
		h := harnessForPath(p)
		if h == "" {
			continue
		}
		e := m.IngestHealth[h]
		e.FailedFiles++
		e.LastError = msg
		m.IngestHealth[h] = e
	}
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
		if errors.Is(err, fs.ErrPermission) && HasManifest(dir) {
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
		return errors.Is(err, fs.ErrPermission)
	}
	_ = f.Close()
	return false
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
		return rebuildForSearch(dir, o, scope, want, progress)
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
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return false, err
	}
	if !ok {
		// A rebuild is already running; serve the current snapshot.
		return true, nil
	}
	defer unlock()
	// Read first, walk second: this is the path every search takes, and
	// re-deriving state for unchanged transcripts was costing 700 ms of the
	// second it takes to answer a query.
	m, err := readManifest(dir)
	want := currentFilesReusing("", priorFiles(m, err))
	if err != nil || m.Version != version || m.Scope != "" || !recordsIntact(dir, m) {
		// No usable index yet (or a rebuild-grade problem): the caller cannot
		// serve anything sensible stale, so build synchronously.
		return false, updateIndex(dir, o.Harness, "", want, false, progress)
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
		return false, updateIndex(dir, o.Harness, "", want, false, progress)
	}
	// Caller detaches the rebuild (it owns the executable path).
	return true, nil
}

func rebuild(dir string, harness string, scope string, files map[string]FileState, progress io.Writer) error {
	return rebuildWithTombstones(dir, harness, scope, files, progress, readTombstones())
}

func rebuildWithTombstones(dir string, harness string, scope string, files map[string]FileState, progress io.Writer, dead map[string]bool) error {
	// This build's counts, not the process's: see writeSessionsWithSync (#1850).
	beginPass()
	emptied.Store(0)
	collisions.Store(0)
	// A rebuild evicts nothing, but a number left by an earlier build must not
	// outlive it (#1861).
	evicted.Store(0)
	lastIngestFiles = len(files)
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
	// Imported sessions are filtered too: excluding a project must also drop
	// what a peer already pushed, not only what arrives next.
	ss = append(ss, sources.FilterSessions(imported.sessions)...)
	ss = filterTombstonedSet(ss, dead)
	// A full build passes every session through the exclusion patterns, so
	// this is the one place the current set can be claimed as applied. An
	// incremental build carries the old stamp forward: it keeps records it
	// wrote under the previous patterns, which is why `deja index` has to ask
	// for a rebuild rather than quietly declaring the new list in force (#1307).
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope,
		ExportWatermarks: imported.watermarks, ExportBoundary: imported.boundary, ImportedRecords: imported.dedupe,
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
	reportPhase("indexing messages", len(ss))
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
			reportAdvance(1)
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
			owns, collided := attributeSession(m.Sessions[key], s)
			if collided {
				collisions.Add(1)
			}
			if owns {
				m.Sessions[key] = metaWithOrd(metaForSession(s), ord)
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
				writtenMessages++
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
	buildCooccur(tmp, ss)
	buildFixes(tmp, ss, func(s model.Session) string { return s.Harness + ":" + s.ID })
	buildCommands(tmp, ss)
	reportPhase("writing index", sp.bucketCount())
	if err := sp.writeBuckets(filepath.Join(tmp, "buckets")); err != nil {
		return err
	}
	// Before the swap: whatever is left in tmp ships inside the index.
	sp.cleanup()
	setOpencodeLastUpdated(m.Files, m.Sessions)
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

// progressWeights is how many files each store contributes, set by the caller
// that already walked the filesystem so the bar advances proportionally.
var progressWeights = map[string]int{}

// loadProgress narrates a full rebuild per harness: a cold pass over a large
// corpus takes seconds and used to look hung.
func loadProgress(h string, progress io.Writer) []model.Session {
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
	for i, hr := range reg {
		if h != "" && h != hr.Name {
			continue
		}
		wg.Add(1)
		go func(i int, name string, load func() []model.Session) {
			defer wg.Done()
			ss := safeLoad(name, load, progress)
			results[i] = loaded{name: name, ss: ss}
			// Report as this store lands rather than after every store has,
			// so the bar moves during the parse instead of jumping at the end.
			msgs := 0
			for _, x := range ss {
				msgs += len(x.Messages)
			}
			reportHarness(name, len(ss), msgs)
			reportAdvance(progressWeights[name])
		}(i, hr.Name, hr.Load)
	}
	wg.Wait()
	unreadable := malformedByHarness()
	var ss []model.Session
	for _, r := range results {
		if len(r.ss) == 0 {
			// A harness deja can see but could not read is worth a line: an
			// index run that narrates every store it read and stays silent
			// about the one it skipped makes an empty deja look like an empty
			// history (#794).
			if reason := sources.SkipReason(r.name); reason != "" && progress != nil && !SuppressHarnessNarration {
				fmt.Fprintf(progress, "deja: %s: skipped — %s\n", r.name, reason)
			}
			continue
		}
		ss = append(ss, r.ss...)
		if progress != nil && !SuppressHarnessNarration {
			fmt.Fprintln(progress, harnessNarration(r.name, r.ss, sources.SkipReason(r.name), unreadable[r.name]))
		}
	}
	return ss
}

// harnessNarration is the line an index run prints for one store. A store can
// be half-readable — cursor keeps CLI transcripts as JSONL and its IDE sessions
// in SQLite — and the count alone then reads as the whole story while half of
// it is missing from recall. The skip reason was printed only for a store that
// yielded nothing at all (#1758, the shape of #794).
func harnessNarration(name string, ss []model.Session, skipped string, unreadable int) string {
	msgs := 0
	for _, s := range ss {
		msgs += len(s.Messages)
	}
	// "deja" is the notes pseudo-source; it narrates as "notes".
	label := name
	if label == "deja" {
		label = "notes"
	}
	line := fmt.Sprintf("deja: %s: %d session%s, %d message%s", label, len(ss), pluralS(len(ss)), msgs, pluralS(msgs))
	if unreadable > 0 {
		line += fmt.Sprintf(" — %d line%s skipped, deja could not read %s", unreadable, pluralS(unreadable), pluralThem(unreadable))
	}
	if skipped != "" {
		line += " — part of this store could not be read: " + skipped
	}
	return line
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
		if h := harnessForPath(p); h != "" {
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

// markShared records that a manifest row covers more than one conversation, so
// a later forget can say what it is about to take (#970).
func markShared(sessions map[string]SessionMeta, key string) {
	if meta, ok := sessions[key]; ok {
		meta.Shared = true
		sessions[key] = meta
	}
}

func rebuildForSearch(dir string, o query.Options, scope string, files map[string]FileState, progress io.Writer) error {
	beginPass()
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o700); err != nil {
		return err
	}
	// The same phase reporting rebuild() has. Without it the first run of a
	// search — which is how almost everyone builds their index the first time,
	// rather than by typing `deja index` — showed a spinner reading "starting"
	// and a bar frozen at one notch for the whole build.
	total := 0
	progressWeights = filesPerHarness(files)
	for _, n := range progressWeights {
		total += n
	}
	reportPhase("reading sessions", total)
	ss := sources.FilterSessions(filterTombstoned(loadProgress("", progress)))
	imported := importedSessions(dir)
	ss = append(ss, imported.sessions...)
	ss = filterTombstoned(ss)
	return writeSessionsWithSync(tmp, dir, ss, files, scope, imported)
}

// dropEmptySessions removes manifest rows that ended up with no records.
//
// A session whose every message strips to empty — harness plumbing, a prompt
// the user never sent — still got a row, so `deja last` printed a blank line
// for it, `show` printed a header with nothing under it, and the counters
// disagreed: brief and doctor read the manifest and stats reads the records
// (1159 against 1157 on my store) (#868).
func dropEmptySessions(m *Manifest, wrote map[string]bool) {
	for key := range m.Sessions {
		if !wrote[key] {
			delete(m.Sessions, key)
			emptied.Add(1)
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

func writeSessions(tmp, dir string, ss []model.Session, files map[string]FileState, scope string) error {
	return writeSessionsWithSync(tmp, dir, ss, files, scope, importedState{})
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
	initialBuild := !HasManifest(dir)
	writtenMessages := 0
	lastIngestFiles = len(files)
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope,
		ExportWatermarks: imp.watermarks, ExportBoundary: imp.boundary, ImportedRecords: imp.dedupe,
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
	reportPhase("indexing messages", len(ss))
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
			reportAdvance(1)
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
			owns, collided := attributeSession(m.Sessions[key], s)
			if collided {
				collisions.Add(1)
			}
			if owns {
				m.Sessions[key] = metaWithOrd(metaForSession(s), ord)
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
				writtenMessages++
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
	buildCooccur(tmp, ss)
	buildFixes(tmp, ss, func(s model.Session) string { return s.Harness + ":" + s.ID })
	buildCommands(tmp, ss)
	reportPhase("writing index", sp.bucketCount())
	if err := sp.writeBuckets(filepath.Join(tmp, "buckets")); err != nil {
		return err
	}
	// Before the swap: whatever is left in tmp ships inside the index.
	sp.cleanup()
	setOpencodeLastUpdated(m.Files, m.Sessions)
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
func tokenizedPart(role, text string) string {
	switch role {
	case roleEdit:
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
func eachIndexKey(text string, when time.Time, fn func(tok string)) {
	seen := map[string]bool{}
	for _, tok := range append(indexKeys(text), dateTokens(when)...) {
		if seen[tok] {
			continue
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
		title = boundSourceTitle(s.Harness, title)
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
	return SessionMeta{ID: s.ID, Harness: s.Harness, Project: s.Project, Path: s.Path, Title: title, AgentTitle: agentTitle, Started: s.Started, Updated: s.Updated, Touched: touched, TouchHits: touchHits, Counted: len(s.Messages), LastMsg: last, Asked: askedHashes(s.Messages), Hit: frictionHashes(s.Messages), GaveUp: gaveUp(s.Messages), Words: sessionWords(s.Messages),
		Kind: s.Kind, Parent: s.Parent, Agent: s.Agent,
		OrigID: s.OrigID, From: s.From, Lifecycle: s.Lifecycle, LifecycleNote: s.LifecycleNote, LifecycleAt: s.LifecycleAt}
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

// askedFromRecords is askedHashes for the import path, which holds a session as
// records rather than messages. Imported sessions used to carry no Asked, so
// the brief's asked-twice line — which reads meta.Asked — never saw a repeat
// that crossed a sync boundary, while stats' RepeatQuestions (from records)
// counted it. The two disagreed on the same store.
func askedFromRecords(recs []Record) []uint64 {
	var out []uint64
	seen := map[uint64]bool{}
	for _, r := range recs {
		if r.Role != "user" {
			continue
		}
		v, ok := askedHashOf(r.Text)
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
	return strings.Contains(t, "no visible output")
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

// touchedFromRecords is topTouchedFiles for the import path, which holds a
// session as records rather than messages. Imported sessions used to carry no
// Touched, so `deja blame` — which reads it — could not attribute a peer's
// edits even though `search --role files` surfaced the same records.
func touchedFromRecords(recs []Record) []string {
	count := map[string]int{}
	for _, r := range recs {
		if r.Role != roleFiles {
			continue
		}
		countTouchedPaths(count, r.Text)
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
		OrigID: meta.OrigID, From: meta.From, Lifecycle: meta.Lifecycle, LifecycleNote: meta.LifecycleNote, LifecycleAt: meta.LifecycleAt,
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

// attributeSession decides which of two transcripts sharing an id owns the
// manifest row, and whether they collided at all. Lexicographically smallest
// path wins, so the answer does not depend on which file was read first.
func attributeSession(held SessionMeta, s model.Session) (owns, collided bool) {
	if held.Path == "" || s.Path == "" || held.Path == s.Path {
		return true, false
	}
	return s.Path < held.Path, true
}

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
	return "", false
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
	return len(strings.Fields(t)) <= 2 && len([]rune(t)) <= 12
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
	return t != "" && !strings.HasPrefix(t, "<local-command") && !strings.HasPrefix(t, "<command-") &&
		!strings.HasPrefix(t, "<task-notification") && !strings.HasPrefix(t, "<teammate-message") &&
		!strings.HasPrefix(t, "Caveat:")
}

// boundSourceTitle collapses and bounds a title the source authored, the way
// a derived one has always been.
//
// Notes are exempt. A promoted note's title ends in its state — "… [rejected]"
// — and the state is what every one-line surface reads it for, so cutting the
// tail would drop exactly the part that matters and, worse, make a state
// change invisible to the comparison that decides whether to rewrite the row
// (#R11). Note titles are deja's own text, so the injection this bound exists
// to stop cannot arrive through them.
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
		countClipped(m, sourcePath, 1)
	}
	n := counts.Total()
	if n == 0 || m == nil {
		return redacted
	}
	m.Redacted += n
	if m.RedactionRules == nil {
		m.RedactionRules = map[string]int{}
	}
	h := harnessForPath(sourcePath)
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
		h := harnessForPath(path)
		if h == "" && path == sources.OpencodeDB() {
			h = "opencode"
		}
		skipHarness[h] = true
	}
	for key, count := range old.RedactionRules {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) == 2 && !skipHarness[parts[0]] {
			m.RedactionRules[key] = count
		}
	}
}

// beginPass clears the parsers' skip counters, which belong to the pass that
// parsed. They used to be cleared only by the manifest fold, so a pass that died
// before writing left its count for the next one to report: one bad line on
// disk, "2 lines skipped" on screen, with the manifest agreeing (#2010).
//
// Called at every place a pass parses: this one, rebuildForSearch — which a
// recall reaches directly once an index is found damaged, without passing
// through updateIndex at all — and rebuildWithTombstones, which forget and
// unforget call for themselves.
func beginPass() {
	sources.DiagSnapshot()
}

func updateIndex(dir, harness, scope string, files map[string]FileState, force bool, progress io.Writer) error {
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
	if force || err != nil || old.Version != version || old.Scope != scope {
		if progress != nil {
			if !hasProgressSink() {
				fmt.Fprintf(progress, "deja: indexing sessions into %s ...\n", displayPath(dir))
			}
		}
		return rebuild(dir, harness, scope, files, progress)
	}
	changed := map[string]FileState{}
	removed := map[string]bool{}
	for p, f := range files {
		if of, ok := old.Files[p]; !ok || !sameFile(of, f) {
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
	// Counted after the keep-back above: records that came off an unmounted
	// volume are still in the index, so they did not go away.
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
	if len(changed) == 0 && len(removed) == 0 {
		lastIngestFiles = 0
		return nil
	}
	if len(removed) == 0 && canAppendIncremental(changed, old.Files) {
		filesTouched, messages, unreadable, err := appendIncremental(dir, harness, scope, old, files, changed)
		if IsCorrupt(err) {
			if progress != nil {
				fmt.Fprintf(progress, "deja: index damaged (%v), rebuilding ...\n", err)
			}
			return rebuild(dir, harness, scope, files, progress)
		}
		if err != nil {
			return fmt.Errorf("append: %w", err)
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
	var replacements []model.Session
	// This pass's counts, like every other build path (#1850).
	emptied.Store(0)
	collisions.Store(0)
	lastIngestFiles = len(changed)
	for p, f := range changed {
		ss, err := parseChangedFile(harness, p, old.Files[p])
		if err != nil {
			// A live-locked or half-written store (Cursor holds its sqlite
			// under WAL) must not fail every search. Keep the old records
			// and the old FileState so the next run retries this file.
			if progress != nil {
				fmt.Fprintf(progress, "deja: skipping %s this pass: %v\n", filepath.Base(p), err)
			}
			delete(changed, p)
			if of, ok := old.Files[p]; ok {
				files[p] = of
			} else {
				delete(files, p)
			}
			continue
		}
		replacements = append(replacements, sources.FilterSessions(filterTombstoned(ss))...)
		files[p] = f
	}
	replaceKeys := map[string]bool{}
	for _, s := range replacements {
		replaceKeys[s.Harness+":"+s.ID] = true
	}
	if progress != nil {
		fmt.Fprintf(progress, "deja: incremental index changed_files=%d removed_files=%d sessions=%d\n", len(changed), len(removed), len(replacements))
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
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(),
		Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope,
		ExportWatermarks: old.ExportWatermarks, ExportBoundary: old.ExportBoundary, ImportedRecords: old.ImportedRecords,
		// Kept from the old index: this build reuses records written under
		// those patterns, so claiming today's set would be a lie the reader
		// cannot check.
		ExcludeFingerprint: old.ExcludeFingerprint,
		ToolFingerprint:    mergedToolFingerprint(priorToolFingerprint(dir))}
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
		seen := map[string]bool{}
		for _, tok := range append(indexKeys(r.Text), dateTokens(r.Time)...) {
			if seen[tok] {
				continue
			}
			seen[tok] = true
			b := bucket(tok)
			if buckets[b] == nil {
				buckets[b] = map[string][]posting{}
			}
			buckets[b][tok] = append(buckets[b][tok], posting{Off: off, Sid: meta.Ord})
		}
		if _, exists := m.Sessions[r.Key]; exists {
			return nil
		}
		m.Sessions[r.Key] = meta
		return nil
	}
	var recErr error
	if err := eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(old), func(r Record) {
		if recErr != nil {
			return
		}
		// Shared-store harnesses (opencode, cursor) are parsed since a
		// watermark, so their untouched sessions are NOT re-emitted on a
		// change — they must be retained, not dropped, or they vanish.
		// Superseded sessions are handled by replaceKeys.
		h := harnessForPath(r.SourcePath)
		sharedStore := h == "opencode" || h == "cursor-db" || h == "goose-db"
		// replaceKeys is scoped to shared stores. A shared store is parsed since
		// a watermark, so a superseded session's old record is not re-read and
		// clause two never reaches it — replaceKeys is what drops it. For a
		// per-file harness, a removed or changed file's old records are already
		// dropped by the two clauses above, so applying replaceKeys there only
		// hurts: two transcripts in different projects can share a filename-derived
		// id, and dropping by key alone erased the sibling that was never re-read
		// (#699). The record's own SourcePath decides its fate for those.
		if removed[r.SourcePath] || (changed[r.SourcePath].Path != "" && !sharedStore) || (sharedStore && replaceKeys[r.Key]) {
			return
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
		owns, collided := attributeSession(held, s)
		if collided {
			collisions.Add(1)
		}
		if owns {
			m.Sessions[key] = metaWithOrd(metaForSession(s), ord)
		} else if _, present := m.Sessions[key]; !present {
			m.Sessions[key] = held
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
	setOpencodeLastUpdated(m.Files, m.Sessions)
	m.RecordStrings = tbl.strs
	if err := writeManifest(tmp, m); err != nil {
		return err
	}
	carrySidecars(dir, tmp)
	// After carrying, not instead of it: both of these write only when they
	// have something to say, and the carried file is what a quiet update leaves.
	mergeFixes(dir, tmp, replacements, replaceKeys)
	buildCommandsFromIndex(tmp)
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

func canAppendIncremental(changed map[string]FileState, old map[string]FileState) bool {
	if len(changed) == 0 {
		return false
	}
	for p, f := range changed {
		of, ok := old[p]
		if !ok || f.Size <= of.Size {
			return false
		}
		// A prior pass that indexed no complete line (a torn first line, or a lone
		// line with no trailing newline) leaves SafeSize==0 with bytes on disk.
		// Resuming an append from that ambiguous 0 would either re-read mid-line
		// (dropping the first message) or duplicate an already-indexed lone line,
		// so route these files through the full re-index path instead (#appendloss).
		if of.SafeSize == 0 && of.Size > 0 {
			return false
		}
		// Growth is not proof that the earlier bytes are untouched: a rewind
		// that truncates and regrows past the old length looks exactly like
		// an append, and appending onto it leaves the rewritten prefix in the
		// index with its old text. Compare the prefix before trusting it.
		if of.PrefixHash != 0 && filePrefixHash(p, of.SafeSize) != of.PrefixHash {
			return false
		}
		switch harnessForPath(p) {
		case "claude", "codex", "codex-history", "opencode", "cursor-db", "goose-db", "deja", "pi", "copilot", "grok":
		default:
			return false
		}
	}
	return true
}

func appendIncremental(dir, harness, scope string, old Manifest, files map[string]FileState, changed map[string]FileState) (filesTouched, messages, unreadable int, err error) {
	// This pass's counts, like the two full paths: an incremental that nobody
	// read between builds otherwise reported its own colliding ids plus the
	// ones before it (#1850).
	emptied.Store(0)
	collisions.Store(0)
	lastIngestFiles = len(changed)
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
	m.Scope = scope
	m.BuiltAt = time.Now()
	m.Files = files
	m.Redacted = 0
	carryRedactions(&m, old, map[string]bool{})
	if m.Sessions == nil {
		m.Sessions = map[string]SessionMeta{}
	}
	// Sorted, not map order: two sessions can claim the same harness:id, and
	// which one wins decided the project a whole conversation was filed under
	// — differently on every run (#698).
	for _, p := range sortedKeys(changed) {
		ss, err := parseAppendedFile(harness, p, old.Files[p])
		if err != nil {
			if of, ok := old.Files[p]; ok {
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
		filesTouched++
		for _, s := range ss {
			key := s.Harness + ":" + s.ID
			meta := m.Sessions[key]
			if meta.ID == "" {
				meta = metaWithOrd(metaForSession(s), nextSessionOrd(m.Sessions))
			}
			if meta.Started.IsZero() || (!s.Started.IsZero() && s.Started.Before(meta.Started)) {
				meta.Started = s.Started
			}
			if s.Updated.After(meta.Updated) {
				meta.Updated = s.Updated
			}
			owns, collided := attributeSession(meta, s)
			if collided {
				collisions.Add(1)
				meta.Shared = true
			}
			if s.Project != "" && s.Project != "-" && owns {
				meta.Project = s.Project
			}
			if s.Path != "" && owns {
				meta.Path = s.Path
			}
			if meta.Title == "" {
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
				if t, _ := redact.Text(s.Title); boundSourceTitle(s.Harness, t) != meta.Title {
					meta.Title, meta.AgentTitle = boundSourceTitle(s.Harness, t), s.AgentTitle
				}
			}
			// Only the session that owns this row. The full build writes these
			// fields under the same condition; here the merge ran regardless,
			// so growing the loser of an id collision moved the owner's Words,
			// flipped its GaveUp and put the loser's files in its Touched —
			// the wrong conversation's files surfacing in blame (#1304).
			if owns {
				extendDerived(&meta, s.Messages)
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
				off, err := rw.write(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time})
				if err != nil {
					return filesTouched, messages, 0, err
				}
				messages++
				seen := map[string]bool{}
				for _, tok := range indexKeys(text) {
					if seen[tok] {
						continue
					}
					seen[tok] = true
					data, err := loadBucket(tok)
					if err != nil {
						return filesTouched, messages, 0, err
					}
					data[tok] = append(data[tok], posting{Off: off, Sid: meta.Ord})
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
	setOpencodeLastUpdated(m.Files, m.Sessions)
	m.RecordStrings = tbl.strs
	// Read before writeManifest: the fold in there drains the counters, and the
	// caller prints its line after this returns (#2007).
	unreadable = totalMalformed()
	if err := writeManifest(dir, m); err != nil {
		return filesTouched, messages, unreadable, err
	}
	return filesTouched, messages, unreadable, nil
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

func parseAppendedFile(harness, p string, old FileState) (ss []model.Session, err error) {
	defer func() {
		if r := recover(); r != nil {
			ss, err = nil, fmt.Errorf("parser panic on %s: %v", p, r)
		}
	}()
	k, ok := kindForPath(p)
	if !ok || k.ParseFrom == nil {
		return nil, nil
	}
	from := old.SafeSize
	if from == 0 || from > old.Size {
		from = old.Size
	}
	return k.ParseFrom(p, from, old.LastUpdated)
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

func setOpencodeLastUpdated(files map[string]FileState, sessions map[string]SessionMeta) {
	setStoreLastUpdated(files, sessions, "opencode", sources.OpencodeDB())
	setStoreLastUpdated(files, sessions, "goose", sources.GooseDB())
	for _, db := range sources.CursorDBs() {
		setStoreLastUpdated(files, sessions, "cursor", db)
	}
}

// setStoreLastUpdated stamps a database-backed store with the newest session
// time so incremental passes can query only newer content.
func setStoreLastUpdated(files map[string]FileState, sessions map[string]SessionMeta, harness, db string) {
	f, ok := files[db]
	if !ok {
		return
	}
	var latest int64
	for _, s := range sessions {
		if s.Harness == harness && s.Updated.UnixNano() > latest {
			latest = s.Updated.UnixNano()
		}
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
			fs := FileState{Path: p, Size: fi.Size(), MTime: fi.ModTime().UnixNano()}
			if strings.HasSuffix(p, ".jsonl") {
				// Deriving these means reading the file: the tail for the last
				// complete line, the head for the prefix hash. When size and
				// mtime are unchanged the bytes are too, and on a large store
				// this is the difference between a stat and 650 ms of reading.
				if of, ok := old[p]; ok && of.Size == fs.Size && of.MTime == fs.MTime {
					fs.SafeSize, fs.PrefixHash = of.SafeSize, of.PrefixHash
				} else {
					fs.SafeSize = lastCompleteLineOffset(p, fi.Size())
					fs.PrefixHash = filePrefixHash(p, fs.SafeSize)
				}
			}
			if harnessForPath(p) == "grok" {
				if summary, err := os.Lstat(filepath.Join(filepath.Dir(p), "summary.json")); err == nil && summary.Mode()&os.ModeSymlink == 0 && !summary.IsDir() {
					fs.MetadataSize = summary.Size()
					fs.MetadataMTime = summary.ModTime().UnixNano()
				}
				if cwd, err := os.Lstat(filepath.Join(filepath.Dir(filepath.Dir(p)), ".cwd")); err == nil && cwd.Mode()&os.ModeSymlink == 0 && !cwd.IsDir() {
					fs.CWDSize = cwd.Size()
					fs.CWDMTime = cwd.ModTime().UnixNano()
				}
			}
			out[p] = fs
		}
	}
	injectHermesPG(out, old)
	return out
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
					// NFC-canonicalise here too: this is the bulk write path and
					// does not go through redactForIngest (#1098).
					redacted, counts := redact.Text(nfcfold.Compose(stripSelfRecall(s.Messages[mi].Text)))
					if len(redacted) > maxIndexedText {
						cut := maxIndexedText
						for cut > 0 && !utf8.RuneStart(redacted[cut]) {
							cut--
						}
						redacted = redacted[:cut]
						mu.Lock()
						countClipped(m, s.Path, 1)
						mu.Unlock()
					}
					s.Messages[mi].Text = redacted
					if n := counts.Total(); n > 0 && m != nil {
						mu.Lock()
						m.Redacted += n
						if m.RedactionRules == nil {
							m.RedactionRules = map[string]int{}
						}
						h := harnessForPath(s.Path)
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

// filePrefixHash fingerprints the first n bytes of a file. Only used to decide
// whether an append is safe, so a fast non-cryptographic hash is the right
// tool: a collision costs one unnecessary full reparse, never a wrong index.
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

// countClipped records messages stored short of the transcript. The caller
// holds the lock where one is needed; redactForIngest runs single-threaded.
func countClipped(m *Manifest, sourcePath string, n int) {
	if m == nil || n == 0 {
		return
	}
	h := harnessForPath(sourcePath)
	if h == "" {
		if _, ok := m.Files[sources.OpencodeDB()]; ok {
			h = "opencode"
		}
	}
	if h == "" {
		return
	}
	if m.IngestHealth == nil {
		m.IngestHealth = map[string]HarnessIngest{}
	}
	e := m.IngestHealth[h]
	e.ClippedMessages += n
	m.IngestHealth[h] = e
}
