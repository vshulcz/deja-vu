package index

import (
	"maps"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
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
// its own wrote the appended file's turns a second time (#5358). New files go
// to rest; prepareSplit checks every rest file against the append half before
// either half writes (#6077).
func splitAppendable(changed, old map[string]FileState, sessions map[string]SessionMeta) (appendable, rest map[string]FileState) {
	// A shared store read in the same pass as the files beside it is how one
	// session moves between them: a ZCode snapshot restored into the CLI
	// database is read from the database alone only when both are read
	// together (#4448). Such a batch stays whole.
	for p := range changed {
		if storeHarness(p) != "" {
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
		if _, known := old[p]; known && canAppendIncremental(map[string]FileState{p: f}, old) {
			appendable[p] = f
		} else {
			rest[p] = f
		}
	}
	return appendable, rest
}

// splitRead keeps the unfiltered parser result and the state it describes.
// Replacement still applies its usual filtering, accounting and skip rules.
type splitRead struct {
	sessions []model.Session
	at       FileState
	again    bool
	deferred bool
	err      error
}

type splitReads struct {
	restFiles, appendFiles map[string]splitRead
	diag                   sources.DiagState
}

// prepareSplit reads every rest file as replacement would, and the append
// tails once. No write may precede the overlap decision. Parser diagnostics
// travel with the rest files to replacement, past the append manifest's fold.
func prepareSplit(harness string, old Manifest, appendable, rest map[string]FileState) (*splitReads, bool) {
	ready := &splitReads{restFiles: map[string]splitRead{}}
	before := sources.DiagSave()
	for p, f := range rest {
		ss, at, again, err := readSettled(p, f, func() ([]model.Session, error) { return parseChangedFile(harness, p, old.Files[p]) })
		ready.restFiles[p] = splitRead{sessions: ss, at: at, again: again, err: err}
	}
	if len(ready.restFiles) == 0 {
		return ready, false
	}
	ready.diag = sources.DiagSave()
	sources.DiagRestore(before)
	ready.appendFiles = make(map[string]splitRead, len(appendable))
	for p, f := range appendable {
		ss, err := parseAppendedFile(harness, p, old.Files[p], false)
		if err == nil && !readAsWalked(p, f) {
			err = errChangedWhileRead
		}
		ready.appendFiles[p] = splitRead{sessions: ss, err: err}
	}
	overlap := splitOverlaps(old, appendable, rest, ready)
	if overlap {
		// The tails will be read whole by replacement instead.
		sources.DiagRestore(ready.diag)
	}
	return ready, overlap
}

// Split only when each half's old and parsed session keys are disjoint.
// Shared sessions must be read together (#5358, #6077).
func splitOverlaps(old Manifest, appendable, rest map[string]FileState, ready *splitReads) bool {
	appendKeys := splitKeys(old, appendable, ready.appendFiles)
	for key := range splitKeys(old, rest, ready.restFiles) {
		if appendKeys[key] {
			return true
		}
	}
	return false
}

func splitKeys(old Manifest, files map[string]FileState, reads map[string]splitRead) map[string]bool {
	keys := map[string]bool{}
	for _, rows := range []map[string]SessionMeta{old.Sessions, old.Empty} {
		for key, meta := range rows {
			for _, p := range sessionFiles(meta) {
				if _, ok := files[p]; ok {
					keys[key] = true
				}
			}
		}
	}
	for _, r := range reads {
		for _, s := range r.sessions {
			keys[s.Key()] = true
		}
	}
	return keys
}

// withoutRest is files with the rest held at what the index already has, so an
// append over the appendable part leaves the rest changed for the pass after.
func withoutRest(files map[string]FileState, rest, old map[string]FileState) map[string]FileState {
	out := maps.Clone(files)
	for p := range rest {
		holdFileAtOld(out, p, old)
	}
	return out
}

// withoutDeferred holds append-deferred files at their indexed state so Pass 2
// leaves them for a later incremental pass instead of forcing a full re-read.
func withoutDeferred(files map[string]FileState, appends map[string]splitRead, old map[string]FileState) map[string]FileState {
	hasDeferred := false
	for _, r := range appends {
		if r.deferred {
			hasDeferred = true
			break
		}
	}
	if !hasDeferred {
		return files
	}
	out := maps.Clone(files)
	for p, r := range appends {
		if r.deferred {
			holdFileAtOld(out, p, old)
		}
	}
	return out
}

func holdFileAtOld(out map[string]FileState, path string, old map[string]FileState) {
	if of, ok := old[path]; ok {
		out[path] = of
		return
	}
	delete(out, path)
}
