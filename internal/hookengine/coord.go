package hookengine

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

// Coordination store schema version, retention TTL, and bounding caps.
const (
	CoordFile               = ".coord"
	CoordStateVersion       = 1
	CoordTTL                = 30 * time.Minute
	CoordMaxSessions        = 64
	CoordMaxFilesPerSession = 32
	CoordMaxStamps          = 256
)

// CoordFileTouch records the Unix timestamp of a file touch by a session.
type CoordFileTouch struct {
	AtUnix int64 `json:"at"`
}

// CoordStamp stores a transient timestamped counter or gate in coordination state.
type CoordStamp struct {
	AtUnix int64 `json:"at"`
	Val    int64 `json:"val,omitempty"`
}

// CoordSession holds recent edits, reads, verification command, and briefing
// metadata for one active agent session.
type CoordSession struct {
	ID                string                    `json:"id"`
	ParentID          string                    `json:"parent,omitempty"`
	Harness           string                    `json:"harness,omitempty"`
	Worktree          string                    `json:"worktree,omitempty"`
	UpdatedAt         int64                     `json:"updated"`
	Edits             map[string]CoordFileTouch `json:"edits,omitempty"`
	Reads             map[string]CoordFileTouch `json:"reads,omitempty"`
	VerifyCmd         string                    `json:"verify_cmd,omitempty"`
	VerifyAt          int64                     `json:"verify_at,omitempty"`
	LastSpawnBriefKey string                    `json:"last_spawn_key,omitempty"`
	LastSpawnBriefAt  int64                     `json:"last_spawn_at,omitempty"`
	BriefedAt         int64                     `json:"briefed_at,omitempty"`
}

// CoordState is the persisted v1 JSON schema for the .coord sidecar.
type CoordState struct {
	Version  int                      `json:"v"`
	Sessions map[string]*CoordSession `json:"sessions,omitempty"`
	Stamps   map[string]CoordStamp    `json:"stamps,omitempty"`
}

// CoordOutcome describes the outcome of a command invocation recorded in .coord.
type CoordOutcome struct {
	SessionID string
	StreakKey string
	VerifyCmd string
	Worktree  string
	Failed    bool
}

// CoordStore serializes reads, TTL/cap pruning, and atomic writes on .coord.
type CoordStore struct {
	path     string
	lockPath string
}

// NewCoordStore returns a CoordStore managing the .coord sidecar beside indexDir.
func NewCoordStore(indexDir string) *CoordStore {
	if strings.TrimSpace(indexDir) == "" {
		return &CoordStore{}
	}
	p := indexDir + CoordFile
	return &CoordStore{
		path:     p,
		lockPath: p + ".lock",
	}
}

// Path returns the filesystem path of the .coord sidecar file.
func (cs *CoordStore) Path() string {
	if cs == nil {
		return ""
	}
	return cs.path
}

// Exists reports whether the underlying .coord file currently exists on disk.
func (cs *CoordStore) Exists() bool {
	if cs == nil || cs.path == "" {
		return false
	}
	_, err := os.Stat(cs.path)
	return err == nil
}

// LoadState reads and parses .coord without acquiring the store lock. Callers
// that already hold Lock() or hold external serialization use this helper to
// avoid re-entering the non-reentrant advisory lock.
func (cs *CoordStore) LoadState() CoordState {
	return (&CoordTx{store: cs}).Load()
}

// SaveState atomically persists st to .coord without acquiring the store lock.
func (cs *CoordStore) SaveState(st CoordState) {
	(&CoordTx{store: cs}).Save(st)
}

// Lock acquires the in-process mutex and bounded cross-process advisory lock
// for .coord, returning a transaction handle and an idempotent release func.
func (cs *CoordStore) Lock() (tx *CoordTx, release func()) {
	if cs == nil || cs.path == "" {
		return &CoordTx{store: cs}, func() {}
	}
	mu := ledgerMutexFor(cs.lockPath)
	mu.Lock()
	_ = atomicfile.MkdirSharedAll(filepath.Dir(cs.lockPath))
	var advRelease func()
	for i := range 5 {
		rel, ok, busy := atomicfile.TakeAdvisoryLock(cs.lockPath, false)
		if ok {
			advRelease = rel
			break
		}
		if !busy || i == 4 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	var once sync.Once
	return &CoordTx{store: cs}, func() {
		once.Do(func() {
			if advRelease != nil {
				advRelease()
			}
			mu.Unlock()
		})
	}
}

// RecordOutcome updates the failure streak and verification command state for
// a command execution without creating sidecar files on uninitialized passing
// non-verify commands. Returns the resulting consecutive failure count.
func (cs *CoordStore) RecordOutcome(now time.Time, opts CoordOutcome) int {
	if cs == nil || cs.path == "" {
		if opts.Failed {
			return 1
		}
		return 0
	}
	if !opts.Failed && opts.VerifyCmd == "" {
		if opts.StreakKey == "" || !cs.Exists() {
			return 0
		}
	}
	tx, release := cs.Lock()
	defer release()

	st := tx.LoadPruned(now)
	nowUnix := now.UTC().Unix()
	if opts.Failed {
		if opts.StreakKey == "" {
			return 1
		}
		next := int(st.Stamps[opts.StreakKey].Val) + 1
		st.Stamps[opts.StreakKey] = CoordStamp{AtUnix: nowUnix, Val: int64(next)}
		tx.Save(st)
		return next
	}

	dirty := false
	if opts.StreakKey != "" {
		if _, exists := st.Stamps[opts.StreakKey]; exists {
			delete(st.Stamps, opts.StreakKey)
			dirty = true
		}
	}
	if opts.VerifyCmd != "" && opts.SessionID != "" {
		sess := st.EnsureSession(opts.SessionID, nowUnix)
		if opts.Worktree != "" {
			sess.Worktree = opts.Worktree
		}
		sess.VerifyCmd = opts.VerifyCmd
		sess.VerifyAt = nowUnix
		dirty = true
	}
	if dirty {
		tx.Save(st)
	}
	return 0
}

// CoordTx operates on .coord inside an active Lock() transaction.
type CoordTx struct {
	store *CoordStore
}

// Load reads and parses the current .coord state, falling back to an empty
// initialized v1 state when missing or corrupt.
func (tx *CoordTx) Load() CoordState {
	st := CoordState{
		Version:  CoordStateVersion,
		Sessions: map[string]*CoordSession{},
		Stamps:   map[string]CoordStamp{},
	}
	if tx == nil || tx.store == nil || tx.store.path == "" {
		return st
	}
	data, err := os.ReadFile(tx.store.path)
	if err != nil || len(data) == 0 {
		return st
	}
	var parsed CoordState
	if json.Unmarshal(data, &parsed) != nil {
		return st
	}
	if parsed.Sessions != nil {
		st.Sessions = parsed.Sessions
	}
	if parsed.Stamps != nil {
		st.Stamps = parsed.Stamps
	}
	st.Version = CoordStateVersion
	return st
}

// LoadPruned loads the current .coord state and applies PruneCoordState at now.
func (tx *CoordTx) LoadPruned(now time.Time) CoordState {
	st := tx.Load()
	PruneCoordState(&st, now)
	return st
}

// Save atomically persists st to .coord using CoordStateVersion.
func (tx *CoordTx) Save(st CoordState) {
	if tx == nil || tx.store == nil || tx.store.path == "" {
		return
	}
	st.Version = CoordStateVersion
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = atomicfile.MkdirSharedAll(filepath.Dir(tx.store.path))
	_ = atomicfile.Write(tx.store.path, data, 0o600)
}

// EnsureSession returns an existing CoordSession for id or creates one,
// refreshing its UpdatedAt timestamp to nowUnix.
func (st *CoordState) EnsureSession(id string, nowUnix int64) *CoordSession {
	if st.Sessions == nil {
		st.Sessions = map[string]*CoordSession{}
	}
	s := st.Sessions[id]
	if s == nil {
		s = &CoordSession{ID: id}
		st.Sessions[id] = s
	}
	s.UpdatedAt = nowUnix
	return s
}

// PruneCoordState drops expired sessions, touches, stamps, and session metadata
// older than CoordTTL and caps retained maps to their configured ceilings.
func PruneCoordState(st *CoordState, now time.Time) {
	if st == nil {
		return
	}
	if st.Sessions == nil {
		st.Sessions = map[string]*CoordSession{}
	}
	if st.Stamps == nil {
		st.Stamps = map[string]CoordStamp{}
	}
	cutoffUnix := now.UTC().Add(-CoordTTL).Unix()
	st.Sessions = pruneCoordSessions(st.Sessions, cutoffUnix)
	st.Stamps = pruneCoordStamps(st.Stamps, cutoffUnix)
}

func pruneCoordSessions(sessions map[string]*CoordSession, cutoffUnix int64) map[string]*CoordSession {
	type sessEntry struct {
		id string
		s  *CoordSession
	}
	kept := make([]sessEntry, 0, len(sessions))
	for id, s := range sessions {
		if id == "" || s == nil || s.UpdatedAt < cutoffUnix {
			continue
		}
		s.ID = id
		s.Edits = pruneCoordTouches(s.Edits, cutoffUnix)
		s.Reads = pruneCoordTouches(s.Reads, cutoffUnix)
		if s.VerifyAt < cutoffUnix {
			s.VerifyCmd = ""
			s.VerifyAt = 0
		}
		if s.LastSpawnBriefAt < cutoffUnix {
			s.LastSpawnBriefKey = ""
			s.LastSpawnBriefAt = 0
		}
		if s.BriefedAt < cutoffUnix {
			s.BriefedAt = 0
		}
		kept = append(kept, sessEntry{id: id, s: s})
	}
	if len(kept) > CoordMaxSessions {
		slices.SortFunc(kept, func(a, b sessEntry) int {
			if c := cmp.Compare(b.s.UpdatedAt, a.s.UpdatedAt); c != 0 {
				return c
			}
			return cmp.Compare(a.id, b.id)
		})
		kept = kept[:CoordMaxSessions]
	}
	next := make(map[string]*CoordSession, len(kept))
	for _, e := range kept {
		next[e.id] = e.s
	}
	return next
}

func pruneCoordStamps(stamps map[string]CoordStamp, cutoffUnix int64) map[string]CoordStamp {
	type stampEntry struct {
		key string
		st  CoordStamp
	}
	kept := make([]stampEntry, 0, len(stamps))
	for k, v := range stamps {
		if k != "" && v.AtUnix >= cutoffUnix {
			kept = append(kept, stampEntry{key: k, st: v})
		}
	}
	if len(kept) > CoordMaxStamps {
		slices.SortFunc(kept, func(a, b stampEntry) int {
			if c := cmp.Compare(b.st.AtUnix, a.st.AtUnix); c != 0 {
				return c
			}
			return cmp.Compare(a.key, b.key)
		})
		kept = kept[:CoordMaxStamps]
	}
	next := make(map[string]CoordStamp, len(kept))
	for _, e := range kept {
		next[e.key] = e.st
	}
	return next
}

func pruneCoordTouches(m map[string]CoordFileTouch, cutoffUnix int64) map[string]CoordFileTouch {
	if len(m) == 0 {
		return nil
	}
	type entry struct {
		path string
		at   int64
	}
	kept := make([]entry, 0, len(m))
	for p, t := range m {
		if p != "" && t.AtUnix >= cutoffUnix {
			kept = append(kept, entry{path: p, at: t.AtUnix})
		}
	}
	if len(kept) == 0 {
		return nil
	}
	if len(kept) > CoordMaxFilesPerSession {
		slices.SortFunc(kept, func(a, b entry) int {
			if c := cmp.Compare(b.at, a.at); c != 0 {
				return c
			}
			return cmp.Compare(a.path, b.path)
		})
		kept = kept[:CoordMaxFilesPerSession]
	}
	out := make(map[string]CoordFileTouch, len(kept))
	for _, e := range kept {
		out[e.path] = CoordFileTouch{AtUnix: e.at}
	}
	return out
}
