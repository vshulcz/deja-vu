package ctxcache

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func testIdentity(head string) Identity {
	return Identity{WorkspaceID: "workspace", Repository: "repo", RepositoryRoot: "/repo", Worktree: "/repo", Branch: "main", GitHead: head, TaskID: "TASK-1", ProjectID: "repo"}
}

func TestCheckpointResumeRefreshAndInvalidate(t *testing.T) {
	root := t.TempDir()
	id := testIdentity("aaa")
	miss, err := Resume(root, id, 1000)
	if err != nil || miss.CacheStatus != "miss" {
		t.Fatalf("miss = %#v, %v", miss, err)
	}
	state := State{Objective: "ship it", Confirmed: []Item{{ID: "finding", Text: "cache is local", Source: "git://repo/a.go#L1"}}, Gaps: []Gap{{Subject: "acceptance criteria", Severity: "required"}}}
	s, err := Checkpoint(root, id, state)
	if err != nil {
		t.Fatal(err)
	}
	if s.Freshness.SnapshotVersion != 1 {
		t.Fatalf("version=%d", s.Freshness.SnapshotVersion)
	}
	hit, err := Resume(root, id, 1000)
	if err != nil || hit.CacheStatus != "hit" {
		t.Fatalf("hit = %#v, %v", hit, err)
	}
	changed := id
	changed.GitHead = "bbb"
	st, err := Inspect(root, changed)
	if err != nil || st.Status != "stale" || len(st.Changed) != 1 || st.Changed[0] != "git" {
		t.Fatalf("status=%#v, %v", st, err)
	}
	updated, layers, err := Refresh(root, changed)
	if err != nil || len(layers) != 1 || layers[0] != "git" {
		t.Fatalf("refresh=%#v %v %v", updated, layers, err)
	}
	if err := Invalidate(root, changed, "task"); err != nil {
		t.Fatal(err)
	}
	st, _ = Inspect(root, changed)
	if st.Status != "stale" {
		t.Fatalf("invalid status=%#v", st)
	}
	history, err := History(root, changed)
	if err != nil || len(history) != 3 {
		t.Fatalf("invalidation was not recorded in history: %d %v", len(history), err)
	}
}

func TestBranchSwitchReusesSnapshotAndCreatesValidationGap(t *testing.T) {
	root := t.TempDir()
	main := testIdentity("aaa")
	if _, err := Checkpoint(root, main, State{Objective: "ship"}); err != nil {
		t.Fatal(err)
	}
	feature := main
	feature.Branch = "feature"
	feature.GitHead = "bbb"
	feature.WorktreeState = "dirty"
	r, err := Resume(root, feature, 1000)
	if err != nil || r.CacheStatus != "stale" {
		t.Fatalf("branch resume=%#v %v", r, err)
	}
	updated, changed, err := Refresh(root, feature)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) < 2 || len(updated.State.Gaps) != 1 || updated.State.Gaps[0].Severity != "required" {
		t.Fatalf("refresh=%#v changes=%v", updated, changed)
	}
}

func TestHistoryDiffExplainAndBudget(t *testing.T) {
	root := t.TempDir()
	id := testIdentity("aaa")
	first, _ := Checkpoint(root, id, State{Confirmed: []Item{{ID: "fact", Text: "old"}}})
	second, _ := Checkpoint(root, id, State{Confirmed: []Item{{ID: "fact", Text: "new"}}, Failing: []Item{{ID: "test", Text: "go test fails"}}})
	h, err := History(root, id)
	if err != nil || len(h) != 2 || h[0].ID != second.ID {
		t.Fatalf("history=%#v %v", h, err)
	}
	d := Diff(first, second)
	if len(d) != 2 {
		t.Fatalf("diff=%#v", d)
	}
	item, section, ok := FindItem(second, "test")
	if !ok || section != "failing" || !strings.HasPrefix(item.Source, "checkpoint://") {
		t.Fatalf("item=%#v %q %v", item, section, ok)
	}
	if _, err := Resume(root, id, 1); err == nil {
		t.Fatal("impossibly small budget was silently exceeded")
	}
	long := State{Objective: "bounded", Unknown: []Item{{ID: "detail", Text: strings.Repeat("x", 2000)}}}
	if _, err := Checkpoint(root, id, long); err != nil {
		t.Fatal(err)
	}
	r, err := Resume(root, id, 700)
	if err != nil || !r.Truncated || len(r.Snapshot.State.Unknown) != 0 {
		t.Fatalf("bounded=%#v %v", r, err)
	}
	b, _ := EncodeResume(r)
	if len(b) > 700 {
		t.Fatalf("bounded packet is %d bytes", len(b))
	}
}

func TestResolveIdentityUsesRepositoryNotAgent(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		p := append([]string{"-C", repo}, args...)
		if out, err := execGit(p...); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "a")
	run("commit", "-m", "initial")
	a, err := ResolveIdentity(repo, "T-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ResolveIdentity(repo, "T-1")
	if err != nil {
		t.Fatal(err)
	}
	if a.WorkspaceID == "" || a.WorkspaceID != b.WorkspaceID || a.GitHead == "" {
		t.Fatalf("identities %#v %#v", a, b)
	}
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	dirty, err := ResolveIdentity(repo, "T-1")
	if err != nil {
		t.Fatal(err)
	}
	if dirty.WorktreeState == a.WorktreeState {
		t.Fatal("worktree edits did not change the freshness marker")
	}
}

func TestCheckpointRejectsAmbiguousStructuredState(t *testing.T) {
	id := testIdentity("aaa")
	_, err := Checkpoint(t.TempDir(), id, State{Confirmed: []Item{{ID: "same", Text: "one"}}, Decisions: []Item{{ID: "same", Text: "two"}}})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate ids: %v", err)
	}
	_, err = Checkpoint(t.TempDir(), id, State{Gaps: []Gap{{Subject: "missing authority"}}})
	if err == nil || !strings.Contains(err.Error(), "severity") {
		t.Fatalf("incomplete gap: %v", err)
	}
}

func TestConcurrentCheckpointsHaveUniqueVersions(t *testing.T) {
	root := t.TempDir()
	id := testIdentity("aaa")
	const count = 8
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := Checkpoint(root, id, State{Objective: "shared"}); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	h, err := History(root, id)
	if err != nil || len(h) != count {
		t.Fatalf("history=%d %v", len(h), err)
	}
	seen := map[uint64]bool{}
	for _, s := range h {
		if seen[s.Freshness.SnapshotVersion] {
			t.Fatalf("duplicate version %d", s.Freshness.SnapshotVersion)
		}
		seen[s.Freshness.SnapshotVersion] = true
	}
}

func execGit(args ...string) (string, error) {
	p := exec.Command("git", args...)
	b, e := p.CombinedOutput()
	return string(b), e
}

func TestDirtyContentChangesAfterCheckpointAreStale(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := execGit(append([]string{"-C", repo}, args...)...); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked.txt")
	run("commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("first dirty edit"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := ResolveIdentity(repo, "T-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Checkpoint(t.TempDir(), first, State{Objective: "x"}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := Checkpoint(root, first, State{Objective: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("second dirty edit"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := ResolveIdentity(repo, "T-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.WorktreeState == second.WorktreeState {
		t.Fatal("dirty-to-dirty content change kept same state")
	}
	r, err := Resume(root, second, 10000)
	if err != nil || r.CacheStatus != "stale" {
		t.Fatalf("resume=%#v err=%v", r, err)
	}
}

func TestInvalidateSurvivesRefreshUntilCheckpoint(t *testing.T) {
	root, id := t.TempDir(), testIdentity("a")
	if _, err := Checkpoint(root, id, State{Objective: "state"}); err != nil {
		t.Fatal(err)
	}
	if err := Invalidate(root, id, "task"); err != nil {
		t.Fatal(err)
	}
	refreshed, err := RefreshDetailed(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(refreshed.Snapshot.Invalid, "task") {
		t.Fatalf("refresh cleared invalidation: %#v", refreshed.Snapshot)
	}
	if !containsGap(refreshed.Snapshot.State.Gaps, "invalidated task context") {
		t.Fatalf("missing validation gap: %#v", refreshed.Snapshot.State.Gaps)
	}
	r, err := Resume(root, id, 10000)
	if err != nil || r.CacheStatus != "stale" {
		t.Fatalf("resume=%#v err=%v", r, err)
	}
	if _, err := Checkpoint(root, id, State{Objective: "validated"}); err != nil {
		t.Fatal(err)
	}
	r, err = Resume(root, id, 10000)
	if err != nil || r.CacheStatus != "hit" {
		t.Fatalf("checkpoint did not clear invalidation: %#v %v", r, err)
	}
}

func TestNormalizationAvoidsGeneratedIDCollisionAndDiffTracksLifecycle(t *testing.T) {
	root, id := t.TempDir(), testIdentity("a")
	first, err := Checkpoint(root, id, State{Confirmed: []Item{{ID: "item-001", Text: "kept"}, {Text: "generated"}}, Decisions: []Item{{ID: "decision", Text: "same", Status: "active", Source: "file://old", Durability: "task"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.State.Confirmed) != 2 || first.State.Confirmed[1].ID == "item-001" {
		t.Fatalf("normalization collided: %#v", first.State.Confirmed)
	}
	second, err := Checkpoint(root, id, State{Confirmed: first.State.Confirmed, Decisions: []Item{{ID: "decision", Text: "same", Status: "superseded", Source: "file://new", Durability: "permanent", Supports: []string{"evidence-2"}}}})
	if err != nil {
		t.Fatal(err)
	}
	changes := Diff(first, second)
	if !hasChange(changes, "decisions", "decision", "~") {
		t.Fatalf("semantic decision change omitted: %#v", changes)
	}
}

func TestEncodeResumeBoundsExactOutput(t *testing.T) {
	root, id := t.TempDir(), testIdentity("a")
	if _, err := Checkpoint(root, id, State{Objective: "keep", Session: []Item{{ID: "scratch", Text: strings.Repeat("x", 4000)}}}); err != nil {
		t.Fatal(err)
	}
	r, err := Resume(root, id, 700)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeResume(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 700 || !r.Truncated || r.Snapshot.State.Objective != "keep" {
		t.Fatalf("packet=%d result=%#v", len(b), r)
	}
}

func containsGap(gaps []Gap, subject string) bool {
	for _, gap := range gaps {
		if gap.Subject == subject {
			return true
		}
	}
	return false
}
func hasChange(changes []Change, section, id, kind string) bool {
	for _, change := range changes {
		if change.Section == section && change.ID == id && change.Kind == kind {
			return true
		}
	}
	return false
}

func TestCorePathAndPromotionHelpers(t *testing.T) {
	t.Setenv("DEJA_CTX_DIR", "/tmp/deja-context")
	if got := Root("/tmp/index/store"); got != "/tmp/deja-context" {
		t.Fatalf("root=%q", got)
	}
	t.Setenv("DEJA_CTX_DIR", "")
	if got := Root("/tmp/index/store"); got != "/tmp/index/ctx" {
		t.Fatalf("default root=%q", got)
	}
	id, err := ResolveIdentityWithVersions(t.TempDir(), "TASK", map[string]string{"project": "2"})
	if err != nil || id.ComponentVersions["project"] != "2" {
		t.Fatalf("identity=%#v err=%v", id, err)
	}
	for _, tc := range []struct{ from, to, want, durability string }{
		{"confirmed", "permanent", "project", "permanent"}, {"project", "task", "confirmed", "task"}, {"failing", "task", "failing", "task"}, {"confirmed", "ephemeral", "session", "ephemeral"}, {"confirmed", "evidence", "evidence", "evidence"},
	} {
		got, durability, err := promotionDestination(tc.from, tc.to)
		if err != nil || got != tc.want || durability != tc.durability {
			t.Fatalf("promotion %#v => %q %q %v", tc, got, durability, err)
		}
	}
	if _, _, err := promotionDestination("confirmed", "bad"); err == nil {
		t.Fatal("accepted invalid destination")
	}
	if isItemSection("bad") || !isItemSection("project") {
		t.Fatal("item sections")
	}
	base := State{Confirmed: []Item{{ID: "one", Text: "one"}}, ProjectItems: []Item{{ID: "two", Text: "two"}}, Promoted: map[string]string{"one": "project"}}
	filtered := filteredPromotions(base, base.Promoted)
	if len(filtered.Confirmed) != 0 || len(filtered.ProjectItems) != 1 {
		t.Fatalf("filtered=%#v", filtered)
	}
	merged := mergePromoted([]Item{{ID: "one", Text: "old"}}, []Item{{ID: "two", Text: "new"}}, map[string]string{"one": "project"}, "project")
	if len(merged) != 2 {
		t.Fatalf("merge=%#v", merged)
	}
	for _, section := range []string{"project", "confirmed", "implemented", "failing", "unknown", "decisions", "next_actions", "session", "evidence"} {
		if itemSet(&base, section) == nil {
			t.Fatalf("missing set %s", section)
		}
	}
	if itemSet(&base, "bad") != nil {
		t.Fatal("bad item set")
	}
}

func TestSourceAvailabilityValidationAndExplain(t *testing.T) {
	root, id := t.TempDir(), testIdentity("source")
	path := filepath.Join(t.TempDir(), "project.json")
	original := []byte(`{"project":{"name":"one"}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Checkpoint(root, id, State{Sources: []Source{{Name: "project", Layer: "project", Path: path}}, Confirmed: []Item{{ID: "fact", Text: "known"}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	r, err := Resume(root, id, 10000)
	if err != nil || r.CacheStatus != "stale" || !containsGap(r.Snapshot.State.Gaps, "source project unavailable") {
		t.Fatalf("resume=%#v err=%v", r, err)
	}
	st, err := Inspect(root, id)
	if err != nil || st.Status != "stale" || !contains(st.Changed, "source:project") {
		t.Fatalf("status=%#v err=%v", st, err)
	}
	refreshed, err := RefreshDetailed(root, id)
	if err != nil || !containsGap(refreshed.Snapshot.State.Gaps, "source project unavailable") {
		t.Fatalf("refresh=%#v err=%v", refreshed, err)
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := RefreshDetailed(root, id)
	if err != nil || containsGap(restored.Snapshot.State.Gaps, "source project unavailable") {
		t.Fatalf("restored refresh=%#v err=%v", restored, err)
	}
	if r, err := Resume(root, id, 10000); err != nil || r.CacheStatus != "hit" {
		t.Fatalf("restored resume=%#v err=%v", r, err)
	}
	explained, err := Explain(root, id, "fact")
	if err != nil || strings.Contains(explained.Why, "stale") {
		t.Fatalf("explain=%#v err=%v", explained, err)
	}
	if _, err := Explain(root, id, "missing"); err == nil {
		t.Fatal("explained missing item")
	}
	for _, source := range []Source{{Name: "", Layer: "project", Path: path}, {Name: "rel", Layer: "project", Path: "relative.json"}, {Name: "bad", Layer: "unknown", Path: path}} {
		if err := validateSource(source); err == nil {
			t.Fatalf("accepted source %#v", source)
		}
	}
	if err := validateSourceState("project", State{Project: map[string]any{"ok": true}, ProjectSource: "forbidden"}); err == nil {
		t.Fatal("accepted project provenance in source")
	}
	if err := validateSourceState("task", State{Project: map[string]any{"forbidden": true}}); err == nil {
		t.Fatal("accepted cross-layer source")
	}
	if _, err := sourceVersion(Source{Name: "missing", Layer: "project", Path: filepath.Join(t.TempDir(), "missing.json")}); err == nil {
		t.Fatal("missing source version succeeded")
	}
}

func TestSourceRecoveryPreservesUserUnavailableGap(t *testing.T) {
	root, id := t.TempDir(), testIdentity("user-source-gap")
	if _, err := Checkpoint(root, id, State{Gaps: []Gap{{
		Subject: "source build unavailable", Severity: "required",
		Reason: "the build environment must be restored before validation",
	}}}); err != nil {
		t.Fatal(err)
	}
	resumed, err := Resume(root, id, 10000)
	if err != nil || !containsGap(resumed.Snapshot.State.Gaps, "source build unavailable") {
		t.Fatalf("user source blocker disappeared: %#v err=%v", resumed, err)
	}
}

func TestSourceRecoveryKeepsSameSubjectManualGap(t *testing.T) {
	root, id := t.TempDir(), testIdentity("manual-source-gap")
	path := filepath.Join(t.TempDir(), "project.json")
	body := []byte(`{"project":{"service":"api"}}`)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	manual := Gap{Subject: "source project unavailable", Severity: "required", Reason: "manual build blocker", Source: "manual://build"}
	if _, err := Checkpoint(root, id, State{Sources: []Source{{Name: "project", Layer: "project", Path: path}}, Gaps: []Gap{manual}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	missing, err := RefreshDetailed(root, id)
	if err != nil || len(missing.Snapshot.State.Gaps) != 2 {
		t.Fatalf("missing source gaps=%#v err=%v", missing.Snapshot.State.Gaps, err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := RefreshDetailed(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Snapshot.State.Gaps) != 1 || restored.Snapshot.State.Gaps[0].Source != manual.Source || restored.Snapshot.State.Gaps[0].Subject != manual.Subject {
		t.Fatalf("restored source removed manual blocker: %#v", restored.Snapshot.State.Gaps)
	}
}

func TestConflictRecoveryPreservesUserRequiredGap(t *testing.T) {
	root, id := t.TempDir(), testIdentity("manual-conflict-gap")
	if _, err := Checkpoint(root, id, State{Gaps: []Gap{{Subject: "unresolved conflict: build", Severity: "required", Reason: "the build result needs a decision"}}}); err != nil {
		t.Fatal(err)
	}
	resumed, err := Resume(root, id, 10000)
	if err != nil || !containsGap(resumed.Snapshot.State.Gaps, "unresolved conflict: build") {
		t.Fatalf("user conflict blocker disappeared: %#v err=%v", resumed, err)
	}
}

func TestConflictResolutionClearsOnlyGeneratedGap(t *testing.T) {
	root, id := t.TempDir(), testIdentity("resolved-conflict")
	manual := Gap{Subject: "unresolved conflict: build", Severity: "required", Reason: "manual build blocker", Source: "manual://build"}
	state := State{
		Gaps:      []Gap{manual},
		Conflicts: []Conflict{{Subject: "build", Candidates: []string{"pass", "fail"}, Resolution: "unresolved", Source: "test://authority"}},
	}
	first, err := Checkpoint(root, id, state)
	if err != nil || len(first.State.Gaps) != 2 {
		t.Fatalf("unresolved checkpoint=%#v err=%v", first, err)
	}
	state = first.State
	state.Conflicts[0].Resolution = "pass"
	resolved, err := Checkpoint(root, id, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.State.Gaps) != 1 || resolved.State.Gaps[0].Source != manual.Source || resolved.State.Gaps[0].Subject != manual.Subject {
		t.Fatalf("resolved conflict gaps=%#v", resolved.State.Gaps)
	}
}

func TestImmutablePersistenceHelpers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "object.json")
	if err := writeImmutable(path, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := writeImmutable(path, []byte(`{"ok":true}`)); err == nil {
		t.Fatal("overwrote immutable object")
	}
	rootFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(rootFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := lock(rootFile); err == nil {
		t.Fatal("locked beneath a regular file")
	}
	if _, err := Load(t.TempDir(), testIdentity("missing")); err == nil {
		t.Fatal("loaded absent snapshot")
	}
}

func TestPersistenceRejectsCorruptionAndCleansFailedPointerWrite(t *testing.T) {
	id := testIdentity("persist")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(currentPath(root, id)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath(root, id), []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, id); err == nil {
		t.Fatal("accepted corrupt pointer")
	}
	if err := os.MkdirAll(filepath.Join(root, "snapshots"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "snapshots", "broken.json"), []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := History(root, id); err == nil {
		t.Fatal("accepted corrupt history")
	}

	failed := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(currentPath(failed, id)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(currentPath(failed, id), 0700); err != nil {
		t.Fatal(err)
	}
	s := Snapshot{ID: "failed-pointer", Identity: id, State: State{Objective: "x"}}
	if err := save(failed, s); err == nil {
		t.Fatal("saved over directory pointer")
	}
	if _, err := os.Stat(filepath.Join(failed, "snapshots", s.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("failed pointer left acknowledged history: %v", err)
	}

	good := t.TempDir()
	s.ID = "immutable"
	if err := save(good, s); err != nil {
		t.Fatal(err)
	}
	if err := save(good, s); err == nil {
		t.Fatal("overwrote immutable history")
	}
}

func TestStructuredValidationAndMetricBranches(t *testing.T) {
	validItem := Item{ID: "one", Text: "text"}
	for _, state := range []State{
		{Confirmed: []Item{{ID: "", Text: ""}}},
		{Confirmed: []Item{validItem}, Decisions: []Item{validItem}},
		{Gaps: []Gap{{Subject: "only-subject"}}},
		{Conflicts: []Conflict{{Subject: "x", Candidates: []string{"a", "a"}, Resolution: "a"}}},
		{Conflicts: []Conflict{{Subject: "x", Candidates: []string{"a", "b"}, Resolution: "c"}}},
		{Sources: []Source{{Name: "a", Layer: "task", Path: "/tmp/a"}, {Name: "b", Layer: "task", Path: "/tmp/b"}}},
	} {
		if err := validateState(state); err == nil {
			t.Fatalf("accepted invalid state %#v", state)
		}
	}
	for _, raw := range []string{"", "null", "[]", `{"objective":"x","objective":"y"}`, `{"confirmed":[{"id":"x","text":"a","text":"b"}]}`, `{"objective":"x"} {}`} {
		if _, err := DecodeState([]byte(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	root := t.TempDir()
	for _, event := range []metricEvent{
		{Kind: "resume_hit", LatencyMS: 2, Gaps: 1, Conflicts: 1, PacketBytes: 8, PacketTokens: 8}, {Kind: "resume_stale"}, {Kind: "resume_miss"}, {Kind: "refresh", RefreshMode: "full", SnapshotBytes: 9}, {Kind: "refresh", RefreshMode: "incremental"}, {Kind: "checkpoint"}, {Kind: "lookup"},
	} {
		recordMetric(root, event)
	}
	if err := os.WriteFile(filepath.Join(root, "metrics.jsonl"), append([]byte("bad\n"), mustRead(t, filepath.Join(root, "metrics.jsonl"))...), 0600); err != nil {
		t.Fatal(err)
	}
	m := ReadMetrics(root)
	if m.ResumeTotal != 3 || m.ResumeCacheHits != 1 || m.ResumeCacheMisses != 1 || m.RefreshFullTotal != 1 || m.RefreshIncrementalTotal != 1 || m.CheckpointTotal != 1 || m.LookupTotal != 1 || m.PacketBytesTotal != 8 || m.ConflictTotal != 1 {
		t.Fatalf("metrics=%#v", m)
	}
}

func TestStorageAndWorktreeErrorBranches(t *testing.T) {
	if got := worktreeDigest(t.TempDir()); got == "" {
		t.Fatal("empty non-git digest")
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "Test"}, {"config", "commit.gpgsign", "false"}} {
		if out, err := execGit(append([]string{"-C", repo}, args...)...); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := execGit("-C", repo, "add", "tracked"); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	if out, err := execGit("-C", repo, "commit", "-m", "base"); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("untracked", filepath.Join(repo, "untracked-link")); err != nil {
		t.Fatal(err)
	}
	first := worktreeDigest(repo)
	if err := os.WriteFile(filepath.Join(repo, "untracked"), []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	if next := worktreeDigest(repo); first == next {
		t.Fatal("untracked content did not affect digest")
	}

	id := testIdentity("save")
	if err := save(t.TempDir(), Snapshot{ID: "marshal", Identity: id, State: State{Project: map[string]any{"bad": func() {}}}}); err == nil {
		t.Fatal("marshaled function")
	}
	rootFile := filepath.Join(t.TempDir(), "root-file")
	if err := os.WriteFile(rootFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := save(rootFile, Snapshot{ID: "bad-root", Identity: id}); err == nil {
		t.Fatal("saved below regular root")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "snapshots"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := save(root, Snapshot{ID: "bad-snapshots", Identity: id}); err == nil {
		t.Fatal("saved below snapshots file")
	}
	if err := writeImmutable(filepath.Join(root, "missing", "object"), []byte("x")); err == nil {
		t.Fatal("wrote immutable in missing parent")
	}
}

func TestProvenanceAndSourceLayerHelpers(t *testing.T) {
	source := Source{Name: "task", Layer: "task", Path: "/tmp/task.json", Version: "hash"}
	dst := State{}
	applySourceLayer(&dst, State{Gaps: []Gap{{Subject: "gap", Severity: "required"}}, Conflicts: []Conflict{{Subject: "conflict", Candidates: []string{"a", "b"}, Resolution: "a"}}}, source)
	if dst.Gaps[0].Source == "" || dst.Conflicts[0].Source == "" {
		t.Fatalf("missing source provenance: %#v", dst)
	}
	for _, pair := range []struct {
		layer string
		state State
	}{
		{"project", State{Project: map[string]any{"ok": true}}}, {"task", State{Objective: "ok", Confirmed: []Item{{ID: "a", Text: "a"}}}}, {"memory", State{Evidence: []Item{{ID: "e", Text: "e"}}}}, {"session", State{Session: []Item{{ID: "s", Text: "s"}}}},
	} {
		if err := validateSourceState(pair.layer, pair.state); err != nil {
			t.Fatalf("valid %s source: %v", pair.layer, err)
		}
	}
	snapshot := Snapshot{ID: "snapshot-id", State: State{Project: map[string]any{"p": true}, Tests: map[string]any{"t": true}, Confirmed: []Item{{ID: "item", Text: "item", Source: "checkpoint://local"}}, Gaps: []Gap{{Subject: "g", Severity: "required"}}, Conflicts: []Conflict{{Subject: "c", Candidates: []string{"a", "b"}, Resolution: "a"}}}}
	stampCheckpointProvenance(&snapshot)
	if !strings.Contains(snapshot.State.Confirmed[0].Source, "snapshot-id") || snapshot.State.ProjectSource == "" || snapshot.State.TestsSource == "" || snapshot.State.Gaps[0].Source == "" || snapshot.State.Conflicts[0].Source == "" {
		t.Fatalf("stamped=%#v", snapshot.State)
	}
	r := ResumeResult{CacheStatus: "hit", TokenBudget: 1, Snapshot: Snapshot{State: State{Objective: "too large"}}}
	if _, err := EncodeResume(r); err == nil {
		t.Fatal("encoded over-budget packet")
	}
	badRoot := filepath.Join(t.TempDir(), "metric-file")
	if err := os.WriteFile(badRoot, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	recordMetric(badRoot, metricEvent{Kind: "resume_hit"})
}

func TestCoreSmallRecoveryBranches(t *testing.T) {
	if got := withGapSource([]Gap{{Subject: "a", Severity: "required", Source: "file://kept"}, {Subject: "b", Severity: "required"}}, "file://fallback"); got[0].Source != "file://kept" || got[1].Source != "file://fallback" {
		t.Fatalf("gaps=%#v", got)
	}
	if got := withConflictSource([]Conflict{{Subject: "a", Candidates: []string{"a", "b"}, Resolution: "a", Source: "file://kept"}, {Subject: "b", Candidates: []string{"a", "b"}, Resolution: "a"}}, "file://fallback"); got[0].Source != "file://kept" || got[1].Source != "file://fallback" {
		t.Fatalf("conflicts=%#v", got)
	}
	if short("") != "none" || short(strings.Repeat("x", 20)) != strings.Repeat("x", 12) {
		t.Fatal("short")
	}
	id := testIdentity("small")
	root := t.TempDir()
	if err := Invalidate(root, id, "task"); err == nil {
		t.Fatal("invalidated missing snapshot")
	}
	if _, err := History(root, id); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeState([]byte(`{"project":{"nested":[1,true,{"x":null}]}}`)); err != nil {
		t.Fatal(err)
	}
	r := ResumeResult{CacheStatus: "hit", Snapshot: Snapshot{State: State{Project: map[string]any{"bad": func() {}}}}}
	if _, err := EncodeResume(r); err == nil {
		t.Fatal("encoded unmarshalable packet")
	}
}

func TestPublicRecoveryAndPromotionErrorPaths(t *testing.T) {
	root := t.TempDir()
	id := testIdentity("public-recovery")
	missing, err := Inspect(root, id)
	if err != nil || missing.Status != "missing" {
		t.Fatalf("missing status=%#v err=%v", missing, err)
	}
	checkpoint, err := Checkpoint(root, id, State{Confirmed: []Item{{ID: "fact", Text: "kept evidence"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Explain(root, id, "absent"); err == nil {
		t.Fatal("explained an absent item")
	}
	explained, err := Explain(root, id, "fact")
	if err != nil || explained.Section != "confirmed" || explained.Item.ID != "fact" {
		t.Fatalf("explanation=%#v err=%v", explained, err)
	}
	if _, err := Promote(root, id, "fact", "invalid"); err == nil {
		t.Fatal("promoted to an invalid destination")
	}
	promoted, err := Promote(root, id, "fact", "task")
	if err != nil || promoted.ID == checkpoint.ID || promoted.State.Confirmed[0].Durability != "task" {
		t.Fatalf("promotion=%#v err=%v", promoted, err)
	}
	noOp, err := Promote(root, id, "fact", "task")
	if err != nil || noOp.ID != promoted.ID {
		t.Fatalf("no-op promotion=%#v err=%v", noOp, err)
	}

	// A stale pointer must fail closed rather than looking like a missing cache.
	if err := os.WriteFile(currentPath(root, id), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resume(root, id, 1000); err == nil {
		t.Fatal("resumed from corrupt pointer")
	}

	// Immutable history rejects a duplicate ID so it cannot silently overwrite
	// an earlier checkpoint object.
	immutableRoot := t.TempDir()
	s := Snapshot{ID: "same", Identity: id}
	if err := save(immutableRoot, s); err != nil {
		t.Fatal(err)
	}
	if err := save(immutableRoot, s); err == nil {
		t.Fatal("rewrote immutable snapshot")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestComponentVersionsAreGenericAndInstructionPayloadsAreRejected(t *testing.T) {
	if _, err := DecodeState([]byte(`{"instructions":[{"id":"not-cache-context"}]}`)); err == nil {
		t.Fatal("ctx cache accepted an instruction payload")
	}
	root := t.TempDir()
	id := testIdentity("generic-component")
	id.ComponentVersions = map[string]string{"policy": "v1"}
	if _, err := Checkpoint(root, id, State{Objective: "generic component refresh"}); err != nil {
		t.Fatal(err)
	}
	id.ComponentVersions["policy"] = "v2"
	refreshed, err := RefreshDetailed(root, id)
	if err != nil || !containsGap(refreshed.Snapshot.State.Gaps, "changed policy context") {
		t.Fatalf("generic component refresh=%#v err=%v", refreshed, err)
	}
}
