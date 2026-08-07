package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// friction reads across the whole store; a peer's recurring errors (its infra
// IPs, its hosts) surfaced here even when the trust policy withheld imported
// content from search. Browsing is the search activation, as in last/stats
// (#937, friction gap #1120).
func TestFrictionHonoursTheTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	// One local tool-output session (does not recur on its own).
	local := `{"type":"user","sessionId":"m1","cwd":"/m","timestamp":"2026-07-25T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"local one-off oddity"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "m1.jsonl"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Import three peer sessions that all hit the same error.
	in := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 1; i <= 3; i++ {
		b.WriteString(`{"harness":"claude","session_id":"pe` + string(rune('0'+i)) + `","project":"p","role":"tool-output","text":"connection refused to 192.0.2.5:5432 backpressure","time":"2026-07-2` + string(rune('0'+i)) + `T10:00:00Z"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(in, "deja-sync-peer-1.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Import(index.DefaultDir(), in); err != nil {
		t.Fatal(err)
	}

	// No policy: the imported error recurs and is shown.
	if out, _ := captureRun(t, "friction"); !strings.Contains(out, "192.0.2.5") {
		t.Fatalf("without policy the imported error should surface:\n%s", out)
	}
	// Deny imported for the search activation: friction must withhold it.
	pol := filepath.Join(os.Getenv("HOME"), ".config", "deja", "policy.json")
	if err := os.MkdirAll(filepath.Dir(pol), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "192.0.2.5") {
		t.Errorf("friction leaked an imported peer error past a deny policy:\n%s", out)
	}
}
