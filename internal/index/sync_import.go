package index

import (
	"hash/fnv"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// importer is the state one Import carries across the files it reads.
type importer struct {
	dir  string
	m    *Manifest
	dead map[string]bool
	// Content tombstones are recorded against the session they came from, so
	// they are read back once here rather than scanned per record.
	deadContent map[string]bool
	ex          sources.Excluder
	// The switches a reader on this machine runs under hold for what a peer
	// indexed under its own: the batch carries every role the other machine
	// kept (#5425).
	offRoles     map[string]bool
	subagentsOff bool
	// So do the other two rules that keep work out of this machine's memory:
	// a store it does not read, and a tree it does not recall from (#5435).
	offHarnesses map[string]bool
	pol          policy.Policy

	recsByKey   map[string][]Record
	metas       map[string]SessionMeta
	titleAt     map[string]time.Time // key -> time of the turn the title came from
	titleRankOf map[string]int       // key -> how good that turn was as a title
	// A shared folder is both an outbox and an inbox: the machine that wrote a
	// batch also runs `sync import` on that directory, and its own records came
	// back as a second, "imported" copy of every session and note it had
	// (#987). Recognised by content, so a peer's genuinely new lines in a
	// session this machine also holds still land.
	localContent       map[string]bool
	localSessions      map[string]bool
	localContentLoaded bool

	added             int
	ownSkipped        int
	forgottenSkipped  int
	incompleteSkipped int
	excludedSkipped   int
	switchedSkipped   int
	harnessSkipped    int
	ignoredSkipped    int
}

func newImporter(dir string) *importer {
	imp := &importer{dir: dir, dead: readTombstones(), deadContent: map[string]bool{}}
	for k := range imp.dead {
		if _, content, ok := strings.Cut(k, "\x00"); ok {
			imp.deadContent[content] = true
		}
	}
	imp.ex = sources.NewExcluder()
	imp.offRoles = sources.SwitchedOffRoles()
	imp.subagentsOff = sources.SubagentsOff()
	imp.offHarnesses = sources.ExcludedHarnesses()
	imp.pol = policy.Load()
	imp.recsByKey = map[string][]Record{}
	imp.metas = map[string]SessionMeta{}
	imp.titleAt = map[string]time.Time{}
	imp.titleRankOf = map[string]int{}
	imp.localContent = map[string]bool{}
	imp.localSessions = map[string]bool{}
	return imp
}

// loadLocalContent reads this machine's own record content keys, once.
func (imp *importer) loadLocalContent() {
	if imp.localContentLoaded {
		return
	}
	imp.localContentLoaded = true
	for _, r := range readRecordsForForget(imp.dir) {
		meta, ok := imp.m.Sessions[r.Record.Key]
		if !ok || meta.OrigID != "" {
			continue
		}
		th := fnv.New64a()
		_, _ = th.Write([]byte(r.Record.Text))
		imp.localContent[recordContentKey(meta.Harness, meta.ID, r.Record.Role, r.Record.Time, th.Sum64())] = true
	}
}

// importFile is what one file contributed, kept apart until it is read whole.
type importFile struct {
	dedupe *[]string
	// Redactions this file contributed, applied to the manifest only once
	// the file has been read whole: a truncated file is rolled back below,
	// and its redaction count must not survive the records it counted.
	redacted int
	rules    map[string]int
}

// readFile imports one batch file whole, or rolls back what it added and
// returns why it was refused.
func (imp *importer) readFile(p string) error {
	// A file is imported whole or not at all, so what it contributed is
	// remembered before it is read and rolled back if it turns out to be
	// truncated: half a transfer is the one outcome nobody can reason
	// about afterwards (#891).
	before := importSnapshot{
		counts: make(map[string]int, len(imp.recsByKey)),
		metas:  make(map[string]SessionMeta, len(imp.metas)),
		at:     make(map[string]time.Time, len(imp.titleAt)),
		rank:   make(map[string]int, len(imp.titleRankOf)),
		added:  imp.added,
	}
	file := &importFile{dedupe: &before.dedupe, rules: map[string]int{}}
	for k, v := range imp.recsByKey {
		before.counts[k] = len(v)
	}
	maps.Copy(before.metas, imp.metas)
	maps.Copy(before.at, imp.titleAt)
	maps.Copy(before.rank, imp.titleRankOf)
	if err := readSyncFile(p, func(sr SyncRecord) error { return imp.add(sr, file) }); err != nil {
		// One unreadable file used to stop the whole directory, so a valid
		// export sitting beside a truncated one never arrived and the
		// reader was told only about a stray character (#891). The file is
		// still refused whole; the others are not held hostage to it.
		for k := range imp.recsByKey {
			n := before.counts[k]
			if n == 0 {
				delete(imp.recsByKey, k)
				continue
			}
			imp.recsByKey[k] = imp.recsByKey[k][:n]
		}
		for _, k := range before.dedupe {
			delete(imp.m.ImportedRecords, k)
		}
		restoreMap(imp.metas, before.metas)
		restoreMap(imp.titleAt, before.at)
		restoreMap(imp.titleRankOf, before.rank)
		imp.added = before.added
		return err
	}
	// The file was read whole: its redactions are real and stay counted.
	if file.redacted > 0 {
		imp.m.Redacted += file.redacted
		if imp.m.RedactionRules == nil {
			imp.m.RedactionRules = map[string]int{}
		}
		for rule, c := range file.rules {
			imp.m.RedactionRules[rule] += c
		}
	}
	return nil
}

// add takes one record of a batch file into the import, unless a rule drops it.
func (imp *importer) add(sr SyncRecord, file *importFile) error {
	origID := sr.SessionID
	if sr.Harness == "" || origID == "" {
		// No session to attribute it to. Count it so the summary does not
		// call a partial transfer complete (#1118).
		imp.incompleteSkipped++
		return nil
	}
	// The same rule the local ingest applies: a message that strips to
	// empty is not a message, and a session with nothing but those is
	// not a session. deja's own exports never carry them, but a
	// hand-made batch or one from a build older than #868 does — and
	// they came back in as blank lines in `last` and rows in every
	// counter (#896).
	if strings.TrimSpace(sr.Text) == "" {
		// A record with no text at all is not an empty message: it is
		// a shape this build does not read, a field renamed by a newer
		// one. Counted, or the batch it empties reads as up to date.
		if sr.Text == "" {
			imp.incompleteSkipped++
		}
		return nil
	}
	importID := ImportedSessionID(sr.Harness, origID)
	text, cnt, dedupe, ok := imp.admit(sr, origID, importID)
	if !ok {
		return nil
	}
	key := sr.Harness + ":" + importID
	// Count what redaction removed, the way local ingest does — the
	// import path redacted the text but threw the count away, so
	// `stats --redaction` under-reported protection on an imported
	// store (measured: two secrets redacted, total said one).
	if n := cnt.Total(); n > 0 {
		file.redacted += n
		for rule, c := range cnt {
			file.rules[sr.Harness+":"+rule] += c
		}
	}
	imp.recsByKey[key] = append(imp.recsByKey[key], Record{Key: key, Role: sr.Role, Text: text, Time: sr.Time, SourcePath: syncImportPath})
	imp.metas[key] = imp.sessionMeta(sr, key, importID, origID, text)
	imp.m.ImportedRecords[dedupe] = true
	*file.dedupe = append(*file.dedupe, dedupe)
	imp.added++
	return nil
}

// admit runs a record past the forget, dedupe and privacy rules. It returns
// the text as stored here and the record's dedupe key, or ok false to drop it.
func (imp *importer) admit(sr SyncRecord, origID, importID string) (text string, cnt redact.Counts, dedupe string, ok bool) {
	m, dead := imp.m, imp.dead
	// Forgetting is primary data, not cache: a tombstoned session
	// must stay dead even when the peer still holds the batch and
	// this index was wiped and rebuilt.
	//
	// Both keys, because a session forgotten here was forgotten under
	// its own id: checking only the imported one let a peer's copy walk
	// the same text back into local search under a new id, while
	// `forget --list` still called it forgotten (#968).
	//
	// Ahead of the dedupe ledger, which answers first for the ordinary
	// case — the same batch handed over twice. Both drop the record;
	// only this one knows why, and behind the ledger the import went
	// silent exactly when the peer re-sent what you forgot (#980).
	if dead[sr.Harness+":"+importID] || dead[sr.Harness+":"+origID] {
		imp.forgottenSkipped++
		return "", nil, "", false
	}
	if parent := string(sr.Parent); parent != "" && (dead[sr.Harness+":"+ImportedSessionID(sr.Harness, parent)] || dead[sr.Harness+":"+parent]) {
		imp.forgottenSkipped++
		return "", nil, "", false
	}
	// Key includes role and a text hash: two messages can legally share
	// a timestamp (aider stamps a whole session with its start time).
	// The legacy time-only key is still honored so batches imported by
	// older versions stay idempotent.
	legacy := sr.Harness + ":" + origID + ":" + sr.Time.UTC().Format(time.RFC3339Nano)
	// Keyed on the text as this machine stores it, redacted here. A
	// peer whose rules widened re-sends the same record with a marker
	// where the value was, and keyed on what it sent the record landed
	// twice. The key on the sent text is what older builds wrote, and
	// is still honoured.
	text, cnt = redact.Text(carriedText(sr.Harness, sr.Role, sr.Text))
	th := fnv.New64a()
	_, _ = th.Write([]byte(text))
	rawHash := th.Sum64()
	if text != sr.Text {
		rh := fnv.New64a()
		_, _ = rh.Write([]byte(sr.Text))
		rawHash = rh.Sum64()
	}
	dedupe = legacy + ":" + sr.Role + ":" + strconv.FormatUint(th.Sum64(), 16)
	sent := legacy + ":" + sr.Role + ":" + strconv.FormatUint(rawHash, 16)
	// A day bucket's id is the sending machine's rendering of a date,
	// not the identity of what it holds: after that machine changes
	// zone the same note arrives under a new bucket and every peer
	// grew a second copy (#977). Key those on what does not move.
	if imp.localSessions[sr.Harness+":"+sr.SessionID] {
		imp.loadLocalContent()
		if imp.localContent[recordContentKey(sr.Harness, sr.SessionID, sr.Role, sr.Time, th.Sum64())] ||
			imp.localContent[recordContentKey(sr.Harness, sr.SessionID, sr.Role, sr.Time, rawHash)] {
			imp.ownSkipped++
			return "", nil, "", false
		}
	}
	if bucket := dayBucketKey(sr, th.Sum64()); bucket != "" {
		sentBucket := dayBucketKey(sr, rawHash)
		if m.ImportedRecords[bucket] || m.ImportedRecords[sentBucket] {
			return "", nil, "", false
		}
		// Forgotten as text: the sending machine renders the day its
		// own way, so a note this machine dropped arrives under a
		// bucket id no tombstone here has ever named (#985).
		if imp.deadContent[bucket] || imp.deadContent[sentBucket] {
			imp.forgottenSkipped++
			return "", nil, "", false
		}
		dedupe, sent = bucket, sentBucket
	}
	// The ledger is an optimisation over the manifest, not authority
	// over it: it says a record already arrived, so re-importing the
	// same batch is a no-op. But forgetting an imported session drops
	// it from the manifest while leaving its rows in the ledger, and a
	// tombstone (checked above) only holds until unforget lifts it —
	// after which the stale ledger silently ate the very re-import
	// unforget tells the user to run, and the "only copy" was
	// unrecoverable. Skip only while the session it belongs to still
	// lives; a ledger row whose session is gone is stale, not a dupe.
	if m.ImportedRecords[dedupe] || m.ImportedRecords[sent] || m.ImportedRecords[legacy] {
		if _, live := m.Sessions[sr.Harness+":"+importID]; live {
			return "", nil, "", false
		}
	}
	// The exclude list keeps a project out of this machine's memory;
	// a sync from another machine must not put it back. Counted, so
	// the batch it empties does not read as an empty batch (#2666).
	if imp.ex.Match(sr.Project) {
		imp.excludedSkipped++
		return "", nil, "", false
	}
	if imp.offHarnesses[strings.ToLower(sr.Harness)] {
		imp.harnessSkipped++
		return "", nil, "", false
	}
	// Matched as recall would see it once stored, and as the peer
	// named it, so a glob written for the local name holds too.
	if imp.pol.Ignored(syncImportPath, importedProjectPrefix+sr.Project) || imp.pol.Ignored("", sr.Project) {
		imp.ignoredSkipped++
		return "", nil, "", false
	}
	spawned := spawnedKind(string(sr.Kind)) || sr.Parent != ""
	if imp.offRoles[sr.Role] || (spawned && imp.subagentsOff) {
		imp.switchedSkipped++
		return "", nil, "", false
	}
	return text, cnt, dedupe, true
}

// sessionMeta folds one admitted record into its session's manifest entry.
func (imp *importer) sessionMeta(sr SyncRecord, key, importID, origID, text string) SessionMeta {
	meta := imp.metas[key]
	if meta.ID == "" {
		meta = SessionMeta{ID: importID, Harness: sr.Harness, Project: importedProjectPrefix + sr.Project, Path: syncImportPath, OrigID: origID, From: string(sr.Origin)}
	}
	if spawnedKind(string(sr.Kind)) {
		meta.Kind = string(sr.Kind)
	} else if sr.Parent != "" {
		meta.Kind = "sidechain" // a run in its parent's log, from a build that sends no kind
	}
	if meta.Started.IsZero() || (!sr.Time.IsZero() && sr.Time.Before(meta.Started)) {
		meta.Started = sr.Time
	}
	if sr.Time.After(meta.Updated) {
		meta.Updated = sr.Time
	}
	imp.noteTitle(&meta, key, sr, text)
	// A promoted note carries its state in the text: "[rejected] …".
	// The states themselves live in the other machine's notes.jsonl,
	// which never travels, so without reading them here a decision
	// retracted there reads as accepted on this machine (#975).
	//
	// The state the sender filed wins over the line's own. The date is
	// the filing day in UTC; a batch from before the fields has only
	// the evidence time, taken as its UTC day so every peer and every
	// rebuild store the same string (#5053). Read from the redacted
	// text: the note lands in the manifest.
	if strings.HasPrefix(origID, "deja-note-") {
		if st, note, at, ok := carriedNoteState(sr); ok {
			meta.Lifecycle, meta.LifecycleNote, meta.LifecycleAt = st, note, at
		} else if st, note, ok := noteStateFromText(text); ok {
			meta.Lifecycle, meta.LifecycleNote = st, note
			if !sr.Time.IsZero() {
				meta.LifecycleAt = sr.Time.UTC().Format(lifecycleDay)
			}
		}
	}
	return meta
}

// noteTitle gives the session a title from this turn when it is the best yet.
func (imp *importer) noteTitle(meta *SessionMeta, key string, sr SyncRecord, text string) {
	// The title does not travel in the record stream, and without it the
	// receiving machine's `last` is a column of hashes — the content
	// arrived, the one line that makes the list readable did not (#670).
	// Derive it the way ingest does, from the earliest user turn that is
	// speech rather than harness plumbing.
	// A user turn always wins; the assistant's first sentence fills in
	// when there is none, the same order ingest uses (#692).
	if rank := titleRank(sr.Role); rank > 0 && titleWorthy(strings.TrimSpace(text)) {
		better := rank > imp.titleRankOf[key]
		sameRank := rank == imp.titleRankOf[key] && !sr.Time.IsZero() && sr.Time.Before(imp.titleAt[key])
		if _, seen := imp.titleAt[key]; !seen || better || sameRank {
			if rank == titleRank(roleToolOutput) {
				meta.Title = toolOutputTitle(strings.TrimSpace(text))
			} else {
				meta.Title = truncateTitle(strings.TrimSpace(text), 60)
			}
			// The listing marks a title that is the agent's own words
			// rather than the reader's question (#1100); the import
			// path derived the title the same way and lost the mark.
			meta.AgentTitle = rank == titleRank("assistant")
			imp.titleAt[key] = sr.Time
			imp.titleRankOf[key] = rank
		}
	} else if meta.Title == "" && titleRank(sr.Role) > 0 {
		// The same last resort ingest has: a session of nothing but
		// harness plumbing arrives titleless and lists as a bare id
		// here too. A real turn later in the stream replaces the
		// stand-in, which is what the branch above does (#2548).
		if t := strings.TrimSpace(text); t != "" {
			meta.Title = harnessOutputTitle(t)
			meta.AgentTitle = false
		}
	}
}
