package index

import (
	"bufio"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
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
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const version = 5
const maxIndexedText = 64 * 1024

var lastIngestFiles int

type FileState struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	MTime       int64  `json:"mtime"`
	LastUpdated int64  `json:"last_updated,omitempty"`
}

type SessionMeta struct {
	ID, Harness, Project, Path, Title string
	Started, Updated                  time.Time
}

type Manifest struct {
	Version  int                    `json:"version"`
	Files    map[string]FileState   `json:"files"`
	Sessions map[string]SessionMeta `json:"sessions"`
	BuiltAt  time.Time              `json:"built_at"`
	Scope    string                 `json:"scope"`
}

type Record struct {
	Key        string
	SourcePath string
	Role       string
	Text       string
	Time       time.Time
	LowerText  string `json:"-"`
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
	want := currentFiles(harness)
	m, err := readManifest(dir)
	if !force && err == nil && manifestFresh(m, want, "") {
		return nil
	}
	return updateIndex(dir, harness, "", want, force, progress)
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
	want := currentFiles(o.Harness)
	scope := scopeFor(o)
	m, err := readManifest(dir)
	if !force && err == nil && manifestFresh(m, want, scope) {
		return nil
	}
	if force || err != nil || m.Version != version || m.Scope != scope {
		if progress != nil {
			fmt.Fprintf(progress, "deja: indexing sessions into %s ...\n", dir)
		}
		return rebuildForSearch(dir, o, scope, want)
	}
	return updateIndex(dir, o.Harness, scope, want, force, progress)
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
		return nil, err
	}
	var offsets []int64
	usedPostings := false
	if !o.Regex {
		if keys := queryKeys(o.Query); len(keys) > 0 {
			if len(keys) == 1 {
				offsets, _ = postingsFor(dir, keys[0])
			} else {
				usedPostings = true
				offsets, err = intersectPostings(dir, keys)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if len(offsets) == 0 {
		if usedPostings {
			return nil, nil
		}
		return scanRecords(dir, m, o, nil)
	}
	return scanRecords(dir, m, o, offsets)
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

func rebuild(dir string, harness string, scope string, files map[string]FileState) error {
	lastIngestFiles = len(files)
	tmp := dir + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o755); err != nil {
		return err
	}
	ss := load(harness)
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Scope: scope}
	recPath := filepath.Join(tmp, "records.bin")
	rf, err := os.Create(recPath)
	if err != nil {
		return err
	}
	buckets, err := indexTextParallel(func(jobs chan<- tokenJob) error {
		for _, s := range ss {
			key := s.Harness + ":" + s.ID
			if old, ok := m.Sessions[key]; ok {
				if s.Started.IsZero() || (!old.Started.IsZero() && old.Started.Before(s.Started)) {
					s.Started = old.Started
				}
				if old.Updated.After(s.Updated) {
					s.Updated = old.Updated
				}
			}
			m.Sessions[key] = metaForSession(s)
			for _, msg := range s.Messages {
				text := msg.Text
				if len(text) > maxIndexedText {
					text = text[:maxIndexedText]
				}
				off, err := writeRecord(rf, Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time})
				if err != nil {
					return err
				}
				jobs <- tokenJob{text: msg.Text, offset: off}
			}
		}
		return nil
	})
	if err != nil {
		rf.Close()
		return err
	}
	if err := rf.Close(); err != nil {
		return err
	}
	if err := writeBucketsConcurrent(filepath.Join(tmp, "buckets"), buckets); err != nil {
		return err
	}
	setOpencodeLastUpdated(m.Files, m.Sessions)
	if err := writeJSON(filepath.Join(tmp, "manifest.json"), m); err != nil {
		return err
	}
	os.RemoveAll(dir)
	return os.Rename(tmp, dir)
}

func load(h string) []model.Session {
	var ss []model.Session
	if h == "" || h == "claude" {
		ss = append(ss, sources.LoadClaude()...)
	}
	if h == "" || h == "codex" {
		ss = append(ss, sources.LoadCodex()...)
	}
	if h == "" || h == "opencode" {
		ss = append(ss, sources.LoadOpencode()...)
	}
	return ss
}

func rebuildForSearch(dir string, o search.Options, scope string, files map[string]FileState) error {
	tmp := dir + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o755); err != nil {
		return err
	}
	ss := load(o.Harness)
	return writeSessions(tmp, dir, ss, files, scope)
}

func writeSessions(tmp, dir string, ss []model.Session, files map[string]FileState, scope string) error {
	lastIngestFiles = len(files)
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Scope: scope}
	recPath := filepath.Join(tmp, "records.bin")
	rf, err := os.Create(recPath)
	if err != nil {
		return err
	}
	buckets, err := indexTextParallel(func(jobs chan<- tokenJob) error {
		for _, s := range ss {
			key := s.Harness + ":" + s.ID
			if old, ok := m.Sessions[key]; ok {
				if s.Started.IsZero() || (!old.Started.IsZero() && old.Started.Before(s.Started)) {
					s.Started = old.Started
				}
				if old.Updated.After(s.Updated) {
					s.Updated = old.Updated
				}
			}
			m.Sessions[key] = metaForSession(s)
			for _, msg := range s.Messages {
				text := msg.Text
				if len(text) > maxIndexedText {
					text = text[:maxIndexedText]
				}
				off, err := writeRecord(rf, Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time})
				if err != nil {
					return err
				}
				jobs <- tokenJob{text: text, offset: off}
			}
		}
		return nil
	})
	if err != nil {
		rf.Close()
		return err
	}
	if err := rf.Close(); err != nil {
		return err
	}
	if err := writeBucketsConcurrent(filepath.Join(tmp, "buckets"), buckets); err != nil {
		return err
	}
	setOpencodeLastUpdated(m.Files, m.Sessions)
	if err := writeJSON(filepath.Join(tmp, "manifest.json"), m); err != nil {
		return err
	}
	os.RemoveAll(dir)
	return os.Rename(tmp, dir)
}

type tokenJob struct {
	text   string
	offset int64
}

type bucketPostings map[string]map[string][]int64

func indexTextParallel(feed func(chan<- tokenJob) error) (bucketPostings, error) {
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan tokenJob, workers*256)
	partials := make([]bucketPostings, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		i := i
		partials[i] = bucketPostings{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				addIndexKeys(partials[i], job.text, job.offset)
			}
		}()
	}
	err := feed(jobs)
	close(jobs)
	wg.Wait()
	if err != nil {
		return nil, err
	}
	merged := bucketPostings{}
	for _, part := range partials {
		for b, toks := range part {
			if merged[b] == nil {
				merged[b] = map[string][]int64{}
			}
			for tok, offsets := range toks {
				merged[b][tok] = append(merged[b][tok], offsets...)
			}
		}
	}
	return merged, nil
}

func addIndexKeys(buckets bucketPostings, text string, off int64) {
	seen := map[string]bool{}
	for _, tok := range indexKeys(text) {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		b := bucket(tok)
		if buckets[b] == nil {
			buckets[b] = map[string][]int64{}
		}
		buckets[b][tok] = append(buckets[b][tok], off)
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
		data map[string][]int64
	}
	jobs := make(chan bucketWrite)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if err := writeGob(filepath.Join(dir, job.name+".gob"), job.data); err != nil {
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

func metaForSession(s model.Session) SessionMeta {
	return SessionMeta{ID: s.ID, Harness: s.Harness, Project: s.Project, Path: s.Path, Title: sessionTitle(s), Started: s.Started, Updated: s.Updated}
}

func sessionFromMeta(meta SessionMeta) model.Session {
	return model.Session{ID: meta.ID, Harness: meta.Harness, Project: meta.Project, Path: meta.Path, Title: meta.Title, Started: meta.Started, Updated: meta.Updated}
}

func sessionTitle(s model.Session) string {
	for _, msg := range s.Messages {
		if msg.Role == "user" {
			return truncateTitle(msg.Text, 60)
		}
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

func updateIndex(dir, harness, scope string, files map[string]FileState, force bool, progress io.Writer) error {
	old, err := readManifest(dir)
	if force || err != nil || old.Version != version || old.Scope != scope {
		if progress != nil {
			fmt.Fprintf(progress, "deja: indexing sessions into %s ...\n", dir)
		}
		return rebuild(dir, harness, scope, files)
	}
	changed := map[string]FileState{}
	removed := map[string]bool{}
	for p, f := range files {
		if of, ok := old.Files[p]; !ok || !sameFile(of, f) {
			changed[p] = f
		}
	}
	for p := range old.Files {
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
		if err != nil {
			return err
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
			return err
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
	m := Manifest{Version: version, Files: files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Scope: scope}
	buckets := bucketPostings{}
	addRec := func(r Record) error {
		if r.SourcePath == "" {
			return nil
		}
		off, err := writeRecord(rf, r)
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
				buckets[b] = map[string][]int64{}
			}
			buckets[b][tok] = append(buckets[b][tok], off)
		}
		if meta, ok := old.Sessions[r.Key]; ok {
			if _, exists := m.Sessions[r.Key]; exists {
				return nil
			}
			m.Sessions[r.Key] = meta
		}
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
		rf.Close()
		return err
	}
	if recErr != nil {
		rf.Close()
		return recErr
	}
	for _, s := range replacements {
		key := s.Harness + ":" + s.ID
		m.Sessions[key] = metaForSession(s)
		for _, msg := range s.Messages {
			text := msg.Text
			if len(text) > maxIndexedText {
				text = text[:maxIndexedText]
			}
			if err := addRec(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time}); err != nil {
				rf.Close()
				return err
			}
		}
	}
	if err := rf.Close(); err != nil {
		return err
	}
	if err := writeBucketsConcurrent(filepath.Join(tmp, "buckets"), buckets); err != nil {
		return err
	}
	setOpencodeLastUpdated(m.Files, m.Sessions)
	if err := writeJSON(filepath.Join(tmp, "manifest.json"), m); err != nil {
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
		case "claude", "codex", "codex-history", "opencode":
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
	defer rf.Close()
	if _, err := rf.Seek(0, io.SeekEnd); err != nil {
		return 0, 0, err
	}
	buckets := bucketPostings{}
	loadBucket := func(tok string) (map[string][]int64, error) {
		b := bucket(tok)
		if data, ok := buckets[b]; ok {
			return data, nil
		}
		data := map[string][]int64{}
		p := filepath.Join(dir, "buckets", b+".gob")
		if err := readGob(p, &data); err != nil && !os.IsNotExist(err) {
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
	if m.Sessions == nil {
		m.Sessions = map[string]SessionMeta{}
	}
	filesTouched, messages := 0, 0
	for p := range changed {
		ss, err := parseAppendedFile(harness, p, old.Files[p])
		if err != nil {
			return filesTouched, messages, err
		}
		filesTouched++
		for _, s := range ss {
			key := s.Harness + ":" + s.ID
			meta := m.Sessions[key]
			if meta.ID == "" {
				meta = metaForSession(s)
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
				off, err := writeRecord(rf, Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time})
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
					data[tok] = append(data[tok], off)
				}
			}
		}
	}
	if err := rf.Close(); err != nil {
		return filesTouched, messages, err
	}
	if err := writeBucketsConcurrent(filepath.Join(dir, "buckets"), buckets); err != nil {
		return filesTouched, messages, err
	}
	setOpencodeLastUpdated(m.Files, m.Sessions)
	if err := writeJSON(filepath.Join(dir, "manifest.json"), m); err != nil {
		return filesTouched, messages, err
	}
	return filesTouched, messages, nil
}

func sameFile(a, b FileState) bool { return a.Path == b.Path && a.Size == b.Size && a.MTime == b.MTime }

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
	default:
		return nil, nil
	}
}

func parseAppendedFile(harness, p string, old FileState) ([]model.Session, error) {
	switch harnessForPath(p) {
	case "claude":
		return sources.ParseClaudeFileFromOffset(p, old.Size)
	case "codex-history":
		return sources.ParseCodexHistoryFromOffset(p, old.Size)
	case "codex":
		return sources.ParseCodexRolloutFromOffset(p, old.Size)
	case "opencode":
		if old.LastUpdated > 0 {
			return sources.ParseOpencodeDBSince(p, time.Unix(0, old.LastUpdated))
		}
		return sources.ParseOpencodeDB(p)
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
	return ""
}

func setOpencodeLastUpdated(files map[string]FileState, sessions map[string]SessionMeta) {
	db := sources.OpencodeDB()
	f, ok := files[db]
	if !ok {
		return
	}
	var latest int64
	for _, s := range sessions {
		if s.Harness == "opencode" && s.Updated.UnixNano() > latest {
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
		addWalk(sources.ClaudeRoot(), func(p string) bool { return strings.HasSuffix(p, ".jsonl") })
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
	out := map[string]FileState{}
	for p := range paths {
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink == 0 && !fi.IsDir() {
			out[p] = FileState{Path: p, Size: fi.Size(), MTime: fi.ModTime().UnixNano()}
		}
	}
	return out
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

func scopeFor(o search.Options) string {
	return "h:" + o.Harness
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
			cp := model.Session{ID: meta.ID, Harness: meta.Harness, Project: meta.Project, Path: meta.Path, Started: meta.Started, Updated: meta.Updated}
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
		defer f.Close()
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

func writeRecord(f *os.File, r Record) (int64, error) {
	off, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return 0, err
	}
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
	return readRecord(bufio.NewReader(f))
}

func eachRecord(path string, fn func(Record)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
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
	var rec Record
	return rec, json.Unmarshal(b, &rec)
}

func postingsFor(dir, tok string) ([]int64, error) {
	var data map[string][]int64
	if err := readGob(filepath.Join(dir, "buckets", bucket(tok)+".gob"), &data); err != nil {
		return nil, err
	}
	return data[tok], nil
}

func intersectPostings(dir string, keys []string) ([]int64, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	lists := make([][]int64, 0, len(keys))
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
	set := make(map[int64]struct{}, len(lists[0]))
	for _, off := range lists[0] {
		set[off] = struct{}{}
	}
	for _, list := range lists[1:] {
		next := make(map[int64]struct{}, min(len(set), len(list)))
		for _, off := range list {
			if _, ok := set[off]; ok {
				next[off] = struct{}{}
			}
		}
		set = next
		if len(set) == 0 {
			return nil, nil
		}
	}
	out := make([]int64, 0, len(set))
	for off := range set {
		out = append(out, off)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
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
	var m Manifest
	err := readJSON(filepath.Join(dir, "manifest.json"), &m)
	return m, err
}
func writeJSON(p string, v any) error {
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(v)
}
func readJSON(p string, v any) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}
func writeGob(p string, v any) error {
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return gob.NewEncoder(f).Encode(v)
}
func readGob(p string, v any) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return gob.NewDecoder(f).Decode(v)
}
