package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sweep in ignore_every_surface_test.go writes the same error and remedy in
// three sessions, so the pair is corroborated and takes the branch that checks
// the ignore rule. One session — the ordinary case — is held back as an
// unconfirmed sighting, and that branch checked only the trust policy: `deja
// fix` handed back a command out of the tree search and `how` refuse to read
// (#3403).
func TestFixDoesNotServeAnUnconfirmedRemedyFromTheIgnoredTree(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	policyFile := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// One session only, so the remedy stays a sighting; and a remedy that
	// names nothing the error names, so it is not promoted by sharing a term.
	writeClaudeFixture(t, filepath.Join(root, "-w-scratch", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","cwd":"/w/scratch","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the zonkobuffer keeps overflowing"}}`,
		`{"type":"user","sessionId":"s1","cwd":"/w/scratch","timestamp":"2026-07-01T10:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"panic: zonkobuffer overflow at shard 7"}]}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"/w/scratch","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"terraform apply -target module.klindra"}}]}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "fix", "panic: zonkobuffer overflow at shard 7")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "klindra") {
		t.Errorf("a withheld session's command reached deja fix:\n%s", out)
	}
	if !strings.Contains(out, "no session") {
		t.Errorf("want the refusal the other surfaces give, got:\n%s", out)
	}
}
