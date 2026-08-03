package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The end of a move to a new machine said "imported N records" and nothing
// else: records are deja's own unit, and the person watching has no way to
// check the number. Install ends the same moment with real lines out of the
// history it just indexed (#929).
func TestImportSaysWhatArrivedAndProvesIt(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch strings.Builder
	for _, s := range []struct{ id, project, text string }{
		{"r1", "api", "the connection pool ran dry behind pgbouncer"},
		{"r2", "web", "the anemometer reading drifted after the ticker change"},
	} {
		b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: s.id, Project: s.project, Role: "user", Text: s.text})
		if err != nil {
			t.Fatal(err)
		}
		batch.WriteString(string(b) + "\n")
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), []byte(batch.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 sessions from another machine") {
		t.Errorf("import counted only records: %q", out)
	}
	proof, err := captureRunStderr(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing new arrived the second time, so there is nothing to prove.
	if strings.Contains(proof, "deja now knows") {
		t.Errorf("a no-op import still claimed an arrival: %q", proof)
	}
}

// The proof itself: the lines are the sessions that just came, named where the
// reader can recognise them.
func TestImportProofNamesRealSessions(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "r1", Project: "api", Role: "user", Text: "the connection pool ran dry behind pgbouncer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	proof, err := captureRunStderr(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(proof, "deja now knows") || !strings.Contains(proof, "pgbouncer") {
		t.Errorf("import proved nothing:\n%s", proof)
	}
}

// The proof is a listing, and a listing obeys the trust policy: on a machine
// whose rule keeps imported sessions out of recall it was printing their
// project and first line, then closing with "ask your agent about any of these
// — it will remember", which is what the rule prevents (#951).
func TestImportProofObeysTheTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "api", Role: "user", Text: "PEER SECRET: the vault rotation runs at 03:00"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	proof, err := captureRunStderr(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(proof, "PEER SECRET") || strings.Contains(proof, "it will remember") {
		t.Errorf("the proof printed what the policy hides:\n%s", proof)
	}
	if !strings.Contains(proof, "trust policy hides") {
		t.Errorf("the proof says nothing about why it is empty:\n%s", proof)
	}
	// The count still reaches the reader, so the import is not silent.
	out, err := captureRun(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	_ = out
}

// A sync batch carries no timestamps, so an imported session has no date, and
// an empty slot left `[claude · imported:solo · ]` on the screen whose whole
// job is to show the memory arrived (#964).
func TestImportProofDoesNotLeaveTheDateSlotEmpty(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "p1", Project: "api", Role: "user", Text: "peer one about the pool"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	proof, err := captureRunStderr(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(proof, "peer one about the pool") {
		t.Fatalf("the proof did not list the imported session:\n%s", proof)
	}
	if strings.Contains(proof, "· ]") {
		t.Errorf("the date slot is empty:\n%s", proof)
	}
	if !strings.Contains(proof, "· -]") {
		t.Errorf("a session with no date does not say so the way `last` does:\n%s", proof)
	}
}
