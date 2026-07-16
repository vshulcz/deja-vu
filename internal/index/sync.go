package index

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/redact"
)

type SyncRecord struct {
	Harness   string    `json:"harness"`
	SessionID string    `json:"session_id"`
	Project   string    `json:"project"`
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	Time      time.Time `json:"time"`
}

// Export writes records newer than the per-source watermarks. ExportFull
// ignores watermarks so a fresh machine can receive the whole history even
// after earlier batch dirs are gone; import-side dedupe makes it safe.
func Export(dir, outDir string) (int, error) {
	return exportRecords(dir, outDir, false)
}

func ExportFull(dir, outDir string) (int, error) {
	return exportRecords(dir, outDir, true)
}

func exportRecords(dir, outDir string, full bool) (int, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return 0, err
	}
	defer unlock()
	m, err := readManifest(dir)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, err
	}
	if m.ExportWatermarks == nil {
		m.ExportWatermarks = map[string]int64{}
	}
	bySource := map[string][]SyncRecord{}
	nextWatermarks := map[string]int64{}
	for k, v := range m.ExportWatermarks {
		nextWatermarks[k] = v
	}
	err = eachRecord(filepath.Join(dir, "records.bin"), func(r Record) {
		if r.SourcePath == syncImportPath {
			return
		}
		source := r.SourcePath
		if source == "" {
			source = r.Key
		}
		if !full && !r.Time.IsZero() && r.Time.UnixNano() <= m.ExportWatermarks[source] {
			return
		}
		meta, ok := m.Sessions[r.Key]
		if !ok {
			return
		}
		text, _ := redact.Text(r.Text)
		rec := SyncRecord{Harness: meta.Harness, SessionID: meta.ID, Project: meta.Project, Role: r.Role, Text: text, Time: r.Time}
		bySource[source] = append(bySource[source], rec)
		if r.Time.UnixNano() > nextWatermarks[source] {
			nextWatermarks[source] = r.Time.UnixNano()
		}
	})
	if err != nil {
		return 0, err
	}
	total := 0
	for source, recs := range bySource {
		if len(recs) == 0 {
			continue
		}
		name := fmt.Sprintf("deja-sync-%s-%d.jsonl", shortHash(source), time.Now().UnixNano())
		f, err := os.Create(filepath.Join(outDir, name))
		if err != nil {
			return total, err
		}
		enc := json.NewEncoder(f)
		for _, rec := range recs {
			if err := enc.Encode(rec); err != nil {
				_ = f.Close()
				return total, err
			}
			total++
		}
		if err := f.Close(); err != nil {
			return total, err
		}
	}
	for source, wm := range nextWatermarks {
		m.ExportWatermarks[source] = wm
	}
	if err := writeManifest(dir, m); err != nil {
		return total, err
	}
	return total, nil
}

func Import(dir, inDir string) (int, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if !HasManifest(dir) {
		if err := initEmptyIndex(dir); err != nil {
			return 0, err
		}
	}
	m, err := readManifest(dir)
	if err != nil {
		return 0, err
	}
	if m.ImportedRecords == nil {
		m.ImportedRecords = map[string]bool{}
	}
	paths, err := filepath.Glob(filepath.Join(inDir, "*.jsonl"))
	if err != nil {
		return 0, err
	}
	sort.Strings(paths)
	recsByKey := map[string][]Record{}
	metas := map[string]SessionMeta{}
	added := 0
	for _, p := range paths {
		if err := readSyncFile(p, func(sr SyncRecord) error {
			origID := sr.SessionID
			if sr.Harness == "" || origID == "" {
				return nil
			}
			// Key includes role and a text hash: two messages can legally share
			// a timestamp (aider stamps a whole session with its start time).
			// The legacy time-only key is still honored so batches imported by
			// older versions stay idempotent.
			legacy := sr.Harness + ":" + origID + ":" + sr.Time.UTC().Format(time.RFC3339Nano)
			th := fnv.New64a()
			_, _ = th.Write([]byte(sr.Text))
			dedupe := legacy + ":" + sr.Role + ":" + strconv.FormatUint(th.Sum64(), 16)
			if m.ImportedRecords[dedupe] || m.ImportedRecords[legacy] {
				return nil
			}
			importID := ImportedSessionID(sr.Harness, origID)
			key := sr.Harness + ":" + importID
			text, _ := redact.Text(sr.Text)
			recsByKey[key] = append(recsByKey[key], Record{Key: key, Role: sr.Role, Text: text, Time: sr.Time, SourcePath: syncImportPath})
			meta := metas[key]
			if meta.ID == "" {
				meta = SessionMeta{ID: importID, Harness: sr.Harness, Project: "imported:" + sr.Project, Path: syncImportPath}
			}
			if meta.Started.IsZero() || (!sr.Time.IsZero() && sr.Time.Before(meta.Started)) {
				meta.Started = sr.Time
			}
			if sr.Time.After(meta.Updated) {
				meta.Updated = sr.Time
			}
			metas[key] = meta
			m.ImportedRecords[dedupe] = true
			added++
			return nil
		}); err != nil {
			return added, err
		}
	}
	if added == 0 {
		return 0, writeManifest(dir, m)
	}
	if err := appendImportedRecords(dir, &m, recsByKey, metas); err != nil {
		return added, err
	}
	return added, nil
}

func initEmptyIndex(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "records.bin"))
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	m := Manifest{Version: version, Files: currentFiles(""), Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), ExportWatermarks: map[string]int64{}, ImportedRecords: map[string]bool{}}
	return writeManifest(dir, m)
}

func readSyncFile(path string, fn func(SyncRecord) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for s.Scan() {
		var rec SyncRecord
		if err := json.Unmarshal(s.Bytes(), &rec); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		if err := fn(rec); err != nil {
			return err
		}
	}
	if err := s.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func appendImportedRecords(dir string, m *Manifest, recsByKey map[string][]Record, metas map[string]SessionMeta) error {
	rf, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	rw, err := newRecordWriter(rf)
	if err != nil {
		_ = rf.Close()
		return err
	}
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
	keys := make([]string, 0, len(recsByKey))
	for k := range recsByKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		meta := metas[key]
		old := m.Sessions[key]
		if old.ID != "" {
			meta.Ord = old.Ord
			if old.Started.Before(meta.Started) || meta.Started.IsZero() {
				meta.Started = old.Started
			}
			if old.Updated.After(meta.Updated) {
				meta.Updated = old.Updated
			}
		} else {
			meta.Ord = nextSessionOrd(m.Sessions)
		}
		m.Sessions[key] = meta
		for _, r := range recsByKey[key] {
			off, err := rw.write(r)
			if err != nil {
				_ = rw.Close()
				return err
			}
			for _, tok := range indexKeys(r.Text) {
				data, err := loadBucket(tok)
				if err != nil {
					_ = rw.Close()
					return err
				}
				data[tok] = append(data[tok], posting{Off: off, Sid: meta.Ord})
			}
		}
	}
	if err := rw.Close(); err != nil {
		return err
	}
	if err := writeBucketsConcurrent(filepath.Join(dir, "buckets"), buckets); err != nil {
		return err
	}
	m.BuiltAt = time.Now()
	return writeManifest(dir, *m)
}

func shortHash(s string) string {
	h := sha1.Sum([]byte(s))
	return strings.TrimLeft(hex.EncodeToString(h[:])[:12], "-")
}

func ImportedSessionID(harness, sessionID string) string {
	return "imported-" + shortHash(harness+":"+sessionID)
}
