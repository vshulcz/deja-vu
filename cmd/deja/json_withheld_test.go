package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The reason an answer is empty travelled on stderr only, so a caller reading
// --json — a script, another agent, our own benchmarks — saw the same object
// whether the history was empty or a rule withheld every row (#990).
func TestJSONSaysWhenTheTrustPolicyWithheldRows(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "work/api", Role: "user", Text: "peer two about the pool cap"})
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

	decode := func(t *testing.T, args ...string) map[string]any {
		t.Helper()
		out, err := captureRun(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatalf("%v is not JSON: %v\n%s", args, err, out)
		}
		return m
	}

	// No rule: the field is absent, so nothing changes for existing callers.
	if got, ok := decode(t, "search", "pool", "--json")["policy_withheld"]; ok {
		t.Errorf("an unrestricted search reported withheld rows: %v", got)
	}
	if got, ok := decode(t, "last", "--json")["policy_withheld"]; ok {
		t.Errorf("an unrestricted listing reported withheld rows: %v", got)
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	got := decode(t, "search", "pool", "--json")
	if got["policy_withheld"] != float64(1) {
		t.Errorf("search --json does not say the rule emptied it: %v", got)
	}
	if hits, _ := got["hits"].([]any); len(hits) != 0 {
		t.Errorf("the hidden hit was returned anyway: %v", got["hits"])
	}
	got = decode(t, "last", "--json")
	if got["policy_withheld"] != float64(1) {
		t.Errorf("last --json does not say a row was kept out: %v", got)
	}
	if ss, _ := got["sessions"].([]any); len(ss) != 1 {
		t.Errorf("the listing returned the wrong rows: %v", got["sessions"])
	}
}
