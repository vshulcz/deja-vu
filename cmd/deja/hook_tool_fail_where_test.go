package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The warning is keyed on the shape of the command, and a shape travels: `go
// test ./...` is every Go checkout on the machine. In a directory deja had
// never seen, 2 of 15 ordinary build and test commands drew a warning and both
// were another repository's own failure — a named test, a ten-minute timeout —
// with nothing in the line to say so.
func TestTheCommandWarningNamesTheProjectItHappenedIn(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// Two sessions in one project where the suite ended on a named test.
	for _, id := range []string{"f1", "f2"} {
		writeClaudeFixture(t, filepath.Join(root, "-work-payments", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/payments","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"run the suite"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/payments","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/payments","timestamp":"2026-01-02T03:04:07Z","toolUseResult":{"stdout":"--- FAIL: TestLedgerRollsBack\nFAIL\n","stderr":""},"message":{"role":"user","content":[{"type":"tool_result","content":"--- FAIL: TestLedgerRollsBack\nFAIL\n"}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	// Asked from the project it happened in: the line says what happened and
	// does not spend words saying where.
	here := commandFailureLine(dir, "/work/payments", "go test ./...")
	if !strings.Contains(here, "TestLedgerRollsBack") {
		t.Fatalf("the failure did not reach the line at home: %q", here)
	}
	if strings.Contains(here, " in work/payments") || strings.Contains(here, " in payments") {
		t.Errorf("the line named the project the agent is already in: %q", here)
	}

	// Asked from a checkout deja has never seen: the same failure, and where it
	// belongs — otherwise it reads as this project's suite being broken.
	away := commandFailureLine(dir, "/work/warehouse", "go test ./...")
	if !strings.Contains(away, "TestLedgerRollsBack") {
		t.Fatalf("the failure did not reach the line away from home: %q", away)
	}
	if !strings.Contains(away, "payments") {
		t.Errorf("another project's failure was reported without naming it: %q", away)
	}
}
