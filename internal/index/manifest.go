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

// OlderFormat reports that the index on disk reads, and was written by a
// format this build no longer understands. Distinct from IsCurrentVersion,
// which cannot tell an old index from an unreadable one — and doctor must not
// call a corrupt manifest "an older deja" (#877).
func OlderFormat(dir string) bool { return FormatDirection(dir) != 0 }

// FormatDirection reports how the index on disk relates to this build's
// format: -1 when it was written by an older deja, +1 by a newer one, 0 when
// it matches or cannot be read.
//
// The direction matters because the fix differs. An older index is rebuilt and
// forgotten; a newer one means the binary was rolled back — a bad release
// reverted, `brew switch`, a pinned version in CI — and telling that reader
// their index is old sends them looking in the wrong place (#890).
func FormatDirection(dir string) int {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return 0
	}
	switch {
	case m.Version < version:
		return -1
	case m.Version > version:
		return 1
	}
	return 0
}

// ImportedSessionCounts reports how many of each harness's indexed sessions
// arrived from another machine. doctor prints files beside sessions, and on a
// store that has both, more sessions than files reads as a miscount unless the
// difference is named (#894).
func ImportedSessionCounts(dir string) map[string]int {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return nil
	}
	out := map[string]int{}
	for _, meta := range m.Sessions {
		if strings.HasPrefix(meta.ID, "imported-") {
			out[meta.Harness]++
		}
	}
	return out
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

// HarnessSessionCounts reports how many indexed sessions each harness holds.
//
// doctor counts transcript files; the number of sessions those files became is
// the other half of the same question, and until now no single command showed
// both. A harness whose ids collide folds many files into one session — 811
// codex rollouts into 74 sessions in #635 — and every doctor row still read as
// a healthy store.
func HarnessSessionCounts(dir string) map[string]int {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return nil
	}
	out := map[string]int{}
	for _, meta := range m.Sessions {
		out[meta.Harness]++
	}
	return out
}

// HarnessSharedCounts reports, per harness, how many manifest rows cover more
// than one transcript. doctor's row shows the files-to-sessions gap; without
// this it cannot say whether the gap is a parse failure or an id collision
// (#1101, the two causes #861 and #970 left indistinguishable).
func HarnessSharedCounts(dir string) map[string]int {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return nil
	}
	out := map[string]int{}
	for _, meta := range m.Sessions {
		if meta.Shared {
			out[meta.Harness]++
		}
	}
	return out
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
	m := Manifest{Version: core.Version, Files: core.Files, BuiltAt: core.BuiltAt, Generation: core.Generation, Scope: core.Scope, Redacted: core.Redacted, RedactionRules: core.RedactionRules, ExportWatermarks: core.ExportWatermarks, ExportBoundary: core.ExportBoundary, ImportedRecords: core.ImportedRecords, RecordStrings: core.RecordStrings, RecordsSize: core.RecordsSize, BucketFiles: core.BucketFiles, IngestHealth: core.IngestHealth, Sessions: map[string]SessionMeta{}}
	if err := readGob(filepath.Join(dir, "sessions.gob"), &m.Sessions); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// writeManifestOnly persists manifest.gob without rewriting sessions.gob —
// for updates that change only core fields (e.g. export watermarks) where the
// caller has not loaded sessions and must not clobber them.
func writeManifestOnly(dir string, m Manifest) error {
	core := manifestCore{Version: m.Version, Files: m.Files, BuiltAt: m.BuiltAt, Generation: m.Generation, Scope: m.Scope, Redacted: m.Redacted, RedactionRules: m.RedactionRules, ExportWatermarks: m.ExportWatermarks, ExportBoundary: m.ExportBoundary, ImportedRecords: m.ImportedRecords, RecordStrings: m.RecordStrings, IngestHealth: m.IngestHealth}
	if fi, err := os.Stat(filepath.Join(dir, "records.bin")); err == nil {
		core.RecordsSize = fi.Size()
	}
	core.BucketFiles = countBucketFiles(filepath.Join(dir, "buckets"))
	return writeGobAtomic(filepath.Join(dir, "manifest.gob"), core)
}

// writeManifest commits the two-file manifest crash-safely. sessions.gob is
// written (and renamed into place) before manifest.gob, and both go through a
// temp file + rename. manifest.gob carries the version/file sizes that decide
// whether the index is fresh, so it must land last: a crash between the two
// leaves the old manifest pointing at old data, and the next run reindexes
// rather than serving a fresh-looking index whose sessions are stale.
func writeManifest(dir string, m Manifest) error {
	mergeIngestDiag(&m)
	core := manifestCore{Version: m.Version, Files: m.Files, BuiltAt: m.BuiltAt, Generation: m.Generation, Scope: m.Scope, Redacted: m.Redacted, RedactionRules: m.RedactionRules, ExportWatermarks: m.ExportWatermarks, ExportBoundary: m.ExportBoundary, ImportedRecords: m.ImportedRecords, RecordStrings: m.RecordStrings, IngestHealth: m.IngestHealth}
	if fi, err := os.Stat(filepath.Join(dir, "records.bin")); err == nil {
		core.RecordsSize = fi.Size()
	}
	core.BucketFiles = countBucketFiles(filepath.Join(dir, "buckets"))
	if err := writeGobAtomic(filepath.Join(dir, "sessions.gob"), m.Sessions); err != nil {
		return err
	}
	return writeGobAtomic(filepath.Join(dir, "manifest.gob"), core)
}

// hasBucketFile reports whether the postings directory holds anything at all.
// It stops at the first entry: the question is emptiness, not the count.
func hasBucketFile(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	names, err := f.Readdirnames(1)
	return err == nil && len(names) > 0
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
	// up to date while every search answers with nothing — the one failure a
	// memory tool must not present as an empty result, because the reader
	// concludes it is useless rather than broken.
	//
	// This check is the detector. The wording of the empty-result message
	// never was: it said "no matches in 0 indexed sessions" on every ordinary
	// miss too, and since #637 it reports the manifest count instead.
	if len(m.Sessions) > 0 {
		bi, err := os.Stat(filepath.Join(dir, "buckets"))
		if err != nil || !bi.IsDir() {
			return false
		}
		// An empty directory is the same loss as a missing one, and it is what
		// `rm buckets/*.bin` and a copy that skipped the contents both leave
		// behind: every check passed, `doctor` said "built, up to date", and
		// search answered "no matches in 3 indexed sessions" about text that
		// was still in the record log (#946).
		if !hasBucketFile(filepath.Join(dir, "buckets")) {
			return false
		}
		// Losing part of the directory is the same loss spread thinner, and it
		// is the shape a partial copy or an interrupted sync actually leaves.
		// Every token in a missing file answered "no matches in N indexed
		// sessions" while its text sat in the record log, and `doctor --deep`
		// called that index healthy (#1088). Fewer than committed only: a
		// larger count belongs to a build this manifest does not describe, and
		// the freshness check already covers that.
		if m.BucketFiles > 0 && countBucketFiles(filepath.Join(dir, "buckets")) < m.BucketFiles {
			return false
		}
	}
	return true
}

// countBucketFiles counts the postings files a buckets directory holds.
func countBucketFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".bin") {
			n++
		}
	}
	return n
}

// Damaged reports whether the store on disk still holds what the manifest
// committed — a truncated record log, or postings that went missing to a
// partial copy or an over-eager delete.
//
// The next search rebuilds when this is true, so nothing is lost permanently.
// What was wrong is the answer `doctor` gave: "built, up to date" about a store
// that could not return a single result (#735).
func Damaged(dir string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		// A manifest that is not there is "not built", which doctor reports
		// already. One that is there but will not decode is a different story:
		// doctor stats the file, calls the index built and up to date, and the
		// next search rebuilds it from scratch.
		if _, statErr := os.Stat(filepath.Join(dir, "manifest.gob")); statErr == nil {
			return true
		}
		return false
	}
	return !recordsIntact(dir, m)
}

// RebuildInProgress reports whether another process holds the index lock. A
// rebuild recreates the directory, so a reader can land in a window where the
// manifest does not exist and get the syscall for it — a moment mistaken for a
// broken store (#822).
func RebuildInProgress(dir string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, ok, err := tryLockDir(dir)
	if err != nil {
		return false
	}
	if ok {
		unlock()
		return false
	}
	return true
}

// OverviewStats is what the brief needs about a whole store.
type OverviewStats struct {
	Sessions      int
	Harnesses     int
	SessionsToday int
	SessionsWeek  int
	// Oldest and Newest bound what the index holds. A store whose work is all
	// older than a week has nothing to say under "today" and "this week", and
	// two zero lines is a poor way to open the first screen someone sees.
	Oldest, Newest time.Time
	// Future counts sessions stamped after now. A wrong clock, a transcript
	// written in another timezone, a harness stamping UTC while the reader is
	// behind it — all produce them, and counting them as today's work made the
	// first screen's top line wrong upward and only upward (#696).
	Future int
}

// Overview summarizes the index from manifest metadata alone — no record
// reads — so the zero-argument brief stays instant.
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
	// now bounds both counters from above. Without it every session ahead of
	// the clock was today's, this week's, and the newest thing in the store —
	// permanently (#696). The bound is now rather than tomorrow's midnight on
	// purpose: work that has not happened yet is not today's work either.
	week := now.AddDate(0, 0, -7)
	hs := map[string]bool{}
	for _, meta := range m.Sessions {
		o.Sessions++
		hs[meta.Harness] = true
		if meta.Updated.After(now) {
			o.Future++
		}
		if meta.Updated.After(day) && !meta.Updated.After(now) {
			o.SessionsToday++
		}
		// Started, not Updated: the earliest bound is when the oldest work
		// began. A session that ran from January to March last touched the
		// index in March, and taking that as the floor hid two months of its
		// own transcript (#786).
		from := meta.Started
		if from.IsZero() {
			from = meta.Updated
		}
		if o.Oldest.IsZero() || (!from.IsZero() && from.Before(o.Oldest)) {
			o.Oldest = from
		}
		if meta.Updated.After(o.Newest) {
			o.Newest = meta.Updated
		}
		if meta.Updated.After(week) && !meta.Updated.After(now) {
			o.SessionsWeek++
		}
	}
	o.Harnesses = len(hs)
	return o, nil
}

// AllMeta returns every session's metadata, no records read and no lock taken.
//
// The session-metadata constructors used elsewhere drop Touched, Asked and Hit
// (#633), so a caller that needs those has to see the manifest entries
// themselves. This is the read a status line can afford: one file, no fork.
func AllMeta(dir string) ([]SessionMeta, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(m.Sessions))
	for _, meta := range m.Sessions {
		out = append(out, meta)
	}
	return out, nil
}

// SessionCount reports how many sessions the store holds, without reading a
// record. A caller reporting an empty result needs the size of the corpus, not
// the size of whatever it just loaded.
func SessionCount(dir string) (int, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return 0, err
	}
	return len(m.Sessions), nil
}
