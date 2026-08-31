package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Six harnesses are read from a SQLite store and each has a since-the-watermark
// parser, but only three stores were ever stamped — so for grok, hermes and zed
// the since parser never ran and every pass read the store whole (#2075).
func TestEveryDatabaseBackedStoreIsStamped(t *testing.T) {
	setHome(t, t.TempDir())

	stores := map[string][]string{
		"opencode": {sources.OpencodeDB()},
		"goose":    {sources.GooseDB()},
		"cursor":   sources.CursorDBs(),
		"grok":     {sources.GrokDB()},
		"hermes":   sources.HermesDBs(),
		"zed":      {sources.ZedDB()},
	}
	for harness, dbs := range stores {
		if len(dbs) == 0 {
			// cursor keeps one database per workspace and a fresh home has
			// none; the other five always name a path.
			continue
		}
		files := map[string]FileState{}
		sessions := map[string]SessionMeta{}
		for _, db := range dbs {
			files[db] = FileState{}
			sessions[harness+":one"] = SessionMeta{
				Harness: harness, ID: "one", Path: db,
				Updated: time.Now(),
			}
		}
		setDatabaseStoreWatermarks(files, sessions)
		for _, db := range dbs {
			if files[db].LastUpdated == 0 {
				t.Errorf("%s (%s) was not stamped, so its since-the-watermark parser never runs", harness, db)
			}
		}
	}
}
