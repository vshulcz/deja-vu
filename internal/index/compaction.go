package index

import (
	"encoding/json"
	"errors"
	"io/fs"
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
	ErrCompactionStale     = errors.New("compaction capture is older than stored state")
	ErrCompactionIgnored   = errors.New("compaction source is excluded by policy")
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
	if readTombstones()[next.Harness+":"+next.SessionID] {
		return CompactionState{}, ErrCompactionForgotten
	}

	unlock, locked, err := tryLockDir(dir)
	if err != nil {
		return CompactionState{}, err
	}
	if !locked {
		return CompactionState{}, ErrCompactionBusy
	}
	defer unlock()

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
	if core.Compactions == nil {
		core.Compactions = map[string]CompactionState{}
	}
	key := compactionKey(next.SessionID, next.Workspace)
	if current, ok := core.Compactions[key]; ok {
		if current.Harness != "" && current.Harness != next.Harness {
			return CompactionState{}, errors.New("compaction session identity belongs to another harness")
		}
		if next.SourceDigest != "" && next.SourceDigest == current.SourceDigest {
			return current, nil
		}
		if compactionIsOlder(next, current) {
			return current, ErrCompactionStale
		}
		next.Revision = current.Revision + 1
	} else {
		next.Revision = 1
	}
	core.Compactions[key] = next
	trimCompactions(core.Compactions)
	if _, kept := core.Compactions[key]; !kept {
		// The input is necessarily one of the newest captures, so this can only
		// happen with a clock so malformed that preserving an older packet is
		// safer than saying the new packet was stored.
		return CompactionState{}, ErrCompactionStale
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return CompactionState{}, err
	}
	if err := writeGobAtomic(filepath.Join(dir, "manifest.gob"), core); err != nil {
		return CompactionState{}, err
	}
	invalidateManifestCache(dir)
	return next, nil
}

// Compaction retrieves exactly one native session/workspace packet. It reads
// only manifest.gob, not sessions.gob or records.bin, keeping the compaction
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
	core, err := readCompactionCore(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return CompactionState{}, false, nil
		}
		return CompactionState{}, false, err
	}
	state, ok := core.Compactions[compactionKey(sessionID, workspace)]
	if !ok || compactionExcluded(state) ||
		readTombstones()[state.Harness+":"+state.SessionID] {
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
	for key, state := range states {
		out[key] = state
	}
	return out
}

// compactionsForRebuild reads the core directly, so a first compaction stored
// before sessions.gob existed survives the first searchable-index build.
func compactionsForRebuild(dir string, dead map[string]bool) map[string]CompactionState {
	core, err := readCompactionCore(dir)
	if err != nil {
		return nil
	}
	states := cloneCompactions(core.Compactions)
	for key, state := range states {
		if dead[state.Harness+":"+state.SessionID] || compactionExcluded(state) {
			delete(states, key)
		}
	}
	return states
}

func compactionExcluded(state CompactionState) bool {
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
		keys = append(keys, state.Harness+":"+state.SessionID)
	}
	sort.Strings(keys)
	return keys
}

// compactionOnlyManifest recognizes exactly the minimal manifest PutCompaction
// creates before deja has indexed its first transcript. Forget needs to remove
// that packet, but a normal corrupted index must remain an error rather than
// being mistaken for an empty store.
func compactionOnlyManifest(dir string) (Manifest, bool) {
	core, err := readCompactionCore(dir)
	if err != nil || core.Version != 0 || len(core.Compactions) == 0 {
		return Manifest{}, false
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions.gob")); !errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, false
	}
	return Manifest{Version: 0, Compactions: cloneCompactions(core.Compactions), Sessions: map[string]SessionMeta{}}, true
}
