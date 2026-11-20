package index

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	compactionStateCap  = 32
	compactionStateSize = 24 << 10
)

var (
	// ErrCompactionBusy says a rebuild or another writer owns the index lock.
	// A compaction hook must not wait behind a full rebuild: it is about to let
	// its host discard context and has a short, best-effort deadline.
	ErrCompactionBusy = errors.New("compaction storage is busy")
	// ErrCompactionStale says a delayed hook tried to replace a newer capture
	// for the same native session and workspace.
	ErrCompactionStale = errors.New("compaction capture is older than stored state")
	// ErrCompactionIgnored says the capture's source is excluded by policy.
	ErrCompactionIgnored = errors.New("compaction source is excluded by policy")
	// ErrCompactionForgotten says the capture's session was forgotten.
	ErrCompactionForgotten = errors.New("compaction session was forgotten")
)

// CompactionState is the durable, bounded continuation packet for one native
// session in one workspace. The packet is stored in manifest.gob only; it is
// never turned into a synthetic searchable session or a record-log row.
type CompactionState struct {
	SessionID      string `json:"session_id"`
	Harness        string `json:"harness"`
	Workspace      string `json:"workspace"`
	Project        string `json:"project,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	SourceDigest   string `json:"source_digest"`
	// SourceHeaderFingerprint detects a rewritten transcript whose tail has
	// grown past the previous offset. The source reader bounds this hash to
	// native header bytes; it is not transcript content.
	SourceHeaderFingerprint   string                  `json:"source_header_fingerprint,omitempty"`
	SourceBoundaryFingerprint string                  `json:"source_boundary_fingerprint,omitempty"`
	SourceSize                int64                   `json:"source_size,omitempty"`
	SourceMTime               time.Time               `json:"source_mtime,omitzero"`
	CapturedAt                time.Time               `json:"captured_at"`
	Revision                  uint64                  `json:"revision"`
	LastToolCallID            string                  `json:"last_tool_call_id,omitempty"`
	Data                      model.CompactionContext `json:"data"`
}

// Key returns the compaction state's canonical "harness:session_id" composite key.
func (s CompactionState) Key() string { return model.SessionKey(s.Harness, s.SessionID) }

// PutCompaction stores a redacted continuation packet. It uses the index lock
// without waiting so a host can compact normally while an index rebuild is in
// progress. The newest source snapshot wins; a duplicate source digest keeps
// the existing revision unchanged.
func PutCompaction(dir string, next CompactionState) (CompactionState, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if err := normalizeCompaction(&next); err != nil {
		return CompactionState{}, err
	}
	if compactionExcluded(next) {
		return CompactionState{}, ErrCompactionIgnored
	}
	if readTombstones()[next.Key()] {
		return CompactionState{}, ErrCompactionForgotten
	}

	unlock, locked, err := tryLockDirFor(dir, 1500*time.Millisecond)
	if err != nil {
		return CompactionState{}, err
	}
	if !locked {
		return putCompactionSidecar(dir, next)
	}
	defer unlock()

	var slocked bool
	if _, statErr := os.Stat(compactionSidecarPath(dir)); statErr == nil || !errors.Is(statErr, fs.ErrNotExist) {
		sunlock, ok, serr := tryLockSidecarFor(dir, 500*time.Millisecond)
		if serr != nil {
			return CompactionState{}, serr
		}
		if ok {
			slocked = true
			defer sunlock()
		}
	}

	core, err := readCompactionCore(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return CompactionState{}, err
		}
		// A compaction can happen before deja has built its first index. A
		// version-zero core keeps the packet without claiming that searchable
		// session data exists; Ensure will rebuild it on the next pass.
		core = manifestCore{Version: 0}
	}
	sidecar, sideErr := readCompactionSidecar(dir)
	if sideErr != nil {
		return CompactionState{}, sideErr
	}
	merged := mergeCompactionMaps(core.Compactions, sidecar)
	if merged == nil {
		merged = map[string]CompactionState{}
	}
	key := compactionKey(next.SessionID, next.Workspace)
	updated, changed, err := upsertCompactionState(merged, key, next)
	if err != nil {
		return updated, err
	}
	if !changed && len(sidecar) == 0 {
		return updated, nil
	}
	if changed {
		merged[key] = updated
		trimCompactions(merged)
		if _, kept := merged[key]; !kept {
			// The input is necessarily one of the newest captures, so this can only
			// happen with a clock so malformed that preserving an older packet is
			// safer than saying the new packet was stored.
			return CompactionState{}, ErrCompactionStale
		}
	} else {
		trimCompactions(merged)
	}
	core.Compactions = merged
	if err := mkdirIndexAll(dir); err != nil {
		return CompactionState{}, err
	}
	if err := writeGobAtomic(filepath.Join(dir, "manifest.gob"), core); err != nil {
		return CompactionState{}, err
	}
	invalidateManifestCache(dir)
	if slocked && len(sidecar) > 0 {
		_ = os.Remove(compactionSidecarPath(dir))
	}
	return updated, nil
}

// Compaction retrieves exactly one native session/workspace packet. It reads
// only manifest.gob (and any busy-rebuild sidecar), keeping the compaction
// continuation path independent of index size.
func Compaction(dir, sessionID, workspace string) (CompactionState, bool, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	workspace, err := canonicalCompactionWorkspace(workspace)
	if err != nil {
		return CompactionState{}, false, err
	}
	if strings.TrimSpace(sessionID) == "" || strings.ContainsRune(sessionID, '\x00') {
		return CompactionState{}, false, nil
	}
	states, err := readMergedCompactions(dir)
	if err != nil {
		return CompactionState{}, false, err
	}
	state, ok := states[compactionKey(sessionID, workspace)]
	if !ok || compactionExcluded(state) ||
		readTombstones()[state.Key()] {
		return CompactionState{}, false, nil
	}
	return state, true, nil
}

func normalizeCompaction(next *CompactionState) error {
	if next == nil || strings.TrimSpace(next.SessionID) == "" || strings.TrimSpace(next.Harness) == "" {
		return errors.New("compaction requires a native session id and harness")
	}
	if strings.ContainsRune(next.SessionID, '\x00') || strings.ContainsRune(next.Harness, '\x00') {
		return errors.New("compaction identity contains a NUL byte")
	}
	workspace, err := canonicalCompactionWorkspace(next.Workspace)
	if err != nil {
		return err
	}
	next.Workspace = workspace
	next.Data = digest.RedactCompactionContext(next.Data)
	b, err := json.Marshal(next.Data)
	if err != nil {
		return err
	}
	if len(b) > compactionStateSize {
		return errors.New("compaction context exceeds 24 KiB")
	}
	if next.CapturedAt.IsZero() {
		next.CapturedAt = time.Now().UTC()
	}
	// The retention limit is for a complete persisted packet. Metadata is
	// supplied by an untrusted hook payload, so bounding only Data would leave
	// transcript paths or ids able to evade the intended 24 KiB ceiling.
	b, err = json.Marshal(*next)
	if err != nil {
		return err
	}
	if len(b) > compactionStateSize {
		return errors.New("compaction state exceeds 24 KiB")
	}
	return nil
}

func canonicalCompactionWorkspace(workspace string) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || strings.ContainsRune(workspace, '\x00') {
		return "", errors.New("compaction requires a workspace")
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func compactionKey(sessionID, workspace string) string { return sessionID + "\x00" + workspace }

func readCompactionCore(dir string) (manifestCore, error) {
	var core manifestCore
	err := readGob(filepath.Join(dir, "manifest.gob"), &core)
	return core, err
}

func compactionIsOlder(next, current CompactionState) bool {
	if !next.CapturedAt.IsZero() && !current.CapturedAt.IsZero() {
		if next.CapturedAt.Before(current.CapturedAt) {
			return true
		}
		if next.CapturedAt.After(current.CapturedAt) {
			return false
		}
	}
	return !next.SourceMTime.IsZero() && !current.SourceMTime.IsZero() && next.SourceMTime.Before(current.SourceMTime)
}

func trimCompactions(states map[string]CompactionState) {
	if len(states) <= compactionStateCap {
		return
	}
	type entry struct {
		key string
		at  time.Time
	}
	all := make([]entry, 0, len(states))
	for key, state := range states {
		all = append(all, entry{key: key, at: state.CapturedAt})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].at.Equal(all[j].at) {
			return all[i].key < all[j].key
		}
		return all[i].at.Before(all[j].at)
	})
	for _, entry := range all[:len(all)-compactionStateCap] {
		delete(states, entry.key)
	}
}

func cloneCompactions(states map[string]CompactionState) map[string]CompactionState {
	if len(states) == 0 {
		return nil
	}
	out := make(map[string]CompactionState, len(states))
	maps.Copy(out, states)
	return out
}

// compactionsForRebuild reads the core and busy sidecar directly, so a
// compaction captured before sessions.gob existed or while a rebuild held the
// index lock survives the next searchable-index build.
func compactionsForRebuild(dir string, dead map[string]bool) map[string]CompactionState {
	states, err := readMergedCompactions(dir)
	if err != nil || len(states) == 0 {
		return nil
	}
	for key, state := range states {
		if dead[state.Key()] || compactionExcluded(state) {
			delete(states, key)
		}
	}
	return states
}

func compactionExcluded(state CompactionState) bool {
	if sources.HarnessExcluded(state.Harness) {
		return true
	}
	if policy.Load().Ignored(state.TranscriptPath, state.Workspace) {
		return true
	}
	return sources.ExcludedProject(state.Project) || sources.ExcludedProject(state.Workspace)
}

func compactionMatches(state CompactionState, o ForgetOptions, exact string) bool {
	if exact != "" && state.SessionID != exact {
		return false
	}
	return sessionMatches(SessionMeta{ID: state.SessionID, Harness: state.Harness, Project: state.Project, Updated: state.CapturedAt}, o)
}

func removeMatchingCompactions(m *Manifest, o ForgetOptions, exact string) []string {
	if m == nil || len(m.Compactions) == 0 {
		return nil
	}
	var keys []string
	for key, state := range m.Compactions {
		if !compactionMatches(state, o, exact) {
			continue
		}
		delete(m.Compactions, key)
		keys = append(keys, state.Key())
	}
	sort.Strings(keys)
	return keys
}

// compactionOnlyManifest recognizes exactly the minimal manifest or sidecar
// state PutCompaction creates before deja has indexed its first transcript.
// Forget needs to remove that packet, but a normal corrupted index must remain
// an error rather than being mistaken for an empty store.
func compactionOnlyManifest(dir string) (Manifest, bool) {
	core, coreErr := readCompactionCore(dir)
	if coreErr != nil && !errors.Is(coreErr, fs.ErrNotExist) {
		return Manifest{}, false
	}
	if coreErr == nil && core.Version != 0 {
		return Manifest{}, false
	}
	sidecar, sideErr := readCompactionSidecar(dir)
	if sideErr != nil {
		return Manifest{}, false
	}
	merged := mergeCompactionMaps(core.Compactions, sidecar)
	if len(merged) == 0 {
		return Manifest{}, false
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions.gob")); !errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, false
	}
	return Manifest{Version: 0, Compactions: merged, Sessions: map[string]SessionMeta{}}, true
}

func compactionSidecarPath(dir string) string { return dir + ".compactions.gob" }

func compactionSidecarLockPath(dir string) string { return dir + ".compactions.lock" }

func tryLockSidecarFor(dir string, patience time.Duration) (func(), bool, error) {
	lockPath := compactionSidecarLockPath(dir)
	deadline := time.Now().Add(patience)
	for {
		unlock, ok, err := tryLockWriteFile(lockPath)
		if ok || err != nil || !time.Now().Before(deadline) {
			return unlock, ok, err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func readCompactionSidecar(dir string) (map[string]CompactionState, error) {
	var states map[string]CompactionState
	if err := readGob(compactionSidecarPath(dir), &states); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]CompactionState{}, nil
		}
		return nil, err
	}
	return states, nil
}

func compactionSidecarNewer(cand, cur CompactionState) bool {
	if compactionIsOlder(cand, cur) {
		return false
	}
	if compactionIsOlder(cur, cand) {
		return true
	}
	return cand.Revision > cur.Revision
}

func mergeCompactionMaps(base, extra map[string]CompactionState) map[string]CompactionState {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := cloneCompactions(base)
	if out == nil {
		out = make(map[string]CompactionState, len(extra))
	}
	for key, cand := range extra {
		if cur, ok := out[key]; !ok || compactionSidecarNewer(cand, cur) {
			out[key] = cand
		}
	}
	return out
}

func readMergedCompactions(dir string) (map[string]CompactionState, error) {
	core, err := readCompactionCore(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	sidecar, sideErr := readCompactionSidecar(dir)
	if sideErr != nil {
		return nil, sideErr
	}
	return mergeCompactionMaps(core.Compactions, sidecar), nil
}

func upsertCompactionState(states map[string]CompactionState, key string, next CompactionState) (CompactionState, bool, error) {
	if current, ok := states[key]; ok {
		if current.Harness != "" && current.Harness != next.Harness {
			return CompactionState{}, false, errors.New("compaction session identity belongs to another harness")
		}
		if next.SourceDigest != "" && next.SourceDigest == current.SourceDigest {
			return current, false, nil
		}
		if compactionIsOlder(next, current) {
			return current, false, ErrCompactionStale
		}
		next.Revision = current.Revision + 1
	} else {
		next.Revision = 1
	}
	return next, true, nil
}

func putCompactionSidecar(dir string, next CompactionState) (CompactionState, error) {
	unlock, locked, err := tryLockSidecarFor(dir, 1500*time.Millisecond)
	if err != nil {
		return CompactionState{}, err
	}
	if !locked {
		return CompactionState{}, ErrCompactionBusy
	}
	defer unlock()

	core, _ := readCompactionCore(dir)
	sidecar, sideErr := readCompactionSidecar(dir)
	if sideErr != nil {
		return CompactionState{}, sideErr
	}
	if sidecar == nil {
		sidecar = map[string]CompactionState{}
	}
	merged := mergeCompactionMaps(core.Compactions, sidecar)
	key := compactionKey(next.SessionID, next.Workspace)
	updated, changed, err := upsertCompactionState(merged, key, next)
	if err != nil {
		return updated, err
	}
	if !changed {
		return updated, nil
	}
	sidecar[key] = updated
	trimCompactions(sidecar)
	if _, kept := sidecar[key]; !kept {
		return CompactionState{}, ErrCompactionStale
	}
	path := compactionSidecarPath(dir)
	if err := mkdirIndexAll(filepath.Dir(path)); err != nil {
		return CompactionState{}, err
	}
	if err := writeGobAtomic(path, sidecar); err != nil {
		return CompactionState{}, err
	}
	return updated, nil
}

func forgetCompactionSidecar(dir string, o ForgetOptions, exact string) error {
	pre, err := readCompactionSidecar(dir)
	if err != nil || len(pre) == 0 {
		return err
	}
	if len(removeMatchingCompactions(&Manifest{Compactions: cloneCompactions(pre)}, o, exact)) == 0 {
		return nil
	}

	unlock, locked, err := tryLockSidecarFor(dir, 1500*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return ErrCompactionBusy
	}
	defer unlock()

	sidecar, err := readCompactionSidecar(dir)
	if err != nil || len(sidecar) == 0 {
		return err
	}
	m := Manifest{Compactions: sidecar}
	removed := removeMatchingCompactions(&m, o, exact)
	if len(removed) == 0 {
		return nil
	}
	path := compactionSidecarPath(dir)
	if len(m.Compactions) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeGobAtomic(path, m.Compactions)
}
