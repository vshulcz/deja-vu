package index

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/sources"
)

type SyncRecord struct {
	Harness   string    `json:"harness"`
	SessionID string    `json:"session_id"`
	Project   string    `json:"project"`
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	Time      time.Time `json:"time"`
}

// Export writes records newer than the watermarks for an unnamed peer (a
// manual `deja sync export`). ExportFull ignores watermarks so a fresh
// machine can receive the whole history even after earlier batch dirs are
// gone; import-side dedupe makes it safe.
func Export(dir, outDir string) (int, error) {
	return exportRecords(dir, outDir, "", false)
}

func ExportFull(dir, outDir string) (int, error) {
	return exportRecords(dir, outDir, "", true)
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

func exportRecords(dir, outDir, peer string, full bool) (int, error) {
	n, commit, err := exportRecordsDeferred(dir, outDir, peer, full)
	if err != nil {
		return n, err
	}
	return n, commit()
}

func exportRecordsDeferred(dir, outDir, peer string, full bool) (int, func() error, error) {
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
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		// The import side of the same mistake is worded; this one handed back
		// `mkdir /…/file: not a directory` (#1112).
		if fi, statErr := os.Stat(outDir); statErr == nil && !fi.IsDir() {
			return 0, nil, fmt.Errorf("%s is a file; sync export wants a directory to write the batch into", outDir)
		}
		return 0, nil, err
	}
	if m.ExportWatermarks == nil {
		m.ExportWatermarks = map[string]int64{}
	}
	bySource := map[string][]SyncRecord{}
	nextWatermarks := map[string]int64{}
	for k, v := range m.ExportWatermarks {
		nextWatermarks[k] = v
	}
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
	err = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		if r.SourcePath == syncImportPath {
			return
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
		text, _ := redact.Text(r.Text)
		rec := SyncRecord{Harness: meta.Harness, SessionID: meta.ID, Project: meta.Project, Role: r.Role, Text: text, Time: r.Time}
		bySource[source] = append(bySource[source], rec)
		touched[wk] = true
		tn := r.Time.UnixNano()
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
	})
	if err != nil {
		return 0, nil, err
	}
	total := 0
	for source, recs := range bySource {
		if len(recs) == 0 {
			continue
		}
		name := fmt.Sprintf("deja-sync-%s-%d.jsonl", shortHash(source), time.Now().UnixNano())
		f, err := os.Create(filepath.Join(outDir, name))
		if err != nil {
			return total, nil, err
		}
		enc := json.NewEncoder(f)
		for _, rec := range recs {
			if err := enc.Encode(rec); err != nil {
				_ = f.Close()
				return total, nil, err
			}
			total++
		}
		if err := f.Close(); err != nil {
			return total, nil, err
		}
	}
	commit := func() error {
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
				sort.Slice(hashes, func(i, j int) bool { return hashes[i] < hashes[j] })
				cur.ExportBoundary[source] = hashes
			} else {
				delete(cur.ExportBoundary, source)
			}
		}
		return writeManifestOnly(dir, cur)
	}
	return total, commit, nil
}

// lastImportSkippedForgotten holds how many records the last Import dropped
// because this machine had forgotten the session they belong to.
var lastImportSkippedForgotten int

// ImportSkippedForgotten reports that count. It is worth saying out loud: a
// peer's copy of a forgotten session is exactly what someone expects deja to
// leave alone (#968).
func ImportSkippedForgotten() int { return lastImportSkippedForgotten }

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
	return state, strings.TrimSpace(text[end+1:]), true
}

// dayBucketKey identifies a note in a day bucket without the bucket id. Empty
// for anything else, so ordinary sessions keep the key they have always had.
func dayBucketKey(sr SyncRecord, textHash uint64) string {
	if sr.Harness != "deja" || !strings.HasPrefix(sr.SessionID, "deja-20") {
		return ""
	}
	return "deja-note-day:" + sr.Project + ":" + sr.Time.UTC().Format(time.RFC3339Nano) + ":" + sr.Role + ":" + strconv.FormatUint(textHash, 16)
}

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
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("no such directory: %s", inDir)
		}
		return 0, err
	}
	if !fi.IsDir() {
		return 0, fmt.Errorf("%s is a file; sync import wants the directory a `sync export` wrote", inDir)
	}
	// Glob swallows a directory it cannot open, so a locked source imported
	// "0 records" — the same words an already-imported batch prints, and the
	// records were there the whole time (#1042).
	if f, oerr := os.Open(inDir); oerr != nil {
		if os.IsPermission(oerr) {
			return 0, fmt.Errorf("cannot read %s — permission denied; check that directory's permissions (on macOS, also Full Disk Access for your terminal)", inDir)
		}
		return 0, oerr
	} else {
		_ = f.Close()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return 0, err
	}
	defer unlock()
	dead := readTombstones()
	// Content tombstones are recorded against the session they came from, so
	// they are read back once here rather than scanned per record.
	deadContent := map[string]bool{}
	for k := range dead {
		if _, content, ok := strings.Cut(k, "\x00"); ok {
			deadContent[content] = true
		}
	}
	ex := sources.NewExcluder()
	if !HasManifest(dir) {
		if err := initEmptyIndex(dir); err != nil {
			return 0, err
		}
	}
	m, err := readManifest(dir)
	if err != nil {
		return 0, err
	}
	if m.ImportedRecords == nil {
		m.ImportedRecords = map[string]bool{}
	}
	paths, err := filepath.Glob(filepath.Join(inDir, "*.jsonl"))
	if err != nil {
		return 0, err
	}
	sort.Strings(paths)
	recsByKey := map[string][]Record{}
	metas := map[string]SessionMeta{}
	titleAt := map[string]time.Time{} // key -> time of the turn the title came from
	titleRankOf := map[string]int{}   // key -> how good that turn was as a title
	// A shared folder is both an outbox and an inbox: the machine that wrote a
	// batch also runs `sync import` on that directory, and its own records came
	// back as a second, "imported" copy of every session and note it had
	// (#987). Recognised by content, so a peer's genuinely new lines in a
	// session this machine also holds still land.
	localContent := map[string]bool{}
	localSessions := map[string]bool{}
	for _, meta := range m.Sessions {
		if meta.OrigID == "" {
			localSessions[meta.Harness+":"+meta.ID] = true
		}
	}
	localContentLoaded := false
	loadLocalContent := func() {
		if localContentLoaded {
			return
		}
		localContentLoaded = true
		for _, r := range readRecordsForForget(dir) {
			meta, ok := m.Sessions[r.Record.Key]
			if !ok || meta.OrigID != "" {
				continue
			}
			th := fnv.New64a()
			_, _ = th.Write([]byte(r.Record.Text))
			localContent[recordContentKey(meta.Harness, meta.ID, r.Record.Role, r.Record.Time, th.Sum64())] = true
		}
	}
	added := 0
	var skipped []string
	ownSkipped := 0
	forgottenSkipped := 0
	defer func() { lastImportSkippedForgotten = forgottenSkipped }()
	defer func() { lastImportSkippedOwn = ownSkipped }()
	for _, p := range paths {
		// A file is imported whole or not at all, so what it contributed is
		// remembered before it is read and rolled back if it turns out to be
		// truncated: half a transfer is the one outcome nobody can reason
		// about afterwards (#891).
		before := importSnapshot{
			counts: make(map[string]int, len(recsByKey)),
			metas:  make(map[string]SessionMeta, len(metas)),
			at:     make(map[string]time.Time, len(titleAt)),
			rank:   make(map[string]int, len(titleRankOf)),
			added:  added,
		}
		fileDedupe := &before.dedupe
		for k, v := range recsByKey {
			before.counts[k] = len(v)
		}
		for k, v := range metas {
			before.metas[k] = v
		}
		for k, v := range titleAt {
			before.at[k] = v
		}
		for k, v := range titleRankOf {
			before.rank[k] = v
		}
		if err := readSyncFile(p, func(sr SyncRecord) error {
			origID := sr.SessionID
			if sr.Harness == "" || origID == "" {
				return nil
			}
			// The same rule the local ingest applies: a message that strips to
			// empty is not a message, and a session with nothing but those is
			// not a session. deja's own exports never carry them, but a
			// hand-made batch or one from a build older than #868 does — and
			// they came back in as blank lines in `last` and rows in every
			// counter (#896).
			if strings.TrimSpace(sr.Text) == "" {
				return nil
			}
			importID := ImportedSessionID(sr.Harness, origID)
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
				forgottenSkipped++
				return nil
			}
			// Key includes role and a text hash: two messages can legally share
			// a timestamp (aider stamps a whole session with its start time).
			// The legacy time-only key is still honored so batches imported by
			// older versions stay idempotent.
			legacy := sr.Harness + ":" + origID + ":" + sr.Time.UTC().Format(time.RFC3339Nano)
			th := fnv.New64a()
			_, _ = th.Write([]byte(sr.Text))
			dedupe := legacy + ":" + sr.Role + ":" + strconv.FormatUint(th.Sum64(), 16)
			// A day bucket's id is the sending machine's rendering of a date,
			// not the identity of what it holds: after that machine changes
			// zone the same note arrives under a new bucket and every peer
			// grew a second copy (#977). Key those on what does not move.
			if localSessions[sr.Harness+":"+sr.SessionID] {
				loadLocalContent()
				if localContent[recordContentKey(sr.Harness, sr.SessionID, sr.Role, sr.Time, th.Sum64())] {
					ownSkipped++
					return nil
				}
			}
			if bucket := dayBucketKey(sr, th.Sum64()); bucket != "" {
				if m.ImportedRecords[bucket] {
					return nil
				}
				// Forgotten as text: the sending machine renders the day its
				// own way, so a note this machine dropped arrives under a
				// bucket id no tombstone here has ever named (#985).
				if deadContent[bucket] {
					forgottenSkipped++
					return nil
				}
				dedupe = bucket
			}
			if m.ImportedRecords[dedupe] || m.ImportedRecords[legacy] {
				return nil
			}
			// The exclude list keeps a project out of this machine's memory;
			// a sync from another machine must not put it back.
			if ex.Match(sr.Project) {
				return nil
			}
			key := sr.Harness + ":" + importID
			text, _ := redact.Text(sr.Text)
			recsByKey[key] = append(recsByKey[key], Record{Key: key, Role: sr.Role, Text: text, Time: sr.Time, SourcePath: syncImportPath})
			meta := metas[key]
			if meta.ID == "" {
				meta = SessionMeta{ID: importID, Harness: sr.Harness, Project: "imported:" + sr.Project, Path: syncImportPath, OrigID: origID}
			}
			if meta.Started.IsZero() || (!sr.Time.IsZero() && sr.Time.Before(meta.Started)) {
				meta.Started = sr.Time
			}
			if sr.Time.After(meta.Updated) {
				meta.Updated = sr.Time
			}
			// The title does not travel in the record stream, and without it the
			// receiving machine's `last` is a column of hashes — the content
			// arrived, the one line that makes the list readable did not (#670).
			// Derive it the way ingest does, from the earliest user turn that is
			// speech rather than harness plumbing.
			// A user turn always wins; the assistant's first sentence fills in
			// when there is none, the same order ingest uses (#692).
			if rank := titleRank(sr.Role); rank > 0 && titleWorthy(strings.TrimSpace(text)) {
				better := rank > titleRankOf[key]
				sameRank := rank == titleRankOf[key] && !sr.Time.IsZero() && sr.Time.Before(titleAt[key])
				if _, seen := titleAt[key]; !seen || better || sameRank {
					meta.Title = truncateTitle(strings.TrimSpace(text), 60)
					// The listing marks a title that is the agent's own words
					// rather than the reader's question (#1100); the import
					// path derived the title the same way and lost the mark.
					meta.AgentTitle = titleRank("user") != rank
					titleAt[key] = sr.Time
					titleRankOf[key] = rank
				}
			}
			// A promoted note carries its state in the text: "[rejected] …".
			// The states themselves live in the other machine's notes.jsonl,
			// which never travels, so without reading them here a decision
			// retracted there reads as accepted on this machine (#975).
			if strings.HasPrefix(origID, "deja-note-") {
				if st, note, ok := noteStateFromText(sr.Text); ok {
					meta.Lifecycle, meta.LifecycleNote = st, note
					if !sr.Time.IsZero() {
						meta.LifecycleAt = sr.Time.Format("2006-01-02")
					}
				}
			}
			metas[key] = meta
			m.ImportedRecords[dedupe] = true
			*fileDedupe = append(*fileDedupe, dedupe)
			added++
			return nil
		}); err != nil {
			// One unreadable file used to stop the whole directory, so a valid
			// export sitting beside a truncated one never arrived and the
			// reader was told only about a stray character (#891). The file is
			// still refused whole; the others are not held hostage to it.
			skipped = append(skipped, err.Error())
			for k := range recsByKey {
				n := before.counts[k]
				if n == 0 {
					delete(recsByKey, k)
					continue
				}
				recsByKey[k] = recsByKey[k][:n]
			}
			for _, k := range before.dedupe {
				delete(m.ImportedRecords, k)
			}
			restoreMap(metas, before.metas)
			restoreMap(titleAt, before.at)
			restoreMap(titleRankOf, before.rank)
			added = before.added
			continue
		}
	}
	if added == 0 {
		if err := writeManifest(dir, m); err != nil {
			return 0, err
		}
		return 0, skippedError(skipped)
	}
	if err := appendImportedRecords(dir, &m, recsByKey, metas); err != nil {
		return added, err
	}
	return added, skippedError(skipped)
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
	for k, v := range saved {
		live[k] = v
	}
}

// skippedError reports the files an import could not read, after the ones it
// could are already in. Callers print the count they imported and this
// alongside it.
func skippedError(skipped []string) error {
	if len(skipped) == 0 {
		return nil
	}
	if len(skipped) == 1 {
		return fmt.Errorf("nothing was imported from 1 file: %s", skipped[0])
	}
	return fmt.Errorf("nothing was imported from %d files: %s", len(skipped), strings.Join(skipped, "; "))
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
	m := Manifest{Version: version, Files: currentFiles(""), Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), ExportWatermarks: map[string]int64{}, ImportedRecords: map[string]bool{}}
	return writeManifest(dir, m)
}

// titleRank orders the roles a title may come from: a user turn beats the
// assistant's, and nothing else is a title at all.
func titleRank(role string) int {
	switch role {
	case "user":
		return 2
	case "assistant":
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
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 8*1024*1024)
	line := 0
	for s.Scan() {
		line++
		var rec SyncRecord
		if err := json.Unmarshal(s.Bytes(), &rec); err != nil {
			// The line number, because "invalid character 'o' in literal null"
			// on a file of thousands is not something anyone can act on. The
			// file is still refused whole: a half-imported transfer is worse
			// than one the reader can retry (#891).
			return fmt.Errorf("%s line %d is not a record deja wrote: %w", filepath.Base(path), line, err)
		}
		// Metadata from a batch is another machine's text: it lands in the
		// project label and the harness tag, which are rendered into result
		// lines a human and a model both read. A newline there forged a whole
		// extra entry — one with no "imported:" prefix, so it read as local
		// work (#1080). Message text is redacted further down; these fields
		// were never touched.
		rec.Project = sanitizeSyncField(rec.Project, syncFieldMax)
		rec.Harness = sanitizeSyncField(rec.Harness, syncFieldMax)
		if err := fn(rec); err != nil {
			return err
		}
	}
	if err := s.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
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
	loadBucket := func(tok string) (map[string][]posting, error) {
		b := bucket(tok)
		if data, ok := buckets[b]; ok {
			return data, nil
		}
		p := filepath.Join(dir, "buckets", b+".bin")
		data, err := readBucket(p)
		if os.IsNotExist(err) {
			data = map[string][]posting{}
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		buckets[b] = data
		return data, nil
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
	for _, key := range keys {
		meta := metas[key]
		old := m.Sessions[key]
		if old.ID != "" {
			meta.Ord = old.Ord
			if old.Started.Before(meta.Started) || meta.Started.IsZero() {
				meta.Started = old.Started
			}
			if old.Updated.After(meta.Updated) {
				meta.Updated = old.Updated
			}
		} else {
			meta.Ord = nextOrd
			nextOrd++
		}
		m.Sessions[key] = meta
		for _, r := range recsByKey[key] {
			off, err := rw.write(r)
			if err != nil {
				_ = rw.Close()
				return err
			}
			for _, tok := range indexKeys(r.Text) {
				data, err := loadBucket(tok)
				if err != nil {
					_ = rw.Close()
					return err
				}
				data[tok] = append(data[tok], posting{Off: off, Sid: meta.Ord})
			}
		}
	}
	if err := rw.Close(); err != nil {
		return err
	}
	m.RecordStrings = tbl.strs
	if err := writeBucketsConcurrent(filepath.Join(dir, "buckets"), buckets); err != nil {
		return err
	}
	m.BuiltAt = time.Now()
	return writeManifest(dir, *m)
}

func shortHash(s string) string {
	h := sha1.Sum([]byte(s))
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
func sanitizeSyncField(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return strings.TrimSpace(string(r[:max])) + "…"
	}
	return s
}
