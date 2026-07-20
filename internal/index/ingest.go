package index

import (
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
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
	want := currentFiles("")
	m, err := readManifest(dir)
	if !force && err == nil && manifestFresh(m, want, "") && recordsIntact(dir, m) {
		return nil
	}
	return updateIndex(dir, "", "", want, force, progress)
}

func EnsureForSearch(dir string, o query.Options, force bool, progress io.Writer) error {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return err
	}
	defer unlock()
	want := currentFiles("")
	scope := ""
	m, err := readManifest(dir)
	if !force && err == nil && manifestFresh(m, want, scope) && recordsIntact(dir, m) {
		return nil
	}
	if force || err != nil || m.Version != version || m.Scope != scope || !recordsIntact(dir, m) {
		if progress != nil {
			fmt.Fprintf(progress, "deja: indexing sessions into %s ...\n", displayPath(dir))
		}
		return rebuildForSearch(dir, o, scope, want, progress)
	}
	if err := updateIndex(dir, o.Harness, scope, want, force, progress); err != nil {
		return fmt.Errorf("update: %w", err)
	}
	return nil
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
	ss := sources.FilterSessions(filterTombstonedSet(loadProgress(harness, progress), dead))
	ss = append(ss, imported.sessions...)
	ss = filterTombstonedSet(ss, dead)
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: scope,
		ExportWatermarks: imported.watermarks, ImportedRecords: imported.dedupe}
	recPath := filepath.Join(tmp, "records.bin")
	rf, err := os.OpenFile(recPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	rw, err := newRecordWriter(rf)
	if err != nil {
		_ = rf.Close()
		return err
	}
	seenMsgs := msgSeen{}
	buckets, err := indexTextParallel(func(push func(tokenJob)) error {
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
			m.Sessions[key] = metaWithOrd(metaForSession(s), ord)
			for _, msg := range s.Messages {
				if seenMsgs.dup(key, msg.Role, msg.Time, msg.Text) {
					continue
				}
				text := redactForIngest(&m, s.Path, msg.Text)
				off, err := rw.write(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time})
				if err != nil {
					return err
				}
				writtenMessages++
				push(tokenJob{text: text, offset: off, sid: m.Sessions[key].Ord})
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
	if err := writeBucketsConcurrent(filepath.Join(tmp, "buckets"), buckets); err != nil {
		return err
	}
	setOpencodeLastUpdated(m.Files, m.Sessions)
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
	out.dedupe = m.ImportedRecords
	by := map[string]*model.Session{}
	_ = eachRecord(filepath.Join(dir, "records.bin"), func(r Record) {
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

// loadProgress narrates a full rebuild per harness: a cold pass over a large
// corpus takes seconds and used to look hung.
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

func loadProgress(h string, progress io.Writer) []model.Session {
	var ss []model.Session
	for _, hr := range sources.Registry() {
		if h != "" && h != hr.Name {
			continue
		}
		got := safeLoad(hr.Name, hr.Load, progress)
		ss = append(ss, got...)
		if progress != nil && len(got) > 0 && !SuppressHarnessNarration {
			msgs := 0
			for _, s := range got {
				msgs += len(s.Messages)
			}
			// "deja" is the notes pseudo-source; it narrates as "notes".
			label := hr.Name
			if label == "deja" {
				label = "notes"
			}
			fmt.Fprintf(progress, "deja: %s: %d sessions, %d messages\n", label, len(got), msgs)
		}
	}
	return ss
}

func rebuildForSearch(dir string, o query.Options, scope string, files map[string]FileState, progress io.Writer) error {
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o700); err != nil {
		return err
	}
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
		ExportWatermarks: imp.watermarks, ImportedRecords: imp.dedupe}
	recPath := filepath.Join(tmp, "records.bin")
	rf, err := os.OpenFile(recPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	rw, err := newRecordWriter(rf)
	if err != nil {
		_ = rf.Close()
		return err
	}
	seenMsgs := msgSeen{}
	buckets, err := indexTextParallel(func(push func(tokenJob)) error {
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
			m.Sessions[key] = metaWithOrd(metaForSession(s), ord)
			for _, msg := range s.Messages {
				if seenMsgs.dup(key, msg.Role, msg.Time, msg.Text) {
					continue
				}
				text := redactForIngest(&m, s.Path, msg.Text)
				off, err := rw.write(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time})
				if err != nil {
					return err
				}
				writtenMessages++
				push(tokenJob{text: text, offset: off, sid: m.Sessions[key].Ord})
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
	if err := writeBucketsConcurrent(filepath.Join(tmp, "buckets"), buckets); err != nil {
		return err
	}
	setOpencodeLastUpdated(m.Files, m.Sessions)
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
					addIndexKeys(partials[i], job.text, job.offset, job.sid)
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

func addIndexKeys(buckets bucketPostings, text string, off int64, sid uint32) {
	seen := map[string]bool{}
	for _, tok := range indexKeys(text) {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		b := bucket(tok)
		if buckets[b] == nil {
			buckets[b] = map[string][]posting{}
		}
		buckets[b][tok] = append(buckets[b][tok], posting{Off: off, Sid: sid})
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
	return SessionMeta{ID: s.ID, Harness: s.Harness, Project: s.Project, Path: s.Path, Title: title, Started: s.Started, Updated: s.Updated}
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

func sessionFromMeta(meta SessionMeta) model.Session {
	return model.Session{ID: meta.ID, Harness: meta.Harness, Project: meta.Project, Path: meta.Path, Title: meta.Title, Started: meta.Started, Updated: meta.Updated}
}

func sessionTitle(s model.Session) string {
	for _, msg := range s.Messages {
		if msg.Role != "user" {
			continue
		}
		t := strings.TrimSpace(msg.Text)
		if t == "" || strings.HasPrefix(t, "<local-command") || strings.HasPrefix(t, "<command-") ||
			strings.HasPrefix(t, "<task-notification") || strings.HasPrefix(t, "<teammate-message") ||
			strings.HasPrefix(t, "Caveat:") {
			continue
		}
		return truncateTitle(t, 60)
	}
	return ""
}

func truncateTitle(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

func recordsForKey(path, key string) ([]Record, error) {
	var out []Record
	err := eachRecord(path, func(r Record) {
		if r.Key == key {
			out = append(out, r)
		}
	})
	return out, err
}

func redactForIngest(m *Manifest, sourcePath, text string) string {
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
			fmt.Fprintf(progress, "deja: indexing sessions into %s ...\n", displayPath(dir))
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
	rw, err := newRecordWriter(rf)
	if err != nil {
		_ = rf.Close()
		return err
	}
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Generation: old.Generation, Scope: scope,
		ExportWatermarks: old.ExportWatermarks, ImportedRecords: old.ImportedRecords}
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
		r.Text = redactForIngest(&m, r.SourcePath, r.Text)
		off, err := rw.write(r)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, tok := range indexKeys(r.Text) {
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
	if err := eachRecord(filepath.Join(dir, "records.bin"), func(r Record) {
		if recErr != nil {
			return
		}
		// Shared-store harnesses (opencode, cursor) are parsed since a
		// watermark, so their untouched sessions are NOT re-emitted on a
		// change — they must be retained, not dropped, or they vanish.
		// Superseded sessions are handled by replaceKeys.
		h := harnessForPath(r.SourcePath)
		sharedStore := h == "opencode" || h == "cursor-db"
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
	for _, s := range replacements {
		key := s.Harness + ":" + s.ID
		ord := uint32(0)
		if om, ok := old.Sessions[key]; ok {
			ord = om.Ord
		} else if cur, ok := m.Sessions[key]; ok {
			ord = cur.Ord
		}
		if ord == 0 {
			ord = nextSessionOrd(m.Sessions)
		}
		m.Sessions[key] = metaWithOrd(metaForSession(s), ord)
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
		switch harnessForPath(p) {
		case "claude", "codex", "codex-history", "opencode", "cursor-db", "deja", "pi", "copilot":
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
	rw, err := newRecordWriter(rf)
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
	for p := range changed {
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
			if s.Project != "" && s.Project != "-" {
				meta.Project = s.Project
			}
			if s.Path != "" {
				meta.Path = s.Path
			}
			if meta.Title == "" {
				meta.Title = sessionTitle(s)
			}
			m.Sessions[key] = meta
			for _, msg := range s.Messages {
				text := redactForIngest(&m, s.Path, msg.Text)
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

func currentFiles(h string) map[string]FileState {
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
				fs.SafeSize = lastCompleteLineOffset(p, fi.Size())
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
