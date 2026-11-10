package index

import (
	"encoding/binary"
	"hash/fnv"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// resumeOffset is where an appended read of a known file starts: the end of
// the last complete line indexed, or the old size when none was recorded.
func resumeOffset(old FileState) int64 {
	if old.SafeSize == 0 || old.SafeSize > old.Size {
		return old.Size
	}
	return old.SafeSize
}

func appendIncremental(dir, harness, scope string, old Manifest, files map[string]FileState, changed map[string]FileState) (filesTouched, messages, unreadable int, err error) {
	// This pass's counts, like the two full paths: an incremental that nobody
	// read between builds otherwise reported its own colliding ids plus the
	// ones before it (#1850).
	emptied.Store(0)
	collisions.Store(nil)
	merged.Store(0)
	lastIngestFiles.Store(int64(len(changed)))
	readThisPass(changed)
	// Deliberately not parsedThisPass: this path reads the appended tail, not
	// the file, so what it finds adds to the file's count instead of replacing
	// it. Marking the file re-read dropped every bad line in the part already
	// indexed — which is every live session.
	rf, err := openIndexFileWrite(filepath.Join(dir, "records.bin"), os.O_CREATE|os.O_WRONLY|os.O_APPEND)
	if err != nil {
		return 0, 0, 0, err
	}
	// The log is being APPENDED to, so ids must continue where the existing
	// records left off. Starting a fresh table would hand id 0 to a new
	// string and silently repoint every record that already uses it.
	tbl := tablesFromManifest(old)
	rw, err := newRecordWriter(rf, tbl)
	if err != nil {
		_ = rf.Close()
		return 0, 0, 0, err
	}
	defer func() { _ = rw.Close() }()
	buckets := bucketPostings{}
	// The same rule the two full builds apply: a message repeated verbatim in
	// one pass is written once. Without it the same rollout held one record or
	// two depending on whether it was appended or rebuilt, so an index compared
	// against a rebuilt copy of itself came out unequal (#3934).
	seenMsgs := msgSeen{}
	// Every session this pass read, for the sidecars at the end.
	var appended []model.Session
	var rowMoved map[string]bool
	// Only what this pass adds: it goes into an overlay beside each bucket
	// rather than into the bucket written whole again (#5387).
	loadBucket := func(tok string) map[string][]posting {
		b := bucket(tok)
		data, ok := buckets[b]
		if !ok {
			data = map[string][]posting{}
			buckets[b] = data
		}
		return data
	}
	m := old
	m.Version = version
	m.Format = onDiskFormat
	m.Scope = scope
	m.BuiltAt = time.Now()
	m.SourcesReadAt = m.BuiltAt
	m.ReadZone = zoneFingerprint()
	m.Files = files
	m.Redacted = 0
	carryRedactions(&m, old, map[string]bool{})
	if m.Sessions == nil {
		m.Sessions = map[string]SessionMeta{}
	}
	m.Empty = keepEmpty(old.Empty, m.Sessions, files)
	// The rows this pass made or took back from Empty, and the ones it wrote a
	// record under: one with none goes back to Empty (#5563).
	emptyBefore, wroteUnder := map[string]bool{}, map[string]bool{}
	// Sorted, not map order: two sessions can claim the same harness:id, and
	// which one wins decided the project a whole conversation was filed under
	// — differently on every run (#698).
	// A file deja has never read is read whole here, not resumed, so its counts
	// start over the way the full paths start them. Leaving it out of the
	// parsed set added this pass's bad lines to a count the file never had.
	newFiles := map[string]FileState{}
	for p, f := range changed {
		if _, ok := old.Files[p]; !ok {
			newFiles[p] = f
		}
	}
	parsedThisPass(newFiles)
	// Prompt logs after the transcripts: a session whose transcript this pass
	// reads is then held from it, and its log line dropped as a rebuild drops
	// it (#5503).
	order := sortedKeys(changed)
	sort.SliceStable(order, func(i, j int) bool { return !isCodexHistory(order[i]) && isCodexHistory(order[j]) })
	for _, p := range order {
		of, known := old.Files[p]
		ss, ok := readAppended(harness, p, of, known, &m, changed)
		if !ok {
			continue
		}
		// Scrub before anything is derived from the text, as the other two paths
		// do. Redacting per message inside the write loop below left the derived
		// fields — the friction signatures especially — hashed from the raw
		// text, so the same session got different signatures depending on which
		// path had touched it last.
		preRedactSessions(&m, ss)
		for i := range ss {
			var from *FileState
			if known {
				from = &of
			}
			dropStoredRepeats(rw, dir, &ss[i], m.Sessions[ss[i].Key()], seenMsgs, m.Files, harness, from)
		}
		// Kept for the sidecars below, after redaction so a pair cannot carry a
		// credential the records do not.
		appended = append(appended, ss...)
		filesTouched++
		for _, s := range ss {
			key := s.Key()
			meta, moved := appendedRow(&m, key, s, p, of, changed[p], known, emptyBefore)
			// Which copy's commands the tables count follows the row's file
			// (foreignPath): one that moved has its runs mined again.
			if moved {
				if rowMoved == nil {
					rowMoved = map[string]bool{}
				}
				rowMoved[key] = true
			}
			m.Sessions[key] = meta
			for _, msg := range s.Messages {
				// Already redacted (and length-capped) by preRedactSessions above.
				text := msg.Text
				// A message that is nothing but harness plumbing strips to empty
				// (#551). Writing it would store a record with no content and give
				// it a posting.
				if strings.TrimSpace(text) == "" {
					continue
				}
				if seenMsgs.dup(key, msg.Role, msg.Time, text) {
					continue
				}
				off, writeErr := rw.write(Record{Key: key, SourcePath: s.Path, Role: msg.Role, Text: text, Time: msg.Time})
				if writeErr != nil {
					return filesTouched, messages, 0, writeErr
				}
				messages++
				wroteUnder[key] = true
				// Keyed the way a rebuild keys it: only the part that earns
				// postings, its date keys, and the tool bit.
				tool := isToolRole(msg.Role)
				eachIndexKey(tokenizedPart(msg.Role, text), msg.Time, func(tok string) {
					data := loadBucket(tok)
					data[tok] = append(data[tok], posting{Off: off, Sid: meta.Ord, Tool: tool})
				})
			}
		}
	}
	if closeErr := rw.Close(); closeErr != nil {
		return filesTouched, messages, 0, closeErr
	}
	for key := range emptyBefore {
		if !wroteUnder[key] {
			setAsideEmpty(&m, key)
		}
	}
	stale, err := addOverlays(filepath.Join(dir, "buckets"), filepath.Join(dir, "buckets"), buckets, m.DeadRecords)
	if err != nil {
		return filesTouched, messages, 0, err
	}
	m.bucketsLeaving = staleCount(stale)
	setDatabaseStoreWatermarks(m.Files, m.Sessions)
	m.RecordStrings = tbl.strs
	// Read before writeManifest: the fold in there drains the counters, and the
	// caller prints its line after this returns (#2007).
	unreadable = totalMalformed()
	// A pass that recorded files is past a first build's early slice; what
	// is still missing is what the next walk finds unread.
	m.PartialOf = 0
	if err := writeManifest(dir, m); err != nil {
		return filesTouched, messages, unreadable, err
	}
	dropStale(stale)
	m.bucketsLeaving = 0
	// The sidecars, from what this pass read and from the records already on
	// file. The replacement path does the same three things after it carries
	// them over; this path used to do none of them, which was survivable while
	// it only ever saw a tail and is not once a new conversation comes through
	// here (#3500). A session whose tail brought a tool record is mined again
	// whole, so a pair that starts before the tail is found; the rest of the
	// table is still earned, and a candidate on file is what a second sighting
	// needs to be promoted.
	if m.SearchOnly {
		return filesTouched, messages, unreadable, nil
	}
	refreshAppendedSidecars(dir, m, appended, rowMoved)
	return filesTouched, messages, unreadable, nil
}

// refreshAppendedSidecars updates the derived sidecar tables in dir after an
// in-place append pass (local transcript append or sync import) has committed
// new records to records.bin and manifest.gob.
func refreshAppendedSidecars(dir string, m Manifest, appended []model.Session, rowMoved map[string]bool) {
	if m.SearchOnly {
		return
	}
	whole, rewhole := wholeSessionsForFixes(dir, m, appended)
	mergeFixes(dir, dir, nil, whole, rewhole)
	grew := map[string]bool{}
	for _, s := range appended {
		grew[s.Key()] = true
	}
	updateReceipts(dir, dir, m, grew)
	// The two command tables are mined from every record the index holds, which
	// is a scan of records.bin — 2.9s of an 8.6s pass on a 191 MB store. Nothing
	// this pass added can change them unless it added a command or the output of
	// one, and most appends are speech, so ask first.
	// A pass killed after its commit and before the tables left them behind
	// the log, which the next pass heals whatever it appended.
	if carriesWork(appended) || commandTablesBehind(dir, m) || len(rowMoved) > 0 {
		// From the states the last update left: only the sessions this pass
		// appended to are read (#4288). The command state goes last, so it
		// stays behind until both tables are written.
		buildCommandFailsFromIndex(dir, readCommandFailState(dir), maps.Clone(rowMoved))
		buildCommandTables(dir, dir, m, len(m.DeadRecords), maps.Clone(rowMoved))
	}
}

// readAppended reads what p holds past the last pass, or whole when the index
// has never read it. A file it cannot take this pass is set back in m.Files to
// what the index held, and ok is false.
func readAppended(harness, p string, of FileState, known bool, m *Manifest, changed map[string]FileState) (ss []model.Session, ok bool) {
	var parseErr error
	if known {
		// Appended to from where the last pass stopped, which a rewrite
		// moves: the next pass decides how to read it.
		ss, parseErr = parseAppendedFile(harness, p, of, false)
		if parseErr == nil && !readAsWalked(p, changed[p]) {
			parseErr = errChangedWhileRead
		}
	} else {
		var f FileState
		ss, f, _, parseErr = readSettled(p, changed[p], func() ([]model.Session, error) { return parseAppendedFile(harness, p, of, true) })
		if parseErr == nil {
			m.Files[p], changed[p] = f, f
		}
	}
	if parseErr != nil {
		if known {
			m.Files[p] = of // retry this file on the next pass
		} else {
			delete(m.Files, p)
		}
		return nil, false
	}
	ss = dropShadowedPrompts(sources.FilterSessions(filterTombstoned(ss)), m.Sessions)
	// A prompt whose transcript has not landed yet is read again next pass,
	// from the same offset, rather than indexed from the log now and taken
	// over by a full rewrite when the transcript arrives.
	if isCodexHistory(p) && waitsForTranscript(ss, m.Sessions, time.Now()) {
		if known {
			m.Files[p] = of
		} else {
			delete(m.Files, p)
		}
		return nil, false
	}
	return ss, true
}

// appendedRow is the row key holds once s, read from p, is folded in, and
// whether the row moved to another copy's file. of is p's state before the
// pass, now its state as walked.
func appendedRow(m *Manifest, key string, s model.Session, p string, of, now FileState, known bool, emptyBefore map[string]bool) (SessionMeta, bool) {
	meta, listed := m.Sessions[key]
	if row, ok := m.Empty[key]; ok && !listed {
		meta = metaWithOrd(row, nextSessionOrd(m.Sessions))
		delete(m.Empty, key)
	}
	if !listed {
		emptyBefore[key] = true
	}
	before := meta
	named := meta.ID != ""
	fresh := !named
	if !named {
		meta = metaWithOrd(metaForSession(s), nextSessionOrd(m.Sessions))
	}
	if meta.Started.IsZero() || (!s.Started.IsZero() && s.Started.Before(meta.Started)) {
		meta.Started = s.Started
	}
	if s.Updated.After(meta.Updated) {
		meta.Updated = s.Updated
	}
	// A recorded path that is not there cannot own the row, and
	// without this the row kept the dead path and was marked as sharing
	// its id with it (#1086). A rename in one pass no longer reaches
	// here: a file deleted this pass sends the pass to the replacement
	// path, whose rename rule pairs the two. A transcript deleted in an
	// earlier pass and kept is a copy, as a rebuild reads it, and owns
	// the row by the same rule as any other (#5380). This is what is
	// left: a path gone without being kept.
	// Not for an arrival with nothing to index: that is Gemini's resume
	// stub beside a deleted transcript, not the transcript moving (#4213).
	if meta.Path != "" && s.Path != "" && meta.Path != s.Path && holdsText(s) && !m.Files[meta.Path].Kept {
		if _, lstatErr := os.Lstat(meta.Path); lstatErr != nil {
			meta.Path = ""
		}
	}
	owns, collided := claimSession(meta, s)
	// A row with nothing to index that changes hands describes the
	// stub, not the transcript taking it over: folding the transcript
	// in on top counted the stub's preamble too, one more message than
	// a rebuild of the same files gives (#4213). Start the row over
	// from the session that owns it, as the full build does.
	// Only for a file read whole: a known file hands over its tail,
	// which is not the session. The span stays what both files cover,
	// as the full build keeps it.
	// A new file that sorts ahead of the one holding the row takes it
	// the same way: a rebuild derives the row from its owner alone.
	if owns && (meta.NoText || collided) && meta.Path != s.Path && !known {
		// The span the row had before s widened it: s's own span is
		// not one the files beside it cover.
		prev := meta
		if named {
			prev.Started, prev.Updated = before.Started, before.Updated
		}
		meta = ownerRow(prev, s, metaForSession(s), prev.Ord, collided)
		fresh = true
		if !prev.Started.IsZero() && (meta.Started.IsZero() || prev.Started.Before(meta.Started)) {
			meta.Started = prev.Started
		}
		if prev.Updated.After(meta.Updated) {
			meta.Updated = prev.Updated
		}
	}
	if !owns {
		meta.SharedStarted, meta.SharedUpdated = widerSpan(meta.SharedStarted, meta.SharedUpdated, s.Started, s.Updated)
	}
	if collided {
		collisions.Add(key)
		meta.Shared = true
	}
	// A tail with no files of its own names only the directory the
	// session started in; the row already holds what the whole file
	// said, which a rebuild also takes.
	if s.Project != "" && s.Project != "-" && owns && (!known || !named || !s.ProjectFromDir || (sources.IsHomeOrEmptyProject(meta.Project) && !sources.IsHomeOrEmptyProject(s.Project))) {
		meta.Project = s.Project
	}
	if s.Path != "" && owns {
		meta.Path = s.Path
	}
	retitleAppended(&meta, s, p, of, now, known, named, owns)
	// Only the session that owns this row. The full build writes these
	// fields under the same condition; here the merge ran regardless,
	// so growing the loser of an id collision moved the owner's Words,
	// flipped its GaveUp and put the loser's files in its Touched —
	// the wrong conversation's files surfacing in blame (#1304).
	if owns {
		extendOwnedRow(&meta, s, p, known, fresh)
	}
	meta.Copies = withCopies(meta.Path, before.Copies, before.Path, s.Path)
	return meta, named && (before.Shared != meta.Shared || meta.Shared && filepath.Dir(before.Path) != filepath.Dir(meta.Path))
}

// extendOwnedRow folds what s's tail says into the row it owns.
func extendOwnedRow(meta *SessionMeta, s model.Session, p string, known, fresh bool) {
	ran := meta.RanCommand
	hadNew := len(newMessages(meta, s.Messages)) > 0
	extendDerived(meta, s.Messages)
	if !fresh {
		if s.Kind != "" && meta.Kind == "" {
			meta.Kind = s.Kind
		}
		if s.Parent != "" && meta.Parent == "" {
			meta.Parent = s.Parent
		}
		if s.Agent != "" {
			meta.Agent = s.Agent
		}
		meta.Model = meta.Model.Merge(s.Model)
		if hadNew {
			meta.Tokens = meta.Tokens.Add(s.Tokens)
			if len(s.ToolCalls) > 0 {
				if meta.ToolCalls == nil {
					meta.ToolCalls = make(map[string]int, len(s.ToolCalls))
				}
				for name, n := range s.ToolCalls {
					meta.ToolCalls[name] += n
				}
			}
			meta.ToolErrors += s.ToolErrors
		}
	}
	// Folding a turn twice changes nothing, so a store that hands the whole
	// session over again is safe here.
	meta.Rules, meta.Revokes = foldUserRules(meta.Rules, meta.Revokes, s.Typed)
	meta.Planted = foldPlanted(meta.Planted, s.Planted)
	// The session's first command: its line can come from what it
	// said before this tail, which the fold never saw. Once per
	// session, so the whole read is affordable here.
	if !ran && meta.RanCommand && known {
		if whole, ok := wholeSession(p, s); ok {
			meta.Settled = sessionSettled(whole)
		}
	}
	// The bridge record can land in any append, and a later one
	// read from the watermark does not carry it again.
	if s.RemoteID != "" {
		meta.RemoteID = s.RemoteID
	}
}

// retitleAppended names the row from s the way a rebuild would name it.
func retitleAppended(meta *SessionMeta, s model.Session, p string, of, now FileState, known, named, owns bool) {
	// A row named from the agent's words or tool output takes the
	// first question that arrives later, as a rebuild names it. Only
	// the owner names it: a copy's tail renamed the session after its
	// own newest turn (#5358).
	outranked := false
	if owns && s.Title == "" && !titlePlaceholder(meta.Title) {
		t, agent := sessionTitleFrom(s)
		outranked = titleRankOfRow(t, agent) > titleRankOfRow(meta.Title, meta.AgentTitle)
	}
	if owns && (titlePlaceholder(meta.Title) || outranked) {
		// The incremental fallback redacted nothing at all before; keep
		// sessionTitleFrom's correct fromAgent bit and redact before the
		// cut, as the full rebuild does.
		meta.Title, meta.AgentTitle = sessionTitleFrom(s)
		meta.Title, _ = redact.Text(meta.Title)
		meta.Title = truncateTitle(meta.Title, 60)
	} else if owns && s.Title != "" {
		// A title the source authored is a live value, not a one-time
		// naming: a promoted note's carries its state, and the loader
		// rewrites it on every correction. Only the full rebuild took
		// the new one, so `promote --state rejected` left every
		// one-line surface — `deja last`, the digest, the citation the
		// hook hands the agent to say aloud — reading "[accepted]"
		// until an unrelated rebuild happened to run (#R11).
		// The same widening the first naming does, or a session that
		// gets its thin title later — dsh and opencode both retitle
		// after the fact — would keep it until an unrelated rebuild.
		//
		// A thin title is widened from the session's first substantial
		// turn, and a tail does not hold it: one appended turn renamed
		// the session after itself. A row already named from the whole
		// session keeps that name (#4452). Unless the sidecar the title
		// lives in changed too: that is a rename, the row holds the old
		// name, and only the whole session can widen the new one
		// (#4592).
		t, _ := redact.Text(s.Title)
		t = boundSourceTitle(s.Harness, t)
		fromTail := known && named && s.Harness != "deja" && thinTitle(t) && !thinTitle(meta.Title)
		w := widenThinSourceTitle(s, t)
		if fromTail && sidecarChanged(of, now) {
			if whole, ok := wholeSession(p, s); ok {
				w, fromTail = widenThinSourceTitle(whole, t), false
			}
		}
		if !fromTail && w != meta.Title {
			meta.Title = w
			meta.AgentTitle = s.AgentTitle
		}
	}
}

// rereadSharedCopies reads again each held transcript that shares a removed
// one's file name, where that name is the id of a row two transcripts share:
// the filename-derived id two projects can share (#699). A turn both copies
// held was written once, under the copy read first; dropping that copy's
// records by path took the survivor's commands and outputs with them, and the
// row stayed on the deleted file until a rebuild (#4310).
func rereadSharedCopies(removed map[string]bool, changed, files map[string]FileState, sessions map[string]SessionMeta) {
	shared := map[string]bool{}
	for _, meta := range sessions {
		if meta.Shared {
			shared[meta.ID] = true
		}
	}
	names := map[string]bool{}
	for p := range removed {
		base := filepath.Base(p)
		if storeHarness(p) == "" && shared[strings.TrimSuffix(base, filepath.Ext(base))] {
			names[base] = true
		}
	}
	if len(names) == 0 {
		return
	}
	for p, f := range files {
		if _, ok := changed[p]; !ok && !removed[p] && names[filepath.Base(p)] && storeHarness(p) == "" {
			changed[p] = f
		}
	}
}

// rewrittenInPlace reports whether a kept file has another beside it under
// the same name in another form: Codex compressing x.jsonl to x.jsonl.zst,
// Gemini rewriting x.json as x.jsonl. That is the file moving, which only the
// replacement path's rename rule pairs up; the append path wrote the new
// file's records beside the old ones and kept the dead path (#4252). A pass
// that ran between zstd writing the .zst and removing the .jsonl held both,
// the row marked shared; the .zst is read again so the rule can pair them.
func rewrittenInPlace(kept map[string]bool, changed, files, held map[string]FileState, sessions map[string]SessionMeta) bool {
	if len(kept) == 0 {
		return false
	}
	stems := map[string]bool{}
	for p := range kept {
		stems[fileStem(p)] = true
	}
	shared := map[string]bool{}
	for _, meta := range sessions {
		if meta.Shared && kept[meta.Path] {
			shared[fileStem(meta.Path)] = true
		}
	}
	found := false
	for p, f := range files {
		if kept[p] || !stems[fileStem(p)] {
			continue
		}
		if _, ok := changed[p]; ok {
			if _, known := held[p]; !known {
				found = true
			}
		} else if shared[fileStem(p)] {
			changed[p] = f
			found = true
		}
	}
	return found
}

// fileStem is p without its compression suffix and its extension.
func fileStem(p string) string {
	p = strings.TrimSuffix(p, ".zst")
	return strings.TrimSuffix(p, filepath.Ext(p))
}

// carriesWork reports whether any of these sessions holds a command or the
// output of one, which is all the two command tables are mined from.
func carriesWork(ss []model.Session) bool {
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == sources.RoleCommand || m.Role == sources.RoleToolOutput {
				return true
			}
		}
	}
	return false
}

// sidecarChanged reports whether the metadata file read with a transcript
// (Kimi's state.json, say) changed between two walks.
func sidecarChanged(a, b FileState) bool {
	return a.MetadataSize != b.MetadataSize || a.MetadataMTime != b.MetadataMTime
}

// wholeSession reads s's file from its first byte and returns s as the full
// build sees it, for what an appended tail cannot tell.
func wholeSession(p string, s model.Session) (model.Session, bool) {
	ss, err := parseAppendedFile("", p, FileState{}, true)
	if err != nil {
		return model.Session{}, false
	}
	for _, w := range ss {
		if w.Harness == s.Harness && w.ID == s.ID {
			w.Messages = dropRepeatedMessages(w.Messages)
			return w, true
		}
	}
	return model.Session{}, false
}

// lastCompleteLineOffset finds the offset just past the final newline, so an
// append can resume without re-reading or losing a torn tail line. Reads at
// most the last 64KB; a longer unterminated tail falls back to full size.
func lastCompleteLineOffset(p string, size int64) int64 {
	if size == 0 {
		return 0
	}
	f, err := os.Open(p)
	if err != nil {
		return size
	}
	defer func() { _ = f.Close() }()
	// Walk backwards window by window: a torn line longer than one window
	// (a fat tool result caught mid-write) must not fool us into treating
	// the whole file as complete, or its message is lost after completion.
	const window = 64 * 1024
	end := size
	for end > 0 {
		start := max(end-window, 0)
		buf := make([]byte, end-start)
		if _, err := f.ReadAt(buf, start); err != nil {
			return size
		}
		for i, b := range slices.Backward(buf) {
			if b == '\n' {
				return start + int64(i) + 1
			}
		}
		end = start
	}
	return 0
}

// indexedText is a message's text as the index stores it: plumbing stripped,
// NFC-canonicalised (this path does not go through redactForIngest, #1098),
// redacted, then cut to maxIndexedText on a rune boundary.
func indexedText(text string) (string, redact.Counts, bool) {
	redacted, counts := redact.Text(nfcfold.Compose(stripSelfRecall(text)))
	if len(redacted) <= maxIndexedText {
		return redacted, counts, false
	}
	cut := maxIndexedText
	for cut > 0 && !utf8.RuneStart(redacted[cut]) {
		cut--
	}
	return redacted[:cut], counts, true
}

// preRedactSessions redacts every message concurrently before the write
// loop. Redaction is regex-heavy and was the serial bottleneck of a cold
// build; the write loop stays sequential (append-only log), but by the time
// it runs every text is already clean. Counters land in the manifest exactly
// as the serial path recorded them.
func preRedactSessions(m *Manifest, ss []model.Session) {
	var mu sync.Mutex
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := min(runtime.GOMAXPROCS(0), len(ss))
	if workers < 1 {
		return
	}
	for range workers {
		wg.Go(func() {
			for si := range jobs {
				s := &ss[si]
				for mi := range s.Messages {
					redacted, counts, clipped := indexedText(s.Messages[mi].Text)
					if clipped {
						mu.Lock()
						countClipped(m, s.Path, s.ID, 1)
						mu.Unlock()
					}
					s.Messages[mi].Text = redacted
					if n := counts.Total(); n > 0 && m != nil {
						mu.Lock()
						m.Redacted += n
						if m.RedactionRules == nil {
							m.RedactionRules = map[string]int{}
						}
						// The store, as above (#2238).
						h := sources.HarnessForKind(harnessForPath(s.Path))
						if h == "" {
							if _, ok := m.Files[sources.OpencodeDB()]; ok {
								h = "opencode"
							}
						}
						for rule, c := range counts {
							m.RedactionRules[h+":"+rule] += c
						}
						if s.Path != "" && m.Files != nil {
							if fs, ok := m.Files[s.Path]; ok {
								fs.Redactions += n
								m.Files[s.Path] = fs
							}
						}
						mu.Unlock()
					}
				}
				s.Messages = dropRepeatedMessages(s.Messages)
			}
		})
	}
	for si := range ss {
		jobs <- si
	}
	close(jobs)
	wg.Wait()
}

// prefixSampleWindow is how much is read at each end. Large enough that a
// rewrite cannot plausibly reproduce it byte for byte, small enough that the
// cost does not depend on how long the session has been running.
const prefixSampleWindow = 512 << 10

// filePrefixSample fingerprints the first n bytes by reading at most two
// windows of them: the head and the bytes ending at n. n is mixed in, so a
// file that grew and one that was rewritten to the same content at a
// different length do not collide.
func filePrefixSample(path string, n int64) uint64 {
	if n <= 0 {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	h := fnv.New64a()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(n))
	_, _ = h.Write(b[:])
	head := min(n, prefixSampleWindow)
	if _, err := io.Copy(h, io.LimitReader(f, head)); err != nil {
		return 0
	}
	if n > head {
		start := max(head, n-prefixSampleWindow)
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return 0
		}
		if _, err := io.Copy(h, io.LimitReader(f, n-start)); err != nil {
			return 0
		}
	}
	return h.Sum64()
}

// filePrefixHash fingerprints the first n bytes of a file. Only used to decide
// whether an append is safe, so a fast non-cryptographic hash is the right
// tool: a collision costs one unnecessary full reparse, never a wrong index.
//
// Kept for manifests written before filePrefixSample: those store a hash of
// every byte, and it takes one more read to verify them before the walk
// records a sample instead.
func filePrefixHash(path string, n int64) uint64 {
	if n <= 0 {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	h := fnv.New64a()
	if _, err := io.Copy(h, io.LimitReader(f, n)); err != nil {
		return 0
	}
	return h.Sum64()
}

// countClipped records messages stored short of the transcript, against the
// file that holds them. The caller holds the lock where one is needed;
// redactForIngest runs single-threaded.
func countClipped(m *Manifest, sourcePath, sessionID string, n int) {
	if m == nil || n == 0 {
		return
	}
	p := sourcePath
	if harnessForPath(p) == "" {
		// opencode sessions carry their project dir as Path; the store on
		// record is the database file.
		if db := sources.OpencodeDB(); db != "" {
			if _, ok := m.Files[db]; ok {
				p = db
			}
		}
	}
	if harnessForPath(p) == "" {
		return
	}
	if m.IngestFiles == nil {
		m.IngestFiles = map[string]FileIngest{}
	}
	e := m.IngestFiles[p]
	e.Clipped += n
	if sessionID != "" {
		if e.ClippedSessions == nil {
			e.ClippedSessions = map[string]int{}
		}
		e.ClippedSessions[sessionID] += n
	}
	m.IngestFiles[p] = e
}
