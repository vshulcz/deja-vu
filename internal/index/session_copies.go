package index

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A session id can live in several transcripts: a fork or a resume copies the
// turns into a new file, and two projects can share a filename-derived id. A
// rebuild reads them together and writes a turn they share once. An
// incremental pass re-read only the files that changed, so a later rewrite of
// one copy wrote the shared turn a second time beside the other copy's record,
// or dropped the one record a turn had while the other copy still held it
// (#5358). The row names its owner in Path and every other per-file transcript
// that holds it in Copies, and a pass that replaces the session reads them all.

// copyable reports whether p is a transcript that can be re-read on its own to
// reconcile a session. A shared store is read from a watermark, a prompt log
// holds a line of thousands of sessions, and neither speaks for one session.
func copyable(p string) bool {
	return p != "" && p != syncImportPath && storeHarness(p) == "" && !isCodexHistory(p) && harnessForPath(p) != "opencode-diff"
}

// recordCopy notes in the row for key the files prev and s came from, now
// that s has been folded into it.
func recordCopy(sessions map[string]SessionMeta, key string, prev SessionMeta, s model.Session) {
	row, ok := sessions[key]
	if !ok {
		return
	}
	row.Copies = withCopies(row.Path, prev.Copies, prev.Path, s.Path)
	sessions[key] = row
}

// withCopies is have plus add, without the owner and what cannot be re-read,
// sorted so the manifest does not depend on read order.
func withCopies(owner string, have []string, add ...string) []string {
	var out []string
	for _, p := range append(slices.Clone(have), add...) {
		if p != owner && copyable(p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// renameCopy follows a transcript that moved from one path to another.
func renameCopy(meta *SessionMeta, from, to string) {
	if i := slices.Index(meta.Copies, from); i >= 0 {
		cp := slices.Clone(meta.Copies)
		cp[i] = to
		meta.Copies = withCopies(meta.Path, cp)
	}
}

// sessionFiles is every file the row was read from.
func sessionFiles(meta SessionMeta) []string {
	if meta.Path == "" {
		return meta.Copies
	}
	return append([]string{meta.Path}, meta.Copies...)
}

// keysByFile maps each file to the sessions held from it that span more than
// one file: the only ones a pass has to reconcile.
func keysByFile(sessions map[string]SessionMeta) map[string][]string {
	out := map[string][]string{}
	for key, meta := range sessions {
		if len(meta.Copies) == 0 {
			continue
		}
		for _, p := range sessionFiles(meta) {
			out[p] = append(out[p], key)
		}
	}
	return out
}

// settleCopies is the replacement path's last word on each row's files. A
// session it replaced was read from every live file that holds it, so its
// copies are the ones it was read from, plus those it could not read again —
// a kept transcript, a file skipped this pass — whose records it carried. A
// row it carried keeps its own, less the files gone from the index.
func settleCopies(m *Manifest, old map[string]SessionMeta, reread map[string]FileState, replacements []model.Session) {
	readFrom := map[string][]string{}
	for _, s := range replacements {
		key := s.Harness + ":" + s.ID
		readFrom[key] = append(readFrom[key], s.Path)
	}
	for key, row := range m.Sessions {
		if len(row.Copies) == 0 && len(old[key].Copies) == 0 && len(readFrom[key]) < 2 && old[key].Path == row.Path {
			continue
		}
		var keep []string
		for _, p := range append(sessionFiles(old[key]), row.Copies...) {
			if _, ok := m.Files[p]; !ok {
				continue
			}
			if _, again := reread[p]; again {
				continue // in readFrom if it still holds the session
			}
			keep = append(keep, p)
		}
		cp := withCopies(row.Path, keep, readFrom[key]...)
		// The last copy beside a row nobody read again left it: what is
		// left is one transcript, as a rebuild finds it (#5380).
		if len(cp) == 0 && len(readFrom[key]) == 0 && len(old[key].Copies) > 0 {
			row.Shared = false
			row.SharedStarted, row.SharedUpdated = time.Time{}, time.Time{}
			m.Sessions[key] = row
		}
		if len(cp) > 0 || len(row.Copies) > 0 {
			row.Copies = cp
			m.Sessions[key] = row
		}
	}
}

// renameReorders reports whether the renames in renamed (new path to old)
// put files, a session's files under their new names, in another order than
// they had under the old ones.
func renameReorders(files []string, renamed map[string]string) bool {
	now := slices.Clone(files)
	slices.Sort(now)
	before := make([]string, len(now))
	for i, p := range now {
		before[i] = p
		if op, ok := renamed[p]; ok {
			before[i] = op
		}
	}
	return !slices.IsSorted(before)
}

// sharedTakeovers names the live copies a file in changed has to be read
// again with: transcripts in another directory under the same filename-derived
// id (#699) whose paths sort after it and that hold a turn it now holds. A
// turn copies share is written once, under the first that holds it, and a
// rebuild reads them in path order, so it is the smaller path's. Appended, the
// turn stayed under the copy that had it first, and the command tables leave a
// turn of another directory's copy out: a copy taking the row lost it from the
// row's facts (#5492), and so did an owner appending a turn a later copy
// already held (#5509). Read again with it, the copies are written in path
// order, as a rebuild writes them.
//
// A file new to the index is read whole and takes them all. A known one is
// asked about its tail alone, against the records filed under each copy, so a
// live owner beside a copy it forked from long ago still appends.
func sharedTakeovers(dir, harness string, old Manifest, changed, files map[string]FileState) map[string]bool {
	var out map[string]bool
	for _, p := range sortedKeys(changed) {
		if !copyable(p) {
			continue
		}
		meta, ok := old.Sessions[harnessForPath(p)+":"+filepath.Base(fileStem(p))]
		if !ok {
			continue
		}
		var after []string
		for _, q := range sessionFiles(meta) {
			if q <= p || filepath.Dir(p) == filepath.Dir(q) || !copyable(q) {
				continue
			}
			// A written-back file holds less than its records do (#4617).
			if f, ok := files[q]; !ok || f.Kept || f.WrittenBack {
				continue
			}
			after = append(after, q)
		}
		if len(after) == 0 {
			continue
		}
		if of, known := old.Files[p]; known {
			after = copiesHoldingTail(dir, harness, old, p, of, after)
		}
		for _, q := range after {
			if out == nil {
				out = map[string]bool{}
			}
			out[q] = true
		}
	}
	return out
}

// copiesHoldingTail is those of qs with a record of a turn the tail of p
// written since of holds. A tail that cannot be read takes them all.
func copiesHoldingTail(dir, harness string, old Manifest, p string, of FileState, qs []string) []string {
	ss, err := parseAppendedFile(harness, p, of, false)
	if err != nil {
		return qs
	}
	ss = sources.FilterSessions(ss)
	preRedactSessions(nil, ss)
	tail, keys := map[repeatKey]bool{}, map[string]bool{}
	for _, s := range ss {
		keys[s.Harness+":"+s.ID] = true
		for _, m := range s.Messages {
			// A command is the same turn before and after its result lands.
			tail[repeatKeyOf(m.Role, m.Time, exitSuffix.ReplaceAllString(m.Text, ""))] = true
		}
	}
	if len(tail) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, q := range qs {
		want[q] = true
	}
	held := map[string]bool{}
	if err := eachRecordForKeys(filepath.Join(dir, "records.bin"), tablesFromManifest(old), keys, func(r Record) {
		if want[r.SourcePath] && tail[repeatKeyOf(r.Role, r.Time, exitSuffix.ReplaceAllString(r.Text, ""))] {
			held[r.SourcePath] = true
		}
	}); err != nil {
		return qs
	}
	var out []string
	for _, q := range qs {
		if held[q] {
			out = append(out, q)
		}
	}
	return out
}

// sortCopies puts the per-file transcripts of ss in path order, in the slots
// they hold, and leaves the rest where they are. A turn copies share is
// written once, under the first that holds it: read in map order, the pass
// filed it under either copy, where a rebuild reads them in path order and the
// command tables leave a copy in another directory out (#5492).
func sortCopies(ss []model.Session) {
	var at []int
	var cp []model.Session
	for i, s := range ss {
		if copyable(s.Path) {
			at = append(at, i)
			cp = append(cp, s)
		}
	}
	slices.SortStableFunc(cp, func(a, b model.Session) int { return strings.Compare(a.Path, b.Path) })
	for k, i := range at {
		ss[i] = cp[k]
	}
}
