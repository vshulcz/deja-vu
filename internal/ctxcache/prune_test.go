package ctxcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func checkpointSeries(t *testing.T, root string, id Identity, count int) []Snapshot {
	t.Helper()
	snapshots := make([]Snapshot, 0, count)
	for i := 0; i < count; i++ {
		s, err := Checkpoint(root, id, State{Objective: "checkpoint history"})
		if err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, s)
	}
	return snapshots
}

func TestPruneKeepsNewestAndCurrentSnapshot(t *testing.T) {
	root, id := t.TempDir(), testIdentity("prune-current")
	snapshots := checkpointSeries(t, root, id, 5)

	// A recovered pointer can validly lag immutable history. It must win over a
	// newer object when deciding the bounded retained set.
	b, err := json.Marshal(snapshots[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath(root, id), append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Prune(root, id, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kept != 2 || result.Removed != 3 || result.CurrentSnapshot != snapshots[0].ID {
		t.Fatalf("prune result=%#v", result)
	}
	history, err := History(root, id)
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%#v err=%v", history, err)
	}
	foundCurrent, foundNewest := false, false
	for _, snapshot := range history {
		foundCurrent = foundCurrent || snapshot.ID == snapshots[0].ID
		foundNewest = foundNewest || snapshot.ID == snapshots[4].ID
	}
	if !foundCurrent || !foundNewest {
		t.Fatalf("retained=%#v", history)
	}
}

func TestPlanPruneDefersDeletionUntilApplied(t *testing.T) {
	root, id := t.TempDir(), testIdentity("prune-plan")
	checkpointSeries(t, root, id, 5)
	plan, err := planPrune(root, id, 2)
	if err != nil {
		t.Fatal(err)
	}
	if plan.result.Kept != 2 || len(plan.remove) != 3 {
		t.Fatalf("plan=%#v", plan)
	}
	history, err := History(root, id)
	if err != nil || len(history) != 5 {
		t.Fatalf("planning mutated history: %d, %v", len(history), err)
	}
	result, err := plan.apply()
	if err != nil || result.Kept != 2 || result.Removed != 3 {
		t.Fatalf("apply=%#v err=%v", result, err)
	}
	history, err = History(root, id)
	if err != nil || len(history) != 2 {
		t.Fatalf("applied history=%d err=%v", len(history), err)
	}
}

func TestPruneDoesNotCrossIdentityAndSerializesConcurrentCalls(t *testing.T) {
	root := t.TempDir()
	first, second := testIdentity("prune-first"), testIdentity("prune-second")
	second.TaskID = "TASK-2"
	checkpointSeries(t, root, first, 8)
	checkpointSeries(t, root, second, 4)

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Prune(root, first, 3)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	firstHistory, err := History(root, first)
	if err != nil || len(firstHistory) != 3 {
		t.Fatalf("first history=%d err=%v", len(firstHistory), err)
	}
	secondHistory, err := History(root, second)
	if err != nil || len(secondHistory) != 4 {
		t.Fatalf("second history=%d err=%v", len(secondHistory), err)
	}
}

func TestPruneSerializesWithCheckpointPublishing(t *testing.T) {
	root, id := t.TempDir(), testIdentity("prune-publish")
	checkpointSeries(t, root, id, 6)

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := Prune(root, id, 3)
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := Checkpoint(root, id, State{Objective: "concurrent publishing"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err := Load(root, id)
	if err != nil {
		t.Fatal(err)
	}
	history, err := History(root, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range history {
		if snapshot.ID == current.ID {
			return
		}
	}
	t.Fatalf("concurrent prune removed current snapshot %q", current.ID)
}

func TestPruneSerializesHistoryReadsWithPublishing(t *testing.T) {
	root, id := t.TempDir(), testIdentity("prune-history-read")
	checkpointSeries(t, root, id, 6)

	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for range 8 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, err := Prune(root, id, 3)
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := Checkpoint(root, id, State{Objective: "concurrent history"})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := History(root, id)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err := Load(root, id)
	if err != nil {
		t.Fatal(err)
	}
	history, err := History(root, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range history {
		if snapshot.ID == current.ID {
			return
		}
	}
	t.Fatalf("history omitted current snapshot %q", current.ID)
}

func TestPruneRejectsInvalidRetentionAndDamagedHistoryBeforeDeletion(t *testing.T) {
	root, id := t.TempDir(), testIdentity("prune-corrupt")
	checkpointSeries(t, root, id, 3)
	if _, err := Prune(root, id, 0); err == nil {
		t.Fatal("accepted zero retention")
	}
	if err := os.WriteFile(filepath.Join(root, "snapshots", "broken.json"), []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prune(root, id, 1); err == nil {
		t.Fatal("pruned corrupt history")
	}
	entries, err := os.ReadDir(filepath.Join(root, "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("prune deleted before validation: %d entries", len(entries))
	}
}

func TestPruneRejectsStructurallyInvalidHistoryBeforeDeletion(t *testing.T) {
	root, id := t.TempDir(), testIdentity("prune-invalid-state")
	snapshots := checkpointSeries(t, root, id, 3)
	path := filepath.Join(root, "snapshots", snapshots[0].ID+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var broken Snapshot
	if err := json.Unmarshal(b, &broken); err != nil {
		t.Fatal(err)
	}
	broken.State.Confirmed = []Item{{ID: "invalid", Text: ""}}
	b, err = json.Marshal(broken)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prune(root, id, 1); err == nil {
		t.Fatal("pruned structurally invalid history")
	}
	entries, err := os.ReadDir(filepath.Join(root, "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("prune deleted before state validation: %d entries", len(entries))
	}
}

func TestFailedPublishDoesNotApplyRetentionPlan(t *testing.T) {
	root, id := t.TempDir(), testIdentity("prune-failed-publish")
	snapshots := checkpointSeries(t, root, id, DefaultHistoryLimit)
	before := historyIDs(t, root, id)
	duplicate := snapshots[0]
	if err := save(root, &duplicate); err == nil {
		t.Fatal("published duplicate immutable snapshot")
	}
	after := historyIDs(t, root, id)
	if len(before) != DefaultHistoryLimit || len(after) != len(before) {
		t.Fatalf("history count changed after failed publish: before=%d after=%d", len(before), len(after))
	}
	for snapshotID := range before {
		if !after[snapshotID] {
			t.Fatalf("failed publish pruned snapshot %q", snapshotID)
		}
	}
}

func TestPruneFailsClosedForBrokenPointerAndHistoryLayout(t *testing.T) {
	id := testIdentity("prune-recovery")
	t.Run("no history", func(t *testing.T) {
		result, err := Prune(t.TempDir(), id, 1)
		if err != nil || result.Kept != 0 || result.Removed != 0 {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})
	t.Run("huge keep does not allocate from user input", func(t *testing.T) {
		root := t.TempDir()
		checkpointSeries(t, root, id, 1)
		if _, err := Prune(root, id, int(^uint(0)>>1)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("history path is a file", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "snapshots"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Prune(root, id, 1); err == nil {
			t.Fatal("pruned a non-directory history path")
		}
	})
	t.Run("cache root cannot be locked", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "cache-file")
		if err := os.WriteFile(root, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Prune(root, id, 1); err == nil {
			t.Fatal("locked below a regular-file cache root")
		}
	})
	t.Run("corrupt current pointer", func(t *testing.T) {
		root := t.TempDir()
		checkpointSeries(t, root, id, 2)
		if err := os.WriteFile(currentPath(root, id), []byte("{"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Prune(root, id, 1); err == nil {
			t.Fatal("pruned with a corrupt current pointer")
		}
	})
	t.Run("current pointer has no snapshot id", func(t *testing.T) {
		root := t.TempDir()
		checkpointSeries(t, root, id, 2)
		current, err := Load(root, id)
		if err != nil {
			t.Fatal(err)
		}
		current.ID = ""
		overwriteCurrentSnapshot(t, root, id, current)
		if _, err := Prune(root, id, 1); err == nil {
			t.Fatal("pruned with an unnamed current pointer")
		}
	})
	t.Run("current pointer belongs to another identity", func(t *testing.T) {
		root := t.TempDir()
		checkpointSeries(t, root, id, 2)
		current, err := Load(root, id)
		if err != nil {
			t.Fatal(err)
		}
		current.Identity.TaskID = "other-task"
		overwriteCurrentSnapshot(t, root, id, current)
		if _, err := Prune(root, id, 1); err == nil {
			t.Fatal("pruned with a cross-identity current pointer")
		}
	})
	t.Run("current immutable object missing", func(t *testing.T) {
		root := t.TempDir()
		checkpointSeries(t, root, id, 2)
		current, err := Load(root, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(root, "snapshots", current.ID+".json")); err != nil {
			t.Fatal(err)
		}
		if _, err := Prune(root, id, 1); err == nil {
			t.Fatal("pruned after losing the current immutable object")
		}
	})
	t.Run("current immutable directory missing", func(t *testing.T) {
		root := t.TempDir()
		checkpointSeries(t, root, id, 1)
		current, err := Load(root, id)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, "snapshots")
		if err := os.Remove(filepath.Join(dir, current.ID+".json")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		if _, err := Prune(root, id, 1); err == nil {
			t.Fatal("pruned after losing the immutable history directory")
		}
	})
	t.Run("nonregular snapshot object", func(t *testing.T) {
		root := t.TempDir()
		checkpointSeries(t, root, id, 2)
		if err := os.Mkdir(filepath.Join(root, "snapshots", "directory.json"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := Prune(root, id, 1); err == nil {
			t.Fatal("pruned with a nonregular history object")
		}
	})
	t.Run("snapshot filename does not match its id", func(t *testing.T) {
		root := t.TempDir()
		checkpointSeries(t, root, id, 2)
		current, err := Load(root, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(root, "snapshots", current.ID+".json"), filepath.Join(root, "snapshots", "mismatched.json")); err != nil {
			t.Fatal(err)
		}
		if _, err := Prune(root, id, 1); err == nil {
			t.Fatal("pruned a mismatched immutable object")
		}
	})
}

func TestReadPruneSnapshotMetadataRejectsTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, []byte(`{"snapshot_id":"one"}{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPruneSnapshotMetadata(path); err == nil {
		t.Fatal("accepted multiple JSON values")
	}
}

func overwriteCurrentSnapshot(t *testing.T, root string, id Identity, snapshot Snapshot) {
	t.Helper()
	b, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath(root, id), append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func historyIDs(t *testing.T, root string, id Identity) map[string]bool {
	t.Helper()
	history, err := History(root, id)
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool, len(history))
	for _, snapshot := range history {
		ids[snapshot.ID] = true
	}
	return ids
}

func TestPruneRefusesSnapshotSymlinks(t *testing.T) {
	root, id := t.TempDir(), testIdentity("prune-symlink")
	checkpointSeries(t, root, id, 2)
	target := filepath.Join(root, "outside.json")
	if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "snapshots", "linked.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Prune(root, id, 1); err == nil {
		t.Fatal("followed a snapshot symlink")
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink was removed: %v", err)
	}
}

// This specifies writer integration: callers must prune before publishing the
// next immutable object so normal checkpoint history stays within the default
// retention bound. It intentionally fails until checkpointStateLocked wires
// pruneLocked into its existing writer lock.
func TestCheckpointHistoryIsBoundedByDefaultRetention(t *testing.T) {
	root, id := t.TempDir(), testIdentity("prune-writer")
	snapshots := checkpointSeries(t, root, id, DefaultHistoryLimit+2)
	history, err := History(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != DefaultHistoryLimit {
		t.Fatalf("history=%d want %d", len(history), DefaultHistoryLimit)
	}
	if history[0].ID != snapshots[len(snapshots)-1].ID {
		t.Fatalf("latest snapshot was pruned: history=%q latest=%q", history[0].ID, snapshots[len(snapshots)-1].ID)
	}
}
