package index

import (
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/redact"
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
				if k := seenKey(s.Harness+":"+s.ID, msg.Role, msg.Time, msg.Text); keptTurns[k] == "" {
					keptTurns[k] = s.Path
				}
			}
		}
	}
	return keptTurns
}

// rebuildRow folds s into the row the rebuild holds for its key.
func rebuildRow(sessions map[string]SessionMeta, s model.Session, text sessionText, orphans orphanState) {
	key := s.Harness + ":" + s.ID
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
		func() { buildFixes(tmp, cmds, func(s model.Session) string { return s.Harness + ":" + s.ID }) },
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
					if s.Harness+":"+s.ID == key && s.Project != "" && s.Project != meta.Project {
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
					if s.Harness+":"+s.ID == key && s.Project != "" {
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
	// tables take the new project from the rows.
	if m, err := readManifest(dir); err == nil {
		buildCommandTables(dir, dir, m, len(m.DeadRecords), nil)
	}
	buildCommandFailsFromIndex(dir, readCommandFailState(dir), nil)
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
			kept[s.Harness+":"+s.ID] = true
		}
	}
	slots := map[string][]int{}
	for i, s := range ss {
		if key := s.Harness + ":" + s.ID; kept[key] {
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
		have[s.Harness+":"+s.ID] = true
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

// ownerRow is the row of s, which owns it. prev is the row held before, and
// collided says whether s collided with it, and meta is s's own row,
// metaForSession's. The span of the files that share
// the id stays in the row, so reading the owner alone does not drop it
// (#4574): prev's own span when prev was another file sharing the id, a
// collision or a stub with nothing to index, and what prev kept when it was
// the same file. A row s supersedes, a ZCode snapshot restored into the
// database, gives it nothing.
func ownerRow(prev SessionMeta, s model.Session, meta SessionMeta, ord uint32, collided bool) SessionMeta {
	meta = metaWithOrd(meta, ord)
	switch {
	case prev.Path == "" || prev.Path == s.Path:
		meta.SharedStarted, meta.SharedUpdated = prev.SharedStarted, prev.SharedUpdated
	case collided || prev.NoText:
		meta.SharedStarted, meta.SharedUpdated = widerSpan(prev.SharedStarted, prev.SharedUpdated, prev.Started, prev.Updated)
	}
	meta.Started, meta.Updated = widerSpan(meta.Started, meta.Updated, meta.SharedStarted, meta.SharedUpdated)
	return meta
}

// rowRetaken is a row about to be taken again from its files: the span of the
// copies beside its owner goes, and a deleted owner gives up the row until its
// records are read back in, as a rebuild reads them after the files on disk.
// So does an owner read again whole, which takes the row back if it still
// holds the session; held, it kept a file that had moved on to another id.
func rowRetaken(row SessionMeta, keptOwner string, reread map[string]FileState) SessionMeta {
	row.SharedStarted, row.SharedUpdated = time.Time{}, time.Time{}
	_, again := reread[row.Path]
	if keptOwner != "" && row.Path == keptOwner || again && copyable(row.Path) {
		row.Path, row.Shared = "", false
	}
	return row
}

// widerSpan is the span covering both.
func widerSpan(started, updated, s2, u2 time.Time) (time.Time, time.Time) {
	if !s2.IsZero() && (started.IsZero() || s2.Before(started)) {
		started = s2
	}
	if u2.After(updated) {
		updated = u2
	}
	return started, updated
}

// claimSession is attributeSession for a session read this pass. A transcript
// with nothing to index is not a second conversation: Gemini CLI's resume
// leaves a file holding only the preamble deja strips, under the id of the
// transcript it appends to. Sort order handed that file the row, and with no
// records under it the session was dropped from the index (#4213). Either
// side can be the empty one, since either can be read first. Only where the
// pair would be reported as a clash: the store pairs attributeSession knows
// (goose, opencode, codex) keep their own rule.
func claimSession(held SessionMeta, s model.Session) (owns, collided bool) {
	owns, collided = attributeSession(held, s)
	if collided {
		if !holdsText(s) {
			return false, false
		}
		if held.NoText {
			return true, false
		}
	}
	return owns, collided
}

// attributeSession decides which of two transcripts sharing an id owns the
// manifest row, and whether they collided at all. Lexicographically smallest
// path wins, so the answer does not depend on which file was read first.
func attributeSession(held SessionMeta, s model.Session) (owns, collided bool) {
	if held.Path == "" || s.Path == "" || held.Path == s.Path {
		return true, false
	}
	merged.Add(1)
	// goose 1.10 moved its sessions into sessions.db and left the JSONL files
	// where they were, so after a migration the same conversation is in both
	// stores under one id — the db row keeps the id the file was named after.
	// That is one conversation across two storage generations, not two
	// transcripts clashing, and sort order decided it: `<stamp>.jsonl` sorts
	// below `sessions.db`, so every migrated session was filed under the
	// superseded copy and counted as a collision the reader can do nothing
	// about (#2066). The live store owns the row and the pair is not reported.
	if s.Harness == "goose" {
		if newIsDB, heldIsDB := isGooseStore(s.Path), isGooseStore(held.Path); newIsDB != heldIsDB {
			return newIsDB, false
		}
	}
	// opencode writes a per-session diff file beside its database and both
	// carry the same session id — one conversation in two stores, the same
	// shape as the goose migration above. The database row owns the session;
	// the diff is that session's account of what it changed, and the messages
	// merge either way. Reporting the pair told a reader with 84 such diffs
	// that 84 of their sessions were clashing, which is a data problem they do
	// not have (#3791).
	if s.Harness == "opencode" {
		if newIsDiff, heldIsDiff := isOpencodeDiff(s.Path), isOpencodeDiff(held.Path); newIsDiff != heldIsDiff {
			return !newIsDiff, false
		}
	}
	// Codex writes an interactive session to its rollout and a line of it to
	// history.jsonl under the same id. The rollout holds the conversation and
	// the directory it ran in; the history line holds the prompt and the
	// project "history". Sort order gave the row to history.jsonl, so a TUI
	// session was filed outside its project and resume refused it as an exec
	// entry (#4180). TRAE writes the same pair, and Claude Code's prompt log
	// sorts ahead of its projects folder the same way (#5046).
	if s.Harness == "codex" || s.Harness == "trae" || s.Harness == "claude" {
		if newIsHist, heldIsHist := isCodexHistory(s.Path), isCodexHistory(held.Path); newIsHist != heldIsHist {
			return !newIsHist, false
		}
	}
	// A ZCode snapshot restored into the CLI database is the same
	// conversation there under the same id, and the snapshot stays on disk
	// unchanged, so an incremental pass never re-reads it to skip it. The
	// database owns the row (#4432).
	if s.Harness == "zcode" {
		if newIsSnap, heldIsSnap := isZCodeSnapshot(s.Path), isZCodeSnapshot(held.Path); newIsSnap != heldIsSnap {
			return !newIsSnap, false
		}
	}
	return s.Path < held.Path, true
}

// coversKept reports whether an arrival holds what the kept row indexed, which
// a moved transcript does and a different file under the same id does not. The
// test is the row's last message: a move carries it, and so does a move that
// grew on the way. Gemini CLI's resume stub gains turns of its own after the
// transcript it was resumed from is deleted, and taking it for the move threw
// away that transcript's records for good (#4213). A count alone would not do:
// the stub can outgrow a short transcript. A row from before LastMsg was kept
// has nothing to compare, so it falls back to the count.
func coversKept(r model.Session, meta SessionMeta) bool {
	if meta.LastMsg == 0 {
		return len(r.Messages) >= meta.Counted
	}
	for _, m := range slices.Backward(r.Messages) {

		// Fingerprinted as stored: the row's was taken after redaction, and
		// the arrival has not been redacted yet.
		m.Text, _, _ = indexedText(m.Text)
		if messageFingerprint(m) == meta.LastMsg {
			return true
		}
		// The command may have been indexed before its result landed; the
		// re-read adds the exit status to the same message. Codex compressing a
		// rollout in place is still the move, so match it without the suffix.
		if base := exitSuffix.ReplaceAllString(m.Text, ""); base != m.Text {
			m.Text = base
			if messageFingerprint(m) == meta.LastMsg {
				return true
			}
		}
	}
	return false
}

// holdsKeptRecords reports whether r holds every turn filed under the kept
// file at path. A copy of the session that only shares its last turn is not
// the file moved, and taking it for one dropped the turns only the deleted
// file held. Asked only once coversKept has matched, which is rare.
func holdsKeptRecords(dir string, m Manifest, r model.Session, path string) bool {
	recs, err := recordsForKey(filepath.Join(dir, "records.bin"), tablesFromManifest(m), r.Harness+":"+r.ID)
	if err != nil {
		return false
	}
	have := map[uint64]bool{}
	for _, msg := range r.Messages {
		msg.Text, _, _ = indexedText(msg.Text)
		have[messageFingerprint(msg)] = true
		if base := exitSuffix.ReplaceAllString(msg.Text, ""); base != msg.Text {
			msg.Text = base
			have[messageFingerprint(msg)] = true
		}
	}
	for _, rec := range recs {
		if rec.SourcePath != path {
			continue
		}
		msg := model.Message{Role: rec.Role, Text: carriedText(r.Harness, rec.Role, rec.Text), Time: rec.Time}
		if have[messageFingerprint(msg)] {
			continue
		}
		if base := exitSuffix.ReplaceAllString(msg.Text, ""); base != msg.Text {
			msg.Text = base
			if have[messageFingerprint(msg)] {
				continue
			}
		}
		return false
	}
	return true
}

// exitSuffix is the status the parsers append to a command once its result is
// read: "  → exit N".
var exitSuffix = regexp.MustCompile(`  → exit -?\d+$`)

// holdsText reports whether any message of s has text left to index once
// plumbing is stripped. Stripping is idempotent, so it answers the same before
// and after preRedactSessions.
func holdsText(s model.Session) bool {
	for _, m := range s.Messages {
		if strings.TrimSpace(stripSelfRecall(m.Text)) != "" {
			return true
		}
	}
	return false
}

// isCodexHistory reports whether a path is a prompt log rather than a
// transcript: Codex's, TRAE's or Claude Code's history.jsonl. Named, for the
// reason isGooseStore gives.
func isCodexHistory(path string) bool {
	return sources.IsPromptLog(path)
}

// promptLogTakeover reports whether a file new to this pass is the transcript
// of a session the index holds from a prompt log alone. Claude Code writes the
// prompt to history.jsonl before the transcript line, and deja's own prompt
// hook runs a pass in between, so a new session's first prompt is indexed from
// the log and its transcript lands a pass later. The append path can only add
// to what is stored; the replacement path drops the log's copy, which is what a
// rebuild, reading the transcript and skipping the line, gives. A Codex rollout
// after its history line is the same pair. The id is in the file name: the
// whole stem for Claude, the trailing UUID of a rollout.
func promptLogTakeover(changed, oldFiles map[string]FileState, sessions map[string]SessionMeta) bool {
	logged := map[string]bool{}
	for _, meta := range sessions {
		if isCodexHistory(meta.Path) {
			logged[meta.ID] = true
		}
	}
	if len(logged) == 0 {
		return false
	}
	for p := range changed {
		if _, known := oldFiles[p]; known || isCodexHistory(p) {
			continue
		}
		stem := filepath.Base(fileStem(p))
		if logged[stem] || (len(stem) > 36 && logged[stem[len(stem)-36:]]) {
			return true
		}
	}
	return false
}

// promptLogTakeovers names the sessions of this batch whose row came from a
// prompt log and that a transcript now holds.
func promptLogTakeovers(ss []model.Session, held map[string]SessionMeta) map[string]bool {
	out := map[string]bool{}
	for _, s := range ss {
		key := s.Harness + ":" + s.ID
		if meta, ok := held[key]; ok && isCodexHistory(meta.Path) && !isCodexHistory(s.Path) {
			out[key] = true
		}
	}
	return out
}

// freshPromptWait is how long a prompt-log line with no transcript beside it
// waits before it is taken for a session whose transcript is gone. Claude Code
// writes the transcript line a moment after the prompt; a pass in between
// would index the prompt twice and leave the takeover to a full rewrite.
const freshPromptWait = 5 * time.Minute

// waitsForTranscript reports whether a prompt log's tail holds a session the
// index does not know whose last prompt is younger than freshPromptWait. The
// file is then left for the next pass to read from the same offset, by which
// time the transcript is on disk and the parser skips the line.
func waitsForTranscript(ss []model.Session, held map[string]SessionMeta, now time.Time) bool {
	for _, s := range ss {
		if _, ok := held[s.Harness+":"+s.ID]; ok {
			continue
		}
		if !s.Updated.IsZero() && now.Sub(s.Updated) < freshPromptWait {
			return true
		}
	}
	return false
}

// dropShadowedPrompts drops a session read from a prompt log when a transcript
// holds the same id, in this batch or in the rows the index already has. A
// prompt log is read for sessions whose transcript is not on disk, and one deja
// kept after the client deleted it is exactly that: its prompts merged into
// the kept conversation a second time, at the log's own instant, and on the
// append path the log took the row because the kept path was gone (#5046). A
// rebuild sees the kept transcript in its batch, an incremental pass in held,
// so both drop the same lines.
func dropShadowedPrompts(ss []model.Session, held map[string]SessionMeta) []model.Session {
	transcript := map[string]bool{}
	logs := false
	for _, s := range ss {
		if isCodexHistory(s.Path) {
			logs = true
		} else {
			transcript[s.Harness+":"+s.ID] = true
		}
	}
	if !logs {
		return ss
	}
	out := make([]model.Session, 0, len(ss))
	for _, s := range ss {
		if isCodexHistory(s.Path) {
			key := s.Harness + ":" + s.ID
			if transcript[key] {
				continue
			}
			if meta, ok := held[key]; ok && meta.Path != "" && !isCodexHistory(meta.Path) {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

// isZCodeSnapshot reports whether a ZCode session's path is a snapshot an
// older ZCode left: the CLI database's sessions carry their directory and the
// transcripts end in .jsonl.
func isZCodeSnapshot(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".json")
}

// isOpencodeDiff reports whether a path is one of opencode's per-session diff
// files rather than its database. Named, not compared against
// sources.OpencodeDiffDir(), for the reason isGooseStore gives: the row deja
// already holds was written by an earlier pass that may have read a
// differently configured root.
func isOpencodeDiff(path string) bool {
	if !strings.EqualFold(filepath.Ext(path), ".json") {
		return false
	}
	return filepath.Base(filepath.Dir(path)) == "session_diff"
}

// isGooseStore reports whether a path is goose's database rather than one of
// the JSONL files beside it. Named, not compared against sources.GooseDB(),
// because the row deja already holds was written by an earlier pass that may
// have read a differently configured root.
func isGooseStore(path string) bool {
	return strings.EqualFold(filepath.Base(path), "sessions.db")
}

// replacementPassMarker opens the line the replacement-grade pass prints, and
// is what the tests that care which path ran match on. They used to match
// "incremental index", which is why that phrase survived in a line a user
// reads: an assertion on prose keeps the prose from being fixed.
const replacementPassMarker = "re-reading"

// pluralS keeps "1 sessions" off the first line anyone sees from deja (#737).
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// pluralThem is pluralS for the pronoun that follows it, so a run never says
// "1 line skipped, deja could not read them".
func pluralThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// recordsForKey collects one session's records. It peeks the key field and
// skips the body of everything else: decoding all of them to keep a few
// hundred cost 300 ms per session on an 80 MB log, paid twice on every bare
// `deja` by the two lines that recover text from a hash (#625).
func recordsForKey(path string, t *recordTables, key string) ([]Record, error) {
	var out []Record
	err := eachRecordForKeys(path, t, map[string]bool{key: true}, func(r Record) {
		out = append(out, r)
	})
	return out, err
}

func redactForIngest(m *Manifest, sourcePath, text string) string { //nolint:unparam // the result is the stored text; tests read only the counts
	// Drop deja's own injected recall before anything else looks at the text,
	// so it is never counted, tokenized or stored.
	text = stripSelfRecall(text)
	// Canonicalise accented text to NFC so an "é" stored decomposed (base + a
	// combining mark, as some editors and macOS filesystems emit) matches a
	// query typed precomposed and the reverse. NFC is lossless — it names the
	// same characters — so unlike a fold it is safe to store, and it makes every
	// downstream surface (postings, snippet, digest) compare like against like
	// (#1098).
	text = nfcfold.Compose(text)
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
		countClipped(m, sourcePath, "", 1)
	}
	n := counts.Total()
	if n == 0 || m == nil {
		return redacted
	}
	m.Redacted += n
	if m.RedactionRules == nil {
		m.RedactionRules = map[string]int{}
	}
	// The store, not the file kind: `deja stats` prints these as headings, and
	// "cline-sdk" is a word no other screen uses (#2238, the shape #2234 fixed
	// for the ingest counters).
	h := sources.HarnessForKind(harnessForPath(sourcePath))
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
		// The store, matching the key the rules are filed under since #2238:
		// asking for the file kind here left "cline-sdk" against a "cline" key,
		// so nothing matched and every incremental pass carried the old counts
		// on top of the fresh ones (#2240).
		h := sources.HarnessForKind(harnessForPath(path))
		if h == "" && path == sources.OpencodeDB() {
			h = "opencode"
		}
		skipHarness[h] = true
	}
	for key, count := range old.RedactionRules {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		// Folded before the lookup: an index written before #2238 files these
		// by file kind, and a pass since then drops by store — so a stale
		// "cline-sdk" count survived its file being re-read and was added to
		// the fresh "cline" one when the report folded them (#2240).
		name := parts[0]
		if store := sources.HarnessForKind(name); store != "" {
			name = store
		}
		if !skipHarness[name] {
			m.RedactionRules[key] = count
		}
	}
}

// fromDatabase reports whether a record came out of a shared store rather than
// a file of its own, which is what exempts it from the changed-file rule: the
// database changes whenever any session in it does, and dropping every record
// that names it would take the untouched sessions along.
//
// The path settles it for goose and cursor, which name the database as the
// session's path. opencode names the project directory, so nothing about its
// path says "database" — the key names the harness (#2033). The same holds for
// every store read through OpenCode's schema (opencodeSchemaDBs).
func fromDatabase(r Record) bool {
	// The key first: asking the path first got this wrong for an opencode
	// project that lives inside another harness's root — a versioned ~/.claude,
	// say — where harnessForPath answers with that harness's kind.
	if h, _, ok := strings.Cut(r.Key, ":"); ok && inOpencodeSchemaDB(h, r.SourcePath) {
		return true
	}
	// A path that names a per-file kind settles it: two transcripts in
	// different projects can share a filename-derived id, and judging those by
	// key erased the sibling that was never re-read (#699).
	return storeHarness(r.SourcePath) != ""
}

// leftItsFile reports a session that a re-read file no longer holds, in a
// harness that keeps many sessions in one file. aider appends every launch to
// one history, and people delete it because it grows forever: the next launch
// starts a new file at the same path, and dropping by path took every session
// the deleted file held, which a deleted transcript keeps (#2970, #4332).
//
// A session the file still holds under another id has not left it: ids before
// #4332 were ordinals, and two launches in one second are told apart by order.
// held is aiderStarts of what the pass read.
func leftItsFile(r Record, reread map[string]bool, meta SessionMeta, held map[string]bool) bool {
	harness, _, _ := strings.Cut(r.Key, ":")
	return harness == "aider" && !reread[r.Key] && !held[aiderStart(r.SourcePath, meta.Started)]
}

// aiderStarts is the history path and start time of every aider session in
// ss, the one thing that names an aider session across a change of its id.
func aiderStarts(ss []model.Session) map[string]bool {
	out := map[string]bool{}
	for _, s := range ss {
		if s.Harness == "aider" {
			out[aiderStart(s.Path, s.Started)] = true
		}
	}
	return out
}

func aiderStart(path string, started time.Time) string {
	return path + "\x00" + started.UTC().String()
}

// readWholeThisPass reports whether the pass re-read this record's store in
// full. Only then may an old record be dropped because its key came back: a
// store read from a watermark hands back the new turns alone, and dropping the
// rest by key would take the earlier turns of a continued session with them.
//
// By store rather than by harness where the record names one: cursor keeps a
// database per workspace as well as the global one, and a first sight of a new
// workspace — opening a project — has no watermark, so a harness-wide flag let
// that pass replace sessions in the store it had only read the tail of.
func readWholeThisPass(r Record) bool {
	if len(passWholeStores) == 0 {
		return false
	}
	// opencode's records name a project directory or, from older passes, a
	// diff file; either way the session is the store's, read whole (#4207).
	// Kilo's and ZCode's are the same store shape (#4396); a record from their
	// transcript files is not a store's and never reaches this.
	if h, _, ok := strings.Cut(r.Key, ":"); ok && opencodeSchemaDBs[h] != nil {
		return passWholeStores[h]
	}
	if storeHarness(r.SourcePath) != "" {
		return passWholeStores[r.SourcePath]
	}
	harness, _, ok := strings.Cut(r.Key, ":")
	return ok && passWholeStores[harness]
}

// passWholeStores names the database-backed stores this pass read whole, by
// harness. Package state for the same reason passParsed is: a pass holds the
// directory lock.
var passWholeStores map[string]bool

// passFromWatermark names the database stores this pass read from their
// watermark, so only rows changed since came back. passWholeStores cannot say
// it: zed is in there on every pass, because the threads it does hand back
// come whole (#4341).
var passFromWatermark map[string]bool

// wholeStoresThisPass records them, under both the store path and the harness:
// a record names the first where it can and the second otherwise.
//
// Every store setDatabaseStoreWatermarks stamps belongs here, and storeHarness
// is the one list of them: a stamped store is parsed from its watermark, so its
// records must survive a pass that did not hand them back, and only a pass that
// read the store whole may drop one because its key came back.
func wholeStoresThisPass(changed, old map[string]FileState) {
	passWholeStores = map[string]bool{}
	passFromWatermark = map[string]bool{}
	// Rebuilt here rather than kept: a store that appeared since the last pass
	// is one the walk has to see.
	passStores = resolveStorePaths()
	passStoreHarness = new(sync.Map)
	passSchemaDBs.Store(nil)
	for p := range changed {
		harness := storeHarness(p)
		if harness == "" {
			continue
		}
		if old[p].LastUpdated != 0 {
			passFromWatermark[p] = true
		}
		if old[p].LastUpdated == 0 || rereadsWholeSessions(p) {
			passWholeStores[harness] = true
			passWholeStores[p] = true
		}
	}
}

// rereadsWholeSessions marks a store whose cursor selects sessions rather than
// messages: goose asks for every message of any session touched since the
// stamp, so a continued session hands back turns already counted, and adding
// them again grew the count on every pass. Starting the file over is the lesser
// wrong — it loses what an untouched session held, which is #2025 again for
// this one store, rather than a number that only climbs. Narrowing the clause
// instead costs the session its earlier turns (#2033), so the re-reading stays.
func rereadsWholeSessions(p string) bool {
	if harnessForPath(p) == "goose-db" {
		return true
	}
	switch storeHarness(p) {
	case "opencode", "kilocode", "zcode":
		// Read by session since #4207: a reply's text lands in a part created
		// before the last pass, so only a touched session read whole carries
		// it, and adding that to what the index held doubled the turns. Its
		// counts start over with each pass, the goose trade above.
		return true
	case "grok", "zed", "hermes", "openclaw", "crush", "claude":
		// The same shape: each asks for the sessions touched since the stamp
		// and hands them back whole, so what comes back replaces what the
		// index holds for that key rather than adding to it (#2075). The
		// clauses are session-scoped for exactly this reason — a message-scoped
		// one returns the newest turn alone, and replacing a session with it
		// loses the rest.
		return true
	case "cursor":
		// Since #4450 and #4451 a touched composer comes back with all its
		// bubbles, so a rename is read and the derived fields count every turn.
		return true
	}
	return false
}

// storeHarness names the harness whose shared store lives at p, and "" for
// anything else.
//
// harnessForPath answers with a kind, and three of the six stores share their
// kind name with the harness's own transcripts — grok and hermes both have
// files as well as a database, and a kind name cannot tell them apart. The
// store paths can, and a record that names one belongs to a store read from a
// watermark: it must not be dropped because the database changed, and it may be
// replaced when its key comes back.
func storeHarness(p string) string {
	if p == "" {
		return ""
	}
	// Per path, not per record: every record of a goose or cursor store names
	// the same database, and asking the registry again for each one was a
	// third of a one-message pass over 5,000 sessions (#4272).
	if h, ok := passStoreHarness.Load(p); ok {
		if s, isString := h.(string); isString {
			return s
		}
	}
	h := resolveStoreHarness(p)
	passStoreHarness.Store(p, h)
	return h
}

// passStoreHarness memoises storeHarness for one pass; wholeStoresThisPass
// starts it over with passStores.
var passStoreHarness = new(sync.Map)

func resolveStoreHarness(p string) string {
	if h, ok := passStorePaths()[p]; ok {
		return h
	}
	if sources.IsHermesPGStore(p) {
		return "hermes"
	}
	for h, db := range passSchemaDBPaths() {
		if p == db {
			return h
		}
	}
	switch harnessForPath(p) {
	case "opencode-diff":
		// A diff file is read as its session from the database (#4207), so it
		// is that store's as much as the database file is.
		return "opencode"
	case "cursor-db":
		return "cursor"
	case "goose-db":
		return "goose"
	case "openclaw-db":
		return "openclaw"
	}
	return ""
}

// passStorePaths is the store-path half of storeHarness, resolved once.
//
// fromDatabase asks per record, and the answer needs the store paths, two of
// which are found by walking a directory. Measured at ~11 µs a record with the
// walk on the hot path, which is a second per hundred thousand records on every
// pass. A pass holds the directory lock, so the map lives beside
// passWholeStores and is built where that one is.
func passStorePaths() map[string]string {
	// wholeStoresThisPass builds it at the top of a pass, before anything asks.
	// The fallback is for a caller outside a pass — a search recovering a
	// damaged index reads records without one.
	if passStores == nil {
		passStores = resolveStorePaths()
	}
	return passStores
}

var passStores map[string]string

// passSchemaDBPaths is opencodeSchemaDBs resolved once a pass. Resolving
// opencode's stats two files when XDG_DATA_HOME is set, and storeHarness asks
// for every path it meets for the first time: on 40,000 transcripts that was
// a second of a pass that replaced 20 of them.
func passSchemaDBPaths() map[string]string {
	if m := passSchemaDBs.Load(); m != nil {
		return *m
	}
	m := make(map[string]string, len(opencodeSchemaDBs))
	for h, db := range opencodeSchemaDBs {
		m[h] = db()
	}
	// Only inside a pass: outside one nothing says when the stores move.
	if passesRunning.Load() > 0 {
		passSchemaDBs.Store(&m)
	}
	return m
}

var (
	passSchemaDBs atomic.Pointer[map[string]string]
	passesRunning atomic.Int32
)

// opencodeSchemaDBs are the databases read through OpenCode's schema, by
// harness. A session from one names its project directory as its path, not the
// database, so only the harness in its key says where it came from. Kilo CLI
// and ZCode vendor that schema, and knowing opencode's alone left their
// sessions re-read whole on every write to the database and appended to the
// records already held: a Kilo session doubled on each pass (#4396).
var opencodeSchemaDBs = map[string]func() string{
	"opencode": sources.OpencodeDB,
	"kilocode": sources.KiloDB,
	"zcode":    sources.ZCodeDB,
}

// inOpencodeSchemaDB reports whether a session of harness h whose path is p
// came out of that harness's OpenCode-schema database.
func inOpencodeSchemaDB(h, p string) bool {
	db, ok := opencodeSchemaDBs[h]
	if !ok {
		return false
	}
	// opencode's diff files, Kilo's task files and ZCode's transcripts and
	// snapshots carry the same harness name; anything else is a project directory. A diff
	// record still counts as the database's through storeHarness, which files
	// the diff path under that store.
	//
	// Asked before the database path: resolving it can stat two files (with
	// XDG_DATA_HOME set, opencode's), and fromDatabase asks this of every
	// record an incremental pass holds.
	return !opencodeSchemaOwnFile(h, p) || p == db()
}

// opencodeSchemaOwnFile reports whether p is one of harness h's own files
// rather than its database or a project directory. Named, for the reason
// isGooseStore gives, and because fromDatabase asks it of every record held:
// asking the registry what a project directory was cost ~68 µs a record.
func opencodeSchemaOwnFile(h, p string) bool {
	switch h {
	case "opencode":
		return isOpencodeDiff(p)
	case "kilocode":
		return strings.EqualFold(filepath.Base(p), "api_conversation_history.json")
	case "zcode":
		// Its transcripts, and the snapshots an older ZCode left, each
		// rewritten whole (#4432).
		return strings.EqualFold(filepath.Ext(p), ".jsonl") || isZCodeSnapshot(p)
	}
	return false
}

func resolveStorePaths() map[string]string {
	out := map[string]string{sources.GrokDB(): "grok", sources.ZedDB(): "zed"}
	for _, db := range sources.HermesDBs() {
		out[db] = "hermes"
	}
	// One store per Crush project, each read from its own watermark (#4381).
	for _, db := range sources.CrushDBs() {
		out[db] = "crush"
	}
	if cm := sources.ClaudeMemDBPath(); cm != "" {
		out[cm] = "claude"
	}
	return out
}

// fullyReadFiles drops the files a pass reads only part of. LastUpdated is
// stamped for database-backed stores alone (setStoreLastUpdated), and it is
// exactly what makes their parse partial: parseChangedFile hands it to the kind
// as the since cursor.
func fullyReadFiles(changed, old map[string]FileState) map[string]FileState {
	out := make(map[string]FileState, len(changed))
	for p, f := range changed {
		if old[p].LastUpdated > 0 && !rereadsWholeSessions(p) {
			continue
		}
		out[p] = f
	}
	return out
}

// copyIngestFiles hands the new manifest its own map: the old one belongs to
// the manifest this build read, which is still in use while the build runs. The
// files this pass will re-read arrive with their clip count zeroed — the pass
// records its own as it goes, and adding it to what the last pass found counted
// one long message twice.
func copyIngestFiles(old map[string]FileIngest, reread map[string]FileState, logTails map[string]map[string]bool) map[string]FileIngest {
	out := make(map[string]FileIngest, len(old))
	for p, e := range old {
		if ids, ok := logTails[p]; ok {
			e = clipsOutsideTail(e, ids)
		} else if _, ok := reread[p]; ok {
			e.Clipped = 0
			e.ClippedSessions = nil
		} else {
			e.ClippedSessions = maps.Clone(e.ClippedSessions)
		}
		out[p] = e
	}
	return out
}

// beginPass clears the parsers' skip counters, which belong to the pass that
// parsed. They used to be cleared only by the manifest fold, so a pass that died
// before writing left its count for the next one to report: one bad line on
// disk, "2 lines skipped" on screen, with the manifest agreeing (#2010).
//
// Called at every place a pass parses: this one and rebuildWithTombstones,
// which a recall reaches directly once an index is found damaged, without
// passing through updateIndex at all, and which forget and unforget call for
// themselves.
func beginPass() {
	sources.DiagBeginPass()
	passParsed = nil
	passRead = nil
	passWholeStores = nil
	passFromWatermark = nil
	passSchemaDBs.Store(nil)
}

// passParsed is the set of files the pass in progress re-read. Package state
// for the same reason lastIngestFiles is: a pass holds the directory lock, so
// only one is ever in flight.
var passParsed map[string]bool

// passRead is the files a pass read without the open failing. The append path
// cannot use parsedThisPass — it reads a tail, so its counts add — but a file
// it read is a file that opens, and an error recorded when it did not has to
// go, or a permission blip stayed in doctor until a forced rebuild (#2015).
var passRead map[string]bool

// passWalked is the walk the pass in progress started from, copied before the
// pass drops the files it could not read from that same map. Deferred needs
// the state the walk saw for exactly those files (#5105). Nil outside a pass,
// so a manifest write that is not one (sync, forget) keeps Deferred as is.
var passWalked map[string]FileState

// walkedThisPass records the walk a pass starts from, until the returned func
// runs. A rebuild inside a pass keeps the outer walk, taken before the outer
// pass changed anything.
func walkedThisPass(files map[string]FileState) func() {
	if passWalked != nil {
		return func() {}
	}
	passWalked = maps.Clone(files)
	return func() { passWalked = nil }
}

// foldDeferred updates the transcripts held as waiting for zstd. A file this
// pass left unread for that reason is held with the state the walk saw; one
// the index now holds, or the walk no longer lists, is let go. A file the pass
// did not try again keeps its entry: nothing about it has changed.
func foldDeferred(m *Manifest, noZstd map[string]bool) {
	if passWalked == nil {
		return
	}
	// A new map: m.Deferred may be shared with the cached manifest.
	out := map[string]FileState{}
	for p, f := range m.Deferred {
		_, walked := passWalked[p]
		_, held := m.Files[p]
		if walked && !held {
			out[p] = f
		}
	}
	for p := range noZstd {
		f, walked := passWalked[p]
		if _, held := m.Files[p]; walked && !held {
			out[p] = f
		}
	}
	if len(out) == 0 {
		out = nil
	}
	m.Deferred = out
}

// readThisPass records a file a pass opened and parsed.
func readThisPass(files map[string]FileState) {
	if passRead == nil {
		passRead = map[string]bool{}
	}
	for p := range files {
		passRead[p] = true
	}
}

// parsedThisPass records which files a pass read, so the fold can start their
// counts over rather than adding to what an earlier pass left (#2015).
func parsedThisPass(files map[string]FileState) {
	if passParsed == nil {
		passParsed = map[string]bool{}
	}
	for p := range files {
		passParsed[p] = true
	}
}
