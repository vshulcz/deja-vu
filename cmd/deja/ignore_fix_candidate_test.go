package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ignore rule reached the confirmed pairs and not the sightings held back
// for a second one: with a single session in an ignored tree, search and `deja
// how` refused it while `deja fix` handed back the command it ran there
// (#3403). #2660 is the same defect for the confirmed half.
func TestFixDoesNotServeASightingFromTheIgnoredTree(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	policyFile := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// One session only: two would confirm the pair and take the other path.
	writeClaudeFixture(t, filepath.Join(root, "-w-scratch", "one.jsonl"), "one", []string{
		`{"type":"user","sessionId":"one","cwd":"/w/scratch","timestamp":"2026-07-01T10:00:00Z",` +
			`"message":{"role":"user","content":"the zarnak bus refuses to bind"}}`,
		`{"type":"user","sessionId":"one","cwd":"/w/scratch","timestamp":"2026-07-01T10:01:00Z",` +
			`"message":{"role":"user","content":[{"type":"tool_result","content":"zarnakctl: FATAL: could not bind sprocket bus at 192.0.2.7:7788"}]}}`,
		`{"type":"assistant","sessionId":"one","cwd":"/w/scratch","timestamp":"2026-07-01T10:02:00Z",` +
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"terraform apply --zarnak-bus"}}]}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "fix", "zarnakctl: FATAL: could not bind sprocket bus at 192.0.2.7:7788")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "terraform apply") {
		t.Errorf("deja fix served a sighting out of the ignored tree:\n%s", out)
	}
	// And the same store answers once the rule is lifted, so this is the rule
	// working rather than the fixture never having had anything to serve.
	if err := os.WriteFile(policyFile, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "fix", "zarnakctl: FATAL: could not bind sprocket bus at 192.0.2.7:7788")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "terraform apply") {
		t.Errorf("with the rule lifted the sighting is still withheld:\n%s", out)
	}
}
