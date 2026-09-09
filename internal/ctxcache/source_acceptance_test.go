package ctxcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceAcceptanceIdentity() Identity {
	return Identity{
		WorkspaceID:    "source-acceptance-workspace",
		Repository:     "source-acceptance-repo",
		RepositoryRoot: "/source-acceptance/repo",
		Worktree:       "/source-acceptance/repo",
		Branch:         "main",
		GitHead:        "source-acceptance-head",
		TaskID:         "SOURCE-ACCEPTANCE-1",
		ProjectID:      "source-acceptance-project",
	}
}

func sourceAcceptanceWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func sourceAcceptanceSource(t *testing.T, sources []Source, name string) Source {
	t.Helper()
	for _, source := range sources {
		if source.Name == name {
			return source
		}
	}
	t.Fatalf("source %q was not retained: %#v", name, sources)
	return Source{}
}

func sourceAcceptanceItem(items []Item, id string) (Item, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return Item{}, false
}

func sourceAcceptanceGap(gaps []Gap, subject string) (Gap, bool) {
	for _, gap := range gaps {
		if gap.Subject == subject {
			return gap, true
		}
	}
	return Gap{}, false
}

func TestSourceAcceptanceRefreshPreservesOtherLayersAndProvenance(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "project.json")
	taskPath := filepath.Join(dir, "task.json")
	memoryPath := filepath.Join(dir, "memory.json")
	sourceAcceptanceWrite(t, projectPath, `{"project":{"service":"api"},"project_items":[{"id":"architecture","text":"API owns authentication"}]}`)
	sourceAcceptanceWrite(t, taskPath, `{"objective":"refresh authoritative task state","status":"active","confirmed":[{"id":"task-fact","text":"the old task finding"}],"tests":{"unit":"passing"},"next_actions":[{"id":"task-next","text":"refresh after authoritative changes"}]}`)
	sourceAcceptanceWrite(t, memoryPath, `{"evidence":[{"id":"memory-evidence","text":"a durable external result"}]}`)

	before, err := Checkpoint(root, sourceAcceptanceIdentity(), State{Sources: []Source{
		{Name: "project", Layer: "project", Path: projectPath},
		{Name: "task", Layer: "task", Path: taskPath},
		{Name: "memory", Layer: "memory", Path: memoryPath},
	}})
	if err != nil {
		t.Fatal(err)
	}
	project := sourceAcceptanceSource(t, before.State.Sources, "project")
	memory := sourceAcceptanceSource(t, before.State.Sources, "memory")
	if before.State.Project["service"] != "api" || before.State.ProjectSource != "file://"+filepath.ToSlash(projectPath)+"#sha256="+project.Version {
		t.Fatalf("project provenance = %#v / %q", before.State.Project, before.State.ProjectSource)
	}
	architecture, ok := sourceAcceptanceItem(before.State.ProjectItems, "architecture")
	if !ok || architecture.Source != before.State.ProjectSource {
		t.Fatalf("project item provenance = %#v", architecture)
	}
	evidence, ok := sourceAcceptanceItem(before.State.Evidence, "memory-evidence")
	wantMemorySource := "file://" + filepath.ToSlash(memoryPath) + "#sha256=" + memory.Version
	if !ok || evidence.Source != wantMemorySource {
		t.Fatalf("memory provenance = %#v, want %q", evidence, wantMemorySource)
	}

	sourceAcceptanceWrite(t, taskPath, `{"objective":"refresh authoritative task state","status":"active","confirmed":[{"id":"task-fact","text":"the refreshed task finding"}],"tests":{"unit":"passing after refresh"},"next_actions":[{"id":"task-next","text":"checkpoint the refreshed task result"}]}`)
	refreshed, err := RefreshDetailed(root, sourceAcceptanceIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(refreshed.DetectedChanges, "task") || !contains(refreshed.RefreshedLayers, "task") {
		t.Fatalf("refresh did not report task source update: %#v", refreshed)
	}
	after := refreshed.Snapshot
	if after.State.ProjectSource != before.State.ProjectSource || after.State.Project["service"] != "api" {
		t.Fatalf("project layer changed during task refresh: before=%#v after=%#v", before.State.Project, after.State.Project)
	}
	afterEvidence, ok := sourceAcceptanceItem(after.State.Evidence, "memory-evidence")
	if !ok || afterEvidence.Source != evidence.Source {
		t.Fatalf("memory layer changed during task refresh: %#v", after.State.Evidence)
	}
	fact, ok := sourceAcceptanceItem(after.State.Confirmed, "task-fact")
	task := sourceAcceptanceSource(t, after.State.Sources, "task")
	if !ok || fact.Text != "the refreshed task finding" || fact.Source != "file://"+filepath.ToSlash(taskPath)+"#sha256="+task.Version {
		t.Fatalf("task source did not refresh with provenance: %#v", fact)
	}
}

func TestSourceAcceptanceSourceRemovalAndManualVersionRequireValidation(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	taskPath := filepath.Join(dir, "task.json")
	sourceAcceptanceWrite(t, taskPath, `{"confirmed":[{"id":"keep","text":"keep this finding","source":"seed"},{"id":"remove","text":"remove this finding","source":"seed"}]}`)
	before, err := Checkpoint(root, sourceAcceptanceIdentity(), State{Sources: []Source{{Name: "task", Layer: "task", Path: taskPath}}})
	if err != nil {
		t.Fatal(err)
	}
	beforeTask := sourceAcceptanceSource(t, before.State.Sources, "task")

	// Omitting an item from the authoritative document is a deletion. The
	// content hash must make that deletion visible even though the descriptor
	// itself did not change.
	sourceAcceptanceWrite(t, taskPath, `{"confirmed":[{"id":"keep","text":"keep this finding","source":"seed"}]}`)
	refreshed, err := RefreshDetailed(root, sourceAcceptanceIdentity())
	if err != nil {
		t.Fatal(err)
	}
	afterTask := sourceAcceptanceSource(t, refreshed.Snapshot.State.Sources, "task")
	if beforeTask.Version == afterTask.Version || !contains(refreshed.DetectedChanges, "task") {
		t.Fatalf("source content removal did not change task watermark: before=%#v after=%#v changes=%v", beforeTask, afterTask, refreshed.DetectedChanges)
	}
	if _, ok := sourceAcceptanceItem(refreshed.Snapshot.State.Confirmed, "remove"); ok {
		t.Fatalf("removed source item survived refresh: %#v", refreshed.Snapshot.State.Confirmed)
	}

	versions := sourceAcceptanceIdentity()
	versions.ComponentVersions = map[string]string{"task": "v1"}
	versionRoot := t.TempDir()
	if _, err := Checkpoint(versionRoot, versions, State{Objective: "validate manual component versions"}); err != nil {
		t.Fatal(err)
	}
	versions.ComponentVersions = map[string]string{"task": "v2"}
	versionRefresh, err := RefreshDetailed(versionRoot, versions)
	if err != nil {
		t.Fatal(err)
	}
	gap, ok := sourceAcceptanceGap(versionRefresh.Snapshot.State.Gaps, "changed task context")
	if !ok || gap.Severity != "required" || gap.Source != "component://task" {
		t.Fatalf("manual version change did not require validation: %#v", versionRefresh.Snapshot.State.Gaps)
	}
}

func TestSourceAcceptanceRejectsMalformedOwnershipUnknownAndDuplicateJSON(t *testing.T) {
	id := sourceAcceptanceIdentity()
	cases := []struct {
		name    string
		layer   string
		body    string
		wantErr string
	}{
		{name: "malformed source document", layer: "task", body: `{"objective":`, wantErr: `decode context source "source"`},
		{name: "project source claiming task field", layer: "project", body: `{"objective":"not owned by project"}`, wantErr: "project source may only contain project"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.json")
			sourceAcceptanceWrite(t, path, tc.body)
			_, err := Checkpoint(t.TempDir(), id, State{Sources: []Source{{Name: "source", Layer: tc.layer, Path: path}}})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Checkpoint error = %v, want %q", err, tc.wantErr)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "unknown field", body: `{"objective":"known","unexpected":true}`, wantErr: "unknown field"},
		{name: "duplicate top-level key", body: `{"objective":"one","objective":"two"}`, wantErr: `duplicate JSON key "objective"`},
		{name: "duplicate nested key", body: `{"confirmed":[{"id":"one","id":"two","text":"fact","source":"test"}]}`, wantErr: `duplicate JSON key "id"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeState([]byte(tc.body)); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("DecodeState(%s) error = %v, want %q", tc.body, err, tc.wantErr)
			}
		})
	}
}

func TestSourceAcceptanceConflictsPromotionDiffAndExplain(t *testing.T) {
	root := t.TempDir()
	id := sourceAcceptanceIdentity()
	first, err := Checkpoint(root, id, State{
		Confirmed: []Item{{ID: "durable-finding", Text: "validated through integration", Source: "test://acceptance"}},
		Conflicts: []Conflict{{Subject: "deployment owner", Candidates: []string{"team-a", "team-b"}, Resolution: "unresolved", Reason: "both sources remain current"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	gap, ok := sourceAcceptanceGap(first.State.Gaps, "unresolved conflict: deployment owner")
	if !ok || gap.Severity != "required" || gap.Source != "checkpoint://local" {
		t.Fatalf("unresolved generic conflict lacks required gap: %#v", first.State.Gaps)
	}

	promoted, err := Promote(root, id, "durable-finding", "project")
	if err != nil {
		t.Fatal(err)
	}
	item, section, ok := FindItem(promoted, "durable-finding")
	if !ok || section != "project" || item.Durability != "project" || item.Source != "test://acceptance" {
		t.Fatalf("promotion did not preserve stable item/provenance: item=%#v section=%q", item, section)
	}
	if promoted.State.Promoted["durable-finding"] != "project" {
		t.Fatalf("promotion map = %#v", promoted.State.Promoted)
	}
	changes := Diff(first, promoted)
	var removedConfirmed, addedProject bool
	for _, change := range changes {
		if change.ID != "durable-finding" {
			continue
		}
		if change.Kind == "-" && change.Section == "confirmed" {
			removedConfirmed = true
		}
		if change.Kind == "+" && change.Section == "project" {
			addedProject = true
		}
	}
	if !removedConfirmed || !addedProject {
		t.Fatalf("promotion semantic diff = %#v", changes)
	}
	explanation, err := Explain(root, id, "durable-finding")
	if err != nil {
		t.Fatal(err)
	}
	if explanation.SnapshotID != promoted.ID || explanation.Section != "project" || explanation.Item.Source != "test://acceptance" || !strings.Contains(explanation.Why, "test://acceptance") {
		t.Fatalf("promotion explanation = %#v", explanation)
	}
}

func TestSourceAcceptanceMetricsRecordActualOperations(t *testing.T) {
	root := t.TempDir()
	id := sourceAcceptanceIdentity()
	if _, err := Resume(root, id, 10000); err != nil {
		t.Fatal(err)
	}
	if _, err := Checkpoint(root, id, State{Objective: "measure local cache activity"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Resume(root, id, 10000); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshDetailed(root, id); err != nil {
		t.Fatal(err)
	}
	RecordLookup(root)

	metrics := ReadMetrics(root)
	if metrics.ResumeTotal != 2 || metrics.ResumeCacheMisses != 1 || metrics.ResumeCacheHits != 1 {
		t.Fatalf("resume metrics = %#v", metrics)
	}
	if metrics.CheckpointTotal != 1 || metrics.RefreshTotal != 1 || metrics.RefreshIncrementalTotal != 1 || metrics.RefreshFullTotal != 0 || metrics.LookupTotal != 1 {
		t.Fatalf("operation metrics = %#v", metrics)
	}
	if metrics.SnapshotBytesTotal == 0 || metrics.PacketBytesTotal == 0 || metrics.PacketTokensTotal == 0 {
		t.Fatalf("payload metrics were not recorded: %#v", metrics)
	}
}
