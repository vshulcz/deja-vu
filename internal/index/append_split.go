package index

import (
	"maps"
	"strings"
)

// touchedOnly reports a transcript whose mtime moved while its bytes did not:
// the same size, every line complete, and the same prefix sample the walk just
// took. Something rewrote or touched the file without adding to it. Re-reading
// it was the cost of #5324: a 108 MB Claude transcript parsed from byte zero
// on every pass for an mtime. It is recorded with its new state, not read.
//
// The sample is the trust the append path already gives a grown file's prefix:
// a same-size rewrite that changes the head or the tail is caught and goes to
// replacement. Only kinds that resume a parse qualify, since those are the
// append-only logs; a store that rewrites itself in place is read as before.
func touchedOnly(p string, of, f FileState) bool {
	if of.Kept || of.Size != f.Size || !strings.HasSuffix(p, ".jsonl") {
		return false
	}
	if of.SafeSize != of.Size || f.SafeSize != f.Size || of.PrefixSample == 0 || f.PrefixSample != of.PrefixSample {
		return false
	}
	g := f
	g.Path, g.MTime = of.Path, of.MTime
	return sameFile(of, g) && appendableKind(harnessForPath(p))
}

// dropTouched takes the touched-only files out of changed and returns them.
// With files, each one's entry becomes what the index held for it with the new
// mtime, so what a read derives — its redaction count — is carried, not zeroed.
func dropTouched(changed, files, old map[string]FileState) map[string]bool {
	var touched map[string]bool
	for p, f := range changed {
		of, ok := old[p]
		if !ok || !touchedOnly(p, of, f) {
			continue
		}
		if touched == nil {
			touched = map[string]bool{}
		}
		touched[p] = true
		delete(changed, p)
		if files != nil {
			of.Path, of.MTime = f.Path, f.MTime
			files[p] = of
		}
	}
	return touched
}

// splitAppendable decides per file what canAppendIncremental decides for a
// batch. One file that cannot be appended used to send every other changed
// file down the replacement path, which parses each from byte zero: a 1.27 GB
// transcript that grew 281 KB was read whole because a different file kept its
// size (#5324). Each part on its own is a pass that could have happened: the
// grown files changing first, the rest after.
//
// Not when the parts may hold one session. Two transcripts that share an id
// are reconciled only when they are read together, and the rest replaced on
// its own wrote the appended file's turns a second time (#5358). A new file's
// ids are not known until it is read, and a fork or a resume copies a session
// into one, so a batch with a new file stays whole too.
func splitAppendable(changed, old map[string]FileState, sessions map[string]SessionMeta) (appendable, rest map[string]FileState) {
	// A shared store read in the same pass as the files beside it is how one
	// session moves between them: a ZCode snapshot restored into the CLI
	// database is read from the database alone only when both are read
	// together (#4448). Such a batch stays whole, as does one with a new file.
	for p := range changed {
		if _, known := old[p]; !known || storeHarness(p) != "" {
			return nil, changed
		}
	}
	// A changed file that owns a row covering more than one transcript keeps
	// the batch whole: the replacement half would read the appended copies
	// again anyway (#5358).
	for _, meta := range sessions {
		if _, ok := changed[meta.Path]; ok && (meta.Shared || !meta.SharedStarted.IsZero() || !meta.SharedUpdated.IsZero()) {
			return nil, changed
		}
	}
	appendable, rest = map[string]FileState{}, map[string]FileState{}
	for p, f := range changed {
		if canAppendIncremental(map[string]FileState{p: f}, old) {
			appendable[p] = f
		} else {
			rest[p] = f
		}
	}
	return appendable, rest
}

// withoutRest is files with the rest held at what the index already has, so an
// append over the appendable part leaves the rest changed for the pass after.
func withoutRest(files map[string]FileState, rest, old map[string]FileState) map[string]FileState {
	out := maps.Clone(files)
	for p := range rest {
		if of, ok := old[p]; ok {
			out[p] = of
		} else {
			delete(out, p)
		}
	}
	return out
}
