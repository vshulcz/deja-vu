package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBenchLiveEvaluatesRepoReadOnlyWithoutMutatingHookSeenOrInjections(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "payments")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/payments\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	relayFile := filepath.Join(proj, "relay.go")
	if err := os.WriteFile(relayFile, []byte("package payments\n\nfunc DispatchSettlementBatch() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	retryFile := filepath.Join(proj, "retry.go")
	if err := os.WriteFile(retryFile, []byte("package payments\n\nfunc BackoffSchedule() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"init files"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, relayFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"1","new_string":"1"}}]}}`, proj, retryFile),
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s2.jsonl"), "s2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"harden relay and retry"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e3","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, relayFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:02:00Z","message":{"role":"assistant","content":"We decided to use advisory locks in relay.go so concurrent workers never double-settle a batch."}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:03:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:03:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"--- FAIL: TestBackoffScheduleJitterBounds (0.01s)\nFAIL\texample.com/payments\t0.02s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:04:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e4","name":"Edit","input":{"file_path":%q,"old_string":"1","new_string":"1"}}]}}`, proj, retryFile),
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	out, err := captureRun(t, "bench", "live", "--json")
	if err != nil {
		t.Fatalf("bench live --json: %v", err)
	}
	var rep liveBenchReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("unmarshal liveBenchReport: %v\n%s", err, out)
	}
	if rep.SourceFiles != 2 || rep.WorkedFiles != 2 {
		t.Fatalf("expected 2 source files and 2 worked files, got %+v", rep)
	}
	if rep.PriorDecision != 1 || rep.PastBreakage != 1 || rep.ActionableFiles != 2 {
		t.Fatalf("expected 1 prior decision + 1 past breakage (2 actionable), got %+v", rep)
	}
	if rep.EphemeralNoise != 0 {
		t.Fatalf("expected 0 ephemeral noise, got %+v", rep)
	}

	// Read-only invariant: bench live must not create .hookseen or .injections.jsonl.
	for _, sideEffect := range []string{".hookseen", ".injections.jsonl", ".usage.jsonl"} {
		if _, statErr := os.Stat(filepath.Join(idxDir, sideEffect)); !os.IsNotExist(statErr) {
			t.Errorf("bench live must be read-only, found %s in index dir", sideEffect)
		}
	}

	textOut, err := captureRun(t, "bench", "live")
	if err != nil {
		t.Fatalf("bench live text: %v", err)
	}
	if !strings.Contains(textOut, "actionable recall: 2 files") {
		t.Errorf("unexpected text output:\n%s", textOut)
	}
	if _, err := captureRun(t, "bench", "live", "--seed", "1"); err == nil {
		t.Errorf("expected bench live --seed 1 to be rejected")
	}
}
