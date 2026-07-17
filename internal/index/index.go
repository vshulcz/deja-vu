package index

import (
	"bufio"
	"encoding/binary"
	"encoding/gob"
	"errors"
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
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const version = 9
const maxIndexedText = 64 * 1024

var bucketMagic = []byte("DJB1")

// errCorruptIndex marks unreadable index structures (e.g. a bucket file cut
// short by a crash). Callers treat it as a cache miss and rebuild.
var errCorruptIndex = errors.New("corrupt index")

// IsCorrupt reports whether err means the on-disk index is damaged and a
// rebuild will heal it.
func IsCorrupt(err error) bool { return errors.Is(err, errCorruptIndex) }

var lastIngestFiles int

// BuildSummary describes the most recent (re)build in this process; the CLI
// uses it to greet a first-ever index with a summary instead of silence.
type HarnessCount struct {
	Name     string
	Sessions int
	Messages int
}

type BuildSummary struct {
	Initial    bool
	Sessions   int
	Messages   int
	Harnesses  int
	PerHarness []HarnessCount
}

var LastBuild BuildSummary

// SuppressHarnessNarration silences the per-harness progress lines for one
// build; the CLI sets it when it is about to greet a first index with the
// same numbers in the summary block.
var SuppressHarnessNarration bool

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

type FileState struct {
	Path          string `json:"path"`
	Size          int64  `json:"size"`
	MTime         int64  `json:"mtime"`
	MetadataSize  int64  `json:"metadata_size,omitempty"`
	MetadataMTime int64  `json:"metadata_mtime,omitempty"`
	CWDSize       int64  `json:"cwd_size,omitempty"`
	CWDMTime      int64  `json:"cwd_mtime,omitempty"`
	LastUpdated   int64  `json:"last_updated,omitempty"`
	Redactions    int    `json:"redactions,omitempty"`
	// SafeSize is the offset just past the last complete line at index time.
	// A session file caught mid-write ends in a torn line; parsing skips it,
	// and the next append must resume from here or that message is lost.
	SafeSize int64 `json:"safe_size,omitempty"`
}

type SessionMeta struct {
	ID, Harness, Project, Path, Title string
	Started, Updated                  time.Time
	Ord                               uint32
}

type Manifest struct {
	Version          int                    `json:"version"`
	Files            map[string]FileState   `json:"files"`
	Sessions         map[string]SessionMeta `json:"sessions"`
	BuiltAt          time.Time              `json:"built_at"`
	Scope            string                 `json:"scope"`
	Redacted         int                    `json:"redacted"`
	ExportWatermarks map[string]int64       `json:"export_watermarks,omitempty"`
	ImportedRecords  map[string]bool        `json:"imported_records,omitempty"`
	// RecordsSize is records.bin's byte length when the manifest was committed.
	// A live index whose records.bin is shorter than this lost its tail to a
	// torn write and must be treated as corrupt.
	RecordsSize int64 `json:"records_size,omitempty"`
}

type manifestCore struct {
	Version          int
	Files            map[string]FileState
	BuiltAt          time.Time
	Scope            string
	Redacted         int
	ExportWatermarks map[string]int64
	ImportedRecords  map[string]bool
	RecordsSize      int64
}

type RedactionStats struct {
	Total int
	Files map[string]int
}

type Record struct {
	Key        string
	SourcePath string
	Role       string
	Text       string
	Time       time.Time
	LowerText  string `json:"-"`
}

type posting struct {
	Off int64
	Sid uint32
}

func DefaultDir() string {
	if v := os.Getenv("DEJA_INDEX_DIR"); v != "" {
		return v
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".cache", "deja", "index.db")
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

func EnsureForSearch(dir string, o search.Options, force bool, progress io.Writer) error {
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

func Search(dir string, o search.Options) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, err := readManifest(dir)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if !recordsIntact(dir, m) {
		return nil, fmt.Errorf("%w: records.bin truncated", errCorruptIndex)
	}
	var posts []posting
	usedPostings := false
	if !o.Regex {
		if keys := queryKeys(o.Query); len(keys) > 0 {
			usedPostings = true
			posts, err = intersectPostings(dir, retrievalKeys(keys))
			if err != nil {
				return nil, fmt.Errorf("postings: %w", err)
			}
			if len(posts) == 0 {
				// grep expectation: "code" should find "opencode". Expand each query
				// token to all indexed tokens containing it (bucket directories only,
				// no record scan), then intersect.
				posts, err = intersectSubstringPostings(dir, tokens(o.Query))
				if err != nil {
					return nil, fmt.Errorf("substr postings: %w", err)
				}
			}
		}
	}
	if len(posts) == 0 {
		if usedPostings {
			return nil, nil
		}
		return scanRecords(dir, m, o, nil)
	}
	posts = cutPostingsBySession(posts, m, o)
	if len(posts) == 0 {
		return nil, nil
	}
	return scanRecords(dir, m, o, postingOffsets(posts))
}

// SearchWithRecovery is Search plus self-healing: a corrupt bucket (crash
// mid-append) triggers one full rebuild instead of erroring until the user
// runs --rebuild by hand.
func SearchWithRecovery(dir string, o search.Options, progress io.Writer) ([]model.Session, error) {
	ss, err := Search(dir, o)
	if err == nil || !IsCorrupt(err) {
		return ss, err
	}
	if progress != nil {
		fmt.Fprintf(progress, "deja: index damaged (%v), rebuilding ...\n", err)
	}
	if rerr := EnsureForSearch(dir, o, true, progress); rerr != nil {
		return nil, rerr
	}
	return Search(dir, o)
}

func Recent(dir string, n int) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	out := make([]model.Session, 0, len(m.Sessions))
	for _, meta := range m.Sessions {
		out = append(out, sessionFromMeta(meta))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// displayPath contracts the home directory to ~ in user-facing messages.
func displayPath(p string) string {
	if h, err := os.UserHomeDir(); err == nil && h != "" && strings.HasPrefix(p, h) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

func HasManifest(dir string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	_, err := os.Stat(filepath.Join(dir, "manifest.gob"))
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "sessions.gob"))
	return err == nil
}

func RecentProject(dir, project string, n int) ([]model.Session, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	project = strings.ToLower(project)
	var metas []SessionMeta
	for _, meta := range m.Sessions {
		p := strings.ToLower(meta.Project)
		if p == project || (project != "" && strings.Contains(p, project)) {
			metas = append(metas, meta)
		}
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Updated.After(metas[j].Updated) })
	if n > 0 && len(metas) > n {
		metas = metas[:n]
	}
	out := make([]model.Session, 0, len(metas))
	for _, meta := range metas {
		s := sessionFromMeta(meta)
		recs, err := recordsForKey(filepath.Join(dir, "records.bin"), meta.Harness+":"+meta.ID)
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
		}
		out = append(out, s)
	}
	return out, nil
}

func FindByPrefix(dir, p string) (model.Session, bool, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	defer unlock()
	m, err := readManifest(dir)
	if err != nil {
		return model.Session{}, false, err
	}
	var matches []SessionMeta
	for _, meta := range m.Sessions {
		if strings.HasPrefix(meta.ID, p) {
			matches = append(matches, meta)
		}
	}
	if len(matches) == 0 {
		return model.Session{}, false, nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Updated.After(matches[j].Updated) })
	meta := matches[0]
	s := sessionFromMeta(meta)
	recs, err := recordsForKey(filepath.Join(dir, "records.bin"), meta.Harness+":"+meta.ID)
	if err != nil {
		return model.Session{}, false, err
	}
	for _, r := range recs {
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}
	return s, true, nil
}

func rebuild(dir string, harness string, scope string, files map[string]FileState, progress io.Writer) error {
	lastIngestFiles = len(files)
	initialBuild := !HasManifest(dir)
	writtenMessages := 0
	imported := importedSessions(dir)
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o755); err != nil {
		return err
	}
	ss := loadProgress(harness, progress)
	ss = append(ss, imported.sessions...)
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Scope: scope,
		ExportWatermarks: imported.watermarks, ImportedRecords: imported.dedupe}
	recPath := filepath.Join(tmp, "records.bin")
	rf, err := os.Create(recPath)
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
				text := msg.Text
				if len(text) > maxIndexedText {
					text = text[:maxIndexedText]
				}
				text = redactForIngest(&m, s.Path, text)
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
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	summarizeBuild(initialBuild, len(m.Sessions), writtenMessages, ss)
	return nil
}

const syncImportPath = "deja-sync-import"

type importedState struct {
	sessions   []model.Session
	watermarks map[string]int64
	dedupe     map[string]bool
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

var harnessLoaders = []struct {
	name string
	load func() []model.Session
}{
	{"claude", sources.LoadClaude},
	{"codex", sources.LoadCodex},
	{"opencode", sources.LoadOpencode},
	{"aider", sources.LoadAider},
	{"gemini", sources.LoadGemini},
	{"cursor", sources.LoadCursor},
	{"antigravity", sources.LoadAntigravity},
	{"grok", sources.LoadGrok},
}

func load(h string) []model.Session { return loadProgress(h, nil) }

// loadProgress narrates a full rebuild per harness: a cold pass over a large
// corpus takes seconds and used to look hung.
func loadProgress(h string, progress io.Writer) []model.Session {
	var ss []model.Session
	for _, hl := range harnessLoaders {
		if h != "" && h != hl.name {
			continue
		}
		got := hl.load()
		ss = append(ss, got...)
		if progress != nil && len(got) > 0 && !SuppressHarnessNarration {
			msgs := 0
			for _, s := range got {
				msgs += len(s.Messages)
			}
			fmt.Fprintf(progress, "deja: %s: %d sessions, %d messages\n", hl.name, len(got), msgs)
		}
	}
	return ss
}

func rebuildForSearch(dir string, o search.Options, scope string, files map[string]FileState, progress io.Writer) error {
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o755); err != nil {
		return err
	}
	ss := loadProgress("", progress)
	imported := importedSessions(dir)
	ss = append(ss, imported.sessions...)
	return writeSessionsWithSync(tmp, dir, ss, files, scope, imported)
}

func writeSessions(tmp, dir string, ss []model.Session, files map[string]FileState, scope string) error {
	return writeSessionsWithSync(tmp, dir, ss, files, scope, importedState{})
}

func writeSessionsWithSync(tmp, dir string, ss []model.Session, files map[string]FileState, scope string, imp importedState) error {
	initialBuild := !HasManifest(dir)
	writtenMessages := 0
	lastIngestFiles = len(files)
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Scope: scope,
		ExportWatermarks: imp.watermarks, ImportedRecords: imp.dedupe}
	recPath := filepath.Join(tmp, "records.bin")
	rf, err := os.Create(recPath)
	if err != nil {
		return err
	}
	rw, err := newRecordWriter(rf)
	if err != nil {
		_ = rf.Close()
		return err
	}
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
				text := msg.Text
				if len(text) > maxIndexedText {
					text = text[:maxIndexedText]
				}
				text = redactForIngest(&m, s.Path, text)
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
	os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	summarizeBuild(initialBuild, len(m.Sessions), writtenMessages, ss)
	return nil
}

type tokenJob struct {
	text   string
	offset int64
	sid    uint32
}

type bucketPostings map[string]map[string][]posting

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

// msgSeen dedupes identical messages within a session across duplicate
// session objects in one indexing pass. Distinct messages (codex history
// accumulation) pass through; format twins (gemini .json/.jsonl, cursor
// multi-store composers) collapse.
type msgSeen map[string]bool

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
	redacted, counts := redact.Text(text)
	n := counts.Total()
	if n == 0 || m == nil {
		return redacted
	}
	m.Redacted += n
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
}

func Redactions(dir string) (RedactionStats, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return RedactionStats{}, err
	}
	out := RedactionStats{Total: m.Redacted, Files: map[string]int{}}
	for p, f := range m.Files {
		if f.Redactions > 0 {
			out.Files[p] = f.Redactions
		}
	}
	return out, nil
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
		replacements = append(replacements, ss...)
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
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o755); err != nil {
		return err
	}
	rf, err := os.Create(filepath.Join(tmp, "records.bin"))
	if err != nil {
		return err
	}
	rw, err := newRecordWriter(rf)
	if err != nil {
		_ = rf.Close()
		return err
	}
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Scope: scope,
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
		if removed[r.SourcePath] || (changed[r.SourcePath].Path != "" && harnessForPath(r.SourcePath) != "opencode") || replaceKeys[r.Key] {
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
			text := msg.Text
			if len(text) > maxIndexedText {
				text = text[:maxIndexedText]
			}
			text = redactForIngest(&m, s.Path, text)
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
	os.RemoveAll(dir)
	return os.Rename(tmp, dir)
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
		switch harnessForPath(p) {
		case "claude", "codex", "codex-history", "opencode", "cursor-db":
		default:
			return false
		}
	}
	return true
}

func appendIncremental(dir, harness, scope string, old Manifest, files map[string]FileState, changed map[string]FileState) (int, int, error) {
	lastIngestFiles = len(changed)
	rf, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
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
				text := msg.Text
				if len(text) > maxIndexedText {
					text = text[:maxIndexedText]
				}
				text = redactForIngest(&m, s.Path, text)
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

func parseChangedFile(harness, p string, old FileState) ([]model.Session, error) {
	switch harnessForPath(p) {
	case "claude":
		return sources.ParseClaudeFile(p)
	case "codex-history":
		return sources.ParseCodexHistory(p)
	case "codex":
		return sources.ParseCodexRollout(p)
	case "opencode":
		if old.LastUpdated > 0 {
			return sources.ParseOpencodeDBSince(p, time.Unix(0, old.LastUpdated))
		}
		return sources.ParseOpencodeDB(p)
	case "cursor-db":
		if old.LastUpdated > 0 {
			return sources.ParseCursorDBSince(p, time.Unix(0, old.LastUpdated))
		}
		return sources.ParseCursorDB(p)
	case "aider":
		return sources.ParseAiderFile(p)
	case "gemini":
		return sources.ParseGeminiFile(p)
	case "cursor":
		return sources.ParseCursorTranscript(p)
	case "antigravity":
		return sources.ParseAntigravityFile(p)
	case "grok":
		return sources.ParseGrokFile(p)
	default:
		return nil, nil
	}
}

func parseAppendedFile(harness, p string, old FileState) ([]model.Session, error) {
	from := old.SafeSize
	if from == 0 || from > old.Size {
		from = old.Size
	}
	switch harnessForPath(p) {
	case "claude":
		return sources.ParseClaudeFileFromOffset(p, from)
	case "codex-history":
		return sources.ParseCodexHistoryFromOffset(p, from)
	case "codex":
		return sources.ParseCodexRolloutFromOffset(p, from)
	case "opencode":
		if old.LastUpdated > 0 {
			return sources.ParseOpencodeDBSince(p, time.Unix(0, old.LastUpdated))
		}
		return sources.ParseOpencodeDB(p)
	case "cursor-db":
		if old.LastUpdated > 0 {
			return sources.ParseCursorDBSince(p, time.Unix(0, old.LastUpdated))
		}
		return sources.ParseCursorDB(p)
	default:
		return nil, nil
	}
}

func harnessForPath(p string) string {
	if p == sources.OpencodeDB() {
		return "opencode"
	}
	if filepath.Base(p) == "history.jsonl" && strings.HasPrefix(p, sources.CodexRoot()) {
		return "codex-history"
	}
	if strings.HasSuffix(p, ".jsonl") && strings.Contains(filepath.Base(p), "rollout-") && strings.HasPrefix(p, filepath.Join(sources.CodexRoot(), "sessions")) {
		return "codex"
	}
	if strings.HasSuffix(p, ".jsonl") && strings.HasPrefix(p, sources.ClaudeRoot()) {
		return "claude"
	}
	if filepath.Base(p) == ".aider.chat.history.md" {
		return "aider"
	}
	if strings.HasPrefix(p, filepath.Join(sources.GeminiRoot(), "tmp")) && (strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".jsonl")) {
		return "gemini"
	}
	if filepath.Base(p) == "state.vscdb" && strings.HasPrefix(p, sources.CursorUserRoot()) {
		return "cursor-db"
	}
	if strings.HasSuffix(p, ".jsonl") && strings.HasPrefix(p, filepath.Join(sources.CursorCLIRoot(), "projects")) {
		return "cursor"
	}
	if filepath.Base(p) == "transcript.jsonl" {
		for _, root := range sources.AntigravityRoots() {
			if strings.HasPrefix(p, root+string(filepath.Separator)) {
				return "antigravity"
			}
		}
	}
	if filepath.Base(p) == "updates.jsonl" && strings.HasPrefix(p, filepath.Join(sources.GrokRoot(), "sessions")) {
		return "grok"
	}
	return ""
}

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
	addWalk := func(root string, pred func(string) bool) {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.Type()&os.ModeSymlink == 0 && !d.IsDir() && pred(p) {
				paths[p] = true
			}
			return nil
		})
	}
	if h == "" || h == "claude" {
		addWalk(sources.ClaudeRoot(), sources.ClaudeFileWanted)
	}
	if h == "" || h == "codex" {
		addWalk(filepath.Join(sources.CodexRoot(), "sessions"), func(p string) bool {
			return strings.HasSuffix(p, ".jsonl") && strings.Contains(filepath.Base(p), "rollout-")
		})
		paths[filepath.Join(sources.CodexRoot(), "history.jsonl")] = true
	}
	if h == "" || h == "opencode" {
		paths[sources.OpencodeDB()] = true
	}
	if h == "" || h == "aider" {
		for _, p := range sources.AiderFiles() {
			paths[p] = true
		}
	}
	if h == "" || h == "gemini" {
		for _, p := range sources.GeminiChatFiles() {
			paths[p] = true
		}
	}
	if h == "" || h == "cursor" {
		for _, p := range sources.CursorDBs() {
			paths[p] = true
		}
		for _, p := range sources.CursorTranscripts() {
			paths[p] = true
		}
	}
	if h == "" || h == "antigravity" {
		for _, p := range sources.AntigravityTranscripts() {
			paths[p] = true
		}
	}
	if h == "" || h == "grok" {
		for _, p := range sources.GrokSessionFiles() {
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
	const window = 64 * 1024
	start := size - window
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil {
		return size
	}
	for i := len(buf) - 1; i >= 0; i-- {
		if buf[i] == '\n' {
			return start + int64(i) + 1
		}
	}
	if start == 0 {
		return 0
	}
	return size
}

func manifestFresh(m Manifest, files map[string]FileState, scope string) bool {
	if m.Version != version || len(m.Files) != len(files) {
		return false
	}
	if m.Scope != scope {
		return false
	}
	for p, f := range files {
		if !sameFile(m.Files[p], f) {
			return false
		}
	}
	return true
}

func scanRecords(dir string, m Manifest, o search.Options, offsets []int64) ([]model.Session, error) {
	by := map[string]*model.Session{}
	add := func(r Record) {
		meta, ok := m.Sessions[r.Key]
		if !ok {
			return
		}
		if o.Harness != "" && meta.Harness != o.Harness {
			return
		}
		if o.Project != "" && !strings.Contains(strings.ToLower(meta.Project), strings.ToLower(o.Project)) {
			return
		}
		if o.Since > 0 && meta.Updated.Before(time.Now().Add(-o.Since)) {
			return
		}
		if o.Role != "" && r.Role != o.Role {
			return
		}
		s := by[r.Key]
		if s == nil {
			cp := model.Session{ID: meta.ID, Harness: meta.Harness, Project: meta.Project, Path: meta.Path, Title: meta.Title, Started: meta.Started, Updated: meta.Updated}
			s = &cp
			by[r.Key] = s
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}
	if len(offsets) > 0 {
		f, err := os.Open(filepath.Join(dir, "records.bin"))
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		offsets = sortedUniqueOffsets(offsets)
		for _, off := range offsets {
			if r, err := readRecordAt(f, off); err == nil && recordMatchesQuery(r, o) {
				add(r)
			}
		}
	} else {
		if err := eachRecord(filepath.Join(dir, "records.bin"), add); err != nil {
			return nil, err
		}
	}
	out := make([]model.Session, 0, len(by))
	for _, s := range by {
		out = append(out, *s)
	}
	return out, nil
}

func cutPostingsBySession(posts []posting, m Manifest, o search.Options) []posting {
	// Rank from posting counts before reading record text. Harness/project/since are
	// session metadata filters; role is record-level, so search.Run applies it after
	// the cut and pre-rank counts may include other roles.
	type candidate struct {
		sid     uint32
		count   int
		updated time.Time
	}
	metaByOrd := sessionMetaByOrd(m)
	counts := map[uint32]int{}
	for _, p := range sortedUniquePostings(posts) {
		meta, ok := metaByOrd[p.Sid]
		if !ok || !sessionMetaMatches(meta, o) {
			continue
		}
		counts[p.Sid]++
	}
	if len(counts) == 0 {
		return nil
	}
	candidates := make([]candidate, 0, len(counts))
	for sid, count := range counts {
		candidates = append(candidates, candidate{sid: sid, count: count, updated: metaByOrd[sid].Updated})
	}
	sort.Slice(candidates, func(i, j int) bool {
		si := preRankScore(candidates[i].count, candidates[i].updated)
		sj := preRankScore(candidates[j].count, candidates[j].updated)
		if si == sj {
			return candidates[i].updated.After(candidates[j].updated)
		}
		return si > sj
	})
	if !o.All && len(candidates) > 15 {
		candidates = candidates[:15]
	}
	keep := make(map[uint32]bool, len(candidates))
	for _, c := range candidates {
		keep[c.sid] = true
	}
	out := make([]posting, 0, len(posts))
	for _, p := range posts {
		if keep[p.Sid] {
			out = append(out, p)
		}
	}
	return out
}

func sessionMetaByOrd(m Manifest) map[uint32]SessionMeta {
	out := make(map[uint32]SessionMeta, len(m.Sessions))
	for _, meta := range m.Sessions {
		out[meta.Ord] = meta
	}
	return out
}

func sessionMetaMatches(meta SessionMeta, o search.Options) bool {
	if o.Harness != "" && meta.Harness != o.Harness {
		return false
	}
	if o.Project != "" && !strings.Contains(strings.ToLower(meta.Project), strings.ToLower(o.Project)) {
		return false
	}
	if o.Since > 0 && meta.Updated.Before(time.Now().Add(-o.Since)) {
		return false
	}
	return true
}

func preRankScore(count int, updated time.Time) float64 {
	age := time.Since(updated).Hours() / 24
	return float64(count) * 1000 / (1 + age)
}

func postingOffsets(posts []posting) []int64 {
	out := make([]int64, 0, len(posts))
	for _, p := range posts {
		out = append(out, p.Off)
	}
	return out
}

func sortedUniqueOffsets(offsets []int64) []int64 {
	out := append([]int64(nil), offsets...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	n := 0
	for _, off := range out {
		if n == 0 || out[n-1] != off {
			out[n] = off
			n++
		}
	}
	return out[:n]
}

func sortedUniquePostings(posts []posting) []posting {
	out := append([]posting(nil), posts...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Off == out[j].Off {
			return out[i].Sid < out[j].Sid
		}
		return out[i].Off < out[j].Off
	})
	n := 0
	for _, p := range out {
		if n == 0 || out[n-1].Off != p.Off {
			out[n] = p
			n++
		}
	}
	return out[:n]
}

// recordWriter appends length-prefixed records through one buffer, tracking
// the file offset in memory: the hot rebuild path used to pay a Seek syscall
// per record, which dominated cold-rebuild profiles.
type recordWriter struct {
	f   *os.File
	w   *bufio.Writer
	off int64
}

func newRecordWriter(f *os.File) (*recordWriter, error) {
	off, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	return &recordWriter{f: f, w: bufio.NewWriterSize(f, 1<<20), off: off}, nil
}

func (rw *recordWriter) write(r Record) (int64, error) {
	b := encodeRecord(r)
	if len(b) > 1<<31 {
		return 0, fmt.Errorf("record too large")
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := rw.w.Write(hdr[:]); err != nil {
		return 0, err
	}
	if _, err := rw.w.Write(b); err != nil {
		return 0, err
	}
	off := rw.off
	rw.off += int64(len(hdr)) + int64(len(b))
	return off, nil
}

func (rw *recordWriter) Close() error {
	ferr := rw.w.Flush()
	cerr := rw.f.Close()
	if ferr != nil {
		return ferr
	}
	return cerr
}

func writeRecord(f *os.File, r Record) (int64, error) {
	off, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	b := encodeRecord(r)
	if len(b) > 1<<31 {
		return 0, fmt.Errorf("record too large")
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := f.Write(hdr[:]); err != nil {
		return 0, err
	}
	_, err = f.Write(b)
	return off, err
}

func readRecordAt(f *os.File, off int64) (Record, error) {
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return Record{}, err
	}
	return readRecord(f)
}

func eachRecord(path string, fn func(Record)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 1024*1024)
	for {
		rec, err := readRecord(r)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
		fn(rec)
	}
}

func readRecord(r io.Reader) (Record, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Record{}, err
	}
	b := make([]byte, binary.LittleEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(r, b); err != nil {
		return Record{}, err
	}
	return decodeRecord(b)
}

func encodeRecord(r Record) []byte {
	b := make([]byte, 0, len(r.Key)+len(r.SourcePath)+len(r.Role)+len(r.Text)+32)
	b = appendField(b, r.Key)
	b = appendField(b, r.SourcePath)
	b = appendField(b, r.Role)
	b = binary.LittleEndian.AppendUint64(b, uint64(r.Time.UnixNano()))
	b = appendField(b, r.Text)
	return b
}

func appendField(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func decodeRecord(b []byte) (Record, error) {
	var rec Record
	var ok bool
	if rec.Key, b, ok = consumeField(b); !ok {
		return rec, io.ErrUnexpectedEOF
	}
	if rec.SourcePath, b, ok = consumeField(b); !ok {
		return rec, io.ErrUnexpectedEOF
	}
	if rec.Role, b, ok = consumeField(b); !ok {
		return rec, io.ErrUnexpectedEOF
	}
	if len(b) < 8 {
		return rec, io.ErrUnexpectedEOF
	}
	rec.Time = time.Unix(0, int64(binary.LittleEndian.Uint64(b[:8])))
	b = b[8:]
	if rec.Text, _, ok = consumeField(b); !ok {
		return rec, io.ErrUnexpectedEOF
	}
	return rec, nil
}

func consumeField(b []byte) (string, []byte, bool) {
	n, used := binary.Uvarint(b)
	if used <= 0 || uint64(len(b)-used) < n {
		return "", nil, false
	}
	start := used
	end := start + int(n)
	return string(b[start:end]), b[end:], true
}

func postingsFor(dir, tok string) ([]posting, error) {
	return readBucketToken(filepath.Join(dir, "buckets", bucket(tok)+".bin"), tok)
}

func intersectPostings(dir string, keys []string) ([]posting, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	lists := make([][]posting, 0, len(keys))
	for _, key := range keys {
		list, err := postingsFor(dir, key)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, nil
		}
		lists = append(lists, list)
	}
	sort.Slice(lists, func(i, j int) bool { return len(lists[i]) < len(lists[j]) })
	set := make(map[int64]posting, len(lists[0]))
	for _, p := range lists[0] {
		set[p.Off] = p
	}
	for _, list := range lists[1:] {
		next := make(map[int64]posting, min(len(set), len(list)))
		for _, p := range list {
			if _, ok := set[p.Off]; ok {
				next[p.Off] = p
			}
		}
		set = next
		if len(set) == 0 {
			return nil, nil
		}
	}
	out := make([]posting, 0, len(set))
	for _, p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Off < out[j].Off })
	return out, nil
}

func intersectSubstringPostings(dir string, bare []string) ([]posting, error) {
	if len(bare) == 0 {
		return nil, nil
	}
	if len(bare) > 3 {
		bare = bare[:3] // longest-first; keep the expansion bounded
	}
	buckets, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	perTok := make([]map[int64]posting, len(bare))
	for i := range perTok {
		perTok[i] = map[int64]posting{}
	}
	for _, de := range buckets {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".bin") {
			continue
		}
		path := filepath.Join(dir, "buckets", de.Name())
		entries, f, err := openBucketDir(path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			tok := strings.TrimPrefix(e.tok, "t")
			for i, b := range bare {
				if !strings.Contains(tok, b) {
					continue
				}
				buf := make([]byte, e.n)
				if _, err := f.ReadAt(buf, int64(e.off)); err != nil {
					continue
				}
				for _, p := range decodePostings(buf) {
					perTok[i][p.Off] = p
				}
			}
		}
		f.Close()
	}
	set := perTok[0]
	for _, m := range perTok[1:] {
		next := map[int64]posting{}
		for off, p := range m {
			if _, ok := set[off]; ok {
				next[off] = p
			}
		}
		set = next
		if len(set) == 0 {
			return nil, nil
		}
	}
	out := make([]posting, 0, len(set))
	for _, p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Off < out[j].Off })
	return out, nil
}

func tokens(s string) []string {
	seen := map[string]bool{}
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() >= 2 {
			t := b.String()
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
		b.Reset()
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b.WriteRune(r)
			if b.Len() > 64 {
				flush()
			}
		} else {
			flush()
		}
	}
	flush()
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func indexKeys(s string) []string {
	var out []string
	for _, tok := range tokens(s) {
		out = append(out, "t"+tok)
	}
	return out
}

func retrievalKeys(keys []string) []string {
	if len(keys) <= 3 {
		return keys
	}
	return keys[:3] // tokens() sorts longest-first; long tokens are the most selective
}

func queryKeys(s string) []string {
	toks := tokens(s)
	if len(toks) == 0 {
		return nil
	}
	out := make([]string, 0, len(toks))
	for _, tok := range toks {
		out = append(out, "t"+tok)
	}
	return out
}

func recordMatchesQuery(r Record, o search.Options) bool {
	if o.Regex {
		return true
	}
	toks := tokens(o.Query)
	if len(toks) == 0 {
		return true
	}
	text := strings.ToLower(r.Text)
	for _, tok := range toks {
		if !strings.Contains(text, tok) {
			return false
		}
	}
	return true
}

func bucket(tok string) string {
	if len(tok) >= 2 {
		return safe(tok[:2])
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(tok))
	return fmt.Sprintf("x%02x", h.Sum32()%256)
}
func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
}

func readManifest(dir string) (Manifest, error) {
	var core manifestCore
	if err := readGob(filepath.Join(dir, "manifest.gob"), &core); err != nil {
		return Manifest{}, err
	}
	m := Manifest{Version: core.Version, Files: core.Files, BuiltAt: core.BuiltAt, Scope: core.Scope, Redacted: core.Redacted, ExportWatermarks: core.ExportWatermarks, ImportedRecords: core.ImportedRecords, RecordsSize: core.RecordsSize, Sessions: map[string]SessionMeta{}}
	if err := readGob(filepath.Join(dir, "sessions.gob"), &m.Sessions); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// writeManifest commits the two-file manifest crash-safely. sessions.gob is
// written (and renamed into place) before manifest.gob, and both go through a
// temp file + rename. manifest.gob carries the version/file sizes that decide
// whether the index is fresh, so it must land last: a crash between the two
// leaves the old manifest pointing at old data, and the next run reindexes
// rather than serving a fresh-looking index whose sessions are stale.
func writeManifest(dir string, m Manifest) error {
	core := manifestCore{Version: m.Version, Files: m.Files, BuiltAt: m.BuiltAt, Scope: m.Scope, Redacted: m.Redacted, ExportWatermarks: m.ExportWatermarks, ImportedRecords: m.ImportedRecords}
	if fi, err := os.Stat(filepath.Join(dir, "records.bin")); err == nil {
		core.RecordsSize = fi.Size()
	}
	if err := writeGobAtomic(filepath.Join(dir, "sessions.gob"), m.Sessions); err != nil {
		return err
	}
	return writeGobAtomic(filepath.Join(dir, "manifest.gob"), core)
}

// recordsIntact reports whether records.bin still holds everything the manifest
// committed. A shorter file means a crash truncated the record log; the index
// must rebuild rather than silently return fewer messages.
func recordsIntact(dir string, m Manifest) bool {
	if m.RecordsSize <= 0 {
		return true // empty index, or one written before the size stamp existed
	}
	fi, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil {
		return false
	}
	return fi.Size() >= m.RecordsSize
}

type bucketEntry struct {
	tok string
	off uint64
	n   uint32
}

func writeBucket(p string, data map[string][]posting) error {
	toks := make([]string, 0, len(data))
	for tok := range data {
		toks = append(toks, tok)
	}
	sort.Strings(toks)
	encoded := make(map[string][]byte, len(toks))
	dirLen := len(bucketMagic) + uvarintLen(uint64(len(toks)))
	for _, tok := range toks {
		dirLen += uvarintLen(uint64(len(tok))) + len(tok) + 8 + 4
		encoded[tok] = encodePostings(data[tok])
	}
	entries := make([]bucketEntry, 0, len(toks))
	pos := uint64(dirLen)
	for _, tok := range toks {
		b := encoded[tok]
		entries = append(entries, bucketEntry{tok: tok, off: pos, n: uint32(len(b))})
		pos += uint64(len(b))
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriterSize(f, 1<<20)
	if _, err := w.Write(bucketMagic); err != nil {
		return err
	}
	var scratch [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(scratch[:], uint64(len(entries)))
	if _, err := w.Write(scratch[:n]); err != nil {
		return err
	}
	for _, e := range entries {
		n = binary.PutUvarint(scratch[:], uint64(len(e.tok)))
		if _, err := w.Write(scratch[:n]); err != nil {
			return err
		}
		if _, err := w.Write([]byte(e.tok)); err != nil {
			return err
		}
		var fixed [12]byte
		binary.LittleEndian.PutUint64(fixed[:8], e.off)
		binary.LittleEndian.PutUint32(fixed[8:], e.n)
		if _, err := w.Write(fixed[:]); err != nil {
			return err
		}
	}
	for _, tok := range toks {
		if _, err := w.Write(encoded[tok]); err != nil {
			return err
		}
	}
	return w.Flush()
}

func readBucket(p string) (map[string][]posting, error) {
	entries, f, err := openBucketDir(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := make(map[string][]posting, len(entries))
	for _, e := range entries {
		b := make([]byte, e.n)
		if _, err := f.ReadAt(b, int64(e.off)); err != nil {
			return nil, err
		}
		out[e.tok] = decodePostings(b)
	}
	return out, nil
}

func readBucketToken(p, tok string) ([]posting, error) {
	entries, f, err := openBucketDir(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	for _, e := range entries {
		if e.tok != tok {
			continue
		}
		b := make([]byte, e.n)
		if _, err := f.ReadAt(b, int64(e.off)); err != nil {
			return nil, err
		}
		return decodePostings(b), nil
	}
	return nil, nil
}

func openBucketDir(p string) ([]bucketEntry, *os.File, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, nil, err
	}
	r := bufio.NewReader(f)
	magic := make([]byte, len(bucketMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
	}
	if string(magic) != string(bucketMagic) {
		f.Close()
		return nil, nil, fmt.Errorf("%w: bad bucket magic", errCorruptIndex)
	}
	count, err := binary.ReadUvarint(r)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
	}
	entries := make([]bucketEntry, 0, count)
	for i := uint64(0); i < count; i++ {
		ln, err := binary.ReadUvarint(r)
		if err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
		}
		tb := make([]byte, ln)
		if _, err := io.ReadFull(r, tb); err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
		}
		var fixed [12]byte
		if _, err := io.ReadFull(r, fixed[:]); err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("%w: %v", errCorruptIndex, err)
		}
		entries = append(entries, bucketEntry{tok: string(tb), off: binary.LittleEndian.Uint64(fixed[:8]), n: binary.LittleEndian.Uint32(fixed[8:])})
	}
	return entries, f, nil
}

func encodePostings(posts []posting) []byte {
	if len(posts) == 0 {
		return nil
	}
	s := sortedUniquePostings(posts)
	b := make([]byte, 0, len(s)*6)
	var prev int64
	for _, p := range s {
		b = binary.AppendUvarint(b, uint64(p.Off-prev))
		b = binary.AppendUvarint(b, uint64(p.Sid))
		prev = p.Off
	}
	return b
}

func decodePostings(b []byte) []posting {
	out := make([]posting, 0)
	var prev int64
	for len(b) > 0 {
		d, n := binary.Uvarint(b)
		if n <= 0 {
			return out
		}
		prev += int64(d)
		b = b[n:]
		sid, n := binary.Uvarint(b)
		if n <= 0 {
			return out
		}
		out = append(out, posting{Off: prev, Sid: uint32(sid)})
		b = b[n:]
	}
	return out
}

func uvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}
func writeGob(p string, v any) error {
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return gob.NewEncoder(f).Encode(v)
}

// writeGobAtomic writes to a sibling temp file and renames it over p, so a
// crash mid-write can never leave p half-decoded.
func writeGobAtomic(p string, v any) error {
	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(f).Encode(v); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}
func readGob(p string, v any) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := gob.NewDecoder(f).Decode(v); err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(p), err)
	}
	return nil
}
