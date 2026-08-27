package sources

import "sync"

// Ingest diagnostics are a side channel, not a parser API change: scanners and
// file loaders report what they skipped, the index aggregates it per harness
// and persists it, and doctor makes it visible. "Not found because it never
// happened" and "not found because ingestion skipped it" must not look
// identical in a memory tool.
var diagMu sync.Mutex
var diagMalformed = map[string]int{}
var diagFailed = map[string]string{}

func diagMalformedLine(path string) {
	diagMu.Lock()
	diagMalformed[path]++
	diagMu.Unlock()
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

// DiagSnapshot returns and clears the counters accumulated since the last
// snapshot: malformed JSONL lines per file, and files whose parse failed
// outright with the error text.
func DiagSnapshot() (malformed map[string]int, failed map[string]string) {
	diagMu.Lock()
	defer diagMu.Unlock()
	malformed, failed = diagMalformed, diagFailed
	diagMalformed = map[string]int{}
	diagFailed = map[string]string{}
	return malformed, failed
}
