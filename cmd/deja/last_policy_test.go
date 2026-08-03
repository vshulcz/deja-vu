package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The listing is titles and project names, which is what the trust policy
// exists to keep off the screen — and it was the one path that never consulted
// it, while `search` under the same rule refused and said so (#937).
func TestLastHonoursTheTrustPolicy(t *testing.T) {
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
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer9", Project: "secret-client/api", Role: "user", Text: "CONFIDENTIAL peer title: migrating the vault tokens"})
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

	// Without a policy the peer's title is listed, which is the point of the
	// command.
	out, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "CONFIDENTIAL peer title") {
		t.Fatalf("the imported session was not listed at all:\n%s", out)
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out, err = captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "CONFIDENTIAL peer title") || strings.Contains(out, "secret-client") {
		t.Errorf("the listing leaked a session the policy hides:\n%s", out)
	}
	if !strings.Contains(out, "local work on the ticker window") {
		t.Errorf("the listing dropped the local session too:\n%s", out)
	}
	// And it says what it withheld rather than quietly showing less.
	note, err := captureRunStderr(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "trust policy hides") {
		t.Errorf("nothing said the policy withheld a row: %q", note)
	}

	// The JSON form is the same data and must not be the way around it.
	raw, err := captureRun(t, "last", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "secret-client") {
		t.Errorf("--json leaked what the text form hides:\n%s", raw)
	}
}

// The rule that emptied the listing is named a line above, so "no sessions
// indexed yet — run `deja index`" is advice for a state deja is not in: the
// backside of teaching the listing to filter at all (#949).
func TestLastEmptiedByPolicyDoesNotAdviseAnIndexRun(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "api", Role: "user", Text: "peer text"})
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

	out, err := captureRunStderr(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "trust policy hides") {
		t.Errorf("the listing did not name the rule that emptied it: %q", out)
	}
	if strings.Contains(out, "no sessions indexed yet") {
		t.Errorf("a store full of filtered sessions was called unindexed: %q", out)
	}
}
