package index

import (
	"slices"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
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
