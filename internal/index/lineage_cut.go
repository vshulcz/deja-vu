package index

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// LineageCut is Lineage with the cut for every session it leaves on the page
// that shares turns with an asker: the time of the last turn they share.
// Recall leaves out that session's records at or before the cut, so a source
// that went on after a fork gives the fork what it did since and not the
// fork's own copy of it again (#5335).
func LineageCut(dir string, ids map[string]bool, heads ...model.Session) (map[string]bool, map[string]time.Time) {
	return lineage(dir, ids, heads, nil)
}

// LineageWholeCut is LineageWhole with the cuts LineageCut gives.
func LineageWholeCut(dir string, ids map[string]bool, wholes ...model.Session) (map[string]bool, map[string]time.Time) {
	return lineage(dir, ids, nil, wholes)
}

// TouchedAfter reports whether meta's session worked on path after at: a
// files record naming it, stamped later. Touched carries no times, so a
// source cut at a fork point would count for edits the fork copied (#5341).
// An untimed record is not after the cut, the way recall cuts it. When no
// record names the path at all (a redacted one) or the read fails, the answer
// is yes, which keeps the session counted as before.
func TouchedAfter(dir string, meta SessionMeta, path string, at time.Time) bool {
	m, err := readManifestCached(dir)
	if err != nil {
		return true
	}
	var named, after bool
	err = eachRecordForKeysInRoles(filepath.Join(dir, "records.bin"), tablesFromManifest(m),
		map[string]bool{meta.Key(): true}, map[string]bool{roleFiles: true}, func(r Record) {
			if after || r.Role != roleFiles {
				return
			}
			for p := range strings.SplitSeq(r.Text, "\n") {
				if p = strings.TrimSpace(p); p != "" && SameFile(p, path) {
					named = true
					after = r.Time.After(at)
					return
				}
			}
		})
	return err != nil || after || !named
}
