package sources

import (
	"maps"
	"sync"
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

func diagMalformedLine(path string) {
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
	diagMu.Lock()
	diagFailed[path] = err.Error()
	diagMu.Unlock()
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

// DiagSnapshot returns and clears the counters accumulated since the last
// snapshot: malformed JSONL lines per file, and files whose parse failed
// outright with the error text.
func DiagSnapshot() (malformed map[string]int, failed map[string]string) {
	diagMu.Lock()
	defer diagMu.Unlock()
	malformed, failed = diagMalformed, diagFailed
	diagMalformed = map[string]int{}
	diagFailed = map[string]string{}
	diagReasons = map[string]string{}
	diagRecords = map[string]map[string]string{}
	return malformed, failed
}
