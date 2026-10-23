package index

import (
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// ManualEnv is the variable that stops automatic index updates. On a busy
// machine the refresh a search, a recall or a session start kicks off ran
// nearly all the time and slowed every answer behind it (#5385). With it set,
// every Ensure answers from the index on disk and only `deja index` writes.
const ManualEnv = "DEJA_NO_AUTO_INDEX"

// explicitBuilds counts the commands in flight that the user ran to write
// the index.
var explicitBuilds atomic.Int32

// AllowBuild marks this process as one asked to write the index, so Ensure
// builds whatever the switch says. It returns the undo.
func AllowBuild() func() {
	explicitBuilds.Add(1)
	var once sync.Once
	return func() { once.Do(func() { explicitBuilds.Add(-1) }) }
}

// Manual reports whether automatic index updates are off:
// DEJA_NO_AUTO_INDEX=1, or "auto_index": false in policy.json. The variable
// wins either way, so DEJA_NO_AUTO_INDEX=0 turns updates back on for one run.
func Manual() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(ManualEnv))) {
	case "":
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
	return policy.AutoIndexOff()
}

// autoUpdatesOff is Manual for a caller that would write the index on its
// own account.
func autoUpdatesOff() bool {
	return explicitBuilds.Load() == 0 && Manual()
}

// ManualStopError is the error an Ensure returns when automatic updates are off
// and the index on disk cannot answer. Its text names the command to run.
type ManualStopError struct{ msg string }

func (e *ManualStopError) Error() string { return e.msg }

// IsManualStop reports an error from a build the switch refused.
func IsManualStop(err error) bool {
	var m *ManualStopError
	return errors.As(err, &m)
}

// manualCheck is every Ensure with automatic updates off: nothing is written,
// and the index on disk either answers or the error says how to build it.
func manualCheck(dir string) error {
	if !HasManifest(dir) {
		return &ManualStopError{"no index yet, and automatic indexing is off — build it with: deja index"}
	}
	m, err := readManifestCached(dir)
	if err != nil || !recordsReadable(dir, m) {
		return &ManualStopError{"the index could not be read, and automatic indexing is off — rebuild it with: deja index --rebuild"}
	}
	if mustRebuildBeforeAnswering(m, version) {
		return &ManualStopError{"the index was written by an older deja and must be rebuilt, and automatic indexing is off — run: deja index"}
	}
	return nil
}

// LastUpdated is when the index on disk last read this machine's stores, or
// zero when there is none.
func LastUpdated(dir string) time.Time {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return time.Time{}
	}
	if !m.SourcesReadAt.IsZero() {
		return m.SourcesReadAt
	}
	return m.BuiltAt
}

// ManualStatus is what an Ensure would answer with automatic updates off:
// nil when the index on disk can answer, otherwise the error naming the
// command that builds it.
func ManualStatus(dir string) error {
	if dir == "" {
		dir = DefaultDir()
	}
	return manualCheck(dir)
}
