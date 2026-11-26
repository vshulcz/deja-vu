package index

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// ReadableSnapshot reports whether the store on disk can be read by this build
// even though its content rules are stale.
//
// A version bump forces a rebuild, and during it every reading surface has to
// choose between an answer under the old rules and no answer at all. When the
// on-disk layout is the one this build writes, the first is strictly better:
// records decode, postings resolve, and what is missing is only whatever the new
// rules would re-derive. When the layout differs — or the store predates the
// field and cannot say — there is nothing a reader can trust.
//
// Damage is not staleness and is the caller's check: a torn record log is
// unreadable whatever its format says.
func ReadableSnapshot(dir string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return false
	}
	return m.Format == onDiskFormat
}

// AnswersWhileRebuilding reports whether the index on disk may answer as it
// stands while a rebuild for this build's version runs behind it: a layout this
// build reads, written at or above the redaction floor. ReadableSnapshot asks
// only the first; text below the floor must not be quoted while it is re-read.
// Damage is the caller's check, as it is there.
func AnswersWhileRebuilding(dir string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	return err == nil && !mustRebuildBeforeAnswering(m, version)
}

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
	return err == nil && (m.Version == version || newerIndex(m))
}

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
	m, err := readManifestCached(dir)
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

// ReadState is what this build may do with the index on disk, which is not the
// same question as how old it is. FormatDirection answers the second, and the
// sentence doctor drove from it was written for the first: a store one content
// version behind reads and answers, and the row said the build could not read
// it while `--deep` re-parsed its sessions three lines below (#3597).
type ReadState int

const (
	// ReadStateCurrent is an index this build wrote, or one that matches it.
	ReadStateCurrent ReadState = iota
	// ReadStateUnreadable is a layout this build cannot read at all.
	ReadStateUnreadable
	// ReadStateWithheld reads, but holds text written before deja knew how to
	// redact something, so it must not be quoted while it is re-read.
	ReadStateWithheld
	// ReadStateOlderRules reads and answers. What is pending is a re-derivation
	// — a role filed better, a title read from a different field — and those
	// bumps outnumber the redaction ones three to one.
	ReadStateOlderRules
	// ReadStateNewer means the binary was rolled back, not the index.
	ReadStateNewer
)

// ErrStoreWithheld is what a by-identity read answers when the store on disk
// was written under redaction rules this build has moved past. The advice is
// the same as every other surface's: run the pass that re-reads the sources.
var ErrStoreWithheld = errors.New("this index was written before deja learned to mask something it now masks — run `deja index` to re-read your sources")

// ReadStateOf reports which of those the index in dir is. An unreadable
// manifest is not one of them — that is damage, and indexDamageReason names it.
func ReadStateOf(dir string) ReadState {
	if dir == "" {
		dir = DefaultDir()
	}
	// Cached, like SessionCount: readManifest also decodes sessions.gob, and
	// the session-start hook asks this on a path where that is the expensive
	// part. The cache stats the file, so it still sees a rebuild that landed.
	m, err := readManifestCached(dir)
	if err != nil {
		return ReadStateCurrent
	}
	switch {
	case m.Format != onDiskFormat:
		return ReadStateUnreadable
	case m.Version > version:
		return ReadStateNewer
	case m.Version < redactionFloor:
		return ReadStateWithheld
	case m.Version < version:
		return ReadStateOlderRules
	}
	return ReadStateCurrent
}

// ImportedSessionCounts reports how many of each harness's indexed sessions
// arrived from another machine. doctor prints files beside sessions, and on a
// store that has both, more sessions than files reads as a miscount unless the
// difference is named (#894).
func ImportedSessionCounts(dir string) map[string]int {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
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
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	excluded := excludedHarnesses()
	out := map[string]int{}
	for _, meta := range m.Sessions {
		if excluded[strings.ToLower(meta.Harness)] {
			continue
		}
		if meta.From != "" && meta.From != "repo" {
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
	_, err := statSessionsFile(dir)
	return err == nil
}

// ManifestBuiltAt returns when the index was last built. Older manifests may
// omit BuiltAt; in that case manifest.gob's mtime is used.
func ManifestBuiltAt(dir string) time.Time {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err == nil && !m.BuiltAt.IsZero() {
		return m.BuiltAt
	}
	if fi, err := statIndexFile(filepath.Join(dir, "manifest.gob")); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

// ManifestSourcesReadAt returns when deja last walked this machine's stores,
// which is the time anything asking "is a store behind?" has to compare
// against. BuiltAt is not that time: an import from a peer rewrites the
// manifest without reading a local transcript, so on a machine that syncs on a
// timer every store looked freshly read (#3747). Falls back to BuiltAt for a
// store written before the field existed.
func ManifestSourcesReadAt(dir string) time.Time {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err == nil && !m.SourcesReadAt.IsZero() {
		return m.SourcesReadAt
	}
	// An index an import built on a machine that had never indexed carries no
	// files on purpose (#1307): nothing local has been read, so the honest
	// answer is "never" rather than the import's own clock. Only for that
	// shape — a store written before this field has files, and answering
	// "never" for it would call every store behind the moment someone
	// upgrades.
	if err == nil && len(m.Files) == 0 {
		return time.Time{}
	}
	return ManifestBuiltAt(dir)
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
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	out := map[string]int{}
	for _, meta := range m.Sessions {
		out[meta.Harness]++
	}
	return out
}

// IndexedSessionKeys reports the harness:id of every session the index holds.
// `forget --list` uses it to tell a tombstone still standing between a
// session and search from one whose session is gone everywhere: a tombstone
// only does anything while a store or the index still has the session it
// names (#3753). Nil when there is no index yet.
func IndexedSessionKeys(dir string) map[string]bool {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	out := make(map[string]bool, len(m.Sessions))
	for _, meta := range m.Sessions {
		out[meta.Key()] = true
	}
	return out
}

// HarnessUnreadCounts reports, per harness, how many transcripts the store
// holds that the index has no state for at all — not read once, not partly,
// never.
//
// Audited on a real store: the index tracked 1,533 files and had never tracked
// ten, five of them written eight weeks earlier, and no surface said so. `deja
// sources` prints a store's session count, but zero there reads as "nothing
// written yet" rather than "five files never opened", which is the difference
// between a quiet machine and a broken one (#3747).
//
// A file counted here is behind at this instant, which is not the same as
// unreadable: a transcript written a second ago is unread until the next pass.
// What makes it worth printing is the shape the audit found — a file that
// cannot be appended from (no offset parser for its kind) forces a replacement
// pass, and replacement only happens on a search, a recall or `deja index`. A
// machine driven by hooks alone never takes that path.
func HarnessUnreadCounts(dir string) map[string]int {
	byHarness := map[string][]string{}
	for _, h := range sources.StoresToRead() {
		byHarness[h.Name] = h.Files()
	}
	return HarnessUnreadCountsFor(dir, byHarness)
}

// HarnessUnreadCountsFor is HarnessUnreadCounts using already-listed store file
// paths per harness so callers like doctor do not walk the filesystem twice.
func HarnessUnreadCountsFor(dir string, byHarness map[string][]string) map[string]int {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	out := map[string]int{}
	for name, files := range byHarness {
		for _, p := range files {
			if _, ok := m.Files[p]; ok {
				continue
			}
			// A path the walk itself would decline is not an unread
			// transcript: the index never intends to hold it.
			if fi, err := os.Lstat(p); err != nil || fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
				continue
			}
			out[name]++
		}
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
	m, err := readManifestCached(dir)
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

// HarnessKeptCounts is how many indexed sessions per harness came from a
// transcript that is no longer on disk while its directory still is — the
// client's own cleanup, kept on purpose (#2970). doctor prints it beside the
// session count, since "1 file, 2 indexed sessions" reads as a miscount
// otherwise.
func HarnessKeptCounts(dir string) map[string]int {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	gone := map[string]bool{}
	out := map[string]int{}
	for _, meta := range m.Sessions {
		if meta.Path == "" {
			continue
		}
		g, seen := gone[meta.Path]
		if !seen {
			_, err := os.Stat(meta.Path)
			g = os.IsNotExist(err)
			if g {
				g = deletedFromLiveStore(meta.Path)
			}
			gone[meta.Path] = g
		}
		if g {
			out[meta.Harness]++
		}
	}
	return out
}

// Redactions reports what redaction masked in the index at dir.
func Redactions(dir string) (RedactionStats, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
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
		// An index written before #2238 holds keys by file kind, and a pass
		// since then writes them by store, so one manifest can carry both. The
		// reader folds, or the screen shows "cline" and "cline-sdk" as two
		// stores with the counts split between them until a full rebuild.
		name := parts[0]
		if store := sources.HarnessForKind(name); store != "" {
			name = store
		}
		if out.Rules[name] == nil {
			out.Rules[name] = map[string]int{}
		}
		out.Rules[name][parts[1]] += count
	}
	return out, nil
}

func manifestFresh(m Manifest, files map[string]FileState, scope string) bool {
	if m.Version != version || len(m.Files)+len(m.Deferred) < len(files) {
		return false
	}
	// A store from before deletions were dated is read once more, so the pass
	// writes the mark and the first deletion after the upgrade is dated.
	if !m.KeptDated {
		return false
	}
	if len(m.Files) > len(files) {
		for p, f := range m.Files {
			// The records a sync brought in have no transcript to list.
			if _, ok := files[p]; ok || p == syncImportPath {
				continue
			}
			// A store this run does not read keeps its rows as they were
			// (#5394): nothing for a pass to do.
			if sources.OutsideStoresRead(p) {
				continue
			}
			// Behind a directory this run may not enter: held as it was, and
			// named as unreadable (#5393).
			if !f.Kept && behindLockedDir(p) {
				continue
			}
			// Still gone, from a store that is still there: nothing for a
			// pass to do. A store that went whole is a change.
			if !f.Kept || !deletedFromLiveStore(p) {
				return false
			}
			if _, err := os.Lstat(p); err == nil {
				return false
			}
		}
	}
	// A store skipped for a missing CLI leaves the transcripts untouched, so
	// nothing here would notice that the tool has since been installed (#1760).
	if toolsChanged(m) || miningChanged(m) {
		return false
	}
	if m.Scope != scope {
		return false
	}
	if !unreadableCurrent(m, files) {
		return false
	}
	// A rollout waiting for zstd changes nothing until it changes or zstd
	// arrives (#5105). The fingerprint cannot say the second: it keeps
	// zstd=true once any process saw zstd, so a rollout a hook deferred with a
	// short PATH would never be read by the terminal that has it. Ask, once.
	zstdMissing := len(m.Deferred) > 0 && !sources.ZstdAvailable()
	for p, f := range files {
		if d, ok := m.Deferred[p]; ok && zstdMissing && sameFile(d, f) {
			continue
		}
		if !sameFile(m.Files[p], f) || m.Files[p].Kept {
			return false
		}
	}
	return true
}

// manifestDecodes counts readManifest's decodes, for the test that pins how
// many a search makes (#5386).
var manifestDecodes atomic.Int64

func readManifest(dir string) (Manifest, error) {
	if err := ensureVaultUnlocked(dir); err != nil {
		return Manifest{}, err
	}
	manifestDecodes.Add(1)
	var core manifestCore
	if err := readGob(filepath.Join(dir, "manifest.gob"), &core); err != nil {
		return Manifest{}, err
	}
	sessions, err := readSessionsCatalog(dir)
	if err != nil {
		return Manifest{}, err
	}
	m := manifestFromCore(core, sessions)
	m.vaultDir = canonicalVaultDir(dir)
	return m, nil
}

// manifestFromCore is the Manifest that manifest.gob's core and sessions make.
func manifestFromCore(core manifestCore, sessions map[string]SessionMeta) Manifest {
	return Manifest{Version: core.Version, Format: core.Format, Files: core.Files, BuiltAt: core.BuiltAt, SourcesReadAt: core.SourcesReadAt, Generation: core.Generation, Scope: core.Scope, Redacted: core.Redacted, RedactionRules: core.RedactionRules, ExportWatermarks: core.ExportWatermarks, ExportBoundary: core.ExportBoundary, ImportedRecords: core.ImportedRecords, RecordStrings: core.RecordStrings, RecordsSize: core.RecordsSize, DeadRecords: core.DeadRecords, DeadBytes: core.DeadBytes, BucketFiles: core.BucketFiles, IngestHealth: core.IngestHealth, IngestFiles: core.IngestFiles, ExcludeFingerprint: core.ExcludeFingerprint, AliasFingerprint: core.AliasFingerprint, ToolFingerprint: core.ToolFingerprint, Compactions: core.Compactions, KeptDated: core.KeptDated, ReadZone: core.ReadZone, Deferred: core.Deferred, PartialOf: core.PartialOf, SearchOnly: core.SearchOnly, Empty: core.Empty, Sessions: sessions}
}

// importedFile stores sync import deduplication keys outside manifest.gob
// so ordinary hook and retrieval reads do not decode the ledger (#6135).
const importedFile = "imported.gob"

// readImportedRecords loads the sync import deduplication ledger from
// imported.gob, merging any true entries from a legacy unsplit manifest.
func readImportedRecords(dir string, legacy map[string]bool) (map[string]bool, error) {
	var out map[string]bool
	if err := readGob(filepath.Join(dir, importedFile), &out); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		out = maps.Clone(legacy)
	}
	if out == nil {
		out = map[string]bool{}
	}
	for k, v := range legacy {
		if v {
			out[k] = true
		}
	}
	return out, nil
}

func writeImportedRecords(dir string, dedupe map[string]bool) error {
	if len(dedupe) == 0 {
		return nil
	}
	return writeGobAtomic(filepath.Join(dir, importedFile), dedupe)
}

// writeManifestOnly persists manifest.gob without rewriting sessions.gob —
// for updates that change only core fields (e.g. export watermarks) where the
// caller has not loaded sessions and must not clobber them. It wrote no
// records, so it keeps the sizes the manifest committed: see stampStore.
func writeManifestOnly(dir string, m Manifest) error {
	if err := writeImportedRecords(dir, m.ImportedRecords); err != nil {
		return err
	}
	core := manifestCore{Version: m.Version, Format: m.Format, Files: m.Files, BuiltAt: m.BuiltAt, SourcesReadAt: m.SourcesReadAt, Generation: m.Generation, Scope: m.Scope, Redacted: m.Redacted, RedactionRules: m.RedactionRules, ExportWatermarks: m.ExportWatermarks, ExportBoundary: m.ExportBoundary, RecordStrings: m.RecordStrings, DeadRecords: m.DeadRecords, DeadBytes: m.DeadBytes, IngestHealth: m.IngestHealth, IngestFiles: m.IngestFiles, ExcludeFingerprint: m.ExcludeFingerprint, AliasFingerprint: m.AliasFingerprint, ToolFingerprint: m.ToolFingerprint, Compactions: m.Compactions, KeptDated: m.KeptDated, ReadZone: m.ReadZone, Deferred: m.Deferred, PartialOf: m.PartialOf, SearchOnly: m.SearchOnly, Empty: m.Empty}
	stampStore(dir, &core, m, false)
	err := writeGobAtomic(filepath.Join(dir, "manifest.gob"), core)
	invalidateManifestCache(dir)
	return err
}

// stampStore sets the record log size and bucket count the manifest commits.
//
// A pass that wrote records takes them from the files: what it wrote is what
// is there. A writer that only changed metadata keeps what the manifest it read
// committed. Taking the files' sizes there too adopted whatever a killed pass
// had appended without committing, and the next pass appended the same turns
// again, so a session held its last turns twice.
func stampStore(dir string, core *manifestCore, m Manifest, wroteRecords bool) {
	if !wroteRecords && m.RecordsSize > 0 {
		core.RecordsSize, core.BucketFiles = m.RecordsSize, m.BucketFiles
		return
	}
	if fi, err := os.Stat(filepath.Join(dir, "records.bin")); err == nil {
		core.RecordsSize = fi.Size()
	}
	core.BucketFiles = max(countBucketFiles(filepath.Join(dir, "buckets"))-m.bucketsLeaving, 0)
}

// writeManifest commits the two-file manifest crash-safely. sessions.bin is
// written (and renamed into place) before manifest.gob, and both go through a
// temp file + rename. manifest.gob carries the version/file sizes that decide
// whether the index is fresh, so it must land last: a crash between the two
// leaves the old manifest pointing at old data, and the next run reindexes
// rather than serving a fresh-looking index whose sessions are stale.
func writeManifest(dir string, m Manifest) error {
	return commitManifest(dir, m, true, false)
}

// writeManifestShared is writeManifest for a pass that is done with m: what
// it wrote becomes this process's cached manifest, so the reads that follow
// the pass do not decode the files it has just encoded (#5471). The caller
// must not change m, its maps or its slices afterwards.
func writeManifestShared(dir string, m Manifest) error {
	return commitManifest(dir, m, true, true)
}

// writeManifestMeta is writeManifest for a writer that changed sessions or
// files but appended no records, such as a followed rename.
func writeManifestMeta(dir string, m Manifest) error {
	return commitManifest(dir, m, false, false)
}

func commitManifest(dir string, m Manifest, wroteRecords, share bool) error {
	if err := writeImportedRecords(dir, m.ImportedRecords); err != nil {
		return err
	}
	mergeIngestDiag(&m)
	core := manifestCore{Version: m.Version, Format: m.Format, Files: m.Files, BuiltAt: m.BuiltAt, SourcesReadAt: m.SourcesReadAt, Generation: m.Generation, Scope: m.Scope, Redacted: m.Redacted, RedactionRules: m.RedactionRules, ExportWatermarks: m.ExportWatermarks, ExportBoundary: m.ExportBoundary, RecordStrings: m.RecordStrings, DeadRecords: m.DeadRecords, DeadBytes: m.DeadBytes, IngestHealth: m.IngestHealth, IngestFiles: m.IngestFiles, ExcludeFingerprint: m.ExcludeFingerprint, AliasFingerprint: m.AliasFingerprint, ToolFingerprint: m.ToolFingerprint, Compactions: m.Compactions, KeptDated: m.KeptDated, ReadZone: m.ReadZone, Deferred: m.Deferred, PartialOf: m.PartialOf, SearchOnly: m.SearchOnly, Empty: m.Empty}
	stampStore(dir, &core, m, wroteRecords)
	sfi, err := writeSessionsBinAtomicStat(filepath.Join(dir, "sessions.bin"), m.Sessions)
	if err != nil {
		return err
	}
	mfi, err := writeGobAtomicStat(filepath.Join(dir, "manifest.gob"), core)
	if err == nil {
		_ = os.Remove(filepath.Join(dir, "sessions.gob"))
	}
	ensureSharedIndexPerms(dir)
	// The cache this process holds is keyed on manifest.gob's mtime and size,
	// and the comment there claimed the atomic swap always changes that pair.
	// It does not: a rewrite that keeps the size and lands inside one tick of
	// the filesystem's timestamp resolution is invisible to it, and the reader
	// then answers from the manifest before this one. The writer knows, so it
	// says so.
	invalidateManifestCache(dir)
	if err == nil && wroteRecords {
		// Best effort: a table that could not be written leaves readers
		// walking the log, which is what they did before it existed.
		_ = writeRecordOffsets(dir, tablesFromManifest(m), core.RecordsSize)
	}
	if err == nil {
		// After the offset table, which it reads through. Also on a commit
		// without records: a forget or a rename moves the walls.
		_ = writeFrictionTexts(dir, m, core.RecordsSize)
	}
	invalidateManifestCache(dir)
	if err == nil && share && mfi != nil && sfi != nil {
		sessions := m.Sessions
		if sessions == nil {
			sessions = map[string]SessionMeta{}
		}
		cached := manifestFromCore(core, sessions)
		cached.vaultDir = canonicalVaultDir(dir)
		cacheManifest(dir, mfi, sfi, cached)
	}
	return err
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

// ProjectAliasesChanged reports whether the project alias file differs from
// the one this index was built under. It names sessions at ingest, so an edit
// leaves what is indexed under the old names until `deja index --rebuild`.
// An index from before the field is taken as built with no file: that deja
// never read one.
func ProjectAliasesChanged(dir string) bool {
	m, err := readManifestCached(dir)
	if err != nil {
		return false
	}
	was := m.AliasFingerprint
	if was == "" {
		was = "none"
	}
	return was != sources.ProjectAliasFingerprint()
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
	// Asked only when the index lacked the tool, so a fresh index does not pay
	// for a sqlite3 probe on every check. A sqlite3 that is on PATH and broken
	// counts as absent, so repairing it reads the stores it had emptied.
	return (!hadSQLite && sources.SQLite3Available()) || (!hadZstd && sources.ZstdAvailable())
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
	hadSQLite, hadZstd := false, false
	if prior != "" {
		hadSQLite, hadZstd = parseToolFingerprint(prior)
	}
	return toolFingerprint(hadSQLite || sources.SQLite3Available(), hadZstd || sources.ZstdAvailable())
}

// toolFingerprint names the external CLIs available to this build. Two states
// each, so a machine that gains or loses one is a machine whose stores must be
// read again.
func toolFingerprint(sqlite, zstd bool) string {
	return fmt.Sprintf("sqlite3=%t,zstd=%t", sqlite, zstd)
}
