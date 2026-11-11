package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/policy"
)

func TestToolHookFindsEpisodeDecisionInMarathonSessionAndOutranksSiblingTestTarget(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "ledger")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/ledger\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "outbox_relay.go"), []byte("package ledger\n\nfunc RelayOutboxBatch() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Long test names in sibling *_test.go so synthetic `— test target:` consumes ~200 bytes.
	testSrc := `package ledger

import "testing"

func TestRelayOutboxBatchPreservesIdempotencyKeyAcrossRetries(t *testing.T) { _ = RelayOutboxBatch() }
func TestRelayOutboxBatchSkipsAlreadyCommittedPartitionsOnRestart(t *testing.T) { _ = RelayOutboxBatch() }
func TestRelayOutboxBatchHonoursAdvisoryLockTimeoutWhenDraining(t *testing.T) { _ = RelayOutboxBatch() }
`
	if err := os.WriteFile(filepath.Join(proj, "outbox_relay_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	// Session 1 touched outbox_relay.go briefly so session count >= 2.
	writeClaudeFixture(t, filepath.Join(root, "ledger", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"create outbox_relay.go"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, filepath.Join(proj, "outbox_relay.go")),
	})

	// Session 2 is a 220-message marathon: the decision on outbox_relay.go is at message 3,
	// followed by 215 unrelated messages that push it completely outside FindTailByIdentity(..., 150).
	var marathon []string
	marathon = append(marathon,
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"fix duplicate sends in outbox_relay.go"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, filepath.Join(proj, "outbox_relay.go")),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:02:00Z","message":{"role":"assistant","content":"We settled on SKIP LOCKED in outbox_relay.go instead of serializable retries because serializable retries aborted concurrent writers under burst load."}}`, proj),
	)
	for i := range 110 {
		ts := fmt.Sprintf("2026-03-02T11:%02d:%02dZ", i/60, i%60)
		marathon = append(marathon,
			fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":%q,"message":{"role":"user","content":"inspect metrics dashboard panel %d"}}`, proj, ts, i),
			fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":"Checked panel %d formatting in the metrics collector."}}`, proj, ts, i),
		)
	}
	writeClaudeFixture(t, filepath.Join(root, "ledger", "s2.jsonl"), "s2", marathon)

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", proj)
	payload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"},"session_id":"live-s3","cwd":%q}`, filepath.Join(proj, "outbox_relay.go"), proj)
	out := toolHookRun(t, payload)
	if !strings.Contains(out, "SKIP LOCKED") {
		t.Fatalf("expected marathon episode decision to surface and outrank synthetic test target, got:\n%s", out)
	}
}

func TestRepoMemoryDecisionPrecisionAndMultiFileMatch(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, ".deja"), 0o755); err != nil {
		t.Fatal(err)
	}
	mem := strings.Join([]string{
		`{"id":"m1","kind":"gotcha","text":"When reviewing pull requests, attach inline diff comments on modified files instead of top-level status notes.","sessions":2,"date":"2026-04-01","from":"deja"}`,
		`{"id":"m2","kind":"gotcha","text":"Editing writeback.go requires updating ingest.go alongside it (9 of 9 sessions)","files":["internal/index/ingest.go","internal/index/writeback.go","internal/sources/writeback.go"],"sessions":9,"date":"2026-04-02","from":"deja"}`,
		`{"id":"m3","kind":"rejected","text":"Keep sync cursor monotonic in internal/index/sync.go.","files":["internal/index/sync.go","internal/index/fixpair.go","internal/index/giveup.go"],"sessions":4,"date":"2026-04-03","from":"deja"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(tmp, ".deja", "memory.jsonl"), []byte(mem), 0o644); err != nil {
		t.Fatal(err)
	}

	pol := policy.Policy{}
	// 1) A 3-file entry must match all 3 listed files (no artificial <=2 cap).
	gotIngest := repoMemoryDecisionForFile(tmp, filepath.Join(tmp, "internal/index/ingest.go"), []string{"IngestSession"}, pol)
	if !strings.Contains(gotIngest, "Editing writeback.go requires updating ingest.go") {
		t.Errorf("expected 3-file repo memory match for internal/index/ingest.go, got %q", gotIngest)
	}

	// 2) Short generic symbols ("files", "status") must not cross-wire global fileless rules onto unrelated files.
	gotUnrelated := repoMemoryDecisionForFile(tmp, filepath.Join(tmp, "cmd/app/store_rows.go"), []string{"files", "status", "rows"}, pol)
	if gotUnrelated != "" {
		t.Errorf("expected no repo memory match for generic short symbols on unrelated file, got %q", gotUnrelated)
	}

	// 3) Directory-qualified entry internal/index/sync.go must not match cmd/app/sync.go.
	gotWrongDir := repoMemoryDecisionForFile(tmp, filepath.Join(tmp, "cmd/app/sync.go"), []string{"runSync"}, pol)
	if gotWrongDir != "" {
		t.Errorf("expected directory-qualified internal/index/sync.go not to match cmd/app/sync.go, got %q", gotWrongDir)
	}
}

func TestToolHookSurfacesRevertedApproachFromGaveUpSession(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "ledger")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/ledger\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "outbox_relay.go"), []byte("package ledger\n\nfunc RelayOutboxBatch() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeClaudeFixture(t, filepath.Join(root, "ledger", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"create outbox_relay.go"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, filepath.Join(proj, "outbox_relay.go")),
	})
	writeClaudeFixture(t, filepath.Join(root, "ledger", "s2.jsonl"), "s2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"try optimistic batching in outbox_relay.go"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, filepath.Join(proj, "outbox_relay.go")),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:02:00Z","message":{"role":"assistant","content":"We reverted optimistic batching in outbox_relay.go because concurrent writers deadlocked under burst load."}}`, proj),
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", proj)
	payload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"},"session_id":"live-rev","cwd":%q}`, filepath.Join(proj, "outbox_relay.go"), proj)
	out := toolHookRun(t, payload)
	if !strings.Contains(out, "reverted approach here:") || !strings.Contains(out, "optimistic batching") {
		t.Fatalf("expected reverted approach warning for outbox_relay.go, got:\n%s", out)
	}
}

func TestToolHookSurfacesPastBreakageWhenNoVerbalDecisionExists(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "ledger")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/ledger\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(proj, "outbox_relay.go")
	if err := os.WriteFile(target, []byte("package ledger\n\nfunc RelayOutboxBatch() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeClaudeFixture(t, filepath.Join(root, "ledger", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"create outbox_relay.go"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, target),
	})
	writeClaudeFixture(t, filepath.Join(root, "ledger", "s2.jsonl"), "s2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"check outbox tests"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"--- FAIL: TestRelayOutboxBatchIdempotency (0.02s)\nFAIL\texample.com/ledger\t0.03s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, target),
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", proj)
	payload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"},"session_id":"live-brk","cwd":%q}`, target, proj)
	out := toolHookRun(t, payload)
	if !strings.Contains(out, "past breakage here:") || !strings.Contains(out, "TestRelayOutboxBatchIdempotency failed and required editing this file") {
		t.Fatalf("expected past breakage memory for outbox_relay.go, got:\n%s", out)
	}
}

func TestIsEphemeralCompletionReportRejectsPRMergeAndCodeLists(t *testing.T) {
	noisy := []string{
		"#5920 (@author) — fix(hook): tighten candidate filter (MERGED)",
		"`#5920` (`@author`) — `fix(hook): tighten candidate filter` (closed)",
		"`internal/digest/conclusions.go` (`decisionMarkers`, `planAfterMarker`)",
	}
	for _, s := range noisy {
		if !isEphemeralCompletionReport(s) {
			t.Errorf("expected noisy line %q to be rejected as ephemeral", s)
		}
	}
	durable := "Use `SKIP LOCKED` in `outbox_relay.go` so concurrent workers never block on committed rows."
	if isEphemeralCompletionReport(durable) {
		t.Errorf("expected durable prose decision %q not to be rejected", durable)
	}
}

func TestCrossModelCausalBreakageTransfersToHooksAndFix(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	root := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "ledger")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/ledger\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(proj, "outbox_relay.go")
	if err := os.WriteFile(target, []byte("package ledger\n\nfunc RelayOutboxBatch() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeClaudeFixture(t, filepath.Join(root, "ledger", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"fix TestRelayOutboxBatchIdempotency"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"--- FAIL: TestRelayOutboxBatchIdempotency (0.02s)\nFAIL\texample.com/ledger\t0.03s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:30Z","message":{"role":"assistant","content":"Root cause: RelayOutboxBatch duplicated rows because markDispatched ran outside the transaction."}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"func RelayOutboxBatch() error {\n\tmarkDispatched()","new_string":"func RelayOutboxBatch() error {\n\ttx.markDispatched()"}}]}}`, proj, target),
	})
	writeClaudeFixture(t, filepath.Join(root, "ledger", "s2.jsonl"), "s2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"re-verify outbox relay"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b2","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b2","content":"--- FAIL: TestRelayOutboxBatchIdempotency (0.02s)\nFAIL\texample.com/ledger\t0.03s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, target),
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", proj)

	// 1. PreToolUse (hook-tool): surfaces broken test + causal Why when editing the file.
	prePayload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"},"session_id":"fast-agent","cwd":%q}`, target, proj)
	preOut := toolHookRun(t, prePayload)
	if !strings.Contains(preOut, "TestRelayOutboxBatchIdempotency broke") || !strings.Contains(preOut, "markDispatched ran outside the transaction") {
		t.Fatalf("PreToolUse hook missing causal breakage context, got:\n%s", preOut)
	}

	// 2. PostToolUse (hook-tool-after): hands file + symbol + causal Why right on test failure.
	afterLine := fixPairLine(dir, proj, "--- FAIL: TestRelayOutboxBatchIdempotency (0.02s)")
	if !strings.Contains(afterLine, "changed next: outbox_relay.go (RelayOutboxBatch)") || !strings.Contains(afterLine, "markDispatched ran outside the transaction") {
		t.Fatalf("PostToolUse fixPairLine missing symbol or causal why, got:\n%s", afterLine)
	}

	// 3. CLI fix --json: carries structured symbol and why fields.
	jsonOut, err := captureRun(t, "fix", "--json", "--", "--- FAIL: TestRelayOutboxBatchIdempotency")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut, `"symbol": "RelayOutboxBatch"`) || !strings.Contains(jsonOut, `"why": "RelayOutboxBatch duplicated rows because markDispatched ran outside the transaction."`) {
		t.Fatalf("fix --json missing symbol/why:\n%s", jsonOut)
	}
}

func TestToolHookSurfacesVerifiedRevertedApproachAndBlocksRollbackNoise(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	root := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "gateway")
	if err := os.MkdirAll(filepath.Join(proj, "internal", "postings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/gateway\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 1) Short filename ("rpc.go") declaring a specific mixedCase symbol (`marshalBatchPayload`).
	rpcFile := filepath.Join(proj, "rpc.go")
	if err := os.WriteFile(rpcFile, []byte("package gateway\n\nfunc marshalBatchPayload() ([]byte, error) { return nil, nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 2) Cold/split file (<2 direct sessions) declaring a >=16-char distinctive symbol.
	splitFile := filepath.Join(proj, "internal", "postings", "postings_merge.go")
	if err := os.WriteFile(splitFile, []byte("package postings\n\nfunc intersectSubstringPostingsDetailed() []int { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 3) Negative-control file touched only by bare git rollback + test-soundness hunk rollback.
	cleanFile := filepath.Join(proj, "codec.go")
	if err := os.WriteFile(cleanFile, []byte("package gateway\n\nfunc decodeFrameHeader() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Older decision on rpc.go (s1), superseded by a newer dead-end reversal in a >300-rune paragraph (s2).
	longReversal := "Confirmed: `marshalBatchPayload` returns a raw JSON array across all transport handlers and downstream clients rely on that exact wire envelope in production. " +
		"Wrapping `marshalBatchPayload` in a text framing header would break that contract for strict decoders. " +
		"My framing attempt on `marshalBatchPayload` was wrong — reverted."
	writeClaudeFixture(t, filepath.Join(root, "gateway", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"update rpc.go"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"return nil, nil","new_string":"return nil, nil"}}]}}`, proj, rpcFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:00Z","message":{"role":"assistant","content":"We decided to keep rpc.go buffer pooling enabled because allocations spiked under load."}}`, proj),
	})
	writeClaudeFixture(t, filepath.Join(root, "gateway", "s2.jsonl"), "s2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"try framing marshalBatchPayload"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"return nil, nil","new_string":"return nil, nil"}}]}}`, proj, rpcFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:03:00Z","message":{"role":"assistant","content":%q}}`, proj, longReversal),
	})
	// Review/subagent session (Touched == nil) naming distinctive symbol in splitFile.
	writeClaudeFixture(t, filepath.Join(root, "gateway", "s3.jsonl"), "s3", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s3","cwd":%q,"timestamp":"2026-03-03T10:00:00Z","message":{"role":"user","content":"review postings tier changes"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s3","cwd":%q,"timestamp":"2026-03-03T10:02:00Z","message":{"role":"assistant","content":"An earlier attempt widened intersectSubstringPostingsDetailed instead; it broke three tier-labelling tests by pre-empting the stem tier, and was reverted."}}`, proj),
	})
	// Negative controls on codec.go: bare rollback without rationale + test-soundness hunk rollback.
	writeClaudeFixture(t, filepath.Join(root, "gateway", "s4.jsonl"), "s4", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:00:00Z","message":{"role":"user","content":"check codec.go"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e4","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, cleanFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:02:00Z","message":{"role":"assistant","content":"Reverted codec.go to HEAD."}}`, proj),
	})
	writeClaudeFixture(t, filepath.Join(root, "gateway", "s5.jsonl"), "s5", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s5","cwd":%q,"timestamp":"2026-03-05T10:00:00Z","message":{"role":"user","content":"verify codec.go test soundness"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s5","cwd":%q,"timestamp":"2026-03-05T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e5","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, cleanFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s5","cwd":%q,"timestamp":"2026-03-05T10:02:00Z","message":{"role":"assistant","content":"Verified: with the codec.go hunk reverted, TestDecodeFrameHeader fails because the length check is missing."}}`, proj),
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", proj)

	// Case 1: rpc.go surfaces the newer dead-end reversal via symbol match from a >300-rune paragraph.
	rpcOut := toolHookRun(t, fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q,"old_string":"return nil, nil","new_string":"return nil, nil"},"session_id":"agent-now","cwd":%q}`, rpcFile, proj))
	if !strings.Contains(rpcOut, "reverted approach here:") || !strings.Contains(rpcOut, "reverted") {
		t.Fatalf("expected rpc.go to surface reverted approach warning, got:\n%s", rpcOut)
	}

	// Case 2: Cold/split file (<2 direct sessions) surfaces un-touched review session reversal via >=16-char distinctive symbol.
	splitOut := toolHookRun(t, fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"},"session_id":"agent-now","cwd":%q}`, splitFile, proj))
	if !strings.Contains(splitOut, "reverted approach here:") || !strings.Contains(splitOut, "intersectSubstringPostingsDetailed") {
		t.Fatalf("expected split file postings_merge.go to surface distinctive-symbol reversal, got:\n%s", splitOut)
	}

	// Case 3: Negative controls on codec.go must NOT emit reverted approach here.
	cleanOut := toolHookRun(t, fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"},"session_id":"agent-now","cwd":%q}`, cleanFile, proj))
	if strings.Contains(cleanOut, "reverted approach here:") {
		t.Fatalf("expected negative-control rollbacks on codec.go to stay silent on reverted approach, got:\n%s", cleanOut)
	}

	// Case 4: Once a newer settled decision lands on rpc.go (s6), it supersedes the older reversal.
	writeClaudeFixture(t, filepath.Join(root, "gateway", "s6.jsonl"), "s6", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s6","cwd":%q,"timestamp":"2026-03-06T10:00:00Z","message":{"role":"user","content":"settle rpc.go envelope"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s6","cwd":%q,"timestamp":"2026-03-06T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e6","name":"Edit","input":{"file_path":%q,"old_string":"return nil, nil","new_string":"return nil, nil"}}]}}`, proj, rpcFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s6","cwd":%q,"timestamp":"2026-03-06T10:02:00Z","message":{"role":"assistant","content":"We decided to version marshalBatchPayload via an optional v2 query flag so existing JSON array callers stay untouched."}}`, proj),
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	supersededOut := toolHookRun(t, fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q,"old_string":"return nil, nil","new_string":"return nil, nil"},"session_id":"agent-now","cwd":%q}`, rpcFile, proj))
	if strings.Contains(supersededOut, "reverted approach here:") || !strings.Contains(supersededOut, "version marshalBatchPayload") {
		t.Fatalf("expected newer settled decision on rpc.go to supersede older reversal, got:\n%s", supersededOut)
	}
}
