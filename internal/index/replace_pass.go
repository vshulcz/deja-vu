package index

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// replacePass is the replacing half of updateIndexPass: the changed files are
// read again whole and the index is written anew beside the live one, or
// appended to its log, and swapped in.
type replacePass struct {
	dir, harness, scope string
	progress            io.Writer
	old                 Manifest
	files, changed      map[string]FileState
	removed, kept       map[string]bool
	unseen, newlyKept   map[string]bool
	sharedBy            map[string][]string

	logTails     map[string]map[string]bool
	keysFrom     map[string][]string
	replacements []model.Session
	diffs        []string
	fromDB       map[string]bool
	storeKeys    map[string]bool
	reread       map[string]FileState
	replaceKeys  map[string]bool
	takeovers    map[string]bool
	aiderHeld    map[string]bool

	tmp                 string
	appending, swapped  bool
	dead                []int64
	deadBytes           int64
	rw                  *recordWriter
	tbl, oldTables      *recordTables
	m                   Manifest
	compacted           map[string]map[string]bool
	buckets             bucketPostings
	offsets             *passOffsets
	freshKeys, dropped  map[string]bool
	seenMsgs, readAgain msgSeen
	carried             *carriedRecords
	rederive, unread    map[string]bool
	compactedKeys       map[string]bool
	unreadMsgs          map[string][]model.Message
	keptOwner, keptCopy map[string]string
}

// run reads the changed files and the copies beside them, then writes the index.
func (rp *replacePass) run(takenFrom map[string]bool) error {
	rereadSharedCopies(rp.removed, rp.changed, rp.files, rp.old.Sessions)
	takenRead := 0
	for p := range takenFrom {
		if _, again := rp.changed[p]; !again {
			rp.changed[p] = rp.files[p]
			takenRead++
		}
	}
	rereadPromptLogs(rp.removed, rp.changed, rp.files, rp.old.Files, rp.old.Sessions)
	// This pass's counts, like every other build path (#1850).
	emptied.Store(0)
	collisions.Store(nil)
	merged.Store(0)
	lastIngestFiles = len(rp.changed)
	// The sessions a shared store handed back. Only those may drop a store's
	// records by key: a per-file transcript under the same id — goose's JSONL
	// beside its database — says nothing about what the store still holds.
	// Judged by path after wholeStoresThisPass, which resolves the store paths.
	rp.storeKeys = map[string]bool{}
	rp.keysFrom = map[string][]string{}
	// A live-locked or half-written store (Cursor holds its sqlite under WAL)
	// must not fail every search. Keep the old records and the old FileState
	// so the next run retries this file.
	rp.logTails = promptLogTails(rp.changed, rp.old.Files, rp.old.IngestFiles)
	// opencode's diff files are read after its database: each is read as its
	// session from the database, which the since read may already have handed
	// back with the diff folded in (#4207).
	rp.fromDB = map[string]bool{}
	for p, f := range rp.changed {
		rp.read(p, f)
	}
	// A session this pass replaces is read from every transcript that holds
	// it, as a rebuild reads it: the ones that changed, and the copies beside
	// them that did not (#5358). What those copies hold can name a session
	// another file holds in turn, so it runs until nothing new is read.
	copiesRead := takenRead + rp.readCopies()
	sortCopies(rp.replacements)
	rp.readDiffs()
	// First, so everything below reads the same list of stores.
	wholeStoresThisPass(rp.changed, rp.old.Files)
	for p, keys := range rp.keysFrom {
		if storeHarness(p) == "" {
			continue
		}
		for _, k := range keys {
			rp.storeKeys[k] = true
		}
	}
	// After the loop, because a file whose parse failed is dropped from
	// `changed` there and keeps what it already held — starting it over would
	// throw the counts away on the one pass that could not read it.
	//
	// A database-backed store reads only what is newer than the cursor the last
	// pass stamped, so this pass cannot speak for the rest of it either: it adds
	// to what the store holds, the way an append does (#2025). The trade is the
	// append path's — a db's counts can only grow until a full rebuild.
	rp.reread = fullyReadFiles(rp.changed, rp.old.Files)
	parsedThisPass(rp.reread)
	// A file the pass read is a file that opens, whether or not it read all of
	// it, so an error recorded when it did not has to go.
	readThisPass(rp.changed)
	// Each file's read drops what the index holds from a transcript; this
	// drops what a transcript in the same batch holds, as a rebuild does. A
	// log read before its session's transcript kept the log's prompt (#5503).
	rp.replacements = dropShadowedPrompts(rp.replacements, rp.old.Sessions)
	rp.replaceKeys = map[string]bool{}
	for _, s := range rp.replacements {
		rp.replaceKeys[s.Harness+":"+s.ID] = true
	}
	// A transcript arriving for a session held from a prompt log alone: the
	// log's records for it go, and the row starts over from the transcript.
	rp.takeovers = promptLogTakeovers(rp.replacements, rp.old.Sessions)
	rp.aiderHeld = aiderStarts(rp.replacements)
	rp.dropMovedKept()
	sayKept(rp.progress, rp.kept, rp.unseen)
	rp.sayPass(copiesRead)
	return rp.write()
}

// skip leaves p for the next pass, with what the index held of it.
func (rp *replacePass) skip(p string, err error) {
	delete(rp.logTails, p)
	if rp.progress != nil {
		fmt.Fprintf(rp.progress, "deja: skipping %s this pass: %v\n", filepath.Base(p), err)
	}
	delete(rp.changed, p)
	if of, ok := rp.old.Files[p]; ok {
		rp.files[p] = of
	} else {
		delete(rp.files, p)
	}
}

// take adds what p handed back to the sessions this pass replaces.
func (rp *replacePass) take(p string, ss []model.Session) {
	ss = dropShadowedPrompts(sources.FilterSessions(filterTombstoned(ss)), rp.old.Sessions)
	for _, s := range ss {
		rp.keysFrom[p] = append(rp.keysFrom[p], s.Harness+":"+s.ID)
	}
	rp.replacements = append(rp.replacements, ss...)
}

// read reads p whole, or from its tail for a prompt log, and takes what it holds.
func (rp *replacePass) read(p string, f FileState) {
	if harnessForPath(p) == "opencode-diff" {
		rp.diffs = append(rp.diffs, p)
		return
	}
	ss, f, again, readErr := readSettled(p, f, func() ([]model.Session, error) { return parseChangedFile(rp.harness, p, rp.old.Files[p]) })
	if readErr != nil {
		rp.skip(p, readErr)
		return
	}
	if _, ok := rp.changed[p]; ok {
		rp.changed[p] = f
	}
	if again {
		// Read whole: the tail was named from the walk's size.
		delete(rp.logTails, p)
	}
	if ids, ok := rp.logTails[p]; ok {
		ss = sessionsNamed(ss, widenLogTail(ids, p, ss, rp.old.Sessions))
	}
	if harnessForPath(p) == "opencode" {
		for _, s := range ss {
			rp.fromDB[s.ID] = true
		}
	}
	rp.take(p, ss)
	rp.files[p] = f
}

// readCopies reads the copies beside the sessions this pass replaces, and
// returns how many it read.
func (rp *replacePass) readCopies() int {
	copiesRead := 0
	if heldBy := keysByFile(rp.old.Sessions); len(heldBy) > 0 {
		var pending []string
		for p := range rp.changed {
			pending = append(pending, heldBy[p]...)
			pending = append(pending, rp.keysFrom[p]...)
		}
		for p := range rp.removed {
			pending = append(pending, heldBy[p]...)
		}
		for p := range rp.newlyKept {
			pending = append(pending, heldBy[p]...)
		}
		seen, tried := map[string]bool{}, map[string]bool{}
		for len(pending) > 0 {
			key := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if seen[key] {
				continue
			}
			seen[key] = true
			meta, ok := rp.old.Sessions[key]
			if !ok || len(meta.Copies) == 0 {
				continue
			}
			for _, q := range sessionFiles(meta) {
				// A written-back file holds less than its records do, and
				// those are carried instead (#4617).
				f, live := rp.files[q]
				if _, again := rp.changed[q]; again || tried[q] || !live || rp.kept[q] || f.Kept || f.WrittenBack || !copyable(q) {
					continue
				}
				tried[q] = true
				copiesRead++
				rp.changed[q] = f
				rp.read(q, f)
				pending = append(pending, heldBy[q]...)
				pending = append(pending, rp.keysFrom[q]...)
			}
		}
	}
	return copiesRead
}

// readDiffs reads the opencode diff files the database read did not hand back.
func (rp *replacePass) readDiffs() {
	var pending []string
	for _, p := range rp.diffs {
		if !rp.fromDB[strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))] {
			pending = append(pending, p)
		}
		rp.files[p] = rp.changed[p]
	}
	if len(pending) > 0 {
		// Read together: one query rather than one a file, each paying for
		// every session's parent and title again.
		ss, parseErr := sources.ParseOpencodeDiffSessions(pending)
		if parseErr != nil {
			for _, p := range pending {
				rp.skip(p, parseErr)
			}
		} else {
			at := map[string]string{}
			for _, p := range pending {
				at[strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))] = p
			}
			for _, s := range ss {
				rp.take(at[s.ID], []model.Session{s})
			}
		}
	}
}

// dropMovedKept sends a kept file whose session arrived again beside it back to
// removed: the transcript moved.
func (rp *replacePass) dropMovedKept() {
	// A kept file whose session arrived again from another path is a rename,
	// not a cleanup: the client moved the transcript, and keeping the old copy
	// would make the session its own second copy (#1086). Those go back to
	// being removed, so the records under the old path are dropped below.
	// Only when the arrival sits in the same directory: two projects can
	// share a filename-derived id (#699), and that is a collision, not the
	// kept session moving.
	// An arrival with nothing to index is not the kept session moving: Gemini
	// CLI's resume stub lands beside the transcript it shares an id with, and
	// taking it for a rename dropped the deleted transcript's records (#4213).
	// Only a file new to the index arrives: a copy read again beside a deleted
	// owner holds its last turn too, and taking it for the move dropped the
	// deleted transcript's records (#5380).
	arrivals := map[string][]int{}
	for i, r := range rp.replacements {
		if _, known := rp.old.Files[r.Path]; known && !rp.removed[r.Path] {
			continue
		}
		if holdsText(r) {
			key := r.Harness + ":" + r.ID
			arrivals[key] = append(arrivals[key], i)
		}
	}
	// A session kept in a directory of its own moves with the directory, so
	// the arrival is never beside the old path; its id is a UUID, which two
	// projects do not share by accident, so the id alone says it moved.
	for key, meta := range rp.old.Sessions {
		if !rp.kept[meta.Path] {
			continue
		}
		ownDir := sessionDirName.MatchString(filepath.Base(filepath.Dir(meta.Path)))
		moved := false
		for _, i := range arrivals[key] {
			r := rp.replacements[i]
			if (ownDir || filepath.Dir(r.Path) == filepath.Dir(meta.Path)) && coversKept(r, meta) && holdsKeptRecords(rp.dir, rp.old, r, meta.Path) {
				moved = true
				break
			}
		}
		if moved {
			rp.removed[meta.Path] = true
			delete(rp.files, meta.Path)
			delete(rp.kept, meta.Path)
		}
	}
}

// sayPass prints the replacing pass's one line.
func (rp *replacePass) sayPass(copiesRead int) {
	if rp.progress != nil {
		// A sentence, like every other line this prints. It used to read
		// "incremental index changed_files=306 removed_files=0 sessions=1789",
		// which is a log line for whoever wrote it, on a path every user hits:
		// the first `deja blame` or search after a day of work prints it above
		// the answer. Zero counts are left out rather than shown as zero.
		n := len(rp.changed) - copiesRead
		line := fmt.Sprintf("deja: %s %d changed transcript%s", replacementPassMarker, n, pluralS(n))
		if copiesRead > 0 {
			line += fmt.Sprintf(", %d cop%s read again", copiesRead, map[bool]string{true: "y", false: "ies"}[copiesRead == 1])
		}
		if len(rp.replaceKeys) > 0 {
			line += fmt.Sprintf(", %d session%s replaced", len(rp.replaceKeys), pluralS(len(rp.replaceKeys)))
		}
		if len(rp.removed) > 0 {
			line += fmt.Sprintf(", %d gone", len(rp.removed))
		}
		fmt.Fprintln(rp.progress, line)
	}
}

// write writes the new index in tmp and swaps it in.
func (rp *replacePass) write() error {
	rp.tmp = rp.dir + ".tmp"
	_ = os.RemoveAll(rp.tmp)
	if mkErr := os.MkdirAll(filepath.Join(rp.tmp, "buckets"), 0o700); mkErr != nil {
		return mkErr
	}
	// Appended to the log the old index holds, or the log written anew: see
	// append_replace.go (#5387).
	rp.appending = appendsInPlace(rp.dir, rp.tmp, rp.old)
	rp.swapped = false
	// Appending, a record that is not carried stays where it is and is listed
	// as dead, and is overwritten once the swap commits.
	if rp.appending {
		defer func() {
			if !rp.swapped {
				undoAppend(rp.dir, rp.old.RecordsSize)
				// Its log is a link to the live one.
				_ = os.RemoveAll(rp.tmp)
			}
		}()
	}
	if err := rp.openLog(); err != nil {
		return err
	}
	rp.startManifest()
	rp.buckets = bucketPostings{}
	// Every record's offset in the new log, as it is written: the table the
	// commit would otherwise build by walking the whole log again (#5402).
	rp.offsets = &passOffsets{byKey: map[string][]int64{}}
	rp.freshKeys = map[string]bool{}
	// The sessions that lost a record here, which the command failure walk
	// has to read again along with the ones re-read (#4288).
	rp.dropped = map[string]bool{}
	// Seeded with the records carried for a session this pass replaces, so a
	// turn a kept or skipped copy already holds is not written again.
	rp.seenMsgs = msgSeen{}
	// Carried records were redacted, encoded and keyed when first written.
	// Each is copied as it is stored, and its postings are moved over below:
	// doing it all again made every replacement pass cost the whole store, on
	// one core (#5383). Only a session this pass replaces, or one a compaction
	// may have kept, has its body read.
	rp.oldTables = tablesFromManifest(rp.old)
	rp.carried = &carriedRecords{}
	rp.prepareCarry()
	if err := carryStoredRecords(rp.dir, rp.old, rp.oldTables, rp.appending, rp.changed, rp.removed, rp.carry, func(key string, offs []int64) {
		for _, off := range offs {
			rp.offsets.add(key, off)
		}
		if _, exists := rp.m.Sessions[key]; !exists {
			rp.m.Sessions[key] = rp.old.Sessions[key]
		}
	}, rp.rederive, rp.storeKeys, rp.takeovers, rp.compactedKeys); err != nil {
		_ = rp.rw.Close()
		return err
	}
	readKeys, err := rp.writeReplacements()
	if err != nil {
		return err
	}
	rp.settleRows(readKeys)
	if err := rp.rw.Close(); err != nil {
		return err
	}
	return rp.commit()
}

// swap moves tmp over the live index.
func (rp *replacePass) swap() error {
	if rp.appending && beforeAppendSwap != nil {
		if hookErr := beforeAppendSwap(); hookErr != nil {
			return hookErr
		}
	}
	if rp.appending && len(rp.dead) > 0 {
		if markErr := markScrubPending(rp.tmp); markErr != nil {
			return markErr
		}
	}
	swapErr := swapIndexDir(rp.dir, rp.tmp)
	rp.swapped = swapErr == nil
	if rp.swapped && len(rp.dead) > 0 {
		scrubSwapped(rp.dir, rp.m, rp.dead)
	}
	return swapErr
}

// openLog opens the record log the pass writes: the live one when appending,
// a new one otherwise.
func (rp *replacePass) openLog() error {
	var err error
	var rf *os.File
	rp.tbl = newRecordTables()
	if rp.appending {
		rf, err = os.OpenFile(filepath.Join(rp.tmp, "records.bin"), os.O_RDWR, 0o600)
		// Ids continue where the log's records left off; clipped, so the
		// table this pass grows is not the old manifest's backing array.
		rp.tbl = tablesFromStrings(slices.Clip(rp.old.RecordStrings))
	} else {
		rf, err = createLog(filepath.Join(rp.tmp, "records.bin"))
	}
	if err != nil {
		return err
	}
	rp.rw, err = newRecordWriter(rf, rp.tbl)
	if err != nil {
		_ = rf.Close()
		return err
	}
	return nil
}

// startManifest starts the manifest the pass writes and scrubs what it read.
func (rp *replacePass) startManifest() {
	// A new generation, not the old one: this path opens records.bin with
	// O_TRUNC and writes every surviving record again, so an offset from before
	// it means nothing afterwards. Carrying the stamp forward told anything
	// keyed on it — the embedding sidecar — that the file it measured was still
	// there (#1357). Only appendIncremental, which appends in place, may keep a
	// generation.
	rp.m = Manifest{Version: version, KeptDated: true, Format: onDiskFormat, SearchOnly: searchOnlyAfter(rp.old), Files: rp.files, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), SourcesReadAt: time.Now(), ReadZone: zoneFingerprint(),
		Generation: time.Now().UTC().Format(time.RFC3339Nano), Scope: rp.scope,
		ExportWatermarks: rp.old.ExportWatermarks, ExportBoundary: rp.old.ExportBoundary, ImportedRecords: rp.old.ImportedRecords,
		Compactions: cloneCompactions(rp.old.Compactions),
		// Kept from the old index: this build reuses records written under
		// those patterns, so claiming today's set would be a lie the reader
		// cannot check.
		ExcludeFingerprint: rp.old.ExcludeFingerprint,
		AliasFingerprint:   rp.old.AliasFingerprint,
		// What the index on disk recorded: old is that manifest, or what the
		// followed renames wrote of it.
		ToolFingerprint: mergedToolFingerprint(rp.old.ToolFingerprint),
		// What deja could not read is about the files, not about the pass that
		// happened to notice: the lines are still there. Dropping the map meant
		// doctor forgot a store's unreadable file because an unrelated
		// transcript changed (#2015). A store this pass re-read starts over,
		// because a file rewritten clean has to be able to clear its count.
		IngestFiles: copyIngestFiles(rp.old.IngestFiles, rp.reread, rp.logTails)}
	if rp.appending && rp.old.Generation != "" {
		// Nothing moved: an offset from before still names the same record.
		rp.m.Generation = rp.old.Generation
	}
	skipRedactions := map[string]bool{}
	for p := range rp.changed {
		skipRedactions[p] = true
	}
	for p := range rp.removed {
		skipRedactions[p] = true
	}
	carryRedactions(&rp.m, rp.old, skipRedactions)
	// Scrub once, up front, the way both full builds do — everything below reads
	// these sessions. Redacting per message inside the record loop instead left
	// the sessions themselves raw, so whatever was mined from them afterwards
	// was raw too: a credential in an error line reached fixes.gob whole while a
	// rebuild of the same corpus produced a clean one. It also left
	// metaForSession hashing unredacted text, so the friction signatures a
	// session carried depended on which build path had touched it last.
	preRedactSessions(&rp.m, rp.replacements)
	// After the redaction, so the texts compare with what the records hold.
	rp.compacted = compactedInPlace(rp.replacements, false)
}

// addRec writes r to the new log and keys it.
func (rp *replacePass) addRec(r Record) error {
	if r.SourcePath == "" {
		return nil
	}
	meta, ok := rp.old.Sessions[r.Key]
	if !ok {
		meta, ok = rp.m.Sessions[r.Key]
	}
	if !ok {
		return nil
	}
	if rp.appending && !rp.freshKeys[r.Key] {
		rp.freshKeys[r.Key] = true
		rp.tbl.fresh(r.Key)
	}
	off, err := rp.rw.write(r)
	if err != nil {
		return err
	}
	rp.offsets.add(r.Key, off)
	passEncoded.Add(1)
	passKeyed.Add(1)
	// The same part and the same tool bit a full build gives the record.
	// Tokenizing the whole text here put every hash of a written side,
	// every replaced span's body and whole build logs into the postings
	// of each session this pass carried, and dropped the bit the
	// per-session read bound uses to spend its budget on speech first:
	// a live index held twice the tokens a rebuild of it did.
	addIndexKeys(rp.buckets, tokenizedPart(r.Role, r.Text), off, meta.Ord, r.Time, isToolRole(r.Role))
	if _, exists := rp.m.Sessions[r.Key]; exists {
		return nil
	}
	rp.m.Sessions[r.Key] = meta
	return nil
}

// prepareCarry works out which sessions are taken again from their records and
// which turns go to a deleted copy.
func (rp *replacePass) prepareCarry() {
	// The files beside a session this pass replaces that it does not read
	// again: a kept transcript, a written-back one, one skipped. A rebuild
	// reads them from their records after the files on disk, and so does this
	// pass, or a copy that left kept its span in the row (#5380).
	// Those rows are taken again too where a copy left them this pass and
	// nothing was read again, and a deleted owner is read from its records
	// like the rest: the span it held was its own and its copies', some of
	// which have gone since.
	rp.rederive = maps.Clone(rp.replaceKeys)
	for _, set := range []map[string]FileState{rp.changed} {
		for p := range set {
			for _, key := range rp.sharedBy[p] {
				rp.rederive[key] = true
			}
		}
	}
	for _, set := range []map[string]bool{rp.removed, rp.newlyKept} {
		for p := range set {
			for _, key := range rp.sharedBy[p] {
				rp.rederive[key] = true
			}
		}
	}
	rp.unread, rp.unreadMsgs = map[string]bool{}, map[string][]model.Message{}
	rp.keptOwner = map[string]string{}
	for key := range rp.rederive {
		row := rp.old.Sessions[key]
		for _, q := range sessionFiles(row) {
			f, ok := rp.files[q]
			if _, again := rp.changed[q]; again || !ok || q == row.Path && !f.Kept {
				continue
			}
			rp.unread[orphanKey(key, q)] = true
			if q == row.Path {
				rp.keptOwner[key] = q
			}
		}
	}
	// A turn a deleted copy shared with a live one was filed once, under
	// whichever was read first. When that live file is read again without
	// it, the turn goes to the deleted copy rather than out of the index:
	// the deleted transcript can no longer be read to say which turns it
	// held, so it keeps every one the session loses here.
	rp.keptCopy = map[string]string{}
	for key := range rp.rederive {
		for _, q := range sessionFiles(rp.old.Sessions[key]) {
			if _, again := rp.changed[q]; !again && rp.files[q].Kept && (rp.keptCopy[key] == "" || q < rp.keptCopy[key]) {
				rp.keptCopy[key] = q
			}
		}
	}
	rp.readAgain = msgSeen{}
	for _, s := range rp.replacements {
		if key := s.Harness + ":" + s.ID; rp.keptCopy[key] != "" {
			for _, msg := range s.Messages {
				rp.readAgain.dup(key, msg.Role, msg.Time, msg.Text)
				// A command whose result landed since is the same turn.
				rp.readAgain.dup(key, msg.Role, msg.Time, exitSuffix.ReplaceAllString(msg.Text, ""))
			}
		}
	}
	rp.compactedKeys = make(map[string]bool, len(rp.compacted))
	for key := range rp.compacted {
		rp.compactedKeys[key] = true
	}
}

// Only a turn from before the copy was last written can be one it held:
// a later one, rewound or rewritten away, was never in it.
func (rp *replacePass) couldHold(r Record, k string) bool {
	f := rp.files[k]
	return r.Time.IsZero() || f.MTime == 0 || r.Time.UnixNano() <= f.MTime
}

// gotAgain reports whether r's turn was read again this pass.
func (rp *replacePass) gotAgain(r Record) bool {
	return rp.readAgain[seenKey(r.Key, r.Role, r.Time, r.Text)] || rp.readAgain[seenKey(r.Key, r.Role, r.Time, exitSuffix.ReplaceAllString(r.Text, ""))]
}

// carry carries one stored record into the new index, files it under a deleted
// copy, or drops it.
func (rp *replacePass) carry(oldOff int64, payload []byte) error {
	kill := func() {
		if rp.appending {
			rp.dead = append(rp.dead, oldOff)
			rp.deadBytes += 4 + int64(len(payload))
		}
	}
	kid, pid, rid, rest, ok := recordPrefix(payload)
	if !ok {
		return errShortRecord
	}
	r := Record{Key: rp.oldTables.lookup(kid), SourcePath: rp.oldTables.lookup(pid), Role: rp.oldTables.lookup(rid)}
	if _, inCompacted := rp.compacted[r.Key]; inCompacted || rp.rederive[r.Key] {
		full, err := decodeRecord(payload, rp.oldTables)
		if err != nil {
			return err
		}
		r = full
	}
	// Shared-store harnesses (opencode, cursor) are parsed since a
	// watermark, so their untouched sessions are NOT re-emitted on a
	// change — they must be retained, not dropped, or they vanish.
	// Superseded sessions are handled by storeKeys.
	fromStore := fromDatabase(r)
	// storeKeys is scoped to shared stores. A shared store is parsed since
	// a watermark, so a superseded session's old record is not re-read and
	// clause two never reaches it — storeKeys is what drops it. For a
	// per-file harness, a removed or changed file's old records are already
	// dropped by the two clauses above, so applying a key there only
	// hurts: two transcripts in different projects can share a filename-derived
	// id, and dropping by key alone erased the sibling that was never re-read
	// (#699). The record's own SourcePath decides its fate for those.
	//
	// And only when the pass read that store whole: a store read from its
	// watermark hands back the new turns alone, so dropping the rest by key
	// would take the earlier turns of every continued session (#2033).
	if rp.removed[r.SourcePath] || (rp.changed[r.SourcePath].Path != "" && !fromStore && !keptFromLogTail(r, rp.logTails) && !leftItsFile(r, rp.replaceKeys, rp.old.Sessions[r.Key], rp.aiderHeld) && !keptThroughCompaction(r, rp.compacted)) || (fromStore && readWholeThisPass(r) && rp.storeKeys[r.Key]) ||
		(rp.takeovers[r.Key] && isCodexHistory(r.SourcePath)) {
		if k := rp.keptCopy[r.Key]; k != "" && !rp.removed[r.SourcePath] && copyable(r.SourcePath) && rp.couldHold(r, k) && !rp.gotAgain(r) {
			kill()
			r.SourcePath = k
			if at := orphanKey(r.Key, k); rp.unread[at] {
				h, _, _ := strings.Cut(r.Key, ":")
				rp.unreadMsgs[at] = append(rp.unreadMsgs[at], model.Message{Role: r.Role, Text: carriedText(h, r.Role, r.Text), Time: r.Time})
			}
			return rp.addRec(r)
		}
		rp.dropped[r.Key] = true
		kill()
		return nil
	}
	if r.SourcePath == "" {
		rp.dropped[r.Key] = true // not carried
		kill()
		return nil
	}
	if rp.replaceKeys[r.Key] {
		rp.seenMsgs.dup(r.Key, r.Role, r.Time, r.Text)
	}
	if len(rp.unread) > 0 && rp.unread[orphanKey(r.Key, r.SourcePath)] {
		at := orphanKey(r.Key, r.SourcePath)
		h, _, _ := strings.Cut(r.Key, ":")
		rp.unreadMsgs[at] = append(rp.unreadMsgs[at], model.Message{Role: r.Role, Text: carriedText(h, r.Role, r.Text), Time: r.Time})
	}
	meta, ok := rp.old.Sessions[r.Key]
	if !ok {
		kill()
		return nil
	}
	if rp.appending {
		rp.offsets.add(r.Key, oldOff)
		if _, exists := rp.m.Sessions[r.Key]; !exists {
			rp.m.Sessions[r.Key] = meta
		}
		return nil
	}
	off, err := rp.rw.writeStored(r.Key, r.SourcePath, r.Role, rest)
	if err != nil {
		return err
	}
	rp.carried.add(oldOff, off, meta.Ord, isToolRole(r.Role))
	rp.offsets.add(r.Key, off)
	if _, exists := rp.m.Sessions[r.Key]; !exists {
		rp.m.Sessions[r.Key] = meta
	}
	return nil
}

// writeReplacements writes the rows and records of the sessions read this pass,
// and returns their keys.
func (rp *replacePass) writeReplacements() (map[string]bool, error) {
	// Ord is the posting's session id, so two sessions sharing one merges
	// their postings. A replacement reclaims its Ord from the OLD manifest,
	// which the new map has not seen yet — so a new session picked from
	// max(new)+1 could collide with an Ord an existing session was about to
	// take back. Reserve both sides before handing any out.
	nextOrd := uint32(0)
	for _, meta := range rp.m.Sessions {
		if meta.Ord > nextOrd {
			nextOrd = meta.Ord
		}
	}
	for _, meta := range rp.old.Sessions {
		if meta.Ord > nextOrd {
			nextOrd = meta.Ord
		}
	}
	rederived := map[string]bool{}
	// What each session's row reads off its text, all at once: in the loop it
	// was one core's work for most of the phase.
	texts := sessionTexts(rp.replacements)
	// The sessions read this pass: one with no record under it when the pass
	// is done goes to Empty, as a rebuild sets it aside (#5563).
	readKeys := map[string]bool{}
	for i, s := range rp.replacements {
		key := s.Harness + ":" + s.ID
		readKeys[key] = true
		ord := uint32(0)
		if om, ok := rp.old.Sessions[key]; ok {
			ord = om.Ord
		} else if cur, ok := rp.m.Sessions[key]; ok {
			ord = cur.Ord
		}
		if ord == 0 {
			nextOrd++
			ord = nextOrd
		}
		held, ok := rp.m.Sessions[key]
		if !ok {
			// The row may exist only in the manifest being replaced, or be set
			// aside there with nothing to index (#5563).
			var listed bool
			if held, listed = rp.old.Sessions[key]; !listed {
				// Set aside without an Ord; the one this pass gives it.
				if row, empty := rp.old.Empty[key]; empty {
					held = metaWithOrd(row, ord)
				}
			}
			// The file that owned it was read again whole and none of its
			// records were carried: the row starts over from what this pass
			// reads, as a rebuild starts it. Held, it kept the span and the
			// owner of a file that may no longer hold the session (#5358).
			_, again := rp.reread[held.Path]
			if rp.takeovers[key] || again && copyable(held.Path) {
				held = SessionMeta{}
			}
		}
		if !rederived[key] {
			// The span of the files beside the owner is taken again, from the
			// ones read now and the ones that were not, as a rebuild takes it:
			// held, it kept the span of a copy that has since left (#5380).
			rederived[key] = true
			held = rowRetaken(held, rp.keptOwner[key], rp.reread)
			if ok {
				rp.m.Sessions[key] = held
			}
		}
		// A renamed transcript arrives as one removed path and one added path
		// in the same pass, so the path deja holds is the one that went away.
		// Comparing them called it a collision with a file that no longer
		// exists: the row kept the dead path until a full rebuild, `--json`
		// handed callers that path, and `forget` warned that dropping the
		// session would take a second conversation with it (#1086).
		if rp.removed[held.Path] {
			held.Path = ""
		}
		owns, collided := claimSession(held, s)
		if collided {
			collisions.Add(key)
		}
		if owns {
			rp.m.Sessions[key] = ownerRow(held, s, texts[i].row(s), ord, collided)
		} else {
			if _, present := rp.m.Sessions[key]; !present {
				rp.m.Sessions[key] = held
			}
			widenSpan(rp.m.Sessions, key, s)
		}
		if collided {
			markShared(rp.m.Sessions, key)
		}
		recordCopy(rp.m.Sessions, key, held, s)
		for _, msg := range s.Messages {
			// Already redacted (and length-capped) by preRedactSessions above.
			text := msg.Text
			// Plumbing alone strips to empty and is not written, as a rebuild
			// does not write it (#551, #5555).
			if strings.TrimSpace(text) == "" {
				continue
			}
			if rp.seenMsgs.dup(key, msg.Role, msg.Time, text) {
				continue
			}
			if err := rp.addRec(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time}); err != nil {
				_ = rp.rw.Close()
				return nil, err
			}
		}
	}
	for key := range rp.rederive {
		if row, ok := rp.m.Sessions[key]; ok && !rederived[key] {
			rp.m.Sessions[key] = rowRetaken(row, rp.keptOwner[key], rp.reread)
		}
	}
	return readKeys, nil
}

// settleRows takes the rows of the files not read again from their records and
// sets aside the sessions left with none.
func (rp *replacePass) settleRows(readKeys map[string]bool) {
	for _, at := range sortedKeys(rp.unread) {
		key, q, _ := strings.Cut(at, "\x00")
		row, ok := rp.m.Sessions[key]
		if !ok || len(rp.unreadMsgs[at]) == 0 {
			continue
		}
		cp := sessionFromMeta(rp.old.Sessions[key])
		retitleWrappedPaste(&cp)
		cp.Path, cp.Messages, cp.Started, cp.Updated = q, rp.unreadMsgs[at], time.Time{}, time.Time{}
		for _, msg := range cp.Messages {
			cp.Started, cp.Updated = widerSpan(cp.Started, cp.Updated, msg.Time, msg.Time)
		}
		owns, collided := claimSession(row, cp)
		if owns {
			next := ownerRow(row, cp, metaForSession(cp), row.Ord, collided)
			// What the records cannot count, as the rebuild carries it.
			if q == rp.keptOwner[key] {
				next = withKeptRow(next, rp.old.Sessions[key], true)
			}
			rp.m.Sessions[key] = next
		} else {
			widenSpan(rp.m.Sessions, key, cp)
		}
		if collided {
			markShared(rp.m.Sessions, key)
		}
		recordCopy(rp.m.Sessions, key, row, cp)
	}
	rp.m.Empty = keepEmpty(rp.old.Empty, rp.m.Sessions, rp.m.Files)
	// A file read again whole says what it holds now: a row set aside for it
	// that this pass did not read again names an id the file no longer has.
	for key, row := range rp.m.Empty {
		// A prompt log read from its tail does not say what the rest holds.
		if _, again := rp.reread[row.Path]; again && !readKeys[key] && rp.logTails[row.Path] == nil {
			delete(rp.m.Empty, key)
		}
	}
	for key := range readKeys {
		if len(rp.offsets.byKey[key]) == 0 {
			setAsideEmpty(&rp.m, key)
		}
	}
}

// commit writes the postings, the manifest and the sidecars, then swaps.
func (rp *replacePass) commit() error {
	// The neighbour map's words an index written anew has no postings for,
	// which the carried map loses (see cooccurUnindexed). Appending, the scrub
	// after the swap takes them out instead.
	var unposted map[string]bool
	if rp.appending {
		// The kept records' postings are the old ones, linked in; this pass's
		// go into overlays, and the dead records' are dropped on read.
		rp.m.DeadRecords = mergeDead(rp.old.DeadRecords, rp.dead)
		rp.m.DeadBytes = rp.old.DeadBytes + rp.deadBytes
		if _, err := addOverlays(filepath.Join(rp.dir, "buckets"), filepath.Join(rp.tmp, "buckets"), rp.buckets, rp.m.DeadRecords); err != nil {
			return err
		}
	} else {
		moved, err := carriedPostings(rp.dir, rp.old, rp.carried)
		if err != nil {
			return err
		}
		mergePostings(moved, rp.buckets)
		if err := writeBucketsConcurrent(filepath.Join(rp.tmp, "buckets"), moved); err != nil {
			return err
		}
		if !rp.m.SearchOnly {
			unposted = cooccurUnindexed(rp.dir, moved)
		}
	}
	settleCopies(&rp.m, rp.old.Sessions, rp.changed, rp.replacements)
	setDatabaseStoreWatermarks(rp.m.Files, rp.m.Sessions)
	rp.m.RecordStrings = rp.tbl.strs
	_ = rp.offsets.write(rp.tmp)
	// The sessions this pass read again or dropped a record of. Everything
	// else was copied as stored, in its order, so what was derived from its
	// records still holds.
	for key := range rp.replaceKeys {
		rp.dropped[key] = true
	}
	// Ahead of the commit, which then reads only the sessions that changed
	// rather than parsing every session that carries a wall again.
	carryFrictionTexts(rp.dir, rp.tmp, rp.old, rp.dropped)
	// Nothing below changes m, so the commands, the swap and the CLI's checks
	// after the pass read the manifest from memory.
	if err := writeManifestShared(rp.tmp, rp.m); err != nil {
		return err
	}
	if rp.m.SearchOnly {
		return rp.swap()
	}
	carrySidecars(rp.dir, rp.tmp)
	if len(unposted) > 0 {
		_ = dropCooccurWords(rp.tmp, func(w string) bool { return unposted[w] })
	}
	// After carrying, not instead of it: the fixes merge writes only when it
	// has something to say, and the carried file is what a quiet update leaves.
	// The command table is recomputed from the sessions this pass changed and
	// the state the last one left, and an empty one removes the carried file,
	// as a full build would not write it (#4441).
	fixSessions, fixKeys := fixSessionsForPass(rp.tmp, rp.old, rp.m, rp.replacements, rp.replaceKeys)
	mergeFixes(rp.dir, rp.tmp, sessionKeys(rp.m), fixSessions, fixKeys)
	buildCommandTables(rp.dir, rp.tmp, rp.m, len(rp.old.DeadRecords), rp.dropped)
	buildCommandFailsFromIndex(rp.tmp, carriedCommandFailState(rp.dir, rp.old, rp.tmp, rp.m.Generation, rp.dropped), rp.dropped)
	return rp.swap()
}
