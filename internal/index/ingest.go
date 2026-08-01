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
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
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
		m.IngestHealth[h] = HarnessIngest{}
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

func Ensure(dir string, harness string, force bool, progress io.Writer) error {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return err
	}
	defer unlock()
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
	prior, priorErr := readManifest(dir)
	want := currentFilesReusing("", priorFiles(prior, priorErr))
	scope := ""
	m, err := prior, priorErr
	if !force && err == nil && manifestFresh(m, want, scope) && recordsIntact(dir, m) {
		return nil
	}
	if force || err != nil || m.Version != version || m.Scope != scope || !recordsIntact(dir, m) {
		if progress != nil {
			if !hasProgressSink() {
				fmt.Fprintf(progress, "deja: indexing sessions into %s ...\n", displayPath(dir))
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
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope,
		ExportWatermarks: imported.watermarks, ExportBoundary: imported.boundary, ImportedRecords: imported.dedupe}
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
	buckets, err := indexTextParallel(func(push func(tokenJob)) error {
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
	buildCooccur(tmp, ss)
	reportPhase("writing index", len(buckets))
	if err := writeBucketsConcurrent(filepath.Join(tmp, "buckets"), buckets); err != nil {
		return err
	}
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
		out.sessions = append(out.sessions, *sess)
	}
	return out
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
	var ss []model.Session
	for _, r := range results {
		if len(r.ss) == 0 {
			continue
		}
		ss = append(ss, r.ss...)
		if progress != nil && !SuppressHarnessNarration {
			msgs := 0
			for _, s := range r.ss {
				msgs += len(s.Messages)
			}
			// "deja" is the notes pseudo-source; it narrates as "notes".
			label := r.name
			if label == "deja" {
				label = "notes"
			}
			fmt.Fprintf(progress, "deja: %s: %d session%s, %d message%s\n", label, len(r.ss), pluralS(len(r.ss)), msgs, pluralS(msgs))
		}
	}
	return ss
}

// ReportCollisions returns how many transcripts shared an id with another since
// the last build, and clears the counter. Silence was the worst part of #698:
// the indexer counted every session on disk while the manifest held fewer, and
// nothing connected the two numbers.
func ReportCollisions() int {
	return int(collisions.Swap(0))
}

func rebuildForSearch(dir string, o query.Options, scope string, files map[string]FileState, progress io.Writer) error {
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

func writeSessions(tmp, dir string, ss []model.Session, files map[string]FileState, scope string) error {
	return writeSessionsWithSync(tmp, dir, ss, files, scope, importedState{})
}

func writeSessionsWithSync(tmp, dir string, ss []model.Session, files map[string]FileState, scope string, imp importedState) error {
	initialBuild := !HasManifest(dir)
	writtenMessages := 0
	lastIngestFiles = len(files)
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope,
		ExportWatermarks: imp.watermarks, ExportBoundary: imp.boundary, ImportedRecords: imp.dedupe}
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
	seenMsgs := msgSeen{}
	reportPhase("indexing messages", len(ss))
	buckets, err := indexTextParallel(func(push func(tokenJob)) error {
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
			for _, msg := range s.Messages {
				if seenMsgs.dup(key, msg.Role, msg.Time, msg.Text) {
					continue
				}
				text := redactForIngest(&m, s.Path, msg.Text)
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
	buildCooccur(tmp, ss)
	reportPhase("writing index", len(buckets))
	if err := writeBucketsConcurrent(filepath.Join(tmp, "buckets"), buckets); err != nil {
		return err
	}
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
			head := text[:signalFloor]
			if len(text) <= signalFloor+signalTail {
				return text
			}
			return head + "\n" + text[len(text)-signalTail:]
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

func addIndexKeys(buckets bucketPostings, text string, off int64, sid uint32, when time.Time, tool bool) {
	seen := map[string]bool{}
	for _, tok := range append(indexKeys(text), dateTokens(when)...) {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		b := bucket(tok)
		if buckets[b] == nil {
			buckets[b] = map[string][]posting{}
		}
		buckets[b][tok] = append(buckets[b][tok], posting{Off: off, Sid: sid, Tool: tool})
	}
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
	if title == "" {
		title = sessionTitle(s)
	}
	// Titles come from unredacted places — an agent-generated summary, a
	// composer name, the first user message — and are persisted in
	// sessions.gob, so they need the same scrubbing as record text.
	title, _ = redact.Text(title)
	return SessionMeta{ID: s.ID, Harness: s.Harness, Project: s.Project, Path: s.Path, Title: title, Started: s.Started, Updated: s.Updated, Touched: topTouchedFiles(s.Messages), Asked: askedHashes(s.Messages), Hit: frictionHashes(s.Messages)}
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
		if notAsked(m.Text) || !looksLikeQuestion(m.Text) {
			continue
		}
		stem := questionStem(m.Text)
		if stem == "" {
			continue
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(stem))
		v := h.Sum64()
		if seen[v] {
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
	if len(fields) < 5 {
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
		for _, p := range strings.Split(m.Text, "\n") {
			if p = strings.TrimSpace(p); p != "" && !agentOwnedFile(p) {
				count[p]++
			}
		}
	}
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
		Title: meta.Title, Started: meta.Started, Updated: meta.Updated, Touched: meta.Touched,
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

func sessionTitle(s model.Session) string {
	for _, msg := range s.Messages {
		if msg.Role != "user" {
			continue
		}
		t := strings.TrimSpace(msg.Text)
		if !titleWorthy(t) {
			continue
		}
		return truncateTitle(t, 60)
	}
	// No user turn worth naming the session after: a session the agent opened
	// itself, or one whose prompts are all harness plumbing. The assistant's
	// first sentence is a worse title than the question that prompted it, and
	// it is far better than the blank line these sessions used to print in
	// `deja last` and on the first screen (#692).
	for _, msg := range s.Messages {
		if msg.Role != "assistant" {
			continue
		}
		t := strings.TrimSpace(msg.Text)
		if !titleWorthy(t) {
			continue
		}
		return truncateTitle(t, 60)
	}
	return ""
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

func updateIndex(dir, harness, scope string, files map[string]FileState, force bool, progress io.Writer) error {
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
	if len(changed) == 0 && len(removed) == 0 {
		lastIngestFiles = 0
		return nil
	}
	if len(removed) == 0 && canAppendIncremental(changed, old.Files) {
		filesTouched, messages, err := appendIncremental(dir, harness, scope, old, files, changed)
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
			fmt.Fprintf(progress, "deja: updated %d file (%d new messages)\n", filesTouched, messages)
		}
		return nil
	}
	var replacements []model.Session
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
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Generation: old.Generation, Scope: scope,
		ExportWatermarks: old.ExportWatermarks, ExportBoundary: old.ExportBoundary, ImportedRecords: old.ImportedRecords}
	skipRedactions := map[string]bool{}
	for p := range changed {
		skipRedactions[p] = true
	}
	for p := range removed {
		skipRedactions[p] = true
	}
	carryRedactions(&m, old, skipRedactions)
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
		if removed[r.SourcePath] || (changed[r.SourcePath].Path != "" && !sharedStore) || replaceKeys[r.Key] {
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
		owns, collided := attributeSession(held, s)
		if collided {
			collisions.Add(1)
		}
		if owns {
			m.Sessions[key] = metaWithOrd(metaForSession(s), ord)
		} else if _, present := m.Sessions[key]; !present {
			m.Sessions[key] = held
		}
		for _, msg := range s.Messages {
			if seenMsgs.dup(key, msg.Role, msg.Time, msg.Text) {
				continue
			}
			text := redactForIngest(&m, s.Path, msg.Text)
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
	return swapIndexDir(dir, tmp)
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
		case "claude", "codex", "codex-history", "opencode", "cursor-db", "goose-db", "deja", "pi", "copilot":
		default:
			return false
		}
	}
	return true
}

func appendIncremental(dir, harness, scope string, old Manifest, files map[string]FileState, changed map[string]FileState) (int, int, error) {
	lastIngestFiles = len(changed)
	rf, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, 0, err
	}
	// The log is being APPENDED to, so ids must continue where the existing
	// records left off. Starting a fresh table would hand id 0 to a new
	// string and silently repoint every record that already uses it.
	tbl := tablesFromManifest(old)
	rw, err := newRecordWriter(rf, tbl)
	if err != nil {
		_ = rf.Close()
		return 0, 0, err
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
	filesTouched, messages := 0, 0
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
			}
			if s.Project != "" && s.Project != "-" && owns {
				meta.Project = s.Project
			}
			if s.Path != "" && owns {
				meta.Path = s.Path
			}
			if meta.Title == "" {
				meta.Title = sessionTitle(s)
			}
			m.Sessions[key] = meta
			for _, msg := range s.Messages {
				text := redactForIngest(&m, s.Path, msg.Text)
				// A message that is nothing but harness plumbing strips to empty
				// (#551). Writing it would store a record with no content and give
				// it a posting.
				if strings.TrimSpace(text) == "" {
					continue
				}
				off, err := rw.write(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time})
				if err != nil {
					return filesTouched, messages, err
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
						return filesTouched, messages, err
					}
					data[tok] = append(data[tok], posting{Off: off, Sid: meta.Ord})
				}
			}
		}
	}
	if err := rw.Close(); err != nil {
		return filesTouched, messages, err
	}
	if err := writeBucketsConcurrent(filepath.Join(dir, "buckets"), buckets); err != nil {
		return filesTouched, messages, err
	}
	setOpencodeLastUpdated(m.Files, m.Sessions)
	m.RecordStrings = tbl.strs
	if err := writeManifest(dir, m); err != nil {
		return filesTouched, messages, err
	}
	return filesTouched, messages, nil
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
func harnessForPath(p string) string { return sources.KindForPath(p) }

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

func currentFiles(h string) map[string]FileState {
	return currentFilesWith(h, nil)
}

func currentFilesStat(h string) map[string]FileState {
	return currentFilesWith(h, nil)
}

func currentFilesWith(h string, old map[string]FileState) map[string]FileState {
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
	return out
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
					redacted, counts := redact.Text(stripSelfRecall(s.Messages[mi].Text))
					if len(redacted) > maxIndexedText {
						cut := maxIndexedText
						for cut > 0 && !utf8.RuneStart(redacted[cut]) {
							cut--
						}
						redacted = redacted[:cut]
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
