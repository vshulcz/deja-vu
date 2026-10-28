package index

import (
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	search "github.com/vshulcz/deja-vu/internal/query"
)

// metaViewsStore is a Claude store under a temp dir and the index beside it.
// add writes one single-turn session and rebuilds the index.
func metaViewsStore(t *testing.T) (dir string, add func(id, text string)) {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	dir = filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(root, "-tmp-garden")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	add = func(id, text string) {
		t.Helper()
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
	}
	return dir, add
}

func sharedIDs(t *testing.T, dir string) string {
	t.Helper()
	metas, err := AllMetaShared(dir)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(metas))
	for _, m := range metas {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// The views recall derives from the manifest are kept between queries. A
// publish has to replace them: a session indexed after the first query must
// be in the next one, by id, by ordinal and in every meta.
func TestMetaViewsFollowAPublish(t *testing.T) {
	dir, add := metaViewsStore(t)
	add("s1", "the compost heap needs turning before the frost")
	if got := sharedIDs(t, dir); got != "s1" {
		t.Fatalf("first build: %q", got)
	}
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first := viewsFor(m); viewsFor(m) != first {
		t.Fatal("the cached manifest's views are rebuilt on every call")
	}
	if n := len(viewsFor(m).metaByOrd()); n != 1 {
		t.Fatalf("%d sessions by ordinal, want 1", n)
	}

	add("s2", "the compost bin lid cracked in the frost")
	if got := sharedIDs(t, dir); got != "s1,s2" {
		t.Fatalf("after a publish the metas are %q, want s1,s2", got)
	}
	m2, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	v := viewsFor(m2)
	if n := len(v.metaByOrd()); n != 2 {
		t.Fatalf("after a publish %d sessions by ordinal, want 2", n)
	}
	if len(v.metasByID()["s2"]) != 1 {
		t.Fatal("after a publish the new session is missing by id")
	}
	ss, err := Search(dir, search.Options{Query: "compost", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 {
		t.Fatalf("search after a publish found %d sessions, want 2", len(ss))
	}
	// The old manifest still answers for itself, not with the new table.
	if n := len(viewsFor(m).metaByOrd()); n != 1 {
		t.Fatalf("an older manifest got the newer views: %d sessions", n)
	}
}

// Another process publishes by swapping sessions.gob under this one; the
// next read sees a new file and must not pair it with the old views.
func TestMetaViewsFollowAnotherProcess(t *testing.T) {
	dir, add := metaViewsStore(t)
	add("s1", "the rain gauge overflowed on tuesday")
	if got := sharedIDs(t, dir); got != "s1" {
		t.Fatalf("first build: %q", got)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	sessions := maps.Clone(m.Sessions)
	sessions["claude:s9"] = SessionMeta{ID: "s9", Harness: "claude", Ord: 99}
	if _, err := writeGobAtomicStat(filepath.Join(dir, "sessions.gob"), sessions); err != nil {
		t.Fatal(err)
	}
	if got := sharedIDs(t, dir); got != "s1,s9" {
		t.Fatalf("after another process wrote the index the metas are %q, want s1,s9", got)
	}
}

// Concurrent MCP calls share one set of views while the index is rewritten
// under them. Run with -race.
func TestMetaViewsConcurrentReaders(t *testing.T) {
	dir, add := metaViewsStore(t)
	add("s1", "the hedge trimmer blade went dull")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := AllMetaShared(dir); err != nil {
					continue
				}
				if m, err := readManifestCached(dir); err == nil {
					v := viewsFor(m)
					_ = v.metaByOrd()
					_ = v.metasByID()
				}
			}
		})
	}
	add("s2", "the hedge along the lane needs a second cut")
	add("s3", "a hedge sparrow nests in the trimmer box")
	close(stop)
	wg.Wait()
	if got := sharedIDs(t, dir); got != "s1,s2,s3" {
		t.Fatalf("after concurrent reads the metas are %q", got)
	}
}
