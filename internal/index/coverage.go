package index

import "time"

// Coverage is what an answer served from the index on disk covers while a
// build runs behind it. "The very newest sessions may not appear yet" was the
// whole of it before, and after an interrupted first build it was wrong: the
// index held the 200 newest of 10,000 sessions, and it was the oldest 9,800
// that were missing (#5390).
type Coverage struct {
	// Stale says the answer comes from a snapshot something newer is being
	// written over. The other fields are only filled when it is set.
	Stale bool
	// At is when the snapshot last read this machine's stores.
	At time.Time
	// Sessions is how many sessions the snapshot holds.
	Sessions int
	// PartialOf is set when the snapshot is the early slice of a first build
	// that has not finished: how many sessions that build found. The slice
	// holds the newest of them.
	PartialOf int
	// Pending is how many transcripts are new or changed since the snapshot,
	// and Found how many the walk saw, when the caller walked the stores.
	// Zero when it did not.
	Pending, Found int
}

// SnapshotCoverage describes the index on disk from its manifest alone, with
// no walk of the stores. Zero when there is no readable manifest.
func SnapshotCoverage(dir string) Coverage {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return Coverage{}
	}
	c := Coverage{At: m.SourcesReadAt, Sessions: len(m.Sessions)}
	if c.At.IsZero() {
		c.At = m.BuiltAt
	}
	// Only while nothing has been recorded over the slice: it is written with
	// no file states, and the first pass that records any is past it.
	if m.PartialOf > 0 && len(m.Files) == 0 {
		c.PartialOf = m.PartialOf
	}
	return c
}
