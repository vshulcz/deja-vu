package ctxcache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultHistoryLimit bounds the immutable snapshots retained for one local
// workspace/task identity. The current snapshot counts toward the limit.
const DefaultHistoryLimit = 100

// PruneResult reports the snapshots that remain after pruning one identity's
// immutable history. CurrentSnapshot is retained even when its freshness is
// older than the other snapshots.
type PruneResult struct {
	Kept            int    `json:"kept"`
	Removed         int    `json:"removed"`
	CurrentSnapshot string `json:"current_snapshot,omitempty"`
}

// Prune retains at most keep immutable snapshots for id. It uses the same
// writer lock as checkpoints so it cannot remove an object while a cache
// writer is publishing it.
func Prune(root string, id Identity, keep int) (PruneResult, error) {
	unlock, err := lock(root)
	if err != nil {
		return PruneResult{}, err
	}
	defer unlock()
	return pruneLocked(root, id, keep)
}

type pruneCandidate struct {
	name     string
	snapshot pruneSnapshotMetadata
}

// prunePlan is a fully validated, immutable cleanup decision. Cache writers
// can make the decision before publishing and apply it only after the new
// snapshot is durably visible.
type prunePlan struct {
	result PruneResult
	remove []string
}

// pruneSnapshotMetadata is deliberately smaller than Snapshot. Pruning can
// inspect a large history without retaining every saved State in memory.
type pruneSnapshotMetadata struct {
	ID        string    `json:"snapshot_id"`
	Identity  Identity  `json:"identity"`
	Freshness Freshness `json:"freshness"`
}

// pruneLocked is Prune for callers that already hold the cache writer lock.
// It validates every snapshot object before deleting anything so damaged
// history never looks like successful retention cleanup.
func pruneLocked(root string, id Identity, keep int) (PruneResult, error) {
	plan, err := planPrune(root, id, keep)
	if err != nil {
		return PruneResult{}, err
	}
	return plan.apply()
}

// planPrune validates and ranks history but performs no mutation. Callers
// publishing a new immutable object should apply the returned plan only after
// that object and its current pointer have committed.
func planPrune(root string, id Identity, keep int) (prunePlan, error) {
	if keep < 1 {
		return prunePlan{}, fmt.Errorf("context history retention must keep at least one snapshot")
	}

	plan := prunePlan{}
	current, err := Load(root, id)
	hasCurrent := err == nil
	if errors.Is(err, os.ErrNotExist) {
		current = Snapshot{}
	} else if err != nil {
		return prunePlan{}, fmt.Errorf("load current context snapshot before pruning: %w", err)
	}
	if hasCurrent {
		if current.ID == "" {
			return prunePlan{}, fmt.Errorf("current context snapshot has no snapshot id")
		}
		if key(current.Identity) != key(id) {
			return prunePlan{}, fmt.Errorf("current context snapshot belongs to another identity")
		}
		plan.result.CurrentSnapshot = current.ID
	}

	dir := filepath.Join(root, "snapshots")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		if hasCurrent {
			return prunePlan{}, fmt.Errorf("current context snapshot %q is missing from immutable history", current.ID)
		}
		return plan, nil
	}
	if err != nil {
		return prunePlan{}, fmt.Errorf("read context history for pruning: %w", err)
	}

	var candidates []pruneCandidate
	currentFound := !hasCurrent
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return prunePlan{}, fmt.Errorf("inspect context history %q before pruning: %w", entry.Name(), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return prunePlan{}, fmt.Errorf("refuse to follow context history symlink %q", entry.Name())
		}
		if !info.Mode().IsRegular() {
			return prunePlan{}, fmt.Errorf("context history %q is not a regular file", entry.Name())
		}
		snapshot, err := readPruneSnapshotMetadata(path)
		if err != nil {
			return prunePlan{}, fmt.Errorf("decode context history %q before pruning: %w", entry.Name(), err)
		}
		if snapshot.ID == "" {
			return prunePlan{}, fmt.Errorf("context history %q has no snapshot id", entry.Name())
		}
		if entry.Name() != snapshot.ID+".json" {
			return prunePlan{}, fmt.Errorf("context history %q does not match its snapshot id", entry.Name())
		}
		if snapshot.ID == current.ID && key(snapshot.Identity) == key(id) {
			currentFound = true
		}
		if key(snapshot.Identity) == key(id) {
			candidates = append(candidates, pruneCandidate{name: entry.Name(), snapshot: snapshot})
		}
	}
	if !currentFound {
		return prunePlan{}, fmt.Errorf("current context snapshot %q is missing from immutable history", current.ID)
	}

	// Freshness versions are monotonic for cache writers. GeneratedAt and the
	// filename make imported or manually-recovered histories deterministic too.
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].snapshot, candidates[j].snapshot
		if a.Freshness.SnapshotVersion != b.Freshness.SnapshotVersion {
			return a.Freshness.SnapshotVersion > b.Freshness.SnapshotVersion
		}
		if !a.Freshness.GeneratedAt.Equal(b.Freshness.GeneratedAt) {
			return a.Freshness.GeneratedAt.After(b.Freshness.GeneratedAt)
		}
		return candidates[i].name < candidates[j].name
	})

	capacity := keep
	if capacity > len(candidates) {
		capacity = len(candidates)
	}
	retained := make(map[string]bool, capacity)
	if hasCurrent {
		for _, candidate := range candidates {
			if candidate.snapshot.ID == current.ID {
				retained[candidate.name] = true
				break
			}
		}
	}
	for _, candidate := range candidates {
		if len(retained) == keep {
			break
		}
		retained[candidate.name] = true
	}

	// Validation above completes before the first mutation. Remove in a stable
	// order so failures are reproducible and never include the current object.
	var remove []string
	for _, candidate := range candidates {
		if !retained[candidate.name] {
			remove = append(remove, filepath.Join(dir, candidate.name))
		}
	}
	sort.Strings(remove)
	plan.remove = remove
	plan.result.Kept = len(retained)
	return plan, nil
}

func (p prunePlan) apply() (PruneResult, error) {
	result := p.result
	for _, path := range p.remove {
		if err := os.Remove(path); err != nil {
			return result, fmt.Errorf("remove context history %q: %w", filepath.Base(path), err)
		}
		result.Removed++
	}
	return result, nil
}

func readPruneSnapshotMetadata(path string) (pruneSnapshotMetadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return pruneSnapshotMetadata{}, err
	}
	decoder := json.NewDecoder(f)
	// Decode and validate one full object at a time, then retain only the
	// compact metadata in the plan. This preserves the cache's fail-closed
	// state contract without retaining every historical State in memory.
	var full Snapshot
	if err := decoder.Decode(&full); err != nil {
		_ = f.Close()
		return pruneSnapshotMetadata{}, err
	}
	if err := validateState(full.State); err != nil {
		_ = f.Close()
		return pruneSnapshotMetadata{}, fmt.Errorf("validate context state: %w", err)
	}
	snapshot := pruneSnapshotMetadata{ID: full.ID, Identity: full.Identity, Freshness: full.Freshness}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		_ = f.Close()
		if err == nil {
			return pruneSnapshotMetadata{}, fmt.Errorf("multiple JSON values")
		}
		return pruneSnapshotMetadata{}, err
	}
	if err := f.Close(); err != nil {
		return pruneSnapshotMetadata{}, err
	}
	return snapshot, nil
}
