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
