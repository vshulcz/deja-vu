package index

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// IsCurrentVersion reports whether the index on disk was written by this
// build's format. Hook paths never call Ensure, so after an upgrade that
// changes the layout they would read the old store directly — for version 12
// that meant non-ASCII tokens hashing to buckets the old index never wrote,
// i.e. silent zero recall for Russian and CJK while English kept working.
func IsCurrentVersion(dir string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	return err == nil && m.Version == version
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

// ManifestBuiltAt returns when the index was last built. Older manifests may
// omit BuiltAt; in that case manifest.gob's mtime is used.
func ManifestBuiltAt(dir string) time.Time {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err == nil && !m.BuiltAt.IsZero() {
		return m.BuiltAt
	}
	if fi, err := os.Stat(filepath.Join(dir, "manifest.gob")); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

func Redactions(dir string) (RedactionStats, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return RedactionStats{}, err
	}
	out := RedactionStats{Total: m.Redacted, Files: map[string]int{}, Rules: map[string]map[string]int{}}
	for p, f := range m.Files {
		if f.Redactions > 0 {
			out.Files[p] = f.Redactions
		}
	}
	for key, count := range m.RedactionRules {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if out.Rules[parts[0]] == nil {
			out.Rules[parts[0]] = map[string]int{}
		}
		out.Rules[parts[0]][parts[1]] = count
	}
	return out, nil
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

func readManifest(dir string) (Manifest, error) {
	var core manifestCore
	if err := readGob(filepath.Join(dir, "manifest.gob"), &core); err != nil {
		return Manifest{}, err
	}
	m := Manifest{Version: core.Version, Files: core.Files, BuiltAt: core.BuiltAt, Generation: core.Generation, Scope: core.Scope, Redacted: core.Redacted, RedactionRules: core.RedactionRules, ExportWatermarks: core.ExportWatermarks, ExportBoundary: core.ExportBoundary, ImportedRecords: core.ImportedRecords, RecordStrings: core.RecordStrings, RecordsSize: core.RecordsSize, IngestHealth: core.IngestHealth, Sessions: map[string]SessionMeta{}}
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
// writeManifestOnly persists manifest.gob without rewriting sessions.gob —
// for updates that change only core fields (e.g. export watermarks) where the
// caller has not loaded sessions and must not clobber them.
func writeManifestOnly(dir string, m Manifest) error {
	core := manifestCore{Version: m.Version, Files: m.Files, BuiltAt: m.BuiltAt, Generation: m.Generation, Scope: m.Scope, Redacted: m.Redacted, RedactionRules: m.RedactionRules, ExportWatermarks: m.ExportWatermarks, ExportBoundary: m.ExportBoundary, ImportedRecords: m.ImportedRecords, RecordStrings: m.RecordStrings, IngestHealth: m.IngestHealth}
	if fi, err := os.Stat(filepath.Join(dir, "records.bin")); err == nil {
		core.RecordsSize = fi.Size()
	}
	return writeGobAtomic(filepath.Join(dir, "manifest.gob"), core)
}

func writeManifest(dir string, m Manifest) error {
	mergeIngestDiag(&m)
	core := manifestCore{Version: m.Version, Files: m.Files, BuiltAt: m.BuiltAt, Generation: m.Generation, Scope: m.Scope, Redacted: m.Redacted, RedactionRules: m.RedactionRules, ExportWatermarks: m.ExportWatermarks, ExportBoundary: m.ExportBoundary, ImportedRecords: m.ImportedRecords, RecordStrings: m.RecordStrings, IngestHealth: m.IngestHealth}
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
	// Shorter: a crash truncated the log. Longer: a crash landed records the
	// manifest never committed; re-appending them would duplicate messages.
	if fi.Size() != m.RecordsSize {
		return false
	}
	// The postings live in buckets/, and losing that directory is not
	// hypothetical: a partial copy, an interrupted sync, a `find -delete` that
	// caught too much. The manifest survives, so the index reads as built and
	// up to date while every search answers "no matches in 0 indexed sessions"
	// — the one failure a memory tool must not present as an empty result,
	// because the reader concludes it is useless rather than broken.
	if len(m.Sessions) > 0 {
		bi, err := os.Stat(filepath.Join(dir, "buckets"))
		if err != nil || !bi.IsDir() {
			return false
		}
	}
	return true
}

// Overview summarizes the index from manifest metadata alone — no record
// reads — so the zero-argument brief stays instant.
type OverviewStats struct {
	Sessions      int
	Harnesses     int
	SessionsToday int
	SessionsWeek  int
	// Oldest and Newest bound what the index holds. A store whose work is all
	// older than a week has nothing to say under "today" and "this week", and
	// two zero lines is a poor way to open the first screen someone sees.
	Oldest, Newest time.Time
}

func Overview(dir string) (OverviewStats, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return OverviewStats{}, err
	}
	var o OverviewStats
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	week := now.AddDate(0, 0, -7)
	hs := map[string]bool{}
	for _, meta := range m.Sessions {
		o.Sessions++
		hs[meta.Harness] = true
		if meta.Updated.After(day) {
			o.SessionsToday++
		}
		if o.Oldest.IsZero() || (!meta.Updated.IsZero() && meta.Updated.Before(o.Oldest)) {
			o.Oldest = meta.Updated
		}
		if meta.Updated.After(o.Newest) {
			o.Newest = meta.Updated
		}
		if meta.Updated.After(week) {
			o.SessionsWeek++
		}
	}
	o.Harnesses = len(hs)
	return o, nil
}
