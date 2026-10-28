package index

import (
	"reflect"
	"sync"
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

	allOnce sync.Once
	all     []SessionMeta
	ordOnce sync.Once
	byOrd   map[uint32]*SessionMeta
	idOnce  sync.Once
	byID    map[string][]*SessionMeta
}

// viewsFor is the views of m's session table: the cached ones when m is the
// cached manifest, fresh ones otherwise.
func viewsFor(m Manifest) *metaViews {
	manifestCache.mu.Lock()
	v := manifestCache.views
	hit := manifestCache.ok && v != nil && sameTable(v.sessions, m.Sessions)
	manifestCache.mu.Unlock()
	if hit {
		return v
	}
	return &metaViews{sessions: m.Sessions}
}

// sameTable reports whether a and b are one map. The cached views hold their
// map, so its address cannot be reused by another map while they are cached.
func sameTable(a, b map[string]SessionMeta) bool {
	return a != nil && b != nil && reflect.ValueOf(a).UnsafePointer() == reflect.ValueOf(b).UnsafePointer()
}

func (v *metaViews) allMeta() []SessionMeta {
	v.allOnce.Do(func() {
		v.all = make([]SessionMeta, 0, len(v.sessions))
		for _, meta := range v.sessions {
			v.all = append(v.all, meta)
		}
	})
	return v.all
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

// metaAt is the session with ordinal sid in byOrd, and whether there is one.
func metaAt(byOrd map[uint32]*SessionMeta, sid uint32) (SessionMeta, bool) {
	if p := byOrd[sid]; p != nil {
		return *p, true
	}
	return SessionMeta{}, false
}
