package index

import "sync"

// Progress reports what a build is doing while it does it. The first build on
// a real corpus takes about ten seconds, and until now those seconds were
// silent: the per-harness lines all appeared at once after parsing finished,
// because the harnesses are parsed concurrently and only narrated at the end.
//
// Implementations must be safe for concurrent use — harness parsing runs in
// parallel and reports as each one lands.
type Progress interface {
	// Phase starts a named stage. total is the unit count the stage will
	// work through, or 0 when it cannot be known in advance.
	Phase(name string, total int)
	// Advance adds to the current stage's completed count.
	Advance(units int)
	// Harness reports a store that finished parsing.
	Harness(name string, sessions, messages int)
}

// progressSink is package state for the same reason SuppressHarnessNarration
// and LastBuild are: Ensure is called from about thirty places and threading a
// reporter through all of them would be a worse trade than this.
var (
	progressMu   sync.Mutex
	progressSink Progress
)

// SetProgress installs the reporter for builds started after this call.
// Passing nil restores the plain line output.
func SetProgress(p Progress) {
	progressMu.Lock()
	progressSink = p
	progressMu.Unlock()
}

func reportPhase(name string, total int) {
	progressMu.Lock()
	p := progressSink
	progressMu.Unlock()
	if p != nil {
		p.Phase(name, total)
	}
}

func reportAdvance(units int) {
	progressMu.Lock()
	p := progressSink
	progressMu.Unlock()
	if p != nil {
		p.Advance(units)
	}
}

func reportHarness(name string, sessions, messages int) {
	progressMu.Lock()
	p := progressSink
	progressMu.Unlock()
	if p != nil {
		p.Harness(name, sessions, messages)
	}
}

// filesPerHarness weights the reading stage: a store with 900 session files
// should move the bar far more than one with three.
func filesPerHarness(files map[string]FileState) map[string]int {
	out := map[string]int{}
	for p := range files {
		out[harnessForPath(p)]++
	}
	return out
}

// hasProgressSink reports whether a live display is drawing, so the plain
// narration can stand down instead of scrolling above it.
func hasProgressSink() bool {
	progressMu.Lock()
	defer progressMu.Unlock()
	return progressSink != nil
}
