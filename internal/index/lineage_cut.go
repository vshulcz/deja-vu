package index

import (
	"path/filepath"
	"strconv"
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

// sharedRecords answers the time of the latest record of other that asker
// holds too, the same role, time and text. Zero when they share none or a
// read fails, which cuts nothing.
func sharedRecords(dir string, m Manifest) func(asker, other SessionMeta) time.Time {
	path := filepath.Join(dir, "records.bin")
	tables := tablesFromManifest(m)
	sets := map[string]map[string]bool{}
	key := func(r Record) string {
		return r.Role + "\x00" + strconv.FormatInt(r.Time.UnixNano(), 10) + "\x00" + r.Text
	}
	return func(asker, other SessionMeta) time.Time {
		k := asker.Harness + ":" + asker.ID
		set, ok := sets[k]
		if !ok {
			if recs, err := recordsForKey(path, tables, k); err == nil {
				set = make(map[string]bool, len(recs))
				for _, r := range recs {
					set[key(r)] = true
				}
			}
			sets[k] = set
		}
		var at time.Time
		if len(set) == 0 || asker.Harness != other.Harness {
			return at
		}
		recs, err := recordsForKey(path, tables, other.Harness+":"+other.ID)
		if err != nil {
			return at
		}
		for _, r := range recs {
			if set[key(r)] && r.Time.After(at) {
				at = r.Time
			}
		}
		return at
	}
}

// wholeShared is sharedRecords for an asker read whole, matched on role and
// millisecond the way wholeHolds matches.
func wholeShared(dir string, m Manifest, turns map[string]bool, other SessionMeta) time.Time {
	var at time.Time
	recs, err := recordsForKey(filepath.Join(dir, "records.bin"), tablesFromManifest(m), other.Harness+":"+other.ID)
	if err != nil {
		return at
	}
	for _, r := range recs {
		if !r.Time.IsZero() && turns[turnKey(r.Role, r.Time.UnixMilli())] && r.Time.After(at) {
			at = r.Time
		}
	}
	return at
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
		map[string]bool{meta.Harness + ":" + meta.ID: true}, map[string]bool{roleFiles: true}, func(r Record) {
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
