package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
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
	// Cached: the hook path calls this next to Damaged and FileSessions in one
	// short-lived process, so decoding the manifest three times per action was
	// pure waste. The cache is keyed on manifest.gob's mtime+size, so a changed
	// or corrupt store still forces a fresh read.
	m, err := readManifestCached(dir)
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

// ImportedByMachine counts indexed sessions per machine they were worked on.
// A peer line that says "last exchange two days ago" and nothing else leaves
// open whether anything ever actually arrived from there.
func ImportedByMachine(dir string) map[string]int {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return nil
	}
	out := map[string]int{}
	for _, meta := range m.Sessions {
		if meta.From != "" {
			out[meta.From]++
		}
	}
	return out
}

// HasManifest reports whether this directory holds an index. A rebuild's swap
// takes the directory away for a moment, and answering "no" there sent every
// hook down the no-index path: it asked for a rebuild that was already running
// and served the prompt without recall (#1319).
func HasManifest(dir string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	if _, err := statIndexFile(filepath.Join(dir, "manifest.gob")); err != nil {
		return false
	}
	// Plain stat: the wait above has already closed the window by the time
	// this runs, and a call site that cannot be made to fail is not a fix.
	_, err := os.Stat(filepath.Join(dir, "sessions.gob"))
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
	if fi, err := statIndexFile(filepath.Join(dir, "manifest.gob")); err == nil {
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
	// A store skipped for a missing CLI leaves the transcripts untouched, so
	// nothing here would notice that the tool has since been installed (#1760).
	if toolsChanged(m) {
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
	m := Manifest{Version: core.Version, Files: core.Files, BuiltAt: core.BuiltAt, Generation: core.Generation, Scope: core.Scope, Redacted: core.Redacted, RedactionRules: core.RedactionRules, ExportWatermarks: core.ExportWatermarks, ExportBoundary: core.ExportBoundary, ImportedRecords: core.ImportedRecords, RecordStrings: core.RecordStrings, RecordsSize: core.RecordsSize, BucketFiles: core.BucketFiles, IngestHealth: core.IngestHealth, IngestFiles: core.IngestFiles, ExcludeFingerprint: core.ExcludeFingerprint, ToolFingerprint: core.ToolFingerprint, Sessions: map[string]SessionMeta{}}
	if err := readGob(filepath.Join(dir, "sessions.gob"), &m.Sessions); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// writeManifestOnly persists manifest.gob without rewriting sessions.gob —
// for updates that change only core fields (e.g. export watermarks) where the
// caller has not loaded sessions and must not clobber them.
func writeManifestOnly(dir string, m Manifest) error {
	core := manifestCore{Version: m.Version, Files: m.Files, BuiltAt: m.BuiltAt, Generation: m.Generation, Scope: m.Scope, Redacted: m.Redacted, RedactionRules: m.RedactionRules, ExportWatermarks: m.ExportWatermarks, ExportBoundary: m.ExportBoundary, ImportedRecords: m.ImportedRecords, RecordStrings: m.RecordStrings, IngestHealth: m.IngestHealth, IngestFiles: m.IngestFiles, ExcludeFingerprint: m.ExcludeFingerprint, ToolFingerprint: m.ToolFingerprint}
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
	core := manifestCore{Version: m.Version, Files: m.Files, BuiltAt: m.BuiltAt, Generation: m.Generation, Scope: m.Scope, Redacted: m.Redacted, RedactionRules: m.RedactionRules, ExportWatermarks: m.ExportWatermarks, ExportBoundary: m.ExportBoundary, ImportedRecords: m.ImportedRecords, RecordStrings: m.RecordStrings, IngestHealth: m.IngestHealth, IngestFiles: m.IngestFiles, ExcludeFingerprint: m.ExcludeFingerprint, ToolFingerprint: m.ToolFingerprint}
	if fi, err := os.Stat(filepath.Join(dir, "records.bin")); err == nil {
		core.RecordsSize = fi.Size()
	}
	core.BucketFiles = countBucketFiles(filepath.Join(dir, "buckets"))
	if err := writeGobAtomic(filepath.Join(dir, "sessions.gob"), m.Sessions); err != nil {
		return err
	}
	return writeGobAtomic(filepath.Join(dir, "manifest.gob"), core)
}

// ExclusionsChanged reports whether the exclude patterns in force differ from
// the ones this index was built under.
//
// They apply at ingest, so setting one today does nothing about what was
// ingested yesterday — and `deja index` returned silently having applied
// nothing, which is the answer that reads most like success (#1307). False
// when the index predates the fingerprint or no patterns are set: an unknown
// state is not a reason to tell someone to rebuild.
func ExclusionsChanged(dir string) bool {
	now := sources.ExclusionFingerprint()
	m, err := readManifestCached(dir)
	if err != nil || m.ExcludeFingerprint == "" {
		return false
	}
	return m.ExcludeFingerprint != now
}

// toolsChanged reports that this machine can now read stores it could not when
// the index was built. Gaining a tool is the change worth a pass; losing one is
// not — nothing new can be read, and what is already indexed stays valid. That
// asymmetry is what keeps a hook running with a minimal PATH from trading full
// rebuilds with a terminal that has both tools. Empty means an older deja wrote
// the manifest: nothing was recorded, so nothing is claimed.
func toolsChanged(m Manifest) bool {
	if m.ToolFingerprint == "" {
		return false
	}
	hadSQLite, hadZstd := parseToolFingerprint(m.ToolFingerprint)
	return (sources.SQLite3Available() && !hadSQLite) || (sources.ZstdAvailable() && !hadZstd)
}

// parseToolFingerprint reads back what toolFingerprint wrote. An unreadable one
// counts as "both present", so a fingerprint this build does not understand
// never triggers a rebuild.
func parseToolFingerprint(s string) (sqlite, zstd bool) {
	return !strings.Contains(s, "sqlite3=false"), !strings.Contains(s, "zstd=false")
}

// priorToolFingerprint reads what the index on disk recorded, so a rebuild does
// not forget a tool this process happens not to see.
func priorToolFingerprint(dir string) string {
	m, err := readManifestCached(dir)
	if err != nil {
		return ""
	}
	return m.ToolFingerprint
}

// mergedToolFingerprint records a tool as available if it is now or was when
// the index was last built. A hook process with a minimal PATH must not erase
// what a terminal build knew, or the next terminal run reads it as a tool newly
// gained and rebuilds — every time.
func mergedToolFingerprint(prior string) string {
	sqlite, zstd := sources.SQLite3Available(), sources.ZstdAvailable()
	if prior != "" {
		hadSQLite, hadZstd := parseToolFingerprint(prior)
		sqlite = sqlite || hadSQLite
		zstd = zstd || hadZstd
	}
	return toolFingerprint(sqlite, zstd)
}

// toolFingerprint names the external CLIs available to this build. Two states
// each, so a machine that gains or loses one is a machine whose stores must be
// read again.
func toolFingerprint(sqlite, zstd bool) string {
	return fmt.Sprintf("sqlite3=%t,zstd=%t", sqlite, zstd)
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
	return recordsIntactSized(dir, m, false)
}

// recordsReadable is recordsIntact for a lock-free reader: it tolerates a
// records.bin LONGER than the manifest committed. An incremental append grows
// records.bin before it rewrites the manifest, and a reader that lands in that
// window would otherwise see the new size against the old stamp and call a
// perfectly healthy concurrent append "corrupt" — triggering a needless full
// rebuild on the MCP path, or a hard error on `deja files`. Only a SHORTER file
// is real truncation.
//
// Reading the longer file is safe not because the tail is never read — a
// keyless or regex query full-scans records.bin to EOF — but because a tail
// record for a not-yet-committed session resolves against the old manifest:
// scanRecords skips a record whose Key is absent from m.Sessions, and a
// half-flushed tail decodes to a short read that eachRecord treats as a clean
// end. So the extra bytes contribute nothing rather than a wrong answer.
func recordsReadable(dir string, m Manifest) bool {
	return recordsIntactSized(dir, m, true)
}

func recordsIntactSized(dir string, m Manifest, tolerateLonger bool) bool {
	if m.RecordsSize <= 0 {
		return true // empty index, or one written before the size stamp existed
	}
	// Plain stat, deliberately: every caller reads the manifest first, and that
	// read waits the swap out — measured, removing the wait here changes
	// nothing (#1319).
	fi, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil {
		return false
	}
	// Shorter: a crash truncated the log. Longer: a crash landed records the
	// manifest never committed, or an incremental append is in flight; either
	// way the tail is unreferenced, so a reader may treat it as a snapshot.
	if fi.Size() < m.RecordsSize || (!tolerateLonger && fi.Size() != m.RecordsSize) {
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
	// Cached like IsCurrentVersion: same short-lived hook process, same store.
	// A manifest that fails to decode is not cached (readManifestCached returns
	// the error), so corruption is still detected here.
	m, err := readManifestCached(dir)
	if err != nil {
		// A manifest that is not there is "not built", which doctor reports
		// already. One that is there but will not decode is a different story:
		// doctor stats the file, calls the index built and up to date, and the
		// next search rebuilds it from scratch.
		if _, statErr := statIndexFile(filepath.Join(dir, "manifest.gob")); statErr == nil {
			return true
		}
		return false
	}
	if recordsIntact(dir, m) {
		return false
	}
	// The manifest and the record log were read a moment apart, and a rebuild
	// can land between them: the manifest is the old one, records.bin is the
	// new one, and their sizes disagree for reasons that have nothing to do
	// with damage. Measured on the hook predicate during eight rebuilds, 19 of
	// 13124 asks came back damaged, and each one served a prompt with no recall
	// (#1319).
	//
	// Only while a build actually holds the lock: a store that is short with
	// nothing running is short, and answering that immediately is what every
	// other caller depends on.
	if !RebuildInProgress(dir) {
		return true
	}
	waitOutSwapWindow()
	m2, err := readManifest(dir)
	if err != nil {
		// The manifest went away under us, which is the swap itself rather than
		// damage; the next call sees the new one.
		return false
	}
	return !recordsIntact(dir, m2)
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

// StampedAhead reports a session stamped later than this machine's clock, and
// so unusable for placing it against the others. Anything ahead counts: a
// session three hours out is still ahead, which is what the brief has counted
// since #696 and what its test pins. One rule in one place, so the brief's
// count and the marker recall prints cannot disagree about the same session
// (#1753).
func StampedAhead(t, now time.Time) bool {
	return t.After(now)
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
		if StampedAhead(meta.Updated, now) {
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
