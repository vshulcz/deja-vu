package index

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// NotesZoneDrift reports whether the store holds note buckets grouped in a
// time zone other than the one this process runs in. A bucket id names a local
// day, so a `remember` under TZ=UTC and one under the machine's own zone put
// notes from the same moment into two buckets, and an incremental pass leaves
// them there: only a rebuild regroups them (#1058).
//
// Only buckets derived from this machine's notes file count. A bucket that
// arrived over sync was grouped on the peer and no local rebuild can regroup
// it, so counting it would rebuild the whole index on every `deja index`.
func NotesZoneDrift(dir string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return false
	}
	return notesZoneDrifted(m)
}

// notesZoneDrifted is NotesZoneDrift over a manifest the caller already holds.
// The ingest paths read the manifest anyway, and only `deja index` used to make
// the check — so a search, `deja last` or the hook left the split buckets in
// place forever and showed one local day as two rows (#1058).
func notesZoneDrifted(m Manifest) bool {
	notes := sources.NotesFile()
	for _, s := range m.Sessions {
		if s.Path != notes {
			continue
		}
		day, ok := noteBucketDay(s.Harness, s.ID)
		if !ok {
			continue
		}
		if !sameLocalDay(s.Updated, day) || (!s.Started.IsZero() && !sameLocalDay(s.Started, day)) {
			return true
		}
	}
	return false
}

// noteBucketDay returns the day a note bucket's id encodes. Promoted notes are
// keyed by their source session instead and carry no day.
func noteBucketDay(harness, id string) (string, bool) {
	if harness != "deja" || !strings.HasPrefix(id, "deja-") {
		return "", false
	}
	rest := strings.TrimPrefix(id, "deja-")
	if len(rest) < 10 {
		return "", false
	}
	day := rest[:10]
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return "", false
	}
	return day, true
}

func sameLocalDay(t time.Time, day string) bool {
	return t.IsZero() || t.Local().Format("2006-01-02") == day
}

// zoneDrifted reports rows that a rebuild in this process's zone would read
// differently: note buckets grouped by another zone's day (#1058) and aider
// headers read in another zone (#5053). Either one makes the pass a rebuild.
func zoneDrifted(m Manifest) bool {
	return notesZoneDrifted(m) || aiderZoneDrifted(m)
}

// AiderZoneDrift reports whether the store holds aider sessions read in a time
// zone other than the one this process runs in. aider writes its local wall
// clock with no zone, and deja reads it in the machine's zone, so after a zone
// change an incremental pass kept the old instants and a rebuild gave new ones
// (#5053).
func AiderZoneDrift(dir string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifest(dir)
	if err != nil {
		return false
	}
	return aiderZoneDrifted(m)
}

// aiderZoneDrifted is AiderZoneDrift over a manifest the caller holds. The
// zone is recorded per pass rather than derived from the rows: a kept row of a
// deleted history stays as it was read, by a rebuild too, and a check over the
// rows would rebuild the index on every pass. A store from before ReadZone
// says nothing, and a store with no aider rows has nothing to reread.
func aiderZoneDrifted(m Manifest) bool {
	if m.ReadZone == "" || m.ReadZone == zoneFingerprint() {
		return false
	}
	for _, s := range m.Sessions {
		if s.Harness == "aider" && s.Path != syncImportPath {
			return true
		}
	}
	return false
}

// zoneFingerprint names this process's zone by its offsets and transitions
// from 2023, before aider's first release, to 2040. Two zones that read every
// aider header the same get the same name, whatever they are called.
func zoneFingerprint() string {
	h := fnv.New64a()
	end := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
	for t := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC); t.Before(end); {
		lt := t.In(time.Local)
		_, off := lt.Zone()
		fmt.Fprintf(h, "%d@%d;", off, t.Unix())
		_, next := lt.ZoneBounds()
		if !next.After(t) {
			break
		}
		t = next
	}
	return strconv.FormatUint(h.Sum64(), 36)
}
