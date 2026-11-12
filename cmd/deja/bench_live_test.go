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
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s3.jsonl"), "s3", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s3","cwd":%q,"timestamp":"2026-03-03T10:00:00Z","message":{"role":"user","content":"prevent advisory locks double-settle batch contention"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s3","cwd":%q,"timestamp":"2026-03-03T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e5","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, relayFile),
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	t.Chdir(proj)

	out, err := captureRun(t, "bench", "live", "--json")
	if err != nil {
		t.Fatalf("bench live --json: %v", err)
	}
	var rep liveBenchReport
	if unmarshalErr := json.Unmarshal([]byte(out), &rep); unmarshalErr != nil {
		t.Fatalf("unmarshal liveBenchReport: %v\n%s", unmarshalErr, out)
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

func TestBenchLiveClassificationAndAuditHelpers(t *testing.T) {
	t.Parallel()

	var rep liveBenchReport
	classifyLiveHookLine(&rep, "")
	classifyLiveHookLine(&rep, "relay.go"+standingLabel+"never push directly")
	classifyLiveHookLine(&rep, "relay.go"+repoDecisionLabel+"keep monotonic cursor")
	classifyLiveHookLine(&rep, "relay.go"+revertedLabel+"abandoned mutex retry because it deadlocked under storm — usually edited alongside store.go")
	classifyLiveHookLine(&rep, "relay.go"+breakageLabel+"TestRelay: nil cursor — verified here: go test ./...")
	classifyLiveHookLine(&rep, "relay.go"+decisionLabel+"I fixed all 14 tests and CI checks pass.")
	classifyLiveHookLine(&rep, "relay.go — test target: go test ./pkg/relay")
	classifyLiveHookLine(&rep, "relay.go — usually edited alongside store.go")
	if rep.Silent != 1 || rep.Standing != 1 || rep.RepoMemory != 1 || rep.RevertedApproach != 1 || rep.PastBreakage != 1 || rep.VerifiedCommand != 1 || rep.EphemeralNoise != 1 || rep.TestTargetOnly != 1 {
		t.Fatalf("unexpected classification counts: %+v", rep)
	}
	if !liveOutputNamesAnyTarget("likely target: pkg/relay_worker.go", []string{"pkg/relay_worker.go"}) || !liveOutputNamesAnyTarget("see relay_worker.go", []string{"pkg/relay_worker.go"}) || liveOutputNamesAnyTarget("", []string{"pkg/relay_worker.go"}) {
		t.Errorf("unexpected liveOutputNamesAnyTarget behavior")
	}
	if emptyRep, err := measureLiveRepo(t.TempDir(), t.TempDir()); err != nil || emptyRep.SourceFiles != 0 {
		t.Errorf("expected empty repo report, got %+v (%v)", emptyRep, err)
	}

	if !liveSkippedDir("node_modules") || liveSkippedDir("internal") {
		t.Errorf("unexpected liveSkippedDir behavior")
	}
	if isLiveBenchSourceFile("api/service.pb.go", "service.pb.go") || !isLiveBenchSourceFile("api/service.rs", "service.rs") || isLiveBenchSourceFile("README.md", "README.md") {
		t.Errorf("unexpected isLiveBenchSourceFile behavior")
	}
	if !liveOutputHasGuardrail("x"+revertedLabel+"y") || liveOutputHasGuardrail("plain line") {
		t.Errorf("unexpected liveOutputHasGuardrail behavior")
	}
	if !liveOutputHasTestRecipe("run pytest tests/unit") || liveOutputHasTestRecipe("no command") {
		t.Errorf("unexpected liveOutputHasTestRecipe behavior")
	}
	if !symbolBriefAlreadyCoversTarget("Prior work in store_codec.go (Decode)", "likely target: store_codec.go — test target: go test ./...") {
		t.Errorf("expected symbolBriefAlreadyCoversTarget = true")
	}
	if symbolBriefAlreadyCoversTarget("Prior work in other.go", "no target prefix") {
		t.Errorf("expected symbolBriefAlreadyCoversTarget = false")
	}
	if got := percentileMS(nil, 0.5); got != 0 {
		t.Errorf("expected percentileMS(nil) = 0, got %v", got)
	}

	tmp := t.TempDir()
	idxDir := filepath.Join(tmp, "index.db")
	snapLines := strings.Join([]string{
		`{"t":"2026-04-01T10:00:00Z","kind":"tool","bytes":120,"digest":"relay.go — prior decision: I fixed all 14 tests and CI checks pass."}`,
		`{"t":"2026-04-01T10:01:00Z","kind":"dejavu","bytes":240,"digest":"deja vu"}`,
		`{"t":"2026-04-01T10:02:00Z","kind":"context","bytes":360,"digest":"digest"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(idxDir+".injections.jsonl", []byte(snapLines), 0o644); err != nil {
		t.Fatal(err)
	}
	audit := auditLiveInjections(idxDir)
	if audit.Total != 3 || audit.Tool != 1 || audit.DejaVu != 1 || audit.Context != 1 || audit.ToolNoise != 1 || audit.MedianBytes != 240 {
		t.Fatalf("unexpected injection audit: %+v", audit)
	}
}
