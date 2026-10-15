package index

import (
	"sort"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// KeptSession is a session whose transcript its agent deleted and the index
// kept, with when an index pass first found the file gone.
type KeptSession struct {
	SessionMeta
	At time.Time
}

// KeptSince lists the sessions, not their subagent runs, whose transcript a pass first found gone in
// [from, to), newest first. Stamps are whole seconds, so two windows that meet
// at one second each hold a stamp in it once: the week note's last window ends
// where the next begins. It reads the manifest alone, never the disk: the week
// note asks from a session-start hook.
func KeptSince(dir string, from, to time.Time) ([]KeptSession, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil, err
	}
	var out []KeptSession
	for _, meta := range m.Sessions {
		f, ok := m.Files[meta.Path]
		if !ok || !f.Kept || f.KeptAt == 0 || f.KeptAt < from.Unix() || f.KeptAt >= to.Unix() {
			continue
		}
		// A subagent's run is a transcript of its own and goes with the
		// session that launched it; counted, one session read as four.
		if spawnedKind(meta.Kind) || sources.IsSubagentPath(meta.Path) {
			continue
		}
		out = append(out, KeptSession{SessionMeta: meta, At: time.Unix(f.KeptAt, 0)})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		if !out[i].Updated.Equal(out[j].Updated) {
			return out[i].Updated.After(out[j].Updated)
		}
		return out[i].Harness+":"+out[i].ID < out[j].Harness+":"+out[j].ID
	})
	return out, nil
}
