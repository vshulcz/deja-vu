package index

import (
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
