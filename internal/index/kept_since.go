package index

import (
	"sort"
	"time"
)

// KeptSession is a session whose transcript its agent deleted and the index
// kept, with when an index pass first found the file gone.
type KeptSession struct {
	SessionMeta
	At time.Time
}

// KeptSince lists the sessions whose transcript a pass first found gone at or
// after since, newest first. It reads the manifest alone, never the disk: the
// week note asks from a session-start hook.
func KeptSince(dir string, since time.Time) ([]KeptSession, error) {
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
		if !ok || !f.Kept || f.KeptAt == 0 || f.KeptAt < since.Unix() {
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
