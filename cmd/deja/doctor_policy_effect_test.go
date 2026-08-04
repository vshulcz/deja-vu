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

// doctor prints the rule's text, and the text is not its effect: `search
// local-only` reads the same whether it withholds nothing or the whole index,
// while every other surface says what it kept back (#978).
func TestDoctorPolicySaysHowMuchTheRuleWithholds(t *testing.T) {
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
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer9", Project: "work/api", Role: "user", Text: "peer work on the vault rotation"})
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

	var out bytes.Buffer
	doctorPolicy(&out, dir)
	got := out.String()
	if !strings.Contains(got, "withholds 1 of 2 indexed sessions") {
		t.Errorf("doctor does not say what the rule keeps back:\n%s", got)
	}
	// The paths the rule leaves alone must not claim to withhold anything.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "local+imported") && strings.Contains(line, "withholds") {
			t.Errorf("an unrestricted path was said to withhold rows: %q", line)
		}
	}

	// A rule that matches nothing in this index is the case the count exists
	// to separate from a rule that empties it.
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported:nobody":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorPolicy(&out, dir)
	if strings.Contains(out.String(), "withholds") {
		t.Errorf("a rule that hides nothing was counted as withholding:\n%s", out.String())
	}
}
