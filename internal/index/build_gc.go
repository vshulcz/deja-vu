package index

import (
	"os"
	"runtime/debug"
	"sync"
)

// buildGCPercent is the collector's target while a full build runs. A full
// build holds every session's text from the read to the last sidecar, about
// 3 GB on a 5 GB store, and at Go's default of 100 the heap grows to twice
// that before a collection: 8.5 GB of RSS on a 16 GB machine. The text is
// pointer-free, so collecting more often costs little.
const buildGCPercent = 25

var (
	buildGCMu   sync.Mutex
	buildGCHeld int
	buildGCWas  int
)

// tightenGCForBuild sets buildGCPercent until the returned func is called,
// unless the user tuned the collector themselves. Builds that overlap in one
// process share it, and the last one out puts the old value back.
func tightenGCForBuild() func() {
	if os.Getenv("GOGC") != "" || os.Getenv("GOMEMLIMIT") != "" {
		return func() {}
	}
	buildGCMu.Lock()
	if buildGCHeld == 0 {
		buildGCWas = debug.SetGCPercent(buildGCPercent)
	}
	buildGCHeld++
	buildGCMu.Unlock()
	return func() {
		buildGCMu.Lock()
		defer buildGCMu.Unlock()
		if buildGCHeld--; buildGCHeld == 0 {
			debug.SetGCPercent(buildGCWas)
		}
	}
}
