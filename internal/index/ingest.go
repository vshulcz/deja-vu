package index

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/model"
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
			wrote[s.Key()] = true
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
			key := s.Key()
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
		seen[s.Key()] = struct{}{}
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
			key := s.Key()
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
		buildFixes(tmp, cmds, func(s model.Session) string { return s.Key() })
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
