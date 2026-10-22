package sources

import (
	"errors"
	"io/fs"
	"maps"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Ingest diagnostics are a side channel, not a parser API change: scanners and
// file loaders report what they skipped, the index aggregates it per harness
// and persists it, and doctor makes it visible. "Not found because it never
// happened" and "not found because ingestion skipped it" must not look
// identical in a memory tool.
var diagMu sync.Mutex
var diagMalformed = map[string]int{}
var diagFailed = map[string]string{}
var diagReasons = map[string]string{}
var diagRecords = map[string]map[string]string{}
var diagZstdMissing = map[string]bool{}

// diagStalled is the part of diagFailed given up for making no progress, with
// how long it sat: the run says so in those words rather than "could not be
// read", which reads like a permission problem.
var diagStalled = map[string]time.Duration{}

// diagWalkFailed is the part of diagFailed the walk recorded: directories it
// could not list and transcripts whose mode refuses a read. A later walk in
// the same process stands in for an earlier one, so BeginWalk drops these and
// the new walk records whatever still fails.
var diagWalkFailed = map[string]string{}

func diagMalformedLine(path string) {
	if isAbandoned(path) {
		return
	}
	diagMu.Lock()
	diagMalformed[path]++
	diagMu.Unlock()
}

// diagUnusableRecord counts one record a store holds and deja could not use,
// with why. A JSONL line explains itself by its position; a row in a database
// does not, and "unknown data_type brotli" is the difference between a bad row
// and a format deja has not learned yet (#4341).
//
// The id is kept too: a store read from its watermark hands back only the rows
// changed since, so the index carries the ids of the rows it skipped before
// rather than forgetting them on the next pass.
func diagUnusableRecord(path, id, reason string) {
	if isAbandoned(path) {
		return
	}
	diagMu.Lock()
	diagMalformed[path]++
	diagReasons[path] = reason
	if diagRecords[path] == nil {
		diagRecords[path] = map[string]string{}
	}
	diagRecords[path][id] = reason
	diagMu.Unlock()
}

// SkippedNoun names the unit a harness's skip count is in. The JSONL readers
// skip lines; Zed skips threads, rows of one database, and "2 lines skipped"
// sent the reader looking for lines that do not exist (#4341).
func SkippedNoun(harness string) string {
	if harness == "zed" {
		return "thread"
	}
	return "line"
}

func diagFileError(path string, err error) {
	if err == nil {
		return
	}
	if _, stalled := err.(*StalledRead); !stalled && isAbandoned(path) {
		return
	}
	diagMu.Lock()
	diagFailed[path] = err.Error()
	var st *StalledRead
	if errors.As(err, &st) {
		diagStalled[path] = st.Idle
	}
	diagMu.Unlock()
}

// DiagStalledPaths returns the paths given up for making no progress so far,
// with the budget each ran out of, without clearing them.
func DiagStalledPaths() map[string]time.Duration {
	diagMu.Lock()
	defer diagMu.Unlock()
	return maps.Clone(diagStalled)
}

// diagWalkError is diagFileError for a path the store walk itself could not
// read.
func diagWalkError(path string, err error) {
	diagMu.Lock()
	diagFailed[path] = err.Error()
	diagWalkFailed[path] = err.Error()
	diagMu.Unlock()
}

// NoteWalkError records a path the walk could not read for another reason.
func NoteWalkError(path string, err error) { diagWalkError(path, err) }

// NoteUnreadable records a transcript the walk found but may not read.
func NoteUnreadable(path string) {
	diagWalkError(path, &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission})
}

// BeginWalk forgets what an earlier walk in this process could not read.
func BeginWalk() {
	diagMu.Lock()
	for p := range diagWalkFailed {
		delete(diagFailed, p)
	}
	diagWalkFailed = map[string]string{}
	diagMu.Unlock()
}

// WalkFailures returns the paths the walk since BeginWalk could not read.
func WalkFailures() map[string]string {
	diagMu.Lock()
	defer diagMu.Unlock()
	return maps.Clone(diagWalkFailed)
}

// diagNoZstd records a transcript left unread because the zstd CLI is missing.
// Unlike a file that would not open, nothing about it changes until zstd is
// installed, so the index can hold it as waiting rather than read it as
// changed on every pass (#5105).
func diagNoZstd(path string) {
	diagMu.Lock()
	diagZstdMissing[path] = true
	diagMu.Unlock()
}

// DiagNoZstdPaths returns the transcripts left unread for want of zstd so far,
// without clearing them.
func DiagNoZstdPaths() map[string]bool {
	diagMu.Lock()
	defer diagMu.Unlock()
	return maps.Clone(diagZstdMissing)
}

// DiagState is what DiagSnapshot clears, held aside by DiagSave.
type DiagState struct {
	malformed map[string]int
	failed    map[string]string
	reasons   map[string]string
	records   map[string]map[string]string
	noZstd    map[string]bool
	stalled   map[string]time.Duration
}

// DiagSave copies everything a pass has reported so far, for a manifest write
// that drains it before the pass that read it is done: the early publish of a
// first build writes a slice of the corpus, and the full build after it folds
// the same reports (#5110).
func DiagSave() DiagState {
	diagMu.Lock()
	defer diagMu.Unlock()
	records := make(map[string]map[string]string, len(diagRecords))
	for p, recs := range diagRecords {
		records[p] = maps.Clone(recs)
	}
	return DiagState{
		malformed: maps.Clone(diagMalformed),
		failed:    maps.Clone(diagFailed),
		reasons:   maps.Clone(diagReasons),
		records:   records,
		noZstd:    maps.Clone(diagZstdMissing),
		stalled:   maps.Clone(diagStalled),
	}
}

// DiagRestore puts back what DiagSave copied, in place of whatever is there.
func DiagRestore(s DiagState) {
	diagMu.Lock()
	defer diagMu.Unlock()
	clear(diagMalformed)
	maps.Copy(diagMalformed, s.malformed)
	clear(diagFailed)
	maps.Copy(diagFailed, s.failed)
	clear(diagReasons)
	maps.Copy(diagReasons, s.reasons)
	clear(diagRecords)
	maps.Copy(diagRecords, s.records)
	clear(diagZstdMissing)
	maps.Copy(diagZstdMissing, s.noZstd)
	clear(diagStalled)
	maps.Copy(diagStalled, s.stalled)
}

// DiagMalformedCounts returns the malformed-line counts accumulated so far
// without clearing them. The index narrates each store as it lands, which is
// before the manifest fold that drains these counters (#1993).
func DiagMalformedCounts() map[string]int {
	diagMu.Lock()
	defer diagMu.Unlock()
	out := make(map[string]int, len(diagMalformed))
	for p, n := range diagMalformed {
		out[p] = n
	}
	return out
}

// DiagFailedPaths returns the paths that could not be read so far, without
// clearing them — the sibling of DiagMalformedCounts, and for the same reason:
// the index narrates each store as it lands, before the manifest fold drains
// these counters (#1993).
func DiagFailedPaths() map[string]string {
	diagMu.Lock()
	defer diagMu.Unlock()
	out := make(map[string]string, len(diagFailed))
	for p, msg := range diagFailed {
		out[p] = msg
	}
	return out
}

// DiagReasons returns why the last unusable record of each store was skipped,
// without clearing them. Only stores whose records carry no line of their own
// to point at report one.
func DiagReasons() map[string]string {
	diagMu.Lock()
	defer diagMu.Unlock()
	out := make(map[string]string, len(diagReasons))
	for p, r := range diagReasons {
		out[p] = r
	}
	return out
}

// DiagUnusableRecords returns the ids of the records each store could not use,
// with why, without clearing them.
func DiagUnusableRecords() map[string]map[string]string {
	diagMu.Lock()
	defer diagMu.Unlock()
	out := make(map[string]map[string]string, len(diagRecords))
	for p, recs := range diagRecords {
		out[p] = maps.Clone(recs)
	}
	return out
}

// DiagBeginPass clears the counters for a pass about to parse, keeping what the
// walk before it could not read: that walk is the one this pass starts from.
func DiagBeginPass() {
	DiagSnapshot()
	diagMu.Lock()
	maps.Copy(diagFailed, diagWalkFailed)
	diagMu.Unlock()
}

// DiagSnapshot returns and clears the counters accumulated since the last
// snapshot: malformed JSONL lines per file, and files whose parse failed
// outright with the error text. What the walk could not read stays known to
// WalkFailures until the next BeginWalk: a pass that writes twice still starts
// from that walk.
func DiagSnapshot() (malformed map[string]int, failed map[string]string) {
	diagMu.Lock()
	defer diagMu.Unlock()
	malformed, failed = diagMalformed, diagFailed
	diagMalformed = map[string]int{}
	diagFailed = map[string]string{}
	diagReasons = map[string]string{}
	diagRecords = map[string]map[string]string{}
	diagZstdMissing = map[string]bool{}
	diagStalled = map[string]time.Duration{}
	return malformed, failed
}

// storeRead hands back what a loader read from one store and records the
// store as unread when the read failed. The loaders a full build runs dropped
// that error, so a database deja could not read, or gave up on, left the
// index without its sessions and the run exited 0 with nothing said (#5517).
// A store that is not there is not unread input.
func storeRead(path string, ss []model.Session, err error) []model.Session {
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		diagFileError(path, err)
	}
	return ss
}

// dbRead is storeRead for a store read through sqlite3. While sqlite3 is
// missing or broken SkipReason names the store on every pass, and the same
// store counted as unread each time made every pass exit 3 over a tool.
func dbRead(path string, ss []model.Session, err error) []model.Session {
	if err != nil && SQLite3Problem() != "" {
		return ss
	}
	return storeRead(path, ss, err)
}
