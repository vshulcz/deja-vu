package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The receipt named the policy whenever it was not the default, so the line
// read the same whether the rule had withheld memory from this very session or
// merely existed. `search` has reported the count since it had one.
func TestHookReceiptSaysWhenThePolicyWithheldSomething(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local: the ticker window is 30s"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"auto":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	message := func(t *testing.T) string {
		t.Helper()
		out, err := captureRun(t, "hook-context")
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			SystemMessage string `json:"systemMessage"`
		}
		if strings.TrimSpace(out) == "" {
			return ""
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("hook output is not JSON: %q (%v)", out, err)
		}
		return resp.SystemMessage
	}

	// The rule is set, but there is nothing imported for it to hide.
	got := message(t)
	if !strings.Contains(got, "policy: local-only") {
		t.Fatalf("the receipt does not name the policy: %q", got)
	}
	if strings.Contains(got, "withheld") {
		t.Errorf("a rule that hid nothing claimed it withheld something: %q", got)
	}

	// Now a session from another machine, in the same project, which the rule
	// does hide.
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "proj", Role: "user", Text: "PEER: the ticker window should be 5s"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(dir + ".receipt")
	for _, p := range mustGlob(t, dir+".hookcache-*") {
		_ = os.Remove(p)
	}

	got = message(t)
	if !strings.Contains(got, "withheld") {
		t.Errorf("the receipt does not say the rule withheld a session: %q", got)
	}
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
