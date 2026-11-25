package index

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// metaViews are what recall derives from one manifest's session table: every
// meta as a slice, and the same metas by ordinal and by id. Rebuilt on every
// query they were a third of what a recall allocated, 82 MB a call on a
// 19,000-session store, and they change only when the session table does. The
// maps point into the slice, so a server holds one extra copy of the metas.
//
// They hang off the manifest cache entry. Whatever replaces that entry, a
// publish in this process or a manifest another process swapped in, replaces
// them with it, so a reader never pairs a new manifest with old views. A
// Manifest that is not the cached one gets views of its own, built on use and
// not kept.
//
// Shared like the cached Manifest: callers must not modify what they return.
type metaViews struct {
	sessions map[string]SessionMeta

	allOnce     sync.Once
	all         []SessionMeta
	maxOrd      int
	ordOnce     sync.Once
	byOrd       map[uint32]*SessionMeta
	idOnce      sync.Once
	byID        map[string][]*SessionMeta
	openingOnce sync.Once
	byOpening   map[uint64][]*SessionMeta
	edgesOnce   sync.Once
	edges       spawnEdges
	countedOnce sync.Once
	counted     int

	lineageMu    sync.Mutex
	lineageCache map[string]cachedLineage

	giveUpMu     sync.Mutex
	giveUpRecs   map[string][]Record
	giveUpLoaded map[string]bool
}

type spawnEdges struct {
	children map[string][]string
	parent   map[string]string
	forks    map[string]SessionMeta
	hasChild map[string]bool
}

type cachedLineage struct {
	out  map[string]bool
	cuts map[string]time.Time
}

// viewsFor is the views of m's session table: the cached ones when m is the
// cached manifest, fresh ones otherwise.
func viewsFor(m Manifest) *metaViews {
	return cachedViewsFor(m)
}

// sameTable reports whether a and b are one map. The cached views hold their
// map, so its address cannot be reused by another map while they are cached.
func sameTable(a, b map[string]SessionMeta) bool {
	return a != nil && b != nil && reflect.ValueOf(a).UnsafePointer() == reflect.ValueOf(b).UnsafePointer()
}

func (v *metaViews) allMeta() []SessionMeta {
	v.allOnce.Do(func() {
		v.all = make([]SessionMeta, 0, len(v.sessions))
		v.maxOrd = -1
		for _, meta := range v.sessions {
			v.all = append(v.all, meta)
			if int(meta.Ord) > v.maxOrd {
				v.maxOrd = int(meta.Ord)
			}
		}
	})
	return v.all
}

func (v *metaViews) countedDocs() int {
	v.countedOnce.Do(func() {
		all := v.allMeta()
		n := 0
		for i := range all {
			c := all[i].Counted
			if c <= 0 {
				c = 1
			}
			n += min(c, dfMessageCap)
		}
		v.counted = n
	})
	return v.counted
}

func (v *metaViews) metaByOrd() map[uint32]*SessionMeta {
	v.ordOnce.Do(func() {
		all := v.allMeta()
		v.byOrd = make(map[uint32]*SessionMeta, len(all))
		for i := range all {
			v.byOrd[all[i].Ord] = &all[i]
		}
	})
	return v.byOrd
}

func (v *metaViews) metasByID() map[string][]*SessionMeta {
	v.idOnce.Do(func() {
		all := v.allMeta()
		v.byID = make(map[string][]*SessionMeta, len(all))
		for i := range all {
			v.byID[all[i].ID] = append(v.byID[all[i].ID], &all[i])
		}
	})
	return v.byID
}

func (v *metaViews) metasByOpening() map[uint64][]*SessionMeta {
	v.openingOnce.Do(func() {
		all := v.allMeta()
		v.byOpening = make(map[uint64][]*SessionMeta)
		for i := range all {
			if all[i].Opening != 0 {
				v.byOpening[all[i].Opening] = append(v.byOpening[all[i].Opening], &all[i])
			}
		}
	})
	return v.byOpening
}

func (v *metaViews) spawnEdges() spawnEdges {
	v.edgesOnce.Do(func() {
		all := v.allMeta()
		v.edges = spawnEdges{
			children: map[string][]string{},
			parent:   map[string]string{},
			forks:    map[string]SessionMeta{},
			hasChild: map[string]bool{},
		}
		for i := range all {
			meta := all[i]
			if meta.Parent == "" || meta.Parent == meta.ID {
				continue
			}
			v.edges.hasChild[meta.Parent] = true
			if meta.IsSpawned() {
				v.edges.children[meta.Parent] = append(v.edges.children[meta.Parent], meta.ID)
				v.edges.parent[meta.ID] = meta.Parent
			} else if meta.Kind == model.KindFork {
				v.edges.forks[meta.ID] = meta
			}
		}
	})
	return v.edges
}

func lineageFileStamp(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	return strconv.FormatInt(fi.Size(), 10) + ":" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}

func lineageCacheKey(dir string, ids map[string]bool) string {
	keys := make([]string, 0, len(ids))
	for id, ok := range ids {
		if ok && id != "" {
			keys = append(keys, id)
		}
	}
	slices.Sort(keys)
	return dir + "\x00" +
		lineageFileStamp(filepath.Join(dir, "records.bin")) + "\x00" +
		lineageFileStamp(filepath.Join(dir, recordOffsetsFile)) + "\x00" +
		strings.Join(keys, "\x00")
}

func (v *metaViews) getLineageCached(key string) (cachedLineage, bool) {
	v.lineageMu.Lock()
	e, ok := v.lineageCache[key]
	v.lineageMu.Unlock()
	if !ok {
		return cachedLineage{}, false
	}
	return cachedLineage{out: maps.Clone(e.out), cuts: maps.Clone(e.cuts)}, true
}

func (v *metaViews) putLineageCached(key string, out map[string]bool, cuts map[string]time.Time) {
	const maxLineageCacheEntries = 16
	v.lineageMu.Lock()
	if v.lineageCache == nil {
		v.lineageCache = make(map[string]cachedLineage, 4)
	} else if len(v.lineageCache) >= maxLineageCacheEntries {
		clear(v.lineageCache)
	}
	v.lineageCache[key] = cachedLineage{out: maps.Clone(out), cuts: maps.Clone(cuts)}
	v.lineageMu.Unlock()
}

// metaAt is the session with ordinal sid in byOrd, and whether there is one.
func metaAt(byOrd map[uint32]*SessionMeta, sid uint32) (SessionMeta, bool) {
	if p := byOrd[sid]; p != nil {
		return *p, true
	}
	return SessionMeta{}, false
}
