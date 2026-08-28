package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// With every tool-output session withheld by a rule, friction said the machine
// had never recorded any — a claim about the store, on the surface someone
// reaches for when recall feels thin (#2319).
func TestFrictionNamesTheRuleThatEmptiedIt(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own question, no commands run"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	batch := t.TempDir()
	var buf bytes.Buffer
	for j := 0; j < 3; j++ {
		rec := index.SyncRecord{
			Harness: "claude", SessionID: "peer" + string(rune('0'+j)), Project: "secretclient/api",
			Role: "tool-output", Text: "panic: quaxbolt overflow in ledger/quaxbolt.go",
			Time: time.Date(2026, 8, 4, 12, j, 0, 0, time.UTC),
		}
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSync(dir, []string{"import", batch}); err != nil {
		t.Fatal(err)
	}

	// The premise: without a rule, friction has something to report.
	var open bytes.Buffer
	if err := runFriction(dir, nil, &open); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(open.String(), "quaxbolt") {
		t.Fatalf("the imported error is not recurring even with no rule set:\n%s", open.String())
	}

	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyPath)

	var closed bytes.Buffer
	if err := runFriction(dir, nil, &closed); err != nil {
		t.Fatal(err)
	}
	got := closed.String()
	if strings.Contains(got, "quaxbolt") {
		t.Fatalf("the withheld error is still on screen:\n%s", got)
	}
	if strings.Contains(got, "none of the") {
		t.Errorf("friction still says the machine recorded no tool output:\n%s", got)
	}
	if !strings.Contains(got, "trust policy") {
		t.Errorf("friction does not name the rule that emptied it:\n%s", got)
	}
}
