package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// stats is the one screen deja calls "wrapped for sharing", and it named the
// other machine's project while every other surface withheld it (#966).
func TestStatsObeysTheTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "p1", Project: "secret/api", Role: "user", Text: "peer text"})
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

	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "imported:secret") {
		t.Fatalf("the imported project is not in stats at all:\n%s", out)
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out, err = captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "imported:secret") {
		t.Errorf("stats named a project the policy hides:\n%s", out)
	}
	note, err := captureRunStderr(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "trust policy hides") {
		t.Errorf("stats dropped sessions without saying why: %q", note)
	}
}

// The rule that emptied the report is named a line above, so "nothing indexed
// yet — run `deja index`" is advice for a state deja is not in: the backside
// `last` grew in #949 and stats kept (#983).
func TestStatsEmptiedByPolicyDoesNotAdviseAnIndexRun(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "work/api", Role: "user", Text: "peer work on the vault"})
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
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "nothing indexed yet") {
		t.Errorf("an index full of withheld sessions was called unbuilt:\n%s", out)
	}
	if !strings.Contains(out, "trust policy") {
		t.Errorf("stats does not name the rule that emptied it:\n%s", out)
	}

	// A machine with nothing indexed keeps the advice that fits it.
	tmp2 := hermeticEnv(t)
	dir2 := filepath.Join(tmp2, "index.db")
	if err := index.Ensure(dir2, "", false, nil); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing indexed yet") {
		t.Errorf("an empty index lost its advice:\n%s", out)
	}
}
