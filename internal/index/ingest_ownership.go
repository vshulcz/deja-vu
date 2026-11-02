package index

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// keepOrphanFiles marks the deleted transcripts this build still writes from
// as kept in files, and says how many there are.
func keepOrphanFiles(ss []model.Session, orphans *orphanState, files map[string]FileState, progress io.Writer) {
	// A kept file is kept for the sessions this build still writes from it.
	// One whose sessions were all forgotten or excluded above holds no
	// records once it is written, and the next rebuild no longer keeps it
	// (#5521).
	held := map[string]bool{}
	for _, s := range ss {
		held[s.Path] = true
	}
	for p := range orphans.files {
		if !held[p] {
			delete(orphans.files, p)
		}
	}
	for p, st := range orphans.files {
		if !st.Kept && orphans.dated {
			st.KeptAt = time.Now().Unix()
		}
		st.Kept = true
		files[p] = st
	}
	if progress != nil && len(orphans.files) > 0 {
		fmt.Fprintf(progress, "deja: %d transcript%s no longer on disk — still searchable; `deja resume <id> --write-back` puts one back, `deja forget --session <id>` drops one for good\n",
			len(orphans.files), pluralS(len(orphans.files)))
	}
	if progress != nil && orphans.unreadable > 0 {
		fmt.Fprintf(progress, "deja: %d transcript%s no longer on disk and written by an older deja — this build cannot read those records, so they go with this rebuild\n",
			orphans.unreadable, pluralS(orphans.unreadable))
	}
}

// keptTurnsOf maps each turn a deleted transcript holds to that transcript.
func keptTurnsOf(ss []model.Session, orphans orphanState) map[seenK]string {
	// A turn a deleted transcript shares with one on disk stays filed under
	// the deleted one, as the incremental pass files it: under the live copy,
	// the next rebuild no longer found it kept, and a rewrite of that copy
	// took it from the index. Written where the live copy has it, so the
	// session keeps its order.
	keptTurns := map[seenK]string{}
	for _, s := range ss {
		if _, ok := orphans.files[s.Path]; ok {
			for _, msg := range s.Messages {
				if k := seenKey(s.Key(), msg.Role, msg.Time, msg.Text); keptTurns[k] == "" {
					keptTurns[k] = s.Path
				}
			}
		}
	}
	return keptTurns
}

// rebuildRow folds s into the row the rebuild holds for its key.
func rebuildRow(sessions map[string]SessionMeta, s model.Session, text sessionText, orphans orphanState) {
	key := s.Key()
	ord := uint32(0)
	own := s
	if old, ok := sessions[key]; ok {
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
		ord = nextSessionOrd(sessions)
	}
	prev := sessions[key]
	owns, collided := claimSession(prev, s)
	if !holdsText(s) {
		emptied.Add(1)
	}
	if collided {
		collisions.Add(key)
	}
	if owns {
		row := ownerRow(prev, s, text.row(s), ord, collided)
		if kept, ok := orphans.rows[orphanKey(key, s.Path)]; ok {
			row = withKeptRow(row, kept, orphans.current)
		}
		sessions[key] = row
	} else {
		widenSpan(sessions, key, own)
	}
	if collided {
		markShared(sessions, key)
	}
	recordCopy(sessions, key, prev, s)
}

// mineRebuildSidecars writes the five sidecars a full build mines from ss.
func mineRebuildSidecars(tmp string, ss []model.Session, m Manifest, keptSource func(key string, s model.Session, msg model.Message) string) *commandFailAcc {
	reportPhase("mining fixes and commands", 5)
	// Five passes over the same sessions, each writing its own file and reading
	// nothing the others write, so they run together rather than one after the
	// other. Profiled on a real store, they were 9.4 s (fixes) and 6.0 s
	// (co-occurrence) of a 51 s build, with the whole machine idle beside them.
	var sidecars sync.WaitGroup
	var failAcc *commandFailAcc
	cmds := commandSessions(ss, m, keptSource)
	for _, build := range []func(){
		func() { buildCooccur(tmp, ss) },
		func() { buildFixes(tmp, cmds, func(s model.Session) string { return s.Key() }) },
		func() { buildCommands(tmp, cmds) },
		func() { failAcc = buildCommandFails(tmp, "", cmds) },
		func() { buildSessionFacts(tmp, cmds) },
	} {
		sidecars.Go(func() {
			build()
			reportAdvance(1)
		})
	}
	// Receipts read the same sessions; no phase of their own.
	sidecars.Go(func() { evidence.Build(tmp, ss, nil) })
	sidecars.Wait()
	return failAcc
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
			retitleWrappedPaste(&cp)
			s = &cp
			by[r.Key] = s
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: carriedText(s.Harness, r.Role, r.Text), Time: r.Time})
	})
	for _, sess := range by {
		deriveImportedNoteState(sess)
		out.sessions = append(out.sessions, *sess)
	}
	return out
}

// detectRenamedFiles pairs a path that has gone with a path that has appeared
// holding the same bytes. Both sides carry a prefix fingerprint already — the
// scan computes one for every .jsonl it has not seen before — so the match is
// on content, not on a name or a timestamp: same size, same last-complete-line
// offset, same sample, same harness.
//
// A wrong pairing would attach one conversation's history to another file, so
// the conditions are deliberately narrow, and a row with no fingerprint (a
// store built before the sample existed) is left alone.
func detectRenamedFiles(oldFiles, files map[string]FileState) map[string]string {
	gone := map[string]FileState{}
	for _, p := range sortedKeys(oldFiles) {
		if p == syncImportPath {
			continue
		}
		of := oldFiles[p]
		if of.PrefixSample == 0 || of.SafeSize == 0 {
			continue
		}
		if _, still := files[p]; still {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			continue // there after all, just not in this pass's set
		}
		gone[p] = of
	}
	if len(gone) == 0 {
		return nil
	}
	out := map[string]string{}
	taken := map[string]bool{}
	for _, np := range sortedKeys(files) {
		if _, seen := oldFiles[np]; seen {
			continue
		}
		nf := files[np]
		if nf.PrefixSample == 0 || nf.SafeSize == 0 {
			continue
		}
		for _, op := range sortedKeys(gone) {
			if taken[op] {
				continue
			}
			of := gone[op]
			if harnessForPath(op) != harnessForPath(np) {
				continue
			}
			same := of.Size == nf.Size && of.SafeSize == nf.SafeSize && of.PrefixSample == nf.PrefixSample
			// The same file renamed and written to since — a resumed session
			// under a new name. The fingerprint covers the bytes deja had read,
			// so it still answers whether this is that file; the row it carries
			// then makes the pass read the tail rather than the whole log.
			grown := !same && nf.Size > of.Size && nf.SafeSize >= of.SafeSize &&
				filePrefixSample(np, of.SafeSize) == of.PrefixSample
			if !same && !grown {
				continue
			}
			out[np] = op
			taken[op] = true
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyRenamedFiles moves a manifest from the old names to the new ones. The
// records themselves do not move: their source path is interned, so one entry
// in that table is every record's path at once. It returns the sessions the
// move put in another project.
func applyRenamedFiles(m *Manifest, renamed map[string]string) map[string]bool {
	reprojected := map[string]bool{}
	for _, np := range sortedKeys(renamed) {
		op := renamed[np]
		of, ok := m.Files[op]
		if !ok {
			continue
		}
		of.Path = np
		m.Files[np] = of
		delete(m.Files, op)
		// A move to another directory can be a move to another project: a
		// renamed checkout is a renamed Claude project folder. The project is
		// what the reader says for the new path, as a rebuild would set it.
		var moved []model.Session
		if filepath.Dir(np) != filepath.Dir(op) {
			moved, _ = parseAppendedFile("", np, FileState{}, true)
		}
		for key, meta := range m.Sessions {
			if slices.Contains(meta.Copies, op) {
				renameCopy(&meta, op, np)
				m.Sessions[key] = meta
			}
			if meta.Path == op {
				meta.Path = np
				for _, s := range moved {
					if s.Key() == key && s.Project != "" && s.Project != meta.Project {
						meta.Project = s.Project
						reprojected[key] = true
					}
				}
				m.Sessions[key] = meta
			}
		}
		// A row set aside follows its file too, or the transcript that takes
		// its id later loses the span a rebuild gives it (#5563).
		for key, meta := range m.Empty {
			if meta.Path == op {
				meta.Path = np
				for _, s := range moved {
					if s.Key() == key && s.Project != "" {
						meta.Project = s.Project
					}
				}
				m.Empty[key] = meta
			}
		}
		for i, str := range m.RecordStrings {
			if str == op {
				m.RecordStrings[i] = np
			}
		}
		if m.IngestFiles != nil {
			if fi, ok := m.IngestFiles[op]; ok {
				m.IngestFiles[np] = fi
				delete(m.IngestFiles, op)
			}
		}
	}
	return reprojected
}

// reprojectSidecars files the tables a session feeds under the project a
// rename moved it to. Nothing was read, so the pairs keep their rows and take
// the new project; the command tables are mined again from the records.
func reprojectSidecars(dir string, sessions map[string]SessionMeta, keys map[string]bool) {
	if BuiltSearchOnly(dir) {
		return
	}
	if raw, ok := readFixRaw(dir); !ok {
		remineFixes(dir)
	} else if len(raw) > 0 {
		dirty := false
		for i, p := range raw {
			if keys[p.Key] {
				raw[i].Project, dirty = sessions[p.Key].Project, true
			}
		}
		if dirty {
			writeFixTables(dir, raw)
		}
	}
	// Nothing was read, so what the records say is what the states say; the
	// tables take the new project from the rows. The command state goes last,
	// so it stays behind until both tables are written.
	buildCommandFailsFromIndex(dir, readCommandFailState(dir), nil)
	if m, err := readManifest(dir); err == nil {
		buildCommandTables(dir, dir, m, len(m.DeadRecords), nil)
	}
}

// orphanState is what a full rebuild has to carry: sessions whose transcript
// the client has deleted, and the file rows that keep them in the manifest.
type orphanState struct {
	sessions []model.Session
	files    map[string]FileState
	// rows is the row each kept file that owned one held, by orphanKey. The
	// records are not the file: a turn another copy holds too was filed under
	// that copy, and a repeated line was written once. Counted from them, the
	// row of a deleted transcript changed with every rebuild (#5380).
	rows map[string]SessionMeta
	// unreadable counts the ones whose records this build cannot decode. A
	// record-layout bump — version 38 moved the role out of the compressed
	// body — leaves an older store's bytes unreadable here, and a transcript
	// that is gone from disk has nowhere else to come from. Measured against
	// released binaries: a store from 0.19.5 (content version 35) upgrades
	// with its deleted-transcript sessions unrecoverable, and saying so is the
	// difference between a loss and a silent one.
	unreadable int
	// dated is the old manifest's KeptDated: a row first found gone here is
	// dated only when the store before this build dated its own.
	dated bool
	// current is whether this version wrote the old manifest.
	current bool
	// bare are the kept copies whose every turn was filed under another file
	// of the session, by key. They hold no records and stay the session's
	// copies, so a rewrite of the file holding their turns leaves them here.
	bare      map[string][]string
	bareFiles map[string]FileState
	// first is where each file of a session first shows in the old log, by
	// key and path: the order its copies are written in again.
	first map[string]map[string]int
}

// orphanedSessions preserves sessions the client's own housekeeping deleted.
// The incremental pass keeps them by never re-reading the file (#2970); a full
// rebuild reads the sources and writes what it read, so it wrote the store
// without them — 6 of 8 sessions gone on a store where the transcripts had
// been cleaned up, and a rebuild runs on a content-version bump, a changed
// exclude list and a damaged index, not only on `deja index --rebuild` (#3529).
//
// The rule is the incremental pass's: a file gone while the store directory
// around it is still there is the cleanup, and a tree gone whole is an
// uninstall or a disk that is not mounted, which is dropped as before.
func orphanedSessions(dir, harness string, files map[string]FileState) orphanState {
	out := orphanState{files: map[string]FileState{}, rows: map[string]SessionMeta{}}
	m, err := readManifest(dir)
	if err != nil {
		return out
	}
	out.dated = m.KeptDated
	out.current = m.Version == version
	want := map[string]bool{}
	var present map[string]bool
	moved := map[string]bool{}
	for _, op := range detectRenamedFiles(m.Files, files) {
		moved[op] = true
	}
	for _, p := range sortedKeys(m.Files) {
		if p == syncImportPath {
			continue // importedSessions carries these
		}
		if _, ok := files[p]; ok {
			continue
		}
		if harness != "" {
			name := harnessForPath(p)
			if store := sources.HarnessForKind(name); store != "" {
				name = store
			}
			if name != harness {
				continue
			}
		}
		if _, err := os.Lstat(p); err == nil {
			continue // on disk after all, just not in this pass's set
		}
		// Renamed or moved to another folder: read from where it is now,
		// as the incremental pass follows it.
		if moved[p] {
			continue
		}
		if !deletedFromLiveStore(p) {
			continue
		}
		// Moved with its directory, not deleted: the session is read from
		// where it is now (#4195).
		if d := goneSessionDir(p); d != "" {
			if present == nil {
				present = sessionDirsUnder(files)
			}
			if present[filepath.Base(d)] {
				continue
			}
		}
		want[p] = true
	}
	if len(want) == 0 {
		return out
	}
	by := map[string]*model.Session{}
	seen := map[string]bool{}
	spans := map[string]bool{}
	// Only the sessions of a kept file can be ordered by it.
	out.first = map[string]map[string]int{}
	for key, meta := range m.Sessions {
		for _, p := range sessionFiles(meta) {
			if want[p] {
				out.first[key] = map[string]int{}
				break
			}
		}
	}
	n := 0
	_ = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		n++
		if seenAt, ok := out.first[r.Key]; ok {
			if _, ok := seenAt[r.SourcePath]; !ok {
				seenAt[r.SourcePath] = n
			}
		}
		if !want[r.SourcePath] {
			return
		}
		seen[r.SourcePath] = true
		// Only a path that still has records: `deja forget` drops the records
		// and leaves the row, and carrying that row put a transcript deja no
		// longer holds back into doctor's "still searchable" count.
		out.files[r.SourcePath] = m.Files[r.SourcePath]
		// One session per file: two kept files under one id are two copies,
		// as they were while on disk (#5380). The one that owns the row is the
		// row; another spans its own records, since the row's span would hand
		// it the owner's.
		at := orphanKey(r.Key, r.SourcePath)
		s := by[at]
		if s == nil {
			meta, ok := m.Sessions[r.Key]
			if !ok {
				return
			}
			cp := sessionFromMeta(meta)
			cp.Path = r.SourcePath
			retitleWrappedPaste(&cp)
			if meta.Path == r.SourcePath {
				out.rows[at] = meta
			}
			// A row one file holds spans that file, as it did when the file
			// was read; beside copies, a file spans its own records.
			alone := meta.Path == r.SourcePath && len(meta.Copies) == 0 && !meta.Shared
			if !alone {
				cp.Started, cp.Updated = time.Time{}, time.Time{}
				spans[at] = true
			}
			s = &cp
			by[at] = s
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: carriedText(s.Harness, r.Role, r.Text), Time: r.Time})
		if spans[at] {
			s.Started, s.Updated = widerSpan(s.Started, s.Updated, r.Time, r.Time)
		}
	})
	for _, key := range sortedKeys(by) {
		out.sessions = append(out.sessions, *by[key])
	}
	heldBy := keysByFile(m.Sessions)
	for _, p := range sortedKeys(want) {
		if seen[p] {
			continue
		}
		if keys := heldBy[p]; len(keys) > 0 {
			if out.bare == nil {
				out.bare, out.bareFiles = map[string][]string{}, map[string]FileState{}
			}
			for _, key := range keys {
				out.bare[key] = append(out.bare[key], p)
			}
			out.bareFiles[p] = m.Files[p]
			continue
		}
		out.unreadable++
	}
	return out
}

// keepCopyOrder puts the copies of a session that has a kept file in the
// order the old log held them, for the records and what is mined from them.
// A transcript rewritten and moved in one pass is no rename: the pass keeps
// the old file's records where they were and appends the new file's, while
// the rebuild wrote every file on disk before the kept ones, so the session
// read back in another order and its facts, fixes and friction texts differed
// from the pass's. A file the log never held goes last, as a pass appends it.
func keepCopyOrder(ss []model.Session, orphans orphanState) {
	if len(orphans.files) == 0 {
		return
	}
	kept := map[string]bool{}
	for _, s := range ss {
		if _, ok := orphans.files[s.Path]; ok {
			kept[s.Key()] = true
		}
	}
	slots := map[string][]int{}
	for i, s := range ss {
		if key := s.Key(); kept[key] {
			slots[key] = append(slots[key], i)
		}
	}
	for key, at := range slots {
		if len(at) < 2 {
			continue
		}
		first := orphans.first[key]
		pos := func(s model.Session) int {
			if n, ok := first[s.Path]; ok {
				return n
			}
			return math.MaxInt
		}
		group := make([]model.Session, len(at))
		for j, i := range at {
			group[j] = ss[i]
		}
		sort.SliceStable(group, func(a, b int) bool { return pos(group[a]) < pos(group[b]) })
		for j, i := range at {
			ss[i] = group[j]
		}
	}
}

// orphanKey names a kept file's session: its row's key and the file.
func orphanKey(key, path string) string { return key + "\x00" + path }

// vanishedFromStores returns the sessions a database-backed store held at the
// last build and no longer hands back, for a store that is still on disk. One
// file there holds every session, so the file-level carry in orphanedSessions
// never sees a single one of them go: the file is still there. The incremental
// pass reads such a store from a cursor and keeps what it had; a rebuild reads
// it whole and, without this, kept only what is in it now.
//
// A store is database-backed when the last build stamped its LastUpdated —
// setStoreLastUpdated does that for those alone. fresh is what this build
// already has, sources and carries both, keyed as harness:id.
func vanishedFromStores(dir, harness string, files map[string]FileState, fresh []model.Session) []model.Session {
	m, err := readManifest(dir)
	if err != nil {
		return nil
	}
	stores := map[string]bool{}
	for p, st := range m.Files {
		// An aider history is a file and still one store of many sessions: a
		// session that left it — the file deleted and started again — is kept
		// by the incremental pass, and a rebuild has to keep it too (#4332).
		if st.LastUpdated <= 0 && harnessForPath(p) != "aider" {
			continue
		}
		if _, ok := files[p]; !ok {
			continue // gone whole: orphanedSessions answers for that
		}
		if harness != "" {
			name := harnessForPath(p)
			if store := sources.HarnessForKind(name); store != "" {
				name = store
			}
			if name != harness {
				continue
			}
		}
		stores[p] = true
	}
	return carryFromStores(dir, m, stores, fresh)
}

// unreadStoresCarry returns what the last build held from the stores this
// pass could not read: a read that failed or was given up for making no
// progress. Read whole, a rebuild wrote only what it read, so a Hermes store
// that stalled took every session it held out of the index (#5517). A store
// skipped is unread, not emptied; its file row is left out, so the next pass
// reads it again.
func unreadStoresCarry(dir, harness string, unread map[string]bool, fresh []model.Session) []model.Session {
	if len(unread) == 0 {
		return nil
	}
	m, err := readManifest(dir)
	if err != nil {
		return nil
	}
	stores := map[string]bool{}
	for p := range unread {
		if _, ok := m.Files[p]; !ok {
			continue
		}
		// Gone since the walk: orphanedSessions answers for that.
		if !sources.IsHermesPGStore(p) {
			if _, err := os.Lstat(p); err != nil {
				continue
			}
		}
		if harness != "" {
			name := harnessForPath(p)
			if store := sources.HarnessForKind(name); store != "" {
				name = store
			}
			if name != harness {
				continue
			}
		}
		stores[p] = true
	}
	return carryFromStores(dir, m, stores, fresh)
}

// carryFromStores returns the sessions the manifest m holds from stores that
// fresh does not, rebuilt from their records.
func carryFromStores(dir string, m Manifest, stores map[string]bool, fresh []model.Session) []model.Session {
	if len(stores) == 0 {
		return nil
	}
	have := make(map[string]bool, len(fresh))
	for _, s := range fresh {
		have[s.Key()] = true
	}
	// An OpenCode-schema session records its project directory as its path,
	// not the database (#2033), so the path alone carried none of them and a
	// rebuild dropped what the incremental pass keeps (#4447). The harness
	// names the store there, as sessionInStore has it.
	schemaDB := map[string]bool{}
	for h, db := range opencodeSchemaDBs {
		if stores[db()] {
			schemaDB[h] = true
		}
	}
	inStore := func(r Record) bool {
		if stores[r.SourcePath] {
			return true
		}
		h, _, _ := strings.Cut(r.Key, ":")
		return schemaDB[h] && inOpencodeSchemaDB(h, r.SourcePath)
	}
	by := map[string]*model.Session{}
	_ = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		if have[r.Key] || !inStore(r) {
			return
		}
		s := by[r.Key]
		if s == nil {
			meta, ok := m.Sessions[r.Key]
			if !ok {
				return
			}
			cp := sessionFromMeta(meta)
			cp.Path = r.SourcePath
			s = &cp
			by[r.Key] = s
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	})
	// An aider session the file still holds under another id — the ordinal
	// ids before #4332 — has not left it, and carrying it would index it twice.
	started := aiderStarts(fresh)
	out := make([]model.Session, 0, len(by))
	for _, key := range sortedKeys(by) {
		s := by[key]
		if s.Harness == "aider" && started[aiderStart(s.Path, s.Started)] {
			continue
		}
		out = append(out, *s)
	}
	return out
}

// deriveImportedNoteState recovers the state of an imported promoted note from
// the note text. #984 started recording that state on the manifest row without
// bumping the index format, so a store that imported a batch before it holds a
// row with no state at all and nothing re-derives it — the batch is deduped,
// so re-importing adds 0 records and the decision the other machine retracted
// reads as accepted here (#1049).
func deriveImportedNoteState(s *model.Session) {
	if s.Harness != "deja" || s.Lifecycle != "" {
		return
	}
	for _, msg := range s.Messages {
		st, note, ok := noteStateFromText(msg.Text)
		if !ok {
			continue
		}
		s.Lifecycle, s.LifecycleNote = st, note
		// The UTC day, as import stores it: records come back in this
		// machine's zone, and the rebuild must not move the date (#5053).
		if !msg.Time.IsZero() {
			s.LifecycleAt = msg.Time.UTC().Format(lifecycleDay)
		}
		return
	}
}

// widenSpan takes the span of a file that shares key's id and does not own
// its row into that row. The owner merges the earlier files' span in when it
// is read second, so without this the row's Started and Updated followed the
// file names' order, where an update takes every file's span (#4253).
func widenSpan(sessions map[string]SessionMeta, key string, s model.Session) {
	meta, ok := sessions[key]
	if !ok {
		return
	}
	meta.Started, meta.Updated = widerSpan(meta.Started, meta.Updated, s.Started, s.Updated)
	meta.SharedStarted, meta.SharedUpdated = widerSpan(meta.SharedStarted, meta.SharedUpdated, s.Started, s.Updated)
	sessions[key] = meta
}
