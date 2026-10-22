package index

import (
	"bufio"
	"crypto/sha1" //nolint:gosec // a short id, not a security hash
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// SyncRecord is one message as a sync batch carries it.
type SyncRecord struct {
	Harness   string    `json:"harness"`
	SessionID string    `json:"session_id"`
	Project   string    `json:"project"`
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	Time      time.Time `json:"time"`
	// Origin is the machine that exported the record. Additive: a batch
	// written before this field existed decodes with it empty, and the
	// receiving side then says only that the session came from elsewhere,
	// which is what it said before.
	Origin syncName `json:"origin,omitempty"`
	// Lifecycle, LifecycleNote and LifecycleAt are a promoted note's current
	// state, as the machine holding its notes.jsonl filed it, on every record
	// of the note. The text carries a state too, but only its own line's, and
	// the record time is the evidence's, in the sender's zone: the receiver
	// dated a rejection by the session it rejected, and every peer showed a
	// different day (#5053). LifecycleAt is the filing day in UTC. Additive:
	// a batch without them falls back to the text.
	Lifecycle     syncName `json:"lifecycle,omitempty"`
	LifecycleNote syncName `json:"lifecycle_note,omitempty"`
	LifecycleAt   syncName `json:"lifecycle_at,omitempty"`
	// Parent is the session whose own log holds this sub-agent run (Copilot
	// CLI's task runs). Forgetting the parent forgets the run, and the run
	// has no tombstone of its own, so the receiver checks the parent's
	// (#5216). Additive.
	Parent syncName `json:"parent,omitempty"`
	// Kind marks a sub-agent run, so DEJA_INCLUDE_SUBAGENTS=0 on the
	// receiving machine leaves it out as its own readers do (#5425).
	// Additive.
	Kind syncName `json:"kind,omitempty"`
}

// lifecycleDay is how a lifecycle date is stored: the UTC day of the filing.
const lifecycleDay = "2006-01-02"

// syncName is a metadata string that survives being sent the wrong shape. A
// batch is another machine's file, and this machine may be older or newer than
// the one that wrote it: decoding a plain string field strictly means one
// record carrying an object where a name was expected refuses the entire file
// and imports nothing. A name deja cannot read is worth losing; the history
// behind it is not.
type syncName string

// importedProjectPrefix marks a project that arrived by sync. Relaying strips
// it so the next machine files the work under its real name.
const importedProjectPrefix = "imported:"

func (n *syncName) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*n = syncName(s)
	}
	return nil
}

// Export writes records newer than the watermarks for an unnamed peer (a
// manual `deja sync export`). ExportFull ignores watermarks so a fresh
// machine can receive the whole history even after earlier batch dirs are
// gone; import-side dedupe makes it safe.
func Export(dir, outDir string) (int, error) {
	return exportRecords(dir, outDir, "", false, false)
}

// ExportFull is Export without the watermarks.
func ExportFull(dir, outDir string) (int, error) {
	return exportRecords(dir, outDir, "", true, false)
}

// ExportRelay is ExportFull and the work that arrived from other machines with
// it, each record under the names it had where it happened rather than this
// machine's. Opt-in, because passing a peer's work on spreads it past whoever
// agreed to the first hop (#2450) — but a machine in the middle of a chain, or
// one being migrated off, has to be able to hand over everything it holds
// (#2962). It never sends a record back to the machine it came from.
func ExportRelay(dir, outDir, peer string) (int, error) {
	return exportRecords(dir, outDir, peer, true, true)
}

// ExportTo is Export for a named peer: the watermark it advances is that
// peer's alone. An empty name keeps the shared one, which is what a hand-taken
// backup uses.
func ExportTo(dir, outDir, peer string) (int, error) {
	return exportRecords(dir, outDir, peer, false, false)
}

// ExportDeferred writes batches like Export but does not advance the
// watermarks; the returned commit persists them once the receiver has
// acknowledged the batch. Watermarked sync must be acknowledged delivery:
// advancing on a failed transport silently drops records from the next push.
func ExportDeferred(dir, outDir, peer string) (int, func() error, error) {
	return exportRecordsDeferred(dir, outDir, peer, false)
}

// watermarkKey namespaces a source's watermark by peer: what a laptop has
// already received says nothing about what a server has. An empty peer keeps
// the bare source key, so manifests written before this existed still read.
func watermarkKey(peer, source string) string {
	if peer == "" {
		return source
	}
	return peer + "\x00" + source
}

// exportBoundaryCap bounds how many record identities a watermark carries.
// The manifest is read on every search, so this is a size trade: only sources
// whose records actually tie on a timestamp carry a full set, and those are
// the ones the boundary exists for — aider stamps a whole session with its
// start time, which blows any small cap and would resend that session on
// every push. A full set costs ~4 KB; a source that still overflows falls
// back to resending its newest instant, which import dedupes.
const exportBoundaryCap = 512

// recordIdentity is a stable fingerprint of one exported record.
func recordIdentity(r Record) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(r.Key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(r.Role))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(r.Text))
	return h.Sum64()
}

func exportRecords(dir, outDir, peer string, full, relay bool) (int, error) {
	n, commit, err := exportRecordsWith(dir, outDir, peer, full, relay)
	if err != nil {
		return n, err
	}
	return n, commit()
}

func exportRecordsDeferred(dir, outDir, peer string, full bool) (int, func() error, error) {
	return exportRecordsWith(dir, outDir, peer, full, false)
}

func exportRecordsWith(dir, outDir, peer string, full, relay bool) (int, func() error, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return 0, nil, err
	}
	defer unlock()
	m, err := readManifest(dir)
	if err != nil {
		return 0, nil, err
	}
	if err = os.MkdirAll(outDir, 0o700); err != nil {
		// The import side of the same mistake is worded; this one handed back
		// `mkdir /…/file: not a directory` (#1112).
		if fi, statErr := os.Stat(outDir); statErr == nil && !fi.IsDir() {
			return 0, nil, fmt.Errorf("%s is a file; sync export wants a directory to write the batch into", search.SafeLine(outDir))
		}
		return 0, nil, err
	}
	if m.ExportWatermarks == nil {
		m.ExportWatermarks = map[string]int64{}
	}
	bySource := map[string][]SyncRecord{}
	self := MachineName()
	nextWatermarks := map[string]int64{}
	maps.Copy(nextWatermarks, m.ExportWatermarks)
	// Records that already went out at exactly the watermark instant. Time
	// alone cannot resume precisely when a harness stamps a whole session
	// with one timestamp, so the boundary is remembered by record identity.
	sentAtBoundary := map[string]map[uint64]bool{}
	for k, hashes := range m.ExportBoundary {
		set := make(map[uint64]bool, len(hashes))
		for _, h := range hashes {
			set[h] = true
		}
		sentAtBoundary[k] = set
	}
	nextBoundary := map[string]map[uint64]bool{}
	// Only sources this export actually touched may have their watermark or
	// boundary rewritten; pushing project B must leave project A's alone.
	touched := map[string]bool{}
	// The exclude list is a privacy control, and it only ran at ingest — so
	// setting it and syncing, which is exactly what someone does the moment
	// they realise a project should not be shared, was the sequence that
	// shipped it to another machine (#1307). Applied here too: the export is
	// where data leaves, and a pattern set after the index was built has to
	// hold at that boundary whether or not the index has been rebuilt.
	ex := sources.NewExcluder()
	// And the other privacy control, for the same reason at the same boundary.
	// The ignore rule is what a reader sets when a tree should stay out of
	// recall — its own documentation calls it the one directory deja never
	// recalls from without being asked — and it was applied everywhere except
	// where memory actually leaves the machine, so the export shipped it to a
	// peer that then served it as imported memory (#2654).
	pol := policy.Load()
	// And a store this machine excludes: set after the index was built, or
	// holding sessions a peer sent before it was set, it went out on the next
	// export or relay (#5435).
	offHarnesses := sources.ExcludedHarnesses()
	// The oldest instant held back per source. A watermark that ran past an
	// excluded record would settle it forever: remove the pattern later and
	// that work never syncs again, because the source resumes from a point
	// beyond it. Held records are not sent, and they are not settled either.
	heldFrom := map[string]int64{}
	var noteStates map[string]sources.Lifecycle
	err = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		// What arrived by sync travels on rather than stopping here. A machine
		// that pulled from a container and is then pulled from itself used to
		// export nothing at all — `--full` included — so a middle machine in a
		// chain was a dead end, and a migration off it lost everything it had
		// gathered (#2962). It relays under the original machine's name, not
		// its own: a relay must not sign someone else's work.
		relayed := r.SourcePath == syncImportPath
		if relayed && !relay {
			// The standing rule: what arrived from elsewhere is not this
			// machine's to pass on, because a second hop spreads it past
			// whoever agreed to the first (#2450). Opt in to relay it — a
			// migration off this machine, or a chain where it sits in the
			// middle — with `sync export --include-imported` (#2962).
			return
		}
		if relayed && peer != "" {
			// Not back where it came from. A pull tells the far side who is
			// asking (`sync export --peer <us>`), and sending a machine its own
			// work is pure traffic: the receiver recognises it and drops it.
			if meta, ok := m.Sessions[r.Key]; ok && meta.From != "" && strings.EqualFold(meta.From, peer) {
				return
			}
		}
		source := r.SourcePath
		if source == "" {
			source = r.Key
		}
		wk := watermarkKey(peer, source)
		rh := recordIdentity(r)
		if !full && !r.Time.IsZero() {
			wm := m.ExportWatermarks[wk]
			// Strictly older is settled; a record sharing the watermark
			// instant is only settled if it was in that push.
			if r.Time.UnixNano() < wm {
				return
			}
			if r.Time.UnixNano() == wm && sentAtBoundary[wk][rh] {
				return
			}
		}
		meta, ok := m.Sessions[r.Key]
		if !ok {
			return
		}
		// A relayed session is filed here under "imported:<project>" and a
		// local id. It goes back out under the names it had where the work
		// happened, so the next machine dedupes it against a batch pulled
		// straight from that machine rather than growing a second copy — and
		// so the privacy rules below, which are written against real project
		// names, match it at all.
		project, sessionID, origin := exportNames(meta, relayed, self)
		if (!ex.Empty() && ex.Match(project)) || pol.Ignored(meta.Path, project) || offHarnesses[strings.ToLower(meta.Harness)] {
			// Held, not settled, for both: a watermark that ran past a
			// withheld record would settle it forever, so lifting the rule
			// later would never send that work.
			if tn := r.Time.UnixNano(); !r.Time.IsZero() {
				if cur, seen := heldFrom[wk]; !seen || tn < cur {
					heldFrom[wk] = tn
				}
			}
			return
		}
		text, _ := redact.Text(r.Text)
		rec := SyncRecord{Harness: meta.Harness, SessionID: sessionID, Project: project, Role: r.Role, Text: text, Time: r.Time, Origin: origin}
		if !relayed && inParentFile(meta.Harness, meta.Kind, meta.Parent) {
			rec.Parent = syncName(meta.Parent)
		}
		if spawnedKind(meta.Kind) {
			rec.Kind = syncName(meta.Kind)
		}
		if meta.Harness == "deja" && strings.HasPrefix(sessionID, "deja-note-") {
			exportNoteLifecycle(&rec, meta, sessionID, relayed, &noteStates)
		}
		bySource[source] = append(bySource[source], rec)
		touched[wk] = true
		advanceExportWatermark(nextWatermarks, nextBoundary, sentAtBoundary, wk, rh, r.Time.UnixNano())
	})
	if err != nil {
		return 0, nil, err
	}
	// Clamp each source's next watermark below the oldest record it held back.
	for wk, held := range heldFrom {
		if nextWatermarks[wk] >= held {
			nextWatermarks[wk] = held - 1
			delete(nextBoundary, wk)
		}
	}
	total, err := writeSyncBatches(outDir, bySource)
	if err != nil {
		return total, nil, err
	}
	commit := func() error { return commitExportWatermarks(dir, touched, nextWatermarks, nextBoundary) }
	return total, commit, nil
}

// exportNames is the project, session id and origin a record goes out under.
func exportNames(meta SessionMeta, relayed bool, self string) (project, sessionID string, origin syncName) {
	project, sessionID, origin = meta.Project, meta.ID, syncName(self)
	if relayed {
		project = strings.TrimPrefix(meta.Project, importedProjectPrefix)
		if meta.OrigID != "" {
			sessionID = meta.OrigID
		}
		if meta.From != "" {
			origin = syncName(meta.From)
		}
	}
	return project, sessionID, origin
}

// exportNoteLifecycle puts a promoted note's state on the record that carries it.
func exportNoteLifecycle(rec *SyncRecord, meta SessionMeta, sessionID string, relayed bool, noteStates *map[string]sources.Lifecycle) {
	if relayed {
		rec.Lifecycle, rec.LifecycleNote, rec.LifecycleAt = syncName(meta.Lifecycle), syncName(meta.LifecycleNote), syncName(meta.LifecycleAt)
		return
	}
	if *noteStates == nil {
		*noteStates = promotedStatesByNoteID()
	}
	if lc, ok := (*noteStates)[sessionID]; ok {
		note, _ := redact.Text(lc.Note)
		rec.Lifecycle, rec.LifecycleNote = syncName(lc.State), syncName(note)
		if !lc.At.IsZero() {
			rec.LifecycleAt = syncName(lc.At.UTC().Format(lifecycleDay))
		}
	}
}

// advanceExportWatermark moves a source's next watermark and boundary set
// past a record this export sends.
func advanceExportWatermark(nextWatermarks map[string]int64, nextBoundary, sentAtBoundary map[string]map[uint64]bool, wk string, rh uint64, tn int64) {
	switch {
	case tn > nextWatermarks[wk]:
		nextWatermarks[wk] = tn
		nextBoundary[wk] = map[uint64]bool{rh: true}
	case tn == nextWatermarks[wk]:
		if nextBoundary[wk] == nil {
			nextBoundary[wk] = map[uint64]bool{}
			for h := range sentAtBoundary[wk] {
				nextBoundary[wk][h] = true
			}
		}
		nextBoundary[wk][rh] = true
	}
}

// writeSyncBatches writes one batch file per source and counts the records.
func writeSyncBatches(outDir string, bySource map[string][]SyncRecord) (int, error) {
	total := 0
	for source, recs := range bySource {
		if len(recs) == 0 {
			continue
		}
		name := fmt.Sprintf("deja-sync-%s-%d.jsonl", shortHash(source), time.Now().UnixNano())
		f, err := os.Create(filepath.Join(outDir, name))
		if err != nil {
			return total, err
		}
		enc := json.NewEncoder(f)
		for _, rec := range recs {
			if err := enc.Encode(rec); err != nil {
				_ = f.Close()
				return total, err
			}
			total++
		}
		if err := f.Close(); err != nil {
			return total, err
		}
	}
	return total, nil
}

// commitExportWatermarks stores the watermarks of the sources an export
// touched, once the batch it wrote has been handed over.
func commitExportWatermarks(dir string, touched map[string]bool, nextWatermarks map[string]int64, nextBoundary map[string]map[uint64]bool) error {
	unlock, err := lockDir(dir)
	if err != nil {
		return err
	}
	defer unlock()
	cur, err := readManifest(dir)
	if err != nil {
		return err
	}
	if cur.ExportWatermarks == nil {
		cur.ExportWatermarks = map[string]int64{}
	}
	if cur.ExportBoundary == nil {
		cur.ExportBoundary = map[string][]uint64{}
	}
	for source := range touched {
		wm := nextWatermarks[source]
		if wm < cur.ExportWatermarks[source] {
			continue
		}
		cur.ExportWatermarks[source] = wm
		// Above the cap the boundary set stops being cheap; drop it and
		// let the next push resend that instant — import dedupes.
		if set := nextBoundary[source]; len(set) > 0 && len(set) <= exportBoundaryCap {
			hashes := make([]uint64, 0, len(set))
			for h := range set {
				hashes = append(hashes, h)
			}
			slices.Sort(hashes)
			cur.ExportBoundary[source] = hashes
		} else {
			delete(cur.ExportBoundary, source)
		}
	}
	return writeManifestOnly(dir, cur)
}

// lastImportSkippedForgotten holds how many records the last Import dropped
// because this machine had forgotten the session they belong to.
var lastImportSkippedForgotten int

// ImportSkippedForgotten reports that count. It is worth saying out loud: a
// peer's copy of a forgotten session is exactly what someone expects deja to
// leave alone (#968).
func ImportSkippedForgotten() int { return lastImportSkippedForgotten }

// lastImportSkippedExcluded holds how many records the last Import dropped
// because the reader's own exclude list covers their project. Its three
// neighbours in the same loop are each counted and reported; this one was not,
// so a batch dropped in full read as an empty or already-synced one — the
// misread #1118 recorded for the drop beside it (#2666).
var lastImportSkippedExcluded int

// ImportSkippedExcluded reports that count.
func ImportSkippedExcluded() int { return lastImportSkippedExcluded }

// lastImportSkippedSwitched holds how many records the last Import dropped
// because a switch set on this machine leaves their kind out: a parse switch
// their role, DEJA_INCLUDE_SUBAGENTS=0 a sub-agent run. They are not entered
// in the ledger, so the same batch imported with the switch on brings them in.
var lastImportSkippedSwitched int

// ImportSkippedSwitched reports that count.
func ImportSkippedSwitched() int { return lastImportSkippedSwitched }

// lastImportSkippedHarness and lastImportSkippedIgnored hold how many records
// the last Import dropped because this machine excludes their harness, or
// because its ignore rule covers their project. Not entered in the ledger
// either, so lifting the rule and importing again brings them in (#5435).
var lastImportSkippedHarness, lastImportSkippedIgnored int

// ImportSkippedHarness reports the first count.
func ImportSkippedHarness() int { return lastImportSkippedHarness }

// ImportSkippedIgnored reports the second.
func ImportSkippedIgnored() int { return lastImportSkippedIgnored }

// lastImportSkippedIncomplete holds how many records the last Import dropped
// because they could not be attributed to a session — no harness or no
// session_id. deja's own exports always carry both; a hand-made or foreign
// batch may not, and dropping such a record silently made "imported 2 records"
// from a 3-record batch read as a complete transfer (#1118).
var lastImportSkippedIncomplete int

// ImportSkippedIncomplete reports that count.
func ImportSkippedIncomplete() int { return lastImportSkippedIncomplete }

// promotedStatesByNoteID is this machine's promoted-note states keyed by the
// note's session id, the name a record carries.
func promotedStatesByNoteID() map[string]sources.Lifecycle {
	out := map[string]sources.Lifecycle{}
	for src, lc := range sources.PromotedLifecycles() {
		out[sources.PromotedNoteID(src)] = lc
	}
	return out
}

// carriedNoteState is the state a record brought in its own fields. Another
// machine's file, so a state deja does not know is no state and a date that
// is not a day is dropped rather than shown.
func carriedNoteState(sr SyncRecord) (state, note, at string, ok bool) {
	state = string(sr.Lifecycle)
	if !sources.NoteStates[state] {
		return "", "", "", false
	}
	note, _ = redact.Text(strings.TrimSpace(string(sr.LifecycleNote)))
	// A full stamp is taken too, as its UTC day: a hand-made batch writes one.
	raw := strings.TrimSpace(string(sr.LifecycleAt))
	if d, err := time.Parse(lifecycleDay, raw); err == nil {
		at = d.Format(lifecycleDay)
	} else if d, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		at = d.UTC().Format(lifecycleDay)
	}
	return state, note, at, true
}

// noteBodyTail is what the loader appends to a promoted note's line: where it
// came from, the evidence day, and the tags. It is not part of the note.
var noteBodyTail = regexp.MustCompile(` \(from [^\s()]+, \d{4}-\d{2}-\d{2}\)( #\S+)*$`)

// noteStateFromText reads the state a promoted note's line carries. deja writes
// them as "[accepted] …" or "[rejected] did not hold", and that prefix is the
// only copy of the state that crosses a machine boundary (#975).
func noteStateFromText(text string) (state, note string, ok bool) {
	if !strings.HasPrefix(text, "[") {
		return "", "", false
	}
	end := strings.IndexByte(text, ']')
	if end <= 1 {
		return "", "", false
	}
	state = text[1:end]
	switch state {
	case "accepted", "rejected", "superseded", "stale":
	default:
		return "", "", false
	}
	// Without the tail the note reads as it does on the machine that filed
	// it, where it comes from notes.jsonl (#5053).
	note = noteBodyTail.ReplaceAllString(strings.TrimSpace(text[end+1:]), "")
	return state, note, true
}

// dayBucketKey identifies a note in a day bucket without the bucket id. Empty
// for anything else, so ordinary sessions keep the key they have always had.
func dayBucketKey(sr SyncRecord, textHash uint64) string {
	if sr.Harness != "deja" || !strings.HasPrefix(sr.SessionID, "deja-20") {
		return ""
	}
	return "deja-note-day:" + sr.Project + ":" + sr.Time.UTC().Format(time.RFC3339Nano) + ":" + sr.Role + ":" + strconv.FormatUint(textHash, 16)
}

// Import reads the sync batches in inDir into the index at dir and returns
// how many records it added.
func Import(dir, inDir string) (int, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	// A wrong path used to import nothing and exit 0 — including the case that
	// matters, a typo for a directory that is right there with records in it
	// (#678). An empty directory that exists is a different answer and stays a
	// quiet zero.
	fi, err := os.Stat(inDir)
	if err != nil {
		// Anything but a permission problem reads as "it is not there" to the
		// person who typed it, and windows says a great deal more than that: a
		// path holding control characters is an invalid name rather than a
		// missing one, so IsNotExist is false and the raw syscall error went
		// back — carrying the unsanitised path with it, which is the escape
		// sequence this wording exists to defuse.
		if !os.IsPermission(err) {
			return 0, fmt.Errorf("no such directory: %s", search.SafeLine(inDir))
		}
		return 0, fmt.Errorf("cannot read %s: %s", search.SafeLine(inDir), search.SafeLine(err.Error()))
	}
	if !fi.IsDir() {
		return 0, fmt.Errorf("%s is a file; sync import wants the directory a `sync export` wrote", search.SafeLine(inDir))
	}
	// Glob swallows a directory it cannot open, so a locked source imported
	// "0 records" — the same words an already-imported batch prints, and the
	// records were there the whole time (#1042).
	f, oerr := os.Open(inDir)
	if oerr != nil {
		if os.IsPermission(oerr) {
			return 0, fmt.Errorf("cannot read %s — permission denied; check that directory's permissions (on macOS, also Full Disk Access for your terminal)", search.SafeLine(inDir))
		}
		return 0, oerr
	}
	_ = f.Close()
	unlock, err := lockDir(dir)
	if err != nil {
		return 0, err
	}
	defer unlock()
	imp := newImporter(dir)
	if !HasManifest(dir) {
		if initErr := initEmptyIndex(dir); initErr != nil {
			return 0, initErr
		}
	}
	m, err := readManifest(dir)
	if err != nil {
		return 0, err
	}
	// An import appends to the record log and then stamps its size, so on a
	// log longer than the manifest committed it would commit a killed pass's
	// uncommitted tail along with the batch. That store is rebuilt first.
	if !recordsIntact(dir, m) {
		return 0, fmt.Errorf("%w: records.bin does not match the manifest — run `deja index` and import again", errCorruptIndex)
	}
	if m.ImportedRecords == nil {
		m.ImportedRecords = map[string]bool{}
	}
	paths, err := filepath.Glob(filepath.Join(inDir, "*.jsonl"))
	if err != nil {
		return 0, err
	}
	sort.Strings(paths)
	imp.m = &m
	for _, meta := range m.Sessions {
		if meta.OrigID == "" {
			imp.localSessions[meta.Harness+":"+meta.ID] = true
		}
	}
	var skipped []string
	defer func() { lastImportSkippedForgotten = imp.forgottenSkipped }()
	defer func() { lastImportSkippedOwn = imp.ownSkipped }()
	defer func() { lastImportSkippedIncomplete = imp.incompleteSkipped }()
	defer func() { lastImportSkippedExcluded = imp.excludedSkipped }()
	defer func() { lastImportSkippedSwitched = imp.switchedSkipped }()
	defer func() { lastImportSkippedHarness = imp.harnessSkipped }()
	defer func() { lastImportSkippedIgnored = imp.ignoredSkipped }()
	for _, p := range paths {
		if err := imp.readFile(p); err != nil {
			skipped = append(skipped, err.Error())
		}
	}
	if imp.added == 0 {
		if err := writeManifest(dir, m); err != nil {
			return 0, err
		}
		return 0, skippedError(skipped)
	}
	if err := appendImportedRecords(dir, &m, imp.recsByKey, imp.metas); err != nil {
		return imp.added, err
	}
	refreshImportSidecars(dir, m, imp.recsByKey)
	return imp.added, skippedError(skipped)
}

// refreshImportSidecars brings the tables mined from sessions up to the batch,
// as the append-only pass does for what it read. Without it a peer's commands
// were missing from the command tables and session facts until a rebuild, and
// a fix the batch confirmed stayed a candidate.
func refreshImportSidecars(dir string, m Manifest, recsByKey map[string][]Record) {
	if m.SearchOnly {
		return
	}
	var batch []model.Session
	for _, key := range sortedKeys(recsByKey) {
		meta := m.Sessions[key]
		s := model.Session{ID: meta.ID, Harness: meta.Harness, Project: meta.Project}
		for _, r := range recsByKey[key] {
			s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
		}
		batch = append(batch, s)
	}
	whole, rewhole := wholeSessionsForFixes(dir, m, batch)
	mergeFixes(dir, dir, nil, whole, rewhole)
	if carriesWork(batch) {
		buildCommandsFromIndex(dir)
		buildCommandFailsFromIndex(dir, readCommandFailState(dir), nil)
		buildSessionFactsFromIndex(dir)
	}
}

// importSnapshot is what one file had contributed before it was read, so a
// file that turns out to be unreadable can be taken back out.
type importSnapshot struct {
	counts map[string]int
	metas  map[string]SessionMeta
	at     map[string]time.Time
	rank   map[string]int
	// dedupe keys added while reading this file. Without taking these back a
	// refused file left its records out of the index and marked as already
	// imported, so the retry after fixing the file brought nothing and the
	// records were lost for good (#897).
	dedupe []string
	added  int
}

func restoreMap[V any](live, saved map[string]V) {
	for k := range live {
		if _, ok := saved[k]; !ok {
			delete(live, k)
		}
	}
	maps.Copy(live, saved)
}

// batchName is how a batch file is named on screen. The name comes from the
// machine that sent it — scp copies whatever matched over there — and these
// sentences reach a terminal through main, which prints an error as it is, so
// an escape byte in a name rewrote the line of whoever was watching a sync
// fail (#1847). The directory beside it has been sanitised since it was first
// printed; the file was not.
func batchName(path string) string {
	return search.SafePath(filepath.Base(path))
}

// skippedError reports the files an import could not read, after the ones it
// could are already in. Callers print the count they imported and this
// alongside it.
func skippedError(skipped []string) error {
	if len(skipped) == 0 {
		return nil
	}
	if len(skipped) == 1 {
		return fmt.Errorf("nothing was imported from 1 file: %s", search.SafeLine(skipped[0]))
	}
	return fmt.Errorf("nothing was imported from %d files: %s", len(skipped), search.SafeLine(strings.Join(skipped, "; ")))
}

func initEmptyIndex(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o700); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "records.bin"))
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Files stays empty on purpose. Snapshotting the local stores here recorded
	// every transcript as already seen while ingesting none of them, so the
	// next `deja index` found the manifest fresh and skipped the reader's own
	// sessions — import on a machine that had never indexed left the local
	// history invisible until a full rebuild. An empty file set makes the next
	// index treat every local file as new and ingest it, next to the imported
	// records this manifest carries.
	// An empty index holds nothing, so whatever the patterns are now, they are
	// applied to all of it. Leaving this blank would read as "written before
	// deja recorded this" and keep `deja index` quiet forever after (#1307).
	// Same reasoning for the tools this build could use: blank reads as "an
	// older deja wrote this", and a store skipped for a missing CLI would then
	// never be re-read once it was installed (#1760).
	m := Manifest{Version: version, KeptDated: true, Format: onDiskFormat, SearchOnly: searchOnlyFor(dir), Files: map[string]FileState{}, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), ExportWatermarks: map[string]int64{}, ImportedRecords: map[string]bool{},
		Compactions:        compactionsForRebuild(dir, readTombstones()),
		ExcludeFingerprint: sources.ExclusionFingerprint(),
		AliasFingerprint:   sources.ProjectAliasFingerprint(),
		ToolFingerprint:    mergedToolFingerprint(priorToolFingerprint(dir))}
	return writeManifest(dir, m)
}

// titleRank orders the roles a title may come from: a user turn beats the
// assistant's, and nothing else is a title at all.
func titleRank(role string) int {
	switch role {
	case "user":
		return 3
	case "assistant":
		return 2
	case roleToolOutput:
		// A session of nothing but tool output would otherwise arrive titleless
		// and list as a bare id on the receiving machine.
		return 1
	}
	return 0
}

func readSyncFile(path string, fn func(SyncRecord) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	// A last line the writer never terminated is the signature of a transfer cut
	// off mid-write, not a foreign record: deja wrote it, it just did not all
	// arrive. The file is still refused whole (#891), but the reason is a
	// truncation to re-fetch, not a batch deja mistrusts (#1117).
	torn := !fileEndsWithNewline(f)
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 8*1024*1024)
	line := 0
	have := s.Scan()
	for have {
		line++
		cur := append([]byte(nil), s.Bytes()...)
		next := s.Scan()
		var rec SyncRecord
		if err := json.Unmarshal(cur, &rec); err != nil {
			// The line number, because "invalid character 'o' in literal null"
			// on a file of thousands is not something anyone can act on. The
			// file is still refused whole: a half-imported transfer is worse
			// than one the reader can retry (#891).
			if !next && torn {
				return fmt.Errorf("%q looks truncated at line %d — the transfer may have been cut off; fetch the batch again", batchName(path), line)
			}
			return fmt.Errorf("%q line %d is not a record deja wrote: %w", batchName(path), line, err)
		}
		// Metadata from a batch is another machine's text: it lands in the
		// project label and the harness tag, which are rendered into result
		// lines a human and a model both read. A newline there forged a whole
		// extra entry — one with no "imported:" prefix, so it read as local
		// work (#1080). The role is rendered the same way (view.go writes it
		// into the preview) and is the same forgery vector, so it gets the same
		// flattening. Message text is redacted further down.
		rec.Project = sanitizeSyncField(rec.Project)
		rec.Harness = sanitizeSyncField(rec.Harness)
		rec.Role = sanitizeSyncField(rec.Role)
		// The origin is rendered into the same result lines, from the same
		// other machine's text, so it takes the same flattening.
		rec.Origin = syncName(sanitizeSyncField(string(rec.Origin)))
		if err := fn(rec); err != nil {
			return err
		}
		have = next
	}
	if err := s.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// fileEndsWithNewline reports whether the file's last byte is a newline. A batch
// deja wrote always ends in one; a missing final newline means the last line was
// never finished — a transfer cut off mid-write. An empty file counts as
// terminated: it has no torn tail.
func fileEndsWithNewline(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return true
	}
	buf := make([]byte, 1)
	if _, err := f.ReadAt(buf, fi.Size()-1); err != nil {
		return true
	}
	return buf[0] == '\n'
}

// mergeCappedU64 unions two hash lists, keeping the first list's order and
// dropping whatever does not fit under the cap.
func mergeCappedU64(a, b []uint64, limit int) []uint64 {
	if len(b) == 0 {
		return a
	}
	seen := make(map[uint64]bool, len(a)+len(b))
	out := make([]uint64, 0, len(a)+len(b))
	for _, v := range append(append([]uint64(nil), a...), b...) {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func appendImportedRecords(dir string, m *Manifest, recsByKey map[string][]Record, metas map[string]SessionMeta) error {
	rf, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	tbl := tablesFromManifest(*m)
	rw, err := newRecordWriter(rf, tbl)
	if err != nil {
		_ = rf.Close()
		return err
	}
	buckets := bucketPostings{}
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
	keys := make([]string, 0, len(recsByKey))
	for k := range recsByKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// One scan for the whole batch, not one per new session: nextSessionOrd
	// walks every manifest entry, so importing n sessions cost O(n²) and a
	// 100k batch took 306s against 1.9s for forgetting the same 100k (#1024).
	nextOrd := nextSessionOrd(m.Sessions)
	// A session whose first command arrives in this batch: how it settled can
	// come from what it said in an earlier batch, which this one does not
	// carry. Read once for all of them.
	firstRun := map[string]bool{}
	for _, key := range keys {
		if old := m.Sessions[key]; old.ID != "" && !old.RanCommand {
			for _, r := range recsByKey[key] {
				if r.Role == roleCommand {
					firstRun[key] = true
					break
				}
			}
		}
	}
	earlier := map[string][]model.Message{}
	if len(firstRun) > 0 {
		if err = eachRecordForKeys(filepath.Join(dir, "records.bin"), tbl, firstRun, func(r Record) {
			earlier[r.Key] = append(earlier[r.Key], model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
		}); err != nil {
			_ = rw.Close()
			return err
		}
	}
	for _, key := range keys {
		meta := metas[key]
		// The derived fields are folded the way local ingest folds an append,
		// over every record the batch carries, so an imported row reads as
		// the one a rebuild of this index writes for it. Counting only the
		// conversation turns, and never setting Settled, Opening or LastMsg,
		// ranked a peer's sessions on other lengths and left them without
		// their lineage and how they ended.
		ms := make([]model.Message, 0, len(recsByKey[key]))
		for _, r := range recsByKey[key] {
			ms = append(ms, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
		}
		batch := model.Session{Harness: meta.Harness, Messages: ms}
		old := m.Sessions[key]
		if old.ID != "" {
			meta.Ord = old.Ord
			// A batch from a build that does not send the kind keeps the
			// one an earlier batch named.
			if meta.Kind == "" {
				meta.Kind = old.Kind
			}
			if old.Started.Before(meta.Started) || meta.Started.IsZero() {
				meta.Started = old.Started
			}
			if old.Updated.After(meta.Updated) {
				meta.Updated = old.Updated
			}
			// A re-import carries only the records new since last time; the
			// ones behind the earlier fields are in the ledger and skipped, so
			// the batch folds into what the row already holds.
			meta.Counted, meta.Words, meta.LastMsg, meta.Opening = old.Counted, old.Words, old.LastMsg, old.Opening
			meta.Asked, meta.Hit, meta.GaveUp, meta.NoText = old.Asked, old.Hit, old.GaveUp, old.NoText
			meta.Touched, meta.TouchHits, meta.Settled, meta.RanCommand = old.Touched, old.TouchHits, old.Settled, old.RanCommand
			// The title is derived from this batch alone, and the turn that
			// named the row came in an earlier one.
			if titleRankOfRow(old.Title, old.AgentTitle) >= titleRankOfRow(meta.Title, meta.AgentTitle) {
				meta.Title, meta.AgentTitle = old.Title, old.AgentTitle
			}
		} else {
			meta.Counted, meta.Words, meta.LastMsg, meta.Opening = 0, 0, 0, 0
			meta.Asked, meta.Hit, meta.GaveUp = nil, nil, false
			meta.Touched, meta.TouchHits, meta.Settled, meta.RanCommand = nil, nil, "", false
			meta.NoText = !holdsText(batch)
			meta.Ord = nextOrd
			nextOrd++
		}
		extendDerived(&meta, ms)
		if firstRun[key] {
			meta.Settled = sessionSettled(model.Session{Harness: meta.Harness, Messages: append(earlier[key], ms...)})
		}
		m.Sessions[key] = meta
		for _, r := range recsByKey[key] {
			off, writeErr := rw.write(r)
			if writeErr != nil {
				_ = rw.Close()
				return writeErr
			}
			// Index an imported record exactly as ingestion would: the same
			// slice of the text earns postings (tokenizedPart), the same date
			// tokens ride along, and the tool bit survives the trip. Routing
			// through bare indexKeys left synced stores without date keys and
			// with unfiltered tool output — the route a record takes into an
			// index must not change what it indexes to.
			eachIndexKey(tokenizedPart(r.Role, r.Text), r.Time, func(tok string) {
				data := loadBucket(tok)
				data[tok] = append(data[tok], posting{Off: off, Sid: meta.Ord, Tool: isToolRole(r.Role)})
			})
		}
	}
	if closeErr := rw.Close(); closeErr != nil {
		return closeErr
	}
	m.RecordStrings = tbl.strs
	stale, err := addOverlays(filepath.Join(dir, "buckets"), filepath.Join(dir, "buckets"), buckets, m.DeadRecords)
	if err != nil {
		return err
	}
	m.BuiltAt = time.Now()
	m.bucketsLeaving = staleCount(stale)
	if err := writeManifest(dir, *m); err != nil {
		return err
	}
	dropStale(stale)
	m.bucketsLeaving = 0
	return nil
}

func shortHash(s string) string {
	h := sha1.Sum([]byte(s)) //nolint:gosec // a short id, not a security hash
	return strings.TrimLeft(hex.EncodeToString(h[:])[:12], "-")
}

// recordContentKey identifies one line of one session by what it says rather
// than by which copy of it this is.
func recordContentKey(harness, sessionID, role string, at time.Time, textHash uint64) string {
	return harness + ":" + sessionID + ":" + at.UTC().Format(time.RFC3339Nano) + ":" + role + ":" + strconv.FormatUint(textHash, 16)
}

// lastImportSkippedOwn holds how many records the last Import recognised as
// this machine's own copies coming home from a shared folder.
var lastImportSkippedOwn int

// ImportSkippedOwn reports that count.
func ImportSkippedOwn() int { return lastImportSkippedOwn }

// ImportedSessionID is the id an imported session is stored under.
func ImportedSessionID(harness, sessionID string) string {
	return "imported-" + shortHash(harness+":"+sessionID)
}

// syncFieldMax bounds one metadata field from a batch. Project names are short
// by construction; anything longer is not a name.
const syncFieldMax = 120

// sanitizeSyncField flattens a metadata field to a single printable line.
//
// Control characters (Cc) cover the newline that forged the extra result entry
// and the escape byte that recolours a terminal; format characters (Cf) cover
// U+202E, which reverses everything rendered after it, and the zero-width
// spaces that pad a label invisibly. Both become spaces rather than vanishing,
// so words on either side do not run together.
func sanitizeSyncField(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > syncFieldMax {
		return strings.TrimSpace(string(r[:syncFieldMax])) + "…"
	}
	return s
}
