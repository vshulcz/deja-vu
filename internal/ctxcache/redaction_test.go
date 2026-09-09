package ctxcache

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestRedactSnapshotRedactsAgentTextWithoutChangingOperationalMetadata(t *testing.T) {
	password := "plain-password-value-123456789"
	token := "plain-token-value-987654321"
	pem := "-----BEGIN PRIVATE KEY-----\nYWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXo=\n-----END PRIVATE KEY-----"
	s := Snapshot{
		ID:          "snapshot-id",
		Fingerprint: "fingerprint",
		Identity: Identity{
			WorkspaceID: "workspace-id",
			TaskID:      "TASK-123",
		},
		Freshness: Freshness{ComponentVersions: map[string]string{"task": "raw-source-digest"}},
		State: State{
			Objective: "deploy with password=" + password,
			Project: map[string]any{
				"configuration": map[string]any{"password": password},
				"non_secret":    "retain this detail",
			},
			Tests: map[string]any{
				"attempts": []any{map[string]any{"token": token}},
				"typed":    map[string]string{"api_key": token},
			},
			ProjectSource: "file:///safe/project.json#sha256=raw-source-digest",
			TestsSource:   "file:///safe/task.json#sha256=raw-source-digest",
			Confirmed: []Item{{
				ID:       "stable-finding-id",
				Text:     "token=" + password,
				Status:   "Bearer " + token,
				Source:   "file:///safe/task.json#sha256=raw-source-digest",
				Supports: []string{"private key:\n" + pem},
			}},
			Gaps:      []Gap{{Subject: "password=" + password, Severity: "required", Reason: "token=" + token, RetrievalHint: pem, Source: "file:///safe/task.json"}},
			Conflicts: []Conflict{{Subject: "secret=" + password, Candidates: []string{"Bearer " + token, "manual check"}, Resolution: "unresolved", Reason: pem, Source: "checkpoint://local"}},
			Sources:   []Source{{Name: "task", Layer: "task", Path: "/safe/task.json", Version: "raw-source-digest"}},
			Promoted:  map[string]string{"stable-finding-id": "project"},
		},
	}

	got := RedactSnapshot(s)
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{password, token, pem} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("redacted snapshot still contains %q: %s", secret, encoded)
		}
	}
	if got.State.Project["non_secret"] != "retain this detail" {
		t.Fatalf("non-secret project content changed: %#v", got.State.Project)
	}
	if !reflect.DeepEqual(got.Identity, s.Identity) || got.ID != s.ID || got.Fingerprint != s.Fingerprint {
		t.Fatalf("identity metadata changed: got=%#v want=%#v", got, s)
	}
	if got.State.ProjectSource != s.State.ProjectSource || got.State.TestsSource != s.State.TestsSource || !reflect.DeepEqual(got.State.Sources, s.State.Sources) {
		t.Fatalf("source metadata changed: got=%#v want=%#v", got.State, s.State)
	}
	if got.State.Confirmed[0].ID != s.State.Confirmed[0].ID || got.State.Confirmed[0].Source != s.State.Confirmed[0].Source || got.State.Promoted["stable-finding-id"] != "project" {
		t.Fatalf("stable item metadata changed: %#v", got.State)
	}
	// The helper must not mutate the maps or slices held by an adapter after it
	// returns the sanitized snapshot.
	if s.State.Project["configuration"].(map[string]any)["password"] != password || s.State.Tests["attempts"].([]any)[0].(map[string]any)["token"] != token || s.State.Confirmed[0].Text == got.State.Confirmed[0].Text {
		t.Fatalf("redaction mutated caller state: %#v", s.State)
	}
}

func TestRedactSnapshotHonorsNoRedactOptOut(t *testing.T) {
	t.Setenv("DEJA_NO_REDACT", "1")
	s := Snapshot{State: State{
		Project:   map[string]any{"password": "plain-password-value-123456789"},
		Confirmed: []Item{{ID: "item", Text: "token=plain-token-value-987654321"}},
	}}
	if got := RedactSnapshot(s); !reflect.DeepEqual(got, s) {
		t.Fatalf("DEJA_NO_REDACT changed snapshot: got=%#v want=%#v", got, s)
	}
}

// This regression covers every writer whose snapshot is returned to a caller
// or copied from a prior snapshot. It intentionally exercises a source-backed
// checkpoint too: source contents are only safe once their materialized State
// has passed through RedactSnapshot before save.
func TestCacheWritersPersistAndReturnRedactedSnapshots(t *testing.T) {
	root := t.TempDir()
	fixtureDir := t.TempDir()
	id := testIdentity("redaction-head")
	sourcePath := fixtureDir + "/task.json"
	sourceSecret := "source-token-value-123456789"
	if err := os.WriteFile(sourcePath, []byte(`{"confirmed":[{"id":"from-source","text":"token=`+sourceSecret+`"}],"tests":{"credentials":{"password":"`+sourceSecret+`"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := Checkpoint(root, id, State{Sources: []Source{{Name: "task", Layer: "task", Path: sourcePath}}})
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotOmits(t, checkpoint, sourceSecret)
	loaded, err := Load(root, id)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotOmits(t, loaded, sourceSecret)

	refreshedSecret := "refreshed-token-value-123456789"
	if err := os.WriteFile(sourcePath, []byte(`{"confirmed":[{"id":"from-source","text":"token=`+refreshedSecret+`"}],"tests":{"credentials":{"password":"`+refreshedSecret+`"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	refreshed, err := RefreshDetailed(root, id)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotOmits(t, refreshed.Snapshot, refreshedSecret)

	// Simulate a checkpoint written under the documented opt-out. Later
	// writers must not copy that historical raw data into new snapshots.
	legacySecret := "legacy-token-value-123456789"
	t.Setenv("DEJA_NO_REDACT", "1")
	legacy, err := Checkpoint(root, id, State{Confirmed: []Item{{ID: "legacy", Text: "token=" + legacySecret}}})
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotContains(t, legacy, legacySecret)
	legacyPersisted, err := Load(root, id)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotContains(t, legacyPersisted, legacySecret)
	t.Setenv("DEJA_NO_REDACT", "")
	promoted, err := Promote(root, id, "legacy", "project")
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotOmits(t, promoted, legacySecret)

	// Invalidation writes a historical snapshot directly, so it needs the same
	// boundary even though it does not return a State payload.
	legacyInvalidate := "invalidate-token-value-123456789"
	t.Setenv("DEJA_NO_REDACT", "1")
	if _, err := Checkpoint(root, id, State{Confirmed: []Item{{ID: "invalidate", Text: "token=" + legacyInvalidate}}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NO_REDACT", "")
	if err := Invalidate(root, id, "task"); err != nil {
		t.Fatal(err)
	}
	invalidated, err := Load(root, id)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotOmits(t, invalidated, legacyInvalidate)
}

func TestCheckpointRejectsConflictCandidatesThatCollideAfterRedaction(t *testing.T) {
	root := t.TempDir()
	_, err := Checkpoint(root, testIdentity("redaction-collision"), State{Conflicts: []Conflict{{
		Subject:    "deployment credential",
		Candidates: []string{"token=first-token-value-123456789", "token=second-token-value-123456789"},
		Resolution: "unresolved",
	}}})
	if err == nil || !strings.Contains(err.Error(), "duplicate candidates") {
		t.Fatalf("Checkpoint collision error = %v, want duplicate candidates", err)
	}
}

func assertSnapshotOmits(t *testing.T, s Snapshot, secret string) {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), secret) {
		t.Fatalf("snapshot contains %q: %s", secret, b)
	}
}

func assertSnapshotContains(t *testing.T, s Snapshot, secret string) {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), secret) {
		t.Fatalf("snapshot does not contain %q: %s", secret, b)
	}
}
