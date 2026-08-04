package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `imported:x` has the shape of a rule deja consults, so #941's check passes it
// even when x is a machine name — and the part after the colon is the first
// path component of the project on the machine the session came from, never a
// machine name. Such a rule reads as in force forever (#955).
func TestDoctorNamesAnImportRuleThatMatchesNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "p1", Project: "work/api", Role: "user", Text: "peer note"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported:laptop":false,"imported:work":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	var out bytes.Buffer
	doctorPolicy(&out, dir)
	got := out.String()
	if !strings.Contains(got, `"imported:laptop" matches nothing`) {
		t.Errorf("a rule that can never fire is reported as in force:\n%s", got)
	}
	// The one that does match is not called inert.
	if strings.Contains(got, `"imported:work" matches nothing`) {
		t.Errorf("a rule that hides a session was called inert:\n%s", got)
	}
}
