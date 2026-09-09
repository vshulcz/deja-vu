// Package ctxcache implements Deja's local, agent-independent working-context
// cache. It deliberately has no dependency on the historical search index:
// resume is a filesystem cache operation, while lookup remains an explicit
// fallback owned by the caller.
package ctxcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Identity struct {
	WorkspaceID    string `json:"workspace_id"`
	Repository     string `json:"repository"`
	RepositoryRoot string `json:"repository_root"`
	Worktree       string `json:"worktree"`
	Branch         string `json:"branch,omitempty"`
	GitHead        string `json:"git_head,omitempty"`
	WorktreeState  string `json:"worktree_state,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	// ComponentVersions are authoritative version markers supplied by local
	// adapters. They are freshness inputs, not cache-key inputs.
	ComponentVersions map[string]string `json:"component_versions,omitempty"`
}

type Item struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	Status     string   `json:"status,omitempty"`
	Durability string   `json:"durability,omitempty"`
	Source     string   `json:"source"`
	Supports   []string `json:"supports,omitempty"`
}

type Gap struct {
	Subject       string `json:"subject"`
	Severity      string `json:"severity"`
	Reason        string `json:"reason,omitempty"`
	RetrievalHint string `json:"retrieval_hint,omitempty"`
	Source        string `json:"source,omitempty"`
}

type Conflict struct {
	Subject    string   `json:"subject"`
	Candidates []string `json:"candidates"`
	Resolution string   `json:"resolution"`
	Reason     string   `json:"reason,omitempty"`
	Source     string   `json:"source,omitempty"`
}

// Source describes a local, authoritative materializer. Its file must be a
// strict State JSON document whose populated fields belong to Layer. Keeping
// the descriptor in the checkpoint makes materialization reproducible without
// turning resume into a network retrieval operation.
type Source struct {
	Name    string `json:"name"`
	Layer   string `json:"layer"`
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
}

type State struct {
	Objective     string            `json:"objective,omitempty"`
	Status        string            `json:"status,omitempty"`
	Project       map[string]any    `json:"project,omitempty"`
	ProjectSource string            `json:"project_source,omitempty"`
	ProjectItems  []Item            `json:"project_items,omitempty"`
	Confirmed     []Item            `json:"confirmed,omitempty"`
	Implemented   []Item            `json:"implemented,omitempty"`
	Failing       []Item            `json:"failing,omitempty"`
	Unknown       []Item            `json:"unknown,omitempty"`
	Decisions     []Item            `json:"decisions,omitempty"`
	Tests         map[string]any    `json:"tests,omitempty"`
	TestsSource   string            `json:"tests_source,omitempty"`
	NextActions   []Item            `json:"next_actions,omitempty"`
	Session       []Item            `json:"session,omitempty"`
	Evidence      []Item            `json:"evidence,omitempty"`
	Gaps          []Gap             `json:"gaps,omitempty"`
	Conflicts     []Conflict        `json:"conflicts,omitempty"`
	Sources       []Source          `json:"sources,omitempty"`
	Promoted      map[string]string `json:"promoted,omitempty"`
}

type Freshness struct {
	SnapshotVersion   uint64            `json:"snapshot_version"`
	GitHead           string            `json:"git_head,omitempty"`
	WorktreeState     string            `json:"worktree_state,omitempty"`
	Checkpoint        uint64            `json:"checkpoint_version"`
	ComponentVersions map[string]string `json:"component_versions,omitempty"`
	GeneratedAt       time.Time         `json:"generated_at"`
}

type Snapshot struct {
	ID          string    `json:"snapshot_id"`
	Fingerprint string    `json:"fingerprint"`
	Identity    Identity  `json:"identity"`
	State       State     `json:"state"`
	Freshness   Freshness `json:"freshness"`
	Invalid     []string  `json:"invalid_layers,omitempty"`
}

type ResumeResult struct {
	CacheStatus string   `json:"cache_status"`
	Snapshot    Snapshot `json:"context"`
	Truncated   bool     `json:"truncated,omitempty"`
	// TokenBudget is internal metadata used by EncodeResume to protect callers
	// that accidentally re-encode a packet after trimming.
	TokenBudget int `json:"-"`
}

type Status struct {
	Status          string   `json:"status"`
	SnapshotID      string   `json:"snapshot_id,omitempty"`
	Fingerprint     string   `json:"fingerprint,omitempty"`
	Changed         []string `json:"changed,omitempty"`
	RefreshRequired []string `json:"refresh_required,omitempty"`
	Metrics         Metrics  `json:"metrics"`
}

type Metrics struct {
	ResumeTotal             uint64 `json:"ctx_resume_total"`
	ResumeCacheHits         uint64 `json:"ctx_resume_cache_hit_total"`
	ResumeCacheMisses       uint64 `json:"ctx_resume_cache_miss_total"`
	RefreshTotal            uint64 `json:"ctx_refresh_total"`
	CheckpointTotal         uint64 `json:"ctx_checkpoint_total"`
	LookupTotal             uint64 `json:"ctx_lookup_total"`
	GapTotal                uint64 `json:"ctx_gap_total"`
	ConflictTotal           uint64 `json:"ctx_conflict_total"`
	ResumeLatencyMS         uint64 `json:"ctx_resume_latency_ms_total"`
	RefreshLatencyMS        uint64 `json:"ctx_refresh_latency_ms_total"`
	RefreshIncrementalTotal uint64 `json:"ctx_refresh_incremental_total"`
	RefreshFullTotal        uint64 `json:"ctx_refresh_full_total"`
	SnapshotBytesTotal      uint64 `json:"ctx_snapshot_bytes_total"`
	PacketBytesTotal        uint64 `json:"ctx_packet_bytes_total"`
	PacketTokensTotal       uint64 `json:"ctx_packet_tokens_total"`
}

type Change struct {
	Kind    string `json:"kind"`
	Section string `json:"section"`
	ID      string `json:"id"`
	Text    string `json:"text"`
}

type RefreshResult struct {
	PreviousSnapshot string   `json:"previous_snapshot,omitempty"`
	NewSnapshot      string   `json:"new_snapshot"`
	DetectedChanges  []string `json:"detected_changes,omitempty"`
	RefreshedLayers  []string `json:"refreshed_layers,omitempty"`
	Snapshot         Snapshot `json:"context"`
}

type Explanation struct {
	Item          Item      `json:"item,omitempty"`
	Section       string    `json:"section"`
	SnapshotID    string    `json:"snapshot_id"`
	Identity      Identity  `json:"identity"`
	Freshness     Freshness `json:"freshness"`
	InvalidLayers []string  `json:"invalid_layers,omitempty"`
	Why           string    `json:"why"`
}

func Root(indexDir string) string {
	if d := os.Getenv("DEJA_CTX_DIR"); d != "" {
		return d
	}
	return filepath.Join(filepath.Dir(indexDir), "ctx")
}

func ResolveIdentity(workspace, task string) (Identity, error) {
	if workspace == "" {
		workspace = "."
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return Identity{}, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = real
	}
	root := git(abs, "rev-parse", "--show-toplevel")
	if root == "" {
		root = abs
	}
	remote := git(root, "config", "--get", "remote.origin.url")
	if remote == "" {
		remote = root
	}
	id := Identity{Repository: remote, RepositoryRoot: root, Worktree: abs, Branch: git(root, "branch", "--show-current"), GitHead: git(root, "rev-parse", "HEAD"), WorktreeState: worktreeDigest(root), TaskID: task, ProjectID: filepath.Base(root)}
	h := sha256.Sum256([]byte(remote + "\x00" + root))
	id.WorkspaceID = hex.EncodeToString(h[:8])
	return id, nil
}

func git(dir string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitBytes(dir string, args ...string) []byte {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return nil
	}
	return out
}

// worktreeDigest includes the actual tracked diff and the names and bytes of
// untracked files. Porcelain alone records only that a file is modified, which
// made repeated edits to an already-dirty file invisible to freshness checks.
func worktreeDigest(root string) string {
	h := sha256.New()
	_, _ = h.Write(gitBytes(root, "status", "--porcelain=v1", "-z", "--untracked-files=all"))
	// Keep index and worktree diffs separate. `git diff HEAD` fails before the
	// first commit, which used to make staged dirty-to-dirty transitions in an
	// unborn repository invisible.
	_, _ = h.Write([]byte("\x00unstaged\x00"))
	_, _ = h.Write(gitBytes(root, "diff", "--no-ext-diff", "--binary"))
	_, _ = h.Write([]byte("\x00staged\x00"))
	_, _ = h.Write(gitBytes(root, "diff", "--cached", "--no-ext-diff", "--binary"))
	for _, name := range strings.Split(string(gitBytes(root, "ls-files", "--others", "--exclude-standard", "-z")), "\x00") {
		if name == "" {
			continue
		}
		// Git paths are repository-relative. Do not follow an untracked symlink
		// out of the workspace; hash the link itself instead.
		path := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil || info.IsDir() {
			continue
		}
		_, _ = h.Write([]byte(name))
		if info.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Readlink(path); err == nil {
				_, _ = h.Write([]byte(target))
			}
			continue
		}
		if b, err := os.ReadFile(path); err == nil {
			_, _ = h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ResolveIdentityWithVersions adds optional local-adapter freshness markers to
// the normal repository identity. It is intentionally a wrapper rather than a
// new required ResolveIdentity argument so existing clients retain cache reuse.
func ResolveIdentityWithVersions(workspace, task string, versions map[string]string) (Identity, error) {
	id, err := ResolveIdentity(workspace, task)
	if err != nil {
		return Identity{}, err
	}
	id.ComponentVersions = copyStrings(versions)
	return id, nil
}

func fingerprint(id Identity) string {
	h := sha256.Sum256([]byte(strings.Join([]string{id.WorkspaceID, id.Repository, id.Branch, id.TaskID, id.ProjectID}, "\x00")))
	return hex.EncodeToString(h[:])
}

func key(id Identity) string {
	// Branch is freshness, not cache identity: keeping it out of the pointer key
	// lets a branch switch reuse project state and refresh only task/Git layers.
	h := sha256.Sum256([]byte(id.WorkspaceID + "\x00" + id.TaskID))
	return hex.EncodeToString(h[:12])
}
func currentPath(root string, id Identity) string {
	return filepath.Join(root, "current", key(id)+".json")
}

func Load(root string, id Identity) (Snapshot, error) {
	b, err := os.ReadFile(currentPath(root, id))
	if err != nil {
		return Snapshot{}, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return Snapshot{}, fmt.Errorf("decode context snapshot: %w", err)
	}
	return s, nil
}

func save(root string, s Snapshot) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Join(root, "current"), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "snapshots"), 0700); err != nil {
		return err
	}
	history := filepath.Join(root, "snapshots", s.ID+".json")
	if err := writeImmutable(history, b); err != nil {
		return err
	}
	committed := false
	defer func() {
		// A returned write failure is not a checkpoint. Remove the object so a
		// later retry cannot inherit its version or expose it in history. A
		// process crash remains safe because the current pointer was never
		// replaced; readers continue to use the prior complete pointer.
		if !committed {
			_ = os.Remove(history)
		}
	}()
	tmp, err := os.CreateTemp(filepath.Join(root, "current"), ".snapshot-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, currentPath(root, s.Identity)); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	committed = true
	// A directory sync makes the pointer rename durable where the platform
	// supports it. The immutable object is deliberately retained on failure:
	// losing history during pointer recovery is worse than an orphan snapshot.
	if d, err := os.Open(filepath.Join(root, "current")); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func writeImmutable(path string, b []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmpPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("immutable snapshot already exists: %w", err)
		}
		return err
	}
	return nil
}

var localLocks sync.Map // map[string]*sync.Mutex

func lock(root string) (func(), error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	v, _ := localLocks.LoadOrStore(root, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	path := filepath.Join(root, ".write.lock")
	f, err := lockFile(path)
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	// The OS lock is released if this process crashes, unlike a create-
	// exclusively lock file. The process-local mutex covers platforms where
	// advisory locks are shared between descriptors in one process.
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
		mu.Unlock()
	}, nil
}

func Resume(root string, id Identity, budget int) (ResumeResult, error) {
	started := time.Now()
	if budget <= 0 {
		budget = 10000
	}
	s, err := Load(root, id)
	if errors.Is(err, os.ErrNotExist) {
		r := ResumeResult{CacheStatus: "miss", Snapshot: emptySnapshot(id)}
		r.TokenBudget = budget
		truncated, trimErr := trimResult(&r, budget)
		if trimErr != nil {
			return ResumeResult{}, trimErr
		}
		r.Truncated = truncated
		recordMetric(root, metricEvent{Kind: "resume_miss", LatencyMS: time.Since(started).Milliseconds(), Gaps: len(r.Snapshot.State.Gaps), Conflicts: len(r.Snapshot.State.Conflicts), PacketBytes: encodedLen(r), PacketTokens: encodedLen(r)})
		return r, nil
	}
	if err != nil {
		return ResumeResult{}, err
	}
	changed, err := detectChanges(s, id)
	if err != nil {
		return ResumeResult{}, err
	}
	status := "hit"
	if len(changed) > 0 || len(s.Invalid) > 0 {
		status = "stale"
	}
	sourceAvailabilityGaps(&s.State)
	r := ResumeResult{CacheStatus: status, Snapshot: s, TokenBudget: budget}
	truncated, err := trimResult(&r, budget)
	if err != nil {
		return ResumeResult{}, err
	}
	r.Truncated = truncated
	recordMetric(root, metricEvent{Kind: "resume_" + status, LatencyMS: time.Since(started).Milliseconds(), Gaps: len(r.Snapshot.State.Gaps), Conflicts: len(r.Snapshot.State.Conflicts), PacketBytes: encodedLen(r), PacketTokens: encodedLen(r)})
	return r, nil
}

func emptySnapshot(id Identity) Snapshot {
	return Snapshot{Fingerprint: fingerprint(id), Identity: id, State: State{Status: "unknown", Gaps: []Gap{{Subject: "working context", Severity: "required", Reason: "no local checkpoint exists", RetrievalHint: "checkpoint durable state or use historical lookup"}}}}
}

func Checkpoint(root string, id Identity, state State) (Snapshot, error) {
	unlock, err := lock(root)
	if err != nil {
		return Snapshot{}, err
	}
	defer unlock()
	return checkpointLocked(root, id, state)
}

func checkpointLocked(root string, id Identity, state State) (Snapshot, error) {
	return checkpointStateLocked(root, id, state, true)
}

func checkpointStateLocked(root string, id Identity, state State, materialize bool) (Snapshot, error) {
	var err error
	if materialize {
		state, err = materializeSources(state)
		if err != nil {
			return Snapshot{}, err
		}
	}
	state = reconcileGenericConflicts(normalize(state))
	if err := validateState(state); err != nil {
		return Snapshot{}, err
	}
	previous, err := Load(root, id)
	if errors.Is(err, os.ErrNotExist) {
		previous = Snapshot{}
	} else if err != nil {
		return Snapshot{}, fmt.Errorf("load previous checkpoint: %w", err)
	}
	version := previous.Freshness.SnapshotVersion + 1
	checkpoint := previous.Freshness.Checkpoint + 1
	now := time.Now().UTC()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", key(id), version, now.UnixNano())))
	s := Snapshot{ID: hex.EncodeToString(sum[:12]), Fingerprint: fingerprint(id), Identity: id, State: state, Freshness: Freshness{SnapshotVersion: version, GitHead: id.GitHead, WorktreeState: id.WorktreeState, Checkpoint: checkpoint, ComponentVersions: copyStrings(id.ComponentVersions), GeneratedAt: now}}
	stampCheckpointProvenance(&s)
	err = save(root, s)
	if err == nil {
		recordMetric(root, metricEvent{Kind: "checkpoint", Gaps: len(s.State.Gaps), Conflicts: len(s.State.Conflicts), SnapshotBytes: snapshotLen(s)})
	}
	return s, err
}

func validateState(s State) error {
	seen := map[string]bool{}
	sets := map[string][]Item{"project": s.ProjectItems, "confirmed": s.Confirmed, "implemented": s.Implemented, "failing": s.Failing, "unknown": s.Unknown, "decisions": s.Decisions, "next_actions": s.NextActions, "session": s.Session, "evidence": s.Evidence}
	for section, items := range sets {
		for _, item := range items {
			if strings.TrimSpace(item.Text) == "" {
				return fmt.Errorf("%s item %q needs text", section, item.ID)
			}
			if item.ID != "" && seen[item.ID] {
				return fmt.Errorf("duplicate context item id %q", item.ID)
			}
			seen[item.ID] = item.ID != ""
		}
	}
	for _, gap := range s.Gaps {
		if strings.TrimSpace(gap.Subject) == "" || strings.TrimSpace(gap.Severity) == "" {
			return fmt.Errorf("each gap needs subject and severity")
		}
	}
	for _, conflict := range s.Conflicts {
		if strings.TrimSpace(conflict.Subject) == "" || len(conflict.Candidates) < 2 || strings.TrimSpace(conflict.Resolution) == "" {
			return fmt.Errorf("each conflict needs a subject, at least two candidates, and a resolution")
		}
		seenCandidates := map[string]bool{}
		for _, candidate := range conflict.Candidates {
			if strings.TrimSpace(candidate) == "" || seenCandidates[candidate] {
				return fmt.Errorf("conflict %q has empty or duplicate candidates", conflict.Subject)
			}
			seenCandidates[candidate] = true
		}
		if conflict.Resolution != "unresolved" && !seenCandidates[conflict.Resolution] {
			return fmt.Errorf("conflict %q resolution %q is not a candidate", conflict.Subject, conflict.Resolution)
		}
	}
	seenSources := map[string]bool{}
	seenSourceLayers := map[string]bool{}
	for _, source := range s.Sources {
		if err := validateSource(source); err != nil {
			return err
		}
		if seenSources[source.Name] {
			return fmt.Errorf("duplicate context source %q", source.Name)
		}
		seenSources[source.Name] = true
		if seenSourceLayers[source.Layer] {
			return fmt.Errorf("multiple context sources own layer %q", source.Layer)
		}
		seenSourceLayers[source.Layer] = true
	}
	return nil
}

// RefreshDetailed atomically identifies local deltas and writes their resulting
// snapshot. RefreshedLayers are only the layers actually materialized from a
// configured local source; metadata-only component changes remain validation
// gaps and are never reported as refreshed context.
func RefreshDetailed(root string, id Identity) (RefreshResult, error) {
	started := time.Now()
	unlock, lockErr := lock(root)
	if lockErr != nil {
		return RefreshResult{}, lockErr
	}
	defer unlock()
	s, err := Load(root, id)
	if errors.Is(err, os.ErrNotExist) {
		s = emptySnapshot(id)
	} else if err != nil {
		return RefreshResult{}, err
	}
	previousID := s.ID
	c, err := detectChanges(s, id)
	if err != nil {
		return RefreshResult{}, err
	}
	previousHead := s.Identity.GitHead
	refreshed, unavailable := refreshSources(&s.State)
	s.Identity = id
	s.Fingerprint = fingerprint(id)
	s.Freshness.SnapshotVersion++
	s.Freshness.GitHead = id.GitHead
	s.Freshness.WorktreeState = id.WorktreeState
	s.Freshness.ComponentVersions = copyStrings(id.ComponentVersions)
	s.Freshness.GeneratedAt = time.Now().UTC()
	if contains(c, "git") || contains(c, "worktree") || contains(c, "task") {
		s.State.Gaps = upsertGap(s.State.Gaps, Gap{Subject: "working state after repository change", Severity: "required", Reason: fmt.Sprintf("repository state changed from %s to %s; Git freshness was updated but checkpoint conclusions require agent validation", short(previousHead), short(id.GitHead)), RetrievalHint: "inspect the local diff, then checkpoint confirmed task state", Source: "git://local"})
	}
	for _, layer := range s.Invalid {
		s.State.Gaps = upsertGap(s.State.Gaps, Gap{Subject: "invalidated " + layer + " context", Severity: "required", Reason: "this layer was explicitly invalidated and has not been replaced by a validated checkpoint", RetrievalHint: "validate this layer, then checkpoint durable state", Source: "checkpoint://local"})
	}
	for _, component := range componentChanges(c) {
		if !contains(refreshed, component) {
			s.State.Gaps = upsertGap(s.State.Gaps, Gap{Subject: "changed " + component + " context", Severity: "required", Reason: "the authoritative component version changed but no local source adapter materialized it", RetrievalHint: "load and validate the authoritative source, then checkpoint", Source: "component://" + component})
		}
	}
	// A restored source can have the same digest as the earlier materialization.
	// Clear its recovery gap even when there was no content delta to apply.
	sourceAvailabilityGaps(&s.State)
	for _, source := range unavailable {
		s.State.Gaps = upsertGap(s.State.Gaps, Gap{Subject: "source " + source.Name + " unavailable", Severity: "required", Reason: source.Reason, RetrievalHint: "restore or repair the source file, then refresh", Source: "file://" + filepath.ToSlash(source.Path)})
	}
	s.State = reconcileGenericConflicts(normalize(s.State))
	if err := validateState(s.State); err != nil {
		return RefreshResult{}, fmt.Errorf("validate refreshed context: %w", err)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", key(id), s.Freshness.SnapshotVersion, s.Freshness.GeneratedAt.UnixNano())))
	s.ID = hex.EncodeToString(sum[:12])
	err = save(root, s)
	if err == nil {
		mode := "incremental"
		if previousID == "" {
			mode = "full"
		}
		recordMetric(root, metricEvent{Kind: "refresh", RefreshMode: mode, LatencyMS: time.Since(started).Milliseconds(), Gaps: len(s.State.Gaps), Conflicts: len(s.State.Conflicts), SnapshotBytes: snapshotLen(s)})
	}
	return RefreshResult{PreviousSnapshot: previousID, NewSnapshot: s.ID, DetectedChanges: c, RefreshedLayers: refreshed, Snapshot: s}, err
}

// Refresh is retained for existing callers. New callers should use
// RefreshDetailed so the previous/new pointer report cannot race a pre-lock
// Load call.
func Refresh(root string, id Identity) (Snapshot, []string, error) {
	r, err := RefreshDetailed(root, id)
	return r.Snapshot, r.DetectedChanges, err
}

func upsertGap(gaps []Gap, gap Gap) []Gap {
	for i := range gaps {
		// The same summary can legitimately describe two authorities. Keep a
		// user checkpoint blocker when a local materializer adds its own gap.
		if gaps[i].Subject == gap.Subject && gaps[i].Source == gap.Source {
			gaps[i] = gap
			return gaps
		}
	}
	return append(gaps, gap)
}

func reconcileGenericConflicts(state State) State {
	const prefix = "unresolved conflict: "
	for i := range state.Conflicts {
		if state.Conflicts[i].Source == "" {
			state.Conflicts[i].Source = "checkpoint://local"
		}
	}
	filtered := state.Gaps[:0]
	for _, gap := range state.Gaps {
		if isGenericConflictGap(gap, state.Conflicts) {
			continue
		}
		filtered = append(filtered, gap)
	}
	state.Gaps = filtered
	for i := range state.Conflicts {
		conflict := &state.Conflicts[i]
		if conflict.Resolution == "unresolved" {
			state.Gaps = upsertGap(state.Gaps, Gap{Subject: prefix + conflict.Subject, Severity: "required", Reason: "context conflict has no safe automatic resolution", RetrievalHint: "obtain a scoped or explicitly superseding decision", Source: conflict.Source})
		}
	}
	return state
}

func isGenericConflictGap(gap Gap, conflicts []Conflict) bool {
	const prefix = "unresolved conflict: "
	for _, conflict := range conflicts {
		// Match the conflict identity even after it has been resolved. The
		// reconciliation caller removes the old generated gap first, then adds
		// one back only if the current resolution is still unresolved.
		if gap.Subject == prefix+conflict.Subject && gap.Source == conflict.Source {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func short(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	if value == "" {
		return "none"
	}
	return value
}

func Inspect(root string, id Identity) (Status, error) {
	s, err := Load(root, id)
	if errors.Is(err, os.ErrNotExist) {
		return Status{Status: "missing", Changed: []string{"snapshot"}, RefreshRequired: []string{"task", "git"}, Metrics: ReadMetrics(root)}, nil
	}
	if err != nil {
		return Status{}, err
	}
	c, err := detectChanges(s, id)
	if err != nil {
		return Status{}, err
	}
	sourceAvailabilityGaps(&s.State)
	c = append(c, s.Invalid...)
	c = unique(c)
	st := Status{Status: "valid", SnapshotID: s.ID, Fingerprint: s.Fingerprint, Changed: c, Metrics: ReadMetrics(root)}
	if len(c) > 0 {
		st.Status = "stale"
		st.RefreshRequired = c
	}
	return st, nil
}

func detectChanges(s Snapshot, id Identity) ([]string, error) {
	var c []string
	if s.Identity.Branch != id.Branch {
		c = append(c, "task")
	}
	if s.Identity.GitHead != id.GitHead {
		c = append(c, "git")
	}
	if s.Identity.WorktreeState != id.WorktreeState {
		c = append(c, "worktree")
	}
	if s.Fingerprint != fingerprint(id) {
		c = append(c, "identity")
	}
	for name, version := range id.ComponentVersions {
		if s.Freshness.ComponentVersions[name] != version {
			c = append(c, "component:"+name)
		}
	}
	for name := range s.Freshness.ComponentVersions {
		if _, ok := id.ComponentVersions[name]; !ok {
			c = append(c, "component:"+name)
		}
	}
	for _, source := range s.State.Sources {
		version, err := sourceVersion(source)
		if err != nil {
			// Retain the last verified materialization and make the missing
			// authority explicit. Resume and status must never silently turn an
			// inaccessible source into a cache hit.
			c = append(c, "source:"+source.Name)
			continue
		}
		if source.Version != version {
			c = append(c, source.Layer)
		}
	}
	return unique(c), nil
}

func sourceAvailabilityGaps(state *State) {
	kept := state.Gaps[:0]
	for _, gap := range state.Gaps {
		// Only remove the recovery gaps that this materializer itself created.
		// A user checkpoint can legitimately describe a different blocker as
		// "source <name> unavailable"; its text alone is not ownership.
		if isSourceAvailabilityGap(gap, state.Sources) {
			continue
		}
		kept = append(kept, gap)
	}
	state.Gaps = kept
	for _, source := range state.Sources {
		if _, err := sourceVersion(source); err != nil {
			state.Gaps = upsertGap(state.Gaps, Gap{Subject: "source " + source.Name + " unavailable", Severity: "required", Reason: "the prior local source materialization was retained because its current file cannot be read", RetrievalHint: "restore or repair the source file, then refresh", Source: "file://" + filepath.ToSlash(source.Path)})
		}
	}
}

func isSourceAvailabilityGap(gap Gap, sources []Source) bool {
	for _, source := range sources {
		if gap.Subject == "source "+source.Name+" unavailable" && gap.Source == "file://"+filepath.ToSlash(source.Path) {
			return true
		}
	}
	return false
}

func componentChanges(changes []string) []string {
	var out []string
	for _, change := range changes {
		if strings.HasPrefix(change, "component:") {
			out = append(out, strings.TrimPrefix(change, "component:"))
		}
	}
	return unique(out)
}

func sourceVersion(source Source) (string, error) {
	if strings.TrimSpace(source.Path) == "" {
		return "", errors.New("source path is required")
	}
	b, err := os.ReadFile(source.Path)
	if err != nil {
		return "", err
	}
	return digestBytes(b), nil
}

func digestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func materializeSources(state State) (State, error) {
	for i := range state.Sources {
		source := &state.Sources[i]
		if err := validateSource(*source); err != nil {
			return State{}, err
		}
		b, err := os.ReadFile(source.Path)
		if err != nil {
			return State{}, fmt.Errorf("read context source %q: %w", source.Name, err)
		}
		loaded, err := DecodeState(b)
		if err != nil {
			return State{}, fmt.Errorf("decode context source %q: %w", source.Name, err)
		}
		if err := validateSourceState(source.Layer, loaded); err != nil {
			return State{}, fmt.Errorf("context source %q: %w", source.Name, err)
		}
		source.Version = digestBytes(b)
		applySourceLayer(&state, loaded, *source)
	}
	return state, nil
}

type unavailableSource struct{ Name, Path, Reason string }

func refreshSources(state *State) ([]string, []unavailableSource) {
	var refreshed []string
	var unavailable []unavailableSource
	for i := range state.Sources {
		source := &state.Sources[i]
		b, err := os.ReadFile(source.Path)
		if err != nil {
			unavailable = append(unavailable, unavailableSource{source.Name, source.Path, "the source file could not be read: " + err.Error()})
			continue
		}
		version := digestBytes(b)
		if source.Version == version {
			continue
		}
		loaded, err := DecodeState(b)
		if err != nil {
			unavailable = append(unavailable, unavailableSource{source.Name, source.Path, "the source file is malformed: " + err.Error()})
			continue
		}
		if err := validateSourceState(source.Layer, loaded); err != nil {
			unavailable = append(unavailable, unavailableSource{source.Name, source.Path, "the source owns fields outside its layer: " + err.Error()})
			continue
		}
		source.Version = version
		applySourceLayer(state, loaded, *source)
		refreshed = append(refreshed, source.Layer)
	}
	return unique(refreshed), unavailable
}

func validateSource(source Source) error {
	if strings.TrimSpace(source.Name) == "" || strings.TrimSpace(source.Path) == "" {
		return errors.New("each source needs name and path")
	}
	if !filepath.IsAbs(source.Path) || filepath.Clean(source.Path) != source.Path {
		return fmt.Errorf("source %q needs a clean absolute path", source.Name)
	}
	switch source.Layer {
	case "project", "task", "memory", "session":
		return nil
	default:
		return fmt.Errorf("source %q has unsupported layer %q", source.Name, source.Layer)
	}
}

func validateSourceState(layer string, state State) error {
	if len(state.Sources) > 0 {
		return errors.New("source documents cannot declare nested sources")
	}
	// Source documents have a single owner so a refresh cannot silently replace
	// another source's layer.
	switch layer {
	case "project":
		if !stateIsOnlyProject(state) {
			return errors.New("project source may only contain project")
		}
	case "task":
		if !stateIsOnlyTask(state) {
			return errors.New("task source contains fields owned by another layer")
		}
	case "memory":
		if state.Objective != "" || state.Status != "" || state.Project != nil || state.ProjectSource != "" || state.TestsSource != "" || len(state.ProjectItems)+len(state.Confirmed)+len(state.Implemented)+len(state.Failing)+len(state.Unknown)+len(state.Decisions)+len(state.NextActions)+len(state.Session)+len(state.Gaps)+len(state.Conflicts)+len(state.Promoted) != 0 || state.Tests != nil {
			return errors.New("memory source may only contain evidence")
		}
	case "session":
		if state.Objective != "" || state.Status != "" || state.Project != nil || state.ProjectSource != "" || state.TestsSource != "" || len(state.ProjectItems)+len(state.Confirmed)+len(state.Implemented)+len(state.Failing)+len(state.Unknown)+len(state.Decisions)+len(state.NextActions)+len(state.Evidence)+len(state.Gaps)+len(state.Conflicts)+len(state.Promoted) != 0 || state.Tests != nil {
			return errors.New("session source may only contain session")
		}
	}
	return nil
}

func stateIsOnlyProject(s State) bool {
	return s.Objective == "" && s.Status == "" && s.ProjectSource == "" && s.TestsSource == "" && len(s.Confirmed)+len(s.Implemented)+len(s.Failing)+len(s.Unknown)+len(s.Decisions)+len(s.NextActions)+len(s.Session)+len(s.Evidence)+len(s.Gaps)+len(s.Conflicts)+len(s.Promoted) == 0 && s.Tests == nil
}
func stateIsOnlyTask(s State) bool {
	return s.Project == nil && s.ProjectSource == "" && s.TestsSource == "" && len(s.ProjectItems)+len(s.Session)+len(s.Evidence)+len(s.Promoted) == 0
}

func applySourceLayer(dst *State, src State, source Source) {
	provenance := "file://" + filepath.ToSlash(source.Path) + "#sha256=" + source.Version
	src = filteredPromotions(src, dst.Promoted)
	switch source.Layer {
	case "project":
		dst.Project, dst.ProjectSource = src.Project, provenance
		dst.ProjectItems = mergePromoted(dst.ProjectItems, withSource(src.ProjectItems, provenance), dst.Promoted, "project")
	case "task":
		dst.Objective, dst.Status = src.Objective, src.Status
		dst.Confirmed = mergePromoted(dst.Confirmed, withSource(src.Confirmed, provenance), dst.Promoted, "confirmed")
		dst.Implemented = mergePromoted(dst.Implemented, withSource(src.Implemented, provenance), dst.Promoted, "implemented")
		dst.Failing = mergePromoted(dst.Failing, withSource(src.Failing, provenance), dst.Promoted, "failing")
		dst.Unknown = mergePromoted(dst.Unknown, withSource(src.Unknown, provenance), dst.Promoted, "unknown")
		dst.Decisions = mergePromoted(dst.Decisions, withSource(src.Decisions, provenance), dst.Promoted, "decisions")
		dst.Tests, dst.TestsSource = src.Tests, provenance
		dst.NextActions = mergePromoted(dst.NextActions, withSource(src.NextActions, provenance), dst.Promoted, "next_actions")
		dst.Gaps, dst.Conflicts = withGapSource(src.Gaps, provenance), withConflictSource(src.Conflicts, provenance)
	case "memory":
		dst.Evidence = mergePromoted(dst.Evidence, withSource(src.Evidence, provenance), dst.Promoted, "evidence")
	case "session":
		dst.Session = mergePromoted(dst.Session, withSource(src.Session, provenance), dst.Promoted, "session")
	}
}

func filteredPromotions(state State, promoted map[string]string) State {
	if len(promoted) == 0 {
		return state
	}
	keep := func(items []Item) []Item {
		out := items[:0]
		for _, item := range items {
			if _, ok := promoted[item.ID]; ok {
				continue
			}
			out = append(out, item)
		}
		return out
	}
	state.ProjectItems, state.Confirmed, state.Implemented, state.Failing, state.Unknown, state.Decisions = keep(state.ProjectItems), keep(state.Confirmed), keep(state.Implemented), keep(state.Failing), keep(state.Unknown), keep(state.Decisions)
	state.NextActions, state.Session, state.Evidence = keep(state.NextActions), keep(state.Session), keep(state.Evidence)
	return state
}

func mergePromoted(existing, incoming []Item, promoted map[string]string, destination string) []Item {
	byID := make(map[string]Item, len(existing)+len(incoming))
	for _, item := range incoming {
		byID[item.ID] = item
	}
	for _, item := range existing {
		if promoted[item.ID] == destination {
			byID[item.ID] = item
		}
	}
	out := make([]Item, 0, len(byID))
	for _, item := range byID {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func withSource(items []Item, source string) []Item {
	for i := range items {
		if items[i].Source == "" {
			items[i].Source = source
		}
	}
	return items
}
func withGapSource(gaps []Gap, source string) []Gap {
	for i := range gaps {
		if gaps[i].Source == "" {
			gaps[i].Source = source
		}
	}
	return gaps
}
func withConflictSource(conflicts []Conflict, source string) []Conflict {
	for i := range conflicts {
		if conflicts[i].Source == "" {
			conflicts[i].Source = source
		}
	}
	return conflicts
}

func Invalidate(root string, id Identity, layer string) error {
	unlock, err := lock(root)
	if err != nil {
		return err
	}
	defer unlock()
	s, err := Load(root, id)
	if err != nil {
		return err
	}
	if layer == "" {
		layer = "all"
	}
	s.Invalid = unique(append(s.Invalid, layer))
	s.Freshness.SnapshotVersion++
	s.Freshness.GeneratedAt = time.Now().UTC()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", key(id), s.Freshness.SnapshotVersion, s.Freshness.GeneratedAt.UnixNano())))
	s.ID = hex.EncodeToString(sum[:12])
	return save(root, s)
}

func History(root string, id Identity) ([]Snapshot, error) {
	entries, err := os.ReadDir(filepath.Join(root, "snapshots"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Snapshot
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, x := os.ReadFile(filepath.Join(root, "snapshots", e.Name()))
		if x != nil {
			return nil, fmt.Errorf("read context history %q: %w", e.Name(), x)
		}
		var s Snapshot
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("decode context history %q: %w", e.Name(), err)
		}
		if key(s.Identity) == key(id) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Freshness.GeneratedAt.After(out[j].Freshness.GeneratedAt) })
	return out, nil
}

func Diff(a, b Snapshot) []Change {
	var out []Change
	old := flatten(a.State)
	cur := flatten(b.State)
	for k, v := range cur {
		if old[k] != v {
			kind := "+"
			if _, ok := old[k]; ok {
				kind = "~"
			}
			parts := strings.SplitN(k, "/", 2)
			out = append(out, Change{kind, parts[0], parts[1], v})
		}
	}
	for k, v := range old {
		if _, ok := cur[k]; !ok {
			parts := strings.SplitN(k, "/", 2)
			out = append(out, Change{"-", parts[0], parts[1], v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Section+out[i].ID < out[j].Section+out[j].ID })
	return out
}
func flatten(s State) map[string]string {
	m := map[string]string{"metadata/objective": s.Objective, "metadata/status": s.Status}
	if b, err := json.Marshal(s.Tests); err == nil && string(b) != "null" {
		m["tests/results"] = string(b)
	}
	for _, gap := range s.Gaps {
		b, _ := json.Marshal(gap)
		m["gaps/"+gap.Subject] = string(b)
	}
	if b, err := json.Marshal(s.Project); err == nil && string(b) != "null" {
		m["project/context"] = string(b)
	}
	for _, conflict := range s.Conflicts {
		b, _ := json.Marshal(conflict)
		m["conflicts/"+conflict.Subject] = string(b)
	}
	sets := map[string][]Item{"project": s.ProjectItems, "confirmed": s.Confirmed, "implemented": s.Implemented, "failing": s.Failing, "unknown": s.Unknown, "decisions": s.Decisions, "next_actions": s.NextActions, "session": s.Session, "evidence": s.Evidence}
	for sec, items := range sets {
		for _, i := range items {
			// Items are structured context, so lifecycle and evidence changes are
			// meaningful diffs even when their explanatory text is unchanged.
			b, _ := json.Marshal(i)
			m[sec+"/"+i.ID] = string(b)
		}
	}
	return m
}

func FindItem(s Snapshot, id string) (Item, string, bool) {
	for sec, items := range map[string][]Item{"project": s.State.ProjectItems, "confirmed": s.State.Confirmed, "implemented": s.State.Implemented, "failing": s.State.Failing, "unknown": s.State.Unknown, "decisions": s.State.Decisions, "next_actions": s.State.NextActions, "session": s.State.Session, "evidence": s.State.Evidence} {
		for _, i := range items {
			if i.ID == id {
				return i, sec, true
			}
		}
	}
	return Item{}, "", false
}

func Explain(root string, id Identity, itemID string) (Explanation, error) {
	s, err := Load(root, id)
	if err != nil {
		return Explanation{}, err
	}
	changes, err := detectChanges(s, id)
	if err != nil {
		return Explanation{}, err
	}
	stale := len(changes) > 0 || len(s.Invalid) > 0
	item, section, ok := FindItem(s, itemID)
	if !ok {
		return Explanation{}, fmt.Errorf("context item %q not found", itemID)
	}
	why := "active item from the current checkpoint for this workspace and task"
	if item.Source != "" {
		why += "; source " + item.Source
	}
	if stale {
		why += "; snapshot is stale"
	}
	return Explanation{Item: item, Section: section, SnapshotID: s.ID, Identity: s.Identity, Freshness: s.Freshness, InvalidLayers: append([]string(nil), s.Invalid...), Why: why}, nil
}

// Promote moves a checkpoint item between context layers while preserving its
// stable ID and provenance. The resulting checkpoint is a deliberate durable
// validation action, so it clears prior invalidation markers.
func Promote(root string, id Identity, itemID, to string) (Snapshot, error) {
	unlock, err := lock(root)
	if err != nil {
		return Snapshot{}, err
	}
	defer unlock()
	s, err := Load(root, id)
	if err != nil {
		return Snapshot{}, err
	}
	item, from, ok := FindItem(s, itemID)
	if !ok {
		return Snapshot{}, fmt.Errorf("context item %q not found", itemID)
	}
	destination, durability, err := promotionDestination(from, to)
	if err != nil {
		return Snapshot{}, fmt.Errorf("unsupported context destination %q", to)
	}
	if from == destination && item.Durability == durability {
		return s, nil
	}
	removeItem(&s.State, from, itemID)
	item.Durability = durability
	appendItem(&s.State, destination, item)
	if s.State.Promoted == nil {
		s.State.Promoted = map[string]string{}
	}
	s.State.Promoted[itemID] = destination
	// The loaded snapshot already contains the exact source versions it
	// materialized. Re-reading a source here could reintroduce an item into its
	// former layer while promotion moves it, producing a duplicate stable ID.
	return checkpointStateLocked(root, id, s.State, false)
}

func isItemSection(section string) bool {
	switch section {
	case "project", "confirmed", "implemented", "failing", "unknown", "decisions", "next_actions", "session", "evidence":
		return true
	}
	return false
}

func promotionDestination(from, to string) (section, durability string, err error) {
	switch to {
	case "permanent", "project":
		return "project", to, nil
	case "task":
		if from == "project" {
			return "confirmed", to, nil
		}
		return from, to, nil
	case "ephemeral":
		return "session", to, nil
	default:
		// Existing section names remain valid for programmatic callers. CLI/MCP
		// advertise the retention vocabulary above.
		if isItemSection(to) {
			return to, to, nil
		}
		return "", "", errors.New("unsupported destination")
	}
}
func removeItem(state *State, section, id string) {
	set := itemSet(state, section)
	for i := range *set {
		if (*set)[i].ID == id {
			*set = append((*set)[:i], (*set)[i+1:]...)
			return
		}
	}
}
func appendItem(state *State, section string, item Item) {
	set := itemSet(state, section)
	*set = append(*set, item)
}
func itemSet(state *State, section string) *[]Item {
	switch section {
	case "project":
		return &state.ProjectItems
	case "confirmed":
		return &state.Confirmed
	case "implemented":
		return &state.Implemented
	case "failing":
		return &state.Failing
	case "unknown":
		return &state.Unknown
	case "decisions":
		return &state.Decisions
	case "next_actions":
		return &state.NextActions
	case "session":
		return &state.Session
	case "evidence":
		return &state.Evidence
	}
	return nil
}

func normalize(s State) State {
	seen := map[string]bool{}
	sets := []*[]Item{&s.ProjectItems, &s.Confirmed, &s.Implemented, &s.Failing, &s.Unknown, &s.Decisions, &s.NextActions, &s.Session, &s.Evidence}
	for _, set := range sets {
		for _, item := range *set {
			if item.ID != "" {
				seen[item.ID] = true
			}
		}
	}
	n := 0
	for _, set := range sets {
		for i := range *set {
			item := &(*set)[i]
			if item.ID == "" {
				for {
					n++
					candidate := fmt.Sprintf("item-%03d", n)
					if !seen[candidate] {
						item.ID = candidate
						seen[candidate] = true
						break
					}
				}
			}
			if item.Source == "" {
				item.Source = "checkpoint://local"
			}
		}
	}
	return s
}

func stampCheckpointProvenance(snapshot *Snapshot) {
	state := &snapshot.State
	prefix := "checkpoint://" + snapshot.ID + "/"
	for _, set := range []*[]Item{&state.ProjectItems, &state.Confirmed, &state.Implemented, &state.Failing, &state.Unknown, &state.Decisions, &state.NextActions, &state.Session, &state.Evidence} {
		for i := range *set {
			if (*set)[i].Source == "checkpoint://local" {
				(*set)[i].Source = prefix + (*set)[i].ID
			}
		}
	}
	if state.Project != nil && state.ProjectSource == "" {
		state.ProjectSource = prefix + "project"
	}
	if state.Tests != nil && state.TestsSource == "" {
		state.TestsSource = prefix + "tests"
	}
	for i := range state.Gaps {
		if state.Gaps[i].Source == "" {
			state.Gaps[i].Source = prefix + "gap"
		}
	}
	for i := range state.Conflicts {
		if state.Conflicts[i].Source == "" {
			state.Conflicts[i].Source = prefix + "conflict"
		}
	}
}

// EncodeResume is the one serialization used for bounded context packets. A
// byte is a conservative upper bound for byte-based tokenizers, so a packet no
// larger than budget bytes has a deterministic conservative transport bound. It intentionally
// emits compact JSON; callers must write these exact bytes rather than reformat
// the result after trimming.
func EncodeResume(r ResumeResult) ([]byte, error) {
	b, err := encodeResumeRaw(r)
	if err != nil {
		return nil, err
	}
	if r.TokenBudget > 0 && len(b) > r.TokenBudget {
		return nil, fmt.Errorf("encoded resume packet is %d bytes over token budget %d", len(b), r.TokenBudget)
	}
	return b, nil
}

func encodeResumeRaw(r ResumeResult) ([]byte, error) { return json.Marshal(r) }

// trimResult uses a one-byte-per-token upper bound over the complete emitted
// response, not only State. Required gaps and the objective stay. A byte limit
// is conservative for byte-based tokenizers; model-specific tokenization is not
// emulated here. A
// budget too small for that irreducible packet is rejected rather than quietly
// returning an oversized response.
func trimResult(r *ResumeResult, budget int) (bool, error) {
	limit := budget
	b, _ := encodeResumeRaw(*r)
	if len(b) <= limit {
		return false, nil
	}
	r.Truncated = true
	s := &r.Snapshot.State
	// Test detail is supporting evidence; explicit failing items remain until
	// every lower-priority section has been removed.
	s.Tests = nil
	s.Session = nil
	s.Evidence = nil
	s.Project = nil
	s.ProjectSource = ""
	s.Sources = nil
	for _, set := range []*[]Item{&s.Unknown, &s.Confirmed, &s.NextActions, &s.Implemented, &s.Decisions, &s.Failing} {
		for len(*set) > 0 {
			*set = (*set)[:len(*set)-1]
			b, _ = encodeResumeRaw(*r)
			if len(b) <= limit {
				return true, nil
			}
		}
	}
	b, _ = encodeResumeRaw(*r)
	if len(b) > limit {
		return false, fmt.Errorf("token budget %d is too small for required context metadata and gaps (minimum %d bytes)", budget, len(b))
	}
	return true, nil
}

func encodedLen(r ResumeResult) int { b, _ := EncodeResume(r); return len(b) }
func snapshotLen(s Snapshot) int    { b, _ := json.Marshal(s); return len(b) }

func copyStrings(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// DecodeState accepts exactly one JSON object, rejects duplicate object keys
// at every depth, and uses the State schema rather than silently discarding
// unknown checkpoint fields.
func DecodeState(b []byte) (State, error) {
	if len(strings.TrimSpace(string(b))) == 0 || strings.TrimSpace(string(b)) == "null" {
		return State{}, errors.New("checkpoint state must be a JSON object")
	}
	check := json.NewDecoder(strings.NewReader(string(b)))
	first, err := check.Token()
	if err != nil {
		return State{}, fmt.Errorf("decode checkpoint state: %w", err)
	}
	if d, ok := first.(json.Delim); !ok || d != '{' {
		return State{}, errors.New("checkpoint state must be a JSON object")
	}
	if err := uniqueObject(check); err != nil {
		return State{}, err
	}
	if _, err := check.Token(); err != io.EOF {
		return State{}, errors.New("checkpoint state contains trailing data")
	}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("decode checkpoint state: %w", err)
	}
	if err := validateState(state); err != nil {
		return State{}, err
	}
	return state, nil
}

// uniqueObject reads the contents after an opening '{' token.
func uniqueObject(decoder *json.Decoder) error {
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("JSON object key is not a string")
		}
		if seen[name] {
			return fmt.Errorf("duplicate JSON key %q", name)
		}
		seen[name] = true
		if err := uniqueValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil {
		return err
	}
	if d, ok := end.(json.Delim); !ok || d != '}' {
		return errors.New("malformed JSON object")
	}
	return nil
}
func uniqueValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	d, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		return uniqueObject(decoder)
	case '[':
		for decoder.More() {
			if err := uniqueValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if d, ok := end.(json.Delim); !ok || d != ']' {
			return errors.New("malformed JSON array")
		}
	}
	return nil
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

type metricEvent struct {
	Kind          string `json:"kind"`
	LatencyMS     int64  `json:"latency_ms,omitempty"`
	Gaps          int    `json:"gaps,omitempty"`
	Conflicts     int    `json:"conflicts,omitempty"`
	RefreshMode   string `json:"refresh_mode,omitempty"`
	SnapshotBytes int    `json:"snapshot_bytes,omitempty"`
	PacketBytes   int    `json:"packet_bytes,omitempty"`
	PacketTokens  int    `json:"packet_tokens,omitempty"`
}

func recordMetric(root string, event metricEvent) {
	if os.MkdirAll(root, 0700) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(root, "metrics.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(event)
	_, _ = f.Write(append(b, '\n'))
}

func RecordLookup(root string) { recordMetric(root, metricEvent{Kind: "lookup"}) }

func ReadMetrics(root string) Metrics {
	b, err := os.ReadFile(filepath.Join(root, "metrics.jsonl"))
	if err != nil {
		return Metrics{}
	}
	var m Metrics
	for _, line := range strings.Split(string(b), "\n") {
		var e metricEvent
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		switch e.Kind {
		case "resume_hit", "resume_stale", "resume_miss":
			m.ResumeTotal++
			if e.Kind == "resume_hit" {
				m.ResumeCacheHits++
			}
			if e.Kind == "resume_miss" {
				m.ResumeCacheMisses++
			}
		case "refresh":
			m.RefreshTotal++
			if e.RefreshMode == "full" {
				m.RefreshFullTotal++
			} else {
				m.RefreshIncrementalTotal++
			}
		case "checkpoint":
			m.CheckpointTotal++
		case "lookup":
			m.LookupTotal++
		}
		m.GapTotal += uint64(e.Gaps)
		m.ConflictTotal += uint64(e.Conflicts)
		m.SnapshotBytesTotal += uint64(e.SnapshotBytes)
		m.PacketBytesTotal += uint64(e.PacketBytes)
		m.PacketTokensTotal += uint64(e.PacketTokens)
		if strings.HasPrefix(e.Kind, "resume_") {
			m.ResumeLatencyMS += uint64(e.LatencyMS)
		}
		if e.Kind == "refresh" {
			m.RefreshLatencyMS += uint64(e.LatencyMS)
		}
	}
	return m
}
