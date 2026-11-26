package index

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

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
		// Repo memory travels with the repository; a copy over sync would
		// outlive the line someone deleted from it.
		if strings.HasPrefix(meta.Project, model.RepoOriginPrefix) || meta.From == model.OriginRepo {
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
		if !relayed && meta.InParentFile() {
			rec.Parent = syncName(meta.Parent)
		}
		if meta.IsSpawned() {
			rec.Kind = syncName(meta.Kind)
		}
		if meta.Harness == model.NotesHarness && model.IsPromotedNoteID(sessionID) {
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
