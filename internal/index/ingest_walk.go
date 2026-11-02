package index

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func sameFile(a, b FileState) bool {
	return a.Path == b.Path && a.Size == b.Size && a.MTime == b.MTime &&
		a.MetadataSize == b.MetadataSize && a.MetadataMTime == b.MetadataMTime &&
		a.CWDSize == b.CWDSize && a.CWDMTime == b.CWDMTime &&
		a.WALSize == b.WALSize && a.WALMTime == b.WALMTime
}

// walStat is the size and mtime of a SQLite store's write-ahead log, zero for
// a store without one and for anything that is not a database. Hermes
// checkpoints every 50 writes of one process, so a few short sessions live in
// state.db-wal alone, and a walk that statted state.db called them unchanged
// (#5140).
func walStat(p string) (size, stamp int64) {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".db", ".sqlite", ".sqlite3", ".vscdb":
	default:
		return 0, 0
	}
	fi, err := os.Lstat(p + "-wal")
	if err != nil || !fi.Mode().IsRegular() {
		return 0, 0
	}
	return fi.Size(), fi.ModTime().UnixNano()
}

// kindForPath returns the registry FileKind whose Match accepts p.
func kindForPath(p string) (sources.FileKind, bool) {
	return kindIn(sources.Registry(), p)
}

// kindIn is kindForPath against a registry already built.
func kindIn(reg []sources.Harness, p string) (sources.FileKind, bool) {
	for _, h := range reg {
		for _, k := range h.Kinds {
			if k.Match(p) {
				return k, true
			}
		}
	}
	return sources.FileKind{}, false
}

func parseChangedFile(_, p string, old FileState) ([]model.Session, error) {
	k, ok := kindForPath(p)
	if !ok {
		return nil, nil
	}
	return sources.ReadWatched(p, func() ([]model.Session, error) { return k.Parse(p, old.LastUpdated) })
}

func parseAppendedFile(_, p string, old FileState, isNew bool) (ss []model.Session, err error) {
	defer func() {
		if r := recover(); r != nil {
			ss, err = nil, fmt.Errorf("parser panic on %s: %v", p, r)
		}
	}()
	k, ok := kindForPath(p)
	if !ok {
		return nil, nil
	}
	if k.ParseFrom == nil {
		// Resuming is what this kind cannot do, and a file deja has never read
		// needs no resuming: read it whole from its first byte, which is the
		// same work the replacement path would do for it (#3747).
		if !isNew || k.Parse == nil {
			return nil, nil
		}
		return sources.ReadWatched(p, func() ([]model.Session, error) { return k.Parse(p, 0) })
	}
	return sources.ReadWatched(p, func() ([]model.Session, error) { return k.ParseFrom(p, resumeOffset(old), old.LastUpdated) })
}

// harnessForPath reports the fine-grained source kind for a path (claude,
// codex-history, cursor-db, ...) via the sources registry, or "" if none.
// harnessForPath maps an ingest-diagnostic path to the harness it belongs to.
// A directory has no transcript extension, so the matchers reject it outright;
// asking what a transcript inside it would be is the same question the walk
// was already answering (#818).
func harnessForPath(p string) string {
	if h := sources.KindForPath(p); h != "" {
		return h
	}
	if filepath.Ext(p) != "" {
		return ""
	}
	for _, name := range []string{"probe.jsonl", "probe.json", "probe.md"} {
		if h := sources.KindForPath(filepath.Join(p, name)); h != "" {
			return h
		}
	}
	return ""
}

// setDatabaseStoreWatermarks stamps every store deja reads through SQLite.
//
// Each of the six has a since-the-watermark parser, and dbParse only calls it
// once the store carries a watermark — so the three that were never stamped
// (grok, hermes, zed) read their store whole on every pass, for the life of
// the index (#2075).
func setDatabaseStoreWatermarks(files map[string]FileState, sessions map[string]SessionMeta) {
	for h, db := range opencodeSchemaDBs {
		setStoreLastUpdated(files, sessions, h, db())
	}
	setStoreLastUpdated(files, sessions, "goose", sources.GooseDB())
	// grok's database had a since-the-watermark parser in the registry and
	// nothing ever stamped it, so every pass read the store whole — the pass
	// with one new line cost what reading everything costs (#2075).
	//
	// Safe to stamp because ParseGrokDBSince selects messages rather than
	// sessions and normalises both sides with a millisecond backoff (#2150),
	// which is the cursor shape. hermes compares whole seconds
	// with a strict >, and zed returns whole threads; each needs its own fix
	// before it can be stamped, so neither is here.
	setStoreLastUpdated(files, sessions, "grok", sources.GrokDB())
	// hermes, once its reader stopped dropping the watermark's own second
	// (#2075). Several stores on one machine: current builds keep one at the
	// root and older ones shard per profile, and each is stamped with its own
	// newest — stamping them alike would give a quiet profile the busy one's
	// time (#2071).
	for _, db := range sources.HermesDBs() {
		setStoreLastUpdated(files, sessions, "hermes", db)
	}
	// zed, whose cursor selects threads rather than messages: a continued
	// thread comes back whole, so it joins rereadsWholeSessions below in the
	// same change — the goose shape, not the cursor one (#2075).
	setStoreLastUpdated(files, sessions, "zed", sources.ZedDB())
	for _, db := range sources.CursorDBs() {
		setStoreLastUpdated(files, sessions, "cursor", db)
	}
	for _, db := range sources.HermesDBs() {
		setStoreLastUpdated(files, sessions, "hermes", db)
	}
	for _, db := range sources.OpenClawAgentDBs() {
		setStoreLastUpdated(files, sessions, "openclaw", db)
	}
	// crush keeps one store per project and was never stamped, so one new
	// turn re-read every session in it: 3000 replaced, 8 s (#4381). Its
	// cursor selects sessions, so it is in rereadsWholeSessions too.
	for _, db := range sources.CrushDBs() {
		setStoreLastUpdated(files, sessions, "crush", db)
	}
	if cm := sources.ClaudeMemDBPath(); cm != "" {
		setStoreLastUpdated(files, sessions, "claude", cm)
	}
}

// sessionInStore reports whether a row came from the store being stamped.
//
// Cursor keeps one database per workspace, and both it and goose record the
// store path in Path, so the row says which one it came from. opencode records
// the project directory instead (#2033) — and has a single database, so there
// the harness is the store, less any transcript files of its own (#4396).
func sessionInStore(s SessionMeta, harness, db string) bool {
	if _, ok := opencodeSchemaDBs[harness]; ok {
		// Not a row the diff files gave: it carries a file's mtime, which
		// says nothing about how far the database has been read (#4207).
		return inOpencodeSchemaDB(harness, s.Path)
	}
	return s.Path == db
}

// setStoreLastUpdated stamps a database-backed store with the newest session
// time so incremental passes can query only newer content.
//
// The newest session in THAT store: taking the newest across the harness
// stamped a quiet Cursor workspace with the busy one's time, and a turn the
// quiet store gained below that line was never asked for again (#2071).
func setStoreLastUpdated(files map[string]FileState, sessions map[string]SessionMeta, harness, db string) {
	f, ok := files[db]
	if !ok {
		return
	}
	var latest int64
	for _, s := range sessions {
		if s.Harness != harness || !sessionInStore(s, harness, db) {
			continue
		}
		if s.Updated.UnixNano() > latest {
			latest = s.Updated.UnixNano()
		}
	}
	// Never past the clock: one session stamped ahead of it (a skewed
	// machine, a store synced from one) put the mark a day out, and every
	// turn written in the meantime sat below it unread.
	if now := time.Now().UnixNano(); latest > now {
		latest = now
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

// missingTree is a directory that vanished along with everything under it.
type missingTree struct {
	dir   string
	files int
	// mount is set when dir is a mount point rather than an ordinary
	// directory: an unplugged disk, not a deletion.
	mount bool
	// renamed is where the same volume turned up under a numbered name.
	renamed string
}

// renamedMount finds the path a missing one moved to when its volume came
// back under a different name: macOS mounts a disk as "/Volumes/Disk 1"
// when something already sits on "/Volumes/Disk", and every path naming the
// old mount point starts failing while the files are right there. Telling
// the user to reconnect a disk that is already connected helps nobody.
func renamedMount(path string) string {
	sep := string(filepath.Separator)
	for _, m := range mountParents {
		if !strings.HasPrefix(path, m+sep) {
			continue
		}
		rest := strings.TrimPrefix(path, m+sep)
		name, sub, _ := strings.Cut(rest, sep)
		if name == "" {
			continue
		}
		for n := 1; n <= 9; n++ {
			cand := filepath.Join(m, fmt.Sprintf("%s %d", name, n), sub)
			if _, err := os.Stat(cand); err == nil {
				return cand
			}
		}
	}
	return ""
}

// mountParents are the directories whose direct children are mount points.
// A test cannot create one for real — /Volumes is not writable — so it
// points this at a temporary directory instead.
var mountParents = defaultMountParents()

func defaultMountParents() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Volumes"}
	case "windows":
		return nil
	default:
		return []string{"/mnt", "/media"}
	}
}

// mountRoot reports whether dir is where a removable volume gets mounted.
// A directory the user deleted is a deletion and its sessions leave the
// index; a mount point that is empty means the disk is elsewhere, and the
// sessions it holds are not gone.
func mountRoot(dir string) bool {
	dir = filepath.Clean(dir)
	parent := filepath.Dir(dir)
	if parent == dir {
		return false
	}
	if runtime.GOOS == "windows" {
		return filepath.VolumeName(dir) == dir || parent == filepath.VolumeName(dir)+`\`
	}
	if slices.Contains(mountParents, parent) {
		return true
	}
	// /run/media/<user>/<volume> is what udisks2 uses.
	return runtime.GOOS == "linux" && filepath.Dir(parent) == "/run/media"
}

// missingTrees groups files that disappeared by the outermost directory that
// is no longer there. One deleted file has an existing parent and is reported
// as nothing; a whole store that went away with its disk is one line.
func missingTrees(removed map[string]bool) []missingTree {
	byDir := map[string]int{}
	for p := range removed {
		dir := filepath.Dir(p)
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			continue
		}
		for {
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			if _, err := os.Stat(parent); !os.IsNotExist(err) {
				break
			}
			dir = parent
		}
		byDir[dir]++
	}
	out := make([]missingTree, 0, len(byDir))
	for dir, n := range byDir {
		out = append(out, missingTree{dir: dir, files: n, mount: mountRoot(dir)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dir < out[j].dir })
	return out
}

// sessionDirName is a directory named for one session: the id itself
// (Cursor, Copilot CLI, Antigravity, a Claude transcript's sidecar), Kimi's
// session_<id>, DeepSeek's session-<id>, Kiro's sess_<id>, and Cline CLI's
// <unix ms>_<suffix>, which `cline history delete` removes whole. Anchored,
// because an encoded working directory under a store root carries a UUID
// whenever the directory did — a temp dir, a sandbox — and that folder is a
// project, not a session.
var sessionDirName = regexp.MustCompile(`^(?:(?:session[_-]|sess_)?[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9]{13}_[0-9a-z]{5})$`)

// deletedFromLiveStore reports whether a file no longer on disk was deleted
// from a store that is still there — the client's cleanup or a deletion by
// hand, which the index keeps (#2970) — rather than with a store that went
// away whole: its directory is still there, or the directory that went with
// it was one session's (#4195).
func deletedFromLiveStore(p string) bool {
	if _, err := os.Stat(filepath.Dir(p)); err == nil {
		return true
	}
	return goneSessionDir(p) != ""
}

// supersededLogs are the removed paths a listed file replaced: an older dsh
// log generation, still on disk beside the one dsh migrated it into. Keeping
// it back as a deleted transcript held the session twice and sent the new log
// down the append path on top of the old records (#4600); left removed, the
// replacement path drops the old file's records and reads the new one whole.
func supersededLogs(removed map[string]bool, files map[string]FileState) map[string]bool {
	out := map[string]bool{}
	if len(removed) == 0 {
		return out
	}
	byDir := map[string][]string{}
	for p := range removed {
		byDir[filepath.Dir(p)] = nil
	}
	for p := range files {
		d := filepath.Dir(p)
		if _, ok := byDir[d]; ok {
			byDir[d] = append(byDir[d], p)
		}
	}
	for p := range removed {
		for _, q := range byDir[filepath.Dir(p)] {
			if sources.DeepSeekLogSupersedes(q, p) {
				out[p] = true
				break
			}
		}
	}
	return out
}

// sessionDirsUnder names the session directories the given files sit in.
func sessionDirsUnder(files map[string]FileState) map[string]bool {
	out := map[string]bool{}
	for p := range files {
		for d, i := filepath.Dir(p), 0; i < 4; d, i = filepath.Dir(d), i+1 {
			if b := filepath.Base(d); sessionDirName.MatchString(b) {
				out[b] = true
			}
		}
	}
	return out
}

// goneSessionDir is the session directory that went with a file, when the
// topmost directory missing above it is one session's under a parent that is
// still there; "" otherwise.
func goneSessionDir(p string) string {
	dir := filepath.Dir(p)
	if _, err := os.Stat(dir); err == nil {
		return ""
	}
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		if _, err := os.Stat(parent); err == nil {
			if deletedSessionDir(dir) {
				return dir
			}
			return ""
		}
		dir = parent
	}
}

// deletedSessionDir reports whether a missing directory is one session's,
// deleted from a store that is still there.
func deletedSessionDir(dir string) bool {
	if !sessionDirName.MatchString(filepath.Base(dir)) {
		return false
	}
	_, err := os.Stat(filepath.Dir(dir))
	return err == nil
}

func pluralFiles(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func currentFiles(h string) map[string]FileState {
	return currentFilesWith(h, nil)
}

// storeWalks counts the walks of the stores, for the test that keeps `deja
// index` to one.
var storeWalks atomic.Int64

func currentFilesWith(h string, old map[string]FileState) map[string]FileState {
	storeWalks.Add(1)
	// Walking the stores is the first thing a build does and, on a network
	// volume, the slowest: it published nothing, so every "memory is on its
	// way" surface reported an ordinary quiet day until the walk finished
	// (#1021).
	reportPhase("finding transcripts", 0)
	sources.BeginWalk()
	defer func() { setLastWalkFailed(sources.WalkFailures()) }()
	paths := map[string]bool{}
	// The stores a rebuild reads, not every store: an excluded harness was
	// walked and indexed here on every incremental pass (#5394). Its rows in
	// the manifest are kept as a store outside this run's view, so what is
	// already indexed stays until a rebuild, as with a project pattern.
	for _, hr := range sources.StoresToRead() {
		if h != "" && h != hr.Name {
			continue
		}
		for _, p := range hr.Files() {
			paths[p] = true
		}
	}
	out := statFiles(paths, old)
	injectHermesPG(out, old)
	noteLockedAway(h, out, old)
	return out
}

// noteLockedAway names an indexed transcript the walk did not list because a
// directory above it now refuses this run. A store that lists its files with
// a glob drops the refusal on the floor, and the transcript then read as gone
// (#5393). Once per directory the walk already named. One Lstat per directory:
// whether a file can be statted is decided by the directories above it.
func noteLockedAway(h string, out, old map[string]FileState) {
	if h != "" {
		return
	}
	var failed map[string]string
	denied := map[string]bool{}
	for p, f := range old {
		if _, ok := out[p]; ok || f.Kept || p == syncImportPath || sources.OutsideStoresRead(p) {
			continue
		}
		d := filepath.Dir(p)
		locked, seen := denied[d]
		if !seen {
			locked = behindLockedDir(p)
			denied[d] = locked
		}
		if !locked {
			continue
		}
		if failed == nil {
			failed = sources.WalkFailures()
		}
		named := false
		for ; d != filepath.Dir(d); d = filepath.Dir(d) {
			if failed[d] != "" {
				named = true
				break
			}
		}
		if !named {
			sources.NoteUnreadable(p)
		}
	}
}

// lastWalkFailed is what the latest store walk could not read: directories it
// could not list and transcripts whose mode refuses a read.
var (
	lastWalkMu     sync.Mutex
	lastWalkFailed map[string]string
)

func setLastWalkFailed(m map[string]string) {
	lastWalkMu.Lock()
	lastWalkFailed = m
	lastWalkMu.Unlock()
}

func walkFailed() map[string]string {
	lastWalkMu.Lock()
	defer lastWalkMu.Unlock()
	return lastWalkFailed
}

// unreadableCurrent reports whether the manifest's unreadable paths are the
// ones the latest walk saw. A chmod changes no size or mtime, and a directory
// that cannot be listed adds no file, so the walk alone called the index up to
// date and nothing ever said part of the history was not being read (#5393).
// Only paths with a recorded error are looked at, so the walk costs no more.
func unreadableCurrent(m Manifest, files map[string]FileState) bool {
	failed := walkFailed()
	for p := range failed {
		if m.IngestFiles[p].Error == "" {
			return false
		}
	}
	for p, e := range m.IngestFiles {
		if e.Error == "" || failed[p] != "" {
			continue
		}
		if f, ok := files[p]; ok {
			if of, held := m.Files[p]; e.Walk && held && sameFile(of, f) {
				return false
			}
			continue
		}
		if gone(p) {
			return false
		}
		if _, held := m.Files[p]; !held && dirListable(p) {
			return false
		}
	}
	return true
}

// behindLockedDir reports an indexed file the walk did not list because a
// directory above it refuses this run, rather than because it went away.
func behindLockedDir(p string) bool {
	_, err := os.Lstat(p)
	return os.IsPermission(err)
}

// gone reports a path that is no longer there at all: a transcript the walk
// once could not read, deleted since, has nothing left to report.
func gone(p string) bool {
	_, err := os.Lstat(p)
	return errors.Is(err, fs.ErrNotExist)
}

// dirListable reports a directory that lists now. Only a directory is
// opened: the path may be a transcript that failed to read.
func dirListable(p string) bool {
	if fi, err := os.Lstat(p); err != nil || !fi.IsDir() {
		return false
	}
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if fi, statErr := f.Stat(); statErr != nil || !fi.IsDir() {
		return false
	}
	_, err = f.Readdirnames(1)
	return err == nil || errors.Is(err, io.EOF)
}

// statFiles is the walk's FileState for each path that is a regular file.
//
// Every search stats each transcript to learn nothing moved: 12k files on the
// store in #5386. The files are independent, so a few workers share them; the
// result is the same map either way.
func statFiles(paths map[string]bool, old map[string]FileState) map[string]FileState {
	list := make([]string, 0, len(paths))
	for p := range paths {
		list = append(list, p)
	}
	states := make([]FileState, len(list))
	isFile := make([]bool, len(list))
	reg := sources.Registry()
	stat := func(i int) {
		p := list[i]
		fi, err := os.Lstat(p)
		if os.IsPermission(err) {
			// Listed by a directory this run may read but not enter.
			sources.NoteUnreadable(p)
			return
		}
		if err == nil && fi.Mode()&os.ModeSymlink == 0 && !fi.IsDir() {
			// Listed all the same: a file this run may not read is not a
			// deleted one, and the sessions already read from it stay.
			if sources.ModeUnreadable(p, fi) {
				sources.NoteUnreadable(p)
			}
			states[i], isFile[i] = walkFileStateIn(reg, p, fi, old), true
		}
	}
	workers := min(runtime.GOMAXPROCS(0), statWorkers, len(list)/statPerWorker)
	if workers <= 1 {
		for i := range list {
			stat(i)
		}
	} else {
		var next atomic.Int64
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				for {
					i := int(next.Add(1)) - 1
					if i >= len(list) {
						return
					}
					stat(i)
				}
			})
		}
		wg.Wait()
	}
	out := make(map[string]FileState, len(list))
	for i, p := range list {
		if isFile[i] {
			out[p] = states[i]
		}
	}
	return out
}

// statWorkers caps the walk's workers: on 12k files four took the walk from
// 55 ms to 28 ms and eight did no better. statPerWorker is the fewest files
// worth starting one for.
const (
	statWorkers   = 4
	statPerWorker = 256
)

// walkFileState is what the walk records for a transcript file.
func walkFileState(p string, fi os.FileInfo) FileState {
	return walkFileStateIn(sources.Registry(), p, fi, nil)
}

// walkFileStateIn is walkFileState against a registry the caller built once.
// Building it is most of what the walk spent per file, twice over — once to
// find the file's kind, once to name its harness — and a walk visits every
// transcript on the machine (#5386).
func walkFileStateIn(reg []sources.Harness, p string, fi os.FileInfo, old map[string]FileState) FileState {
	fs := FileState{Path: p, Size: fi.Size(), MTime: fi.ModTime().UnixNano()}
	if strings.HasSuffix(p, ".jsonl") {
		// Deriving these means reading the file: the tail for the last
		// complete line, the head for the prefix hash. When size and
		// mtime are unchanged the bytes are too, and on a large store
		// this is the difference between a stat and 650 ms of reading.
		if of, ok := old[p]; ok && of.Size == fs.Size && of.MTime == fs.MTime {
			fs.SafeSize, fs.PrefixHash = of.SafeSize, of.PrefixHash
			fs.PrefixSample = of.PrefixSample
			fs.WrittenBack = of.WrittenBack
		} else {
			// Under the store read budget: the walk reads the tail and the
			// head of a changed transcript, and a file on a mount that stopped
			// answering held the run in "finding transcripts" (#5396).
			type ends struct {
				safe   int64
				sample uint64
			}
			e, err := sources.Watch(p, func() (ends, error) {
				safe := lastCompleteLineOffset(p, fi.Size())
				return ends{safe, filePrefixSample(p, safe)}, nil
			})
			if err != nil {
				// 0 is "not known": the next pass reads the file whole
				// rather than resuming from a guess.
				sources.NoteWalkError(p, err)
				e = ends{}
			}
			fs.SafeSize, fs.PrefixSample = e.safe, e.sample
		}
	}
	k, ok := kindIn(reg, p)
	if ok && k.Sidecar != nil {
		fs.MetadataSize, fs.MetadataMTime = k.Sidecar(p)
	}
	fs.WALSize, fs.WALMTime = walStat(p)
	// The kind's name is what harnessForPath answers for a path it matched;
	// only a path no kind claims goes on to its probe names.
	harness := k.Name
	if !ok {
		harness = harnessForPath(p)
	}
	if harness == "grok" {
		if cwd, err := os.Lstat(filepath.Join(filepath.Dir(filepath.Dir(p)), ".cwd")); err == nil && cwd.Mode()&os.ModeSymlink == 0 && !cwd.IsDir() {
			fs.CWDSize = cwd.Size()
			fs.CWDMTime = cwd.ModTime().UnixNano()
		}
	}
	return fs
}

// injectHermesPG adds the Postgres-backed Hermes store, which has no inode to
// stat. Its fingerprint is a query: count(*) as the size, max(timestamp) as the
// mtime, so the ordinary size/mtime change check drives a re-read (#1018). A
// store deja cannot reach this instant, or a DSN turned off for one run, keeps
// its old state — an unset env is an unmounted disk, not a deletion, and its
// sessions must not be dropped as if forgotten.
func injectHermesPG(out, old map[string]FileState) {
	dsn := sources.HermesPGDSN()
	if dsn == "" || !sources.StoreRead("hermes") {
		for p, of := range old {
			if sources.IsHermesPGStore(p) {
				out[p] = of
			}
		}
		return
	}
	token := sources.HermesPGStorePath(dsn)
	rows, newest, err := sources.HermesPGFingerprint(dsn)
	if err != nil {
		if of, ok := old[token]; ok {
			out[token] = of
		}
		return
	}
	out[token] = FileState{Path: token, Size: rows, MTime: newest, LastUpdated: newest}
}
