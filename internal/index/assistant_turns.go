package index

import (
	"path/filepath"
	"time"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// EachAssistantTurnSince calls fn with every assistant turn written after
// since, in sessions updated after since. Only those sessions' records are
// read, assistant role only, so the cost follows the week's work rather than
// the size of the store. allow gates the speaking session's project; ignored
// sessions are skipped the way TopFriction skips them.
func EachAssistantTurnSince(dir string, since time.Time, allow func(project string) bool, fn func(meta SessionMeta, text string, at time.Time)) error {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return err
	}
	pol := policy.Load()
	keys := map[string]bool{}
	metas := map[string]SessionMeta{}
	for key, meta := range m.Sessions {
		if !meta.Updated.After(since) {
			continue
		}
		if allow != nil && !allow(meta.Project) {
			continue
		}
		if pol.Ignored(meta.Path, meta.Project) {
			continue
		}
		keys[key] = true
		metas[key] = meta
	}
	if len(keys) == 0 {
		return nil
	}
	return eachRecordForKeysInRoles(filepath.Join(dir, "records.bin"), tablesFromManifest(m), keys,
		map[string]bool{"assistant": true}, func(r Record) {
			if r.Time.IsZero() || !r.Time.After(since) {
				return
			}
			fn(metas[r.Key], r.Text, r.Time)
		})
}
