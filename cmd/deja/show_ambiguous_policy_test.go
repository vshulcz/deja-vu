package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The ambiguity note counted every session a prefix reached, including the
// ones the trust policy withholds — so a rule that hides a peer's work still
// announced that it exists, and the advice led to a session nobody could
// open (#2401).
func TestTheAmbiguityNoteCountsWhatTheRuleAllows(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	line := `{"type":"user","sessionId":"shared-local","timestamp":"` + at + `","cwd":"/work/app",` +
		`"message":{"role":"user","content":"the retry budget on main"}}`
	if err := os.WriteFile(filepath.Join(store, "shared-local.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	peer := `{"harness":"claude","session_id":"shared-peer","project":"secretclient/api","role":"user",` +
		`"text":"the client ledger cutover","time":"` + time.Now().Add(-time.Hour).UTC().Format(time.RFC3339) + `","origin":"laptop"}`
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(peer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	captureBoth(t, "sync", "import", batch)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile,
		[]byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	_, errOut := captureBoth(t, "show", "shared")
	if strings.Contains(errOut, "2 sessions match") {
		t.Errorf("the note counted a session the rule withholds:\n%s", errOut)
	}
	if strings.Contains(errOut, "sessions match") {
		t.Errorf("with one session reachable there is nothing ambiguous:\n%s", errOut)
	}
}
