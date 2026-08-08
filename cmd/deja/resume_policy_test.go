package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// resume takes an exact id, but naming one is still browsing under the search
// activation: show, share, promote and handoff all refuse a session a trust
// rule withholds, and resume must too — otherwise the command that reopens a
// hidden session (and, with --exec, the session itself) leaks past the rule.
func TestResumeObeysTheTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work to reopen"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// No rule: resume prints the reopening command, as it should.
	var open bytes.Buffer
	if err := runResume(dir, []string{"loc"}, &open); err != nil {
		t.Fatalf("plain resume failed: %v", err)
	}
	if !strings.Contains(open.String(), "claude --resume loc") {
		t.Fatalf("plain resume did not print the command: %q", open.String())
	}

	// A rule that withholds local sessions must stop resume from revealing one.
	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	var out bytes.Buffer
	err := runResume(dir, []string{"loc"}, &out)
	if err == nil {
		t.Fatalf("resume leaked a policy-hidden session: %q", out.String())
	}
	if strings.Contains(out.String(), "claude --resume") {
		t.Errorf("resume printed the reopen command for a hidden session: %q", out.String())
	}
	if !strings.Contains(err.Error(), "no session matches") {
		t.Errorf("hidden resume error = %v, want the same not-found form the siblings use", err)
	}
}
