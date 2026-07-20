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
