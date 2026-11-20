package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
	marathon := make([]string, 0, 223)
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
	gotIngest := repoMemoryDecisionForFile(tmp, filepath.Join(tmp, "internal", "index", "ingest.go"), []string{"IngestSession"}, pol)
	if !strings.Contains(gotIngest, "Editing writeback.go requires updating ingest.go") {
		t.Errorf("expected 3-file repo memory match for internal/index/ingest.go, got %q", gotIngest)
	}

	// 2) Short generic symbols ("files", "status") must not cross-wire global fileless rules onto unrelated files.
	gotUnrelated := repoMemoryDecisionForFile(tmp, filepath.Join(tmp, "cmd", "app", "store_rows.go"), []string{"files", "status", "rows"}, pol)
	if gotUnrelated != "" {
		t.Errorf("expected no repo memory match for generic short symbols on unrelated file, got %q", gotUnrelated)
	}

	// 3) Directory-qualified entry internal/index/sync.go must not match cmd/app/sync.go.
	gotWrongDir := repoMemoryDecisionForFile(tmp, filepath.Join(tmp, "cmd", "app", "sync.go"), []string{"runSync"}, pol)
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

func TestToolHookReadVsEditSiblingTestTargetAndAnonDedup(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	proj := filepath.Join(tmp, "work", "svc")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/svc\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcFile := filepath.Join(proj, "token_signer.go")
	testFile := filepath.Join(proj, "token_signer_test.go")
	if err := os.WriteFile(srcFile, []byte("package svc\n\nfunc SignAccessToken(id string) string { return id }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testFile, []byte("package svc\n\nimport \"testing\"\n\nfunc TestSignAccessToken(t *testing.T) { _ = SignAccessToken(\"u1\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", proj)

	// 1. Read-only call on a cold file with no guardrail stays silent (no bare test target noise).
	readPayload := fmt.Sprintf(`{"hook_event_name":"AfterTool","tool_name":"read_file","tool_input":{"file_path":%q},"session_id":"s-read","cwd":%q}`, srcFile, proj)
	if got := toolHookRun(t, readPayload); got != "" {
		t.Fatalf("expected read-only call without guardrail to stay silent, got:\n%s", got)
	}

	// 1b. Warm file with zero decisions/co-change/historical commands stays silent on PostToolUse read_file/Read.
	if got := fileHookLineOutsideWithMode(dir, proj, srcFile, "", true, nil); got != "" {
		t.Fatalf("expected readOnly mode with no guardrails to suppress bare test target, got %q", got)
	}

	// 2. Edit call with empty session_id surfaces the sibling test target once and deduplicates repeat calls.
	editAnon := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q,"old_string":"return id","new_string":"return id + \"!\""},"cwd":%q}`, srcFile, proj)
	first := toolHookRun(t, editAnon)
	if !strings.Contains(first, "test target:") || !strings.Contains(first, "TestSignAccessToken") {
		t.Fatalf("expected Edit call to surface sibling test target, got:\n%s", first)
	}
	if second := toolHookRun(t, editAnon); second != "" {
		t.Fatalf("expected repeat anonymous Edit call in same workspace to deduplicate, got:\n%s", second)
	}

	// 3. decisionLineOf filters ephemeral Task:/prompt echoes from promoted notes.
	notes := []sources.PromotedNote{
		{State: "accepted", Text: "Task: confirm that opencode reads XDG_DATA_HOME on macOS."},
		{State: "accepted", Text: "Keep `SignAccessToken` deterministic in `token_signer.go` so replay tests stay stable."},
	}
	dec := decisionLineOf(notes, toolHookDecisionBudget)
	if strings.Contains(dec, "Task: confirm") || !strings.Contains(dec, "SignAccessToken") {
		t.Fatalf("expected decisionLineOf to filter Task: echo and keep real decision, got %q", dec)
	}
}

func TestDeveloperPainGuardsPostEditSignatureMissingFileAndDiagnostics(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	proj := filepath.Join(tmp, "work", "svc")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/svc\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proj, "internal", "gateway"), 0o755); err != nil {
		t.Fatal(err)
	}
	signerFile := filepath.Join(proj, "internal", "gateway", "frame_codec.go")
	callerFile := filepath.Join(proj, "internal", "gateway", "middleware.go")
	// Simulate post-edit state on disk where DecodeFrameHeader ALREADY has the new 2-arg signature.
	if err := os.WriteFile(signerFile, []byte("package gateway\n\nfunc DecodeFrameHeader(id string, ttl int) (string, error) { return id, nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(callerFile, []byte("package gateway\n\nfunc HandleFrame() string {\n\treturn DecodeFrameHeader(\"u1\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", proj)

	// 1. Post-edit signature change detection: disk already has the new signature, but old_string -> new_string changed it.
	postEditPayload := fmt.Sprintf(`{"hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":%q,"old_string":"func DecodeFrameHeader(id string) string","new_string":"func DecodeFrameHeader(id string, ttl int) (string, error)"},"session_id":"s-sig","cwd":%q}`, signerFile, proj)
	sigOut := toolHookRun(t, postEditPayload)
	if !strings.Contains(sigOut, "callers of DecodeFrameHeader:") || !strings.Contains(sigOut, "middleware.go:4") {
		t.Fatalf("expected post-edit signature change to surface caller middleware.go:4, got:\n%s", sigOut)
	}

	// 2. Ghost / moved file radar: reading a file at the wrong directory resolves its real repo location.
	wrongPath := filepath.Join(proj, "frame_codec.go")
	readMovedPayload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":%q},"session_id":"s-move","cwd":%q}`, wrongPath, proj)
	movedOut := toolHookRun(t, readMovedPayload)
	if !strings.Contains(movedOut, "located in repo: internal/gateway/frame_codec.go") {
		t.Fatalf("expected Read on wrong path to point to internal/gateway/frame_codec.go, got:\n%s", movedOut)
	}

	// 3. Ghost / sibling file radar: reading a non-existent sibling filename in internal/gateway lists sibling files.
	ghostSibling := filepath.Join(proj, "internal", "gateway", "frame_writer.go")
	readSiblingPayload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":%q},"session_id":"s-sib","cwd":%q}`, ghostSibling, proj)
	sibOut := toolHookRun(t, readSiblingPayload)
	if !strings.Contains(sibOut, "sibling files: frame_codec.go") {
		t.Fatalf("expected Read on nonexistent sibling to suggest frame_codec.go, got:\n%s", sibOut)
	}

	// 4. Compiler return-value assignment mismatch surfaces live symbol signature in repo.
	compileErr := "internal/gateway/middleware.go:4:9: assignment mismatch: 1 variable but DecodeFrameHeader returns 2 values"
	afterCompile := fixPairLine(dir, proj, compileErr)
	if !strings.Contains(afterCompile, "symbol signature in repo") || !strings.Contains(afterCompile, "func DecodeFrameHeader(id string, ttl int) (string, error)") {
		t.Fatalf("expected assignment mismatch to surface live symbol signature, got:\n%s", afterCompile)
	}

	// 5. stripFileLineColDiagnostic extracts linter/compiler message without file:line:col prefix.
	probes := nearErrorProbesFromOutput("cmd/deja/hook.go:118:20: G602: slice index out of range (gosec)")
	if len(probes) == 0 || probes[0] != "G602: slice index out of range (gosec)" {
		t.Fatalf("expected nearErrorProbesFromOutput to extract stripped diagnostic probe, got %v", probes)
	}
}

// ranStoreFor writes sessions that all edit one file and all run one command,
// with the outcome the caller asks for, and returns the built index.
func ranStoreFor(t *testing.T, clean bool, sessions int) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	for k := range sessions {
		sid := fmt.Sprintf("s%d", k)
		bad := ""
		if !clean {
			bad = `"is_error":true,`
		}
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(300-20*k) + `","cwd":"/work/app","message":{"role":"user","content":"the golden files under internal/store are stale again"}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(299-20*k) + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/internal/store/store.go","old_string":"x","new_string":"y"}}]}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(298-20*k) + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"go test -tags golden ./internal/store"}}]}}`,
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(297-20*k) + `","cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1",` + bad + `"content":"ok"}]}}`,
			// An inspection command that also passed, which must not be what the
			// line hands over.
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(296-20*k) + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","id":"c2","name":"Bash","input":{"command":"git status --short"}}]}}`,
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(295-20*k) + `","cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c2","content":"ok"}]}}`,
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The line before an edit said how many sessions had worked on the file and,
// with nothing else to say, offered `deja blame`. What those sessions ran to
// check their own edit was in the index and never reached the agent — and
// finding it again is the expensive part: on a 325-file fixture, ten to sixteen
// reads went into it after the answer had already arrived.
func TestTheFileLineHandsOverTheCommandThatPassedHere(t *testing.T) {
	dir := ranStoreFor(t, true, 5)
	line := fileHookLine(dir, "/work/app", "/work/app/internal/store/store.go")
	if line == "" {
		t.Fatal("the fixture produced no line at all")
	}
	if !strings.Contains(line, "go test -tags golden ./internal/store") {
		t.Errorf("the command those sessions ran is not in the line:\n  %s", line)
	}
	if strings.Contains(line, "deja blame") {
		t.Errorf("the pointer was served over the command itself:\n  %s", line)
	}
	if strings.Contains(line, "git status") {
		t.Errorf("an inspection command was handed over as the way to check an edit:\n  %s", line)
	}
	if strings.Count(line, "\n") > 0 {
		t.Errorf("the hook wrote more than one line:\n  %s", line)
	}
}

// The claim is that the command passed, so a command the transcript recorded as
// failing must not become one.
func TestAFailingCommandIsNotOfferedAsTheWayToCheckAnEdit(t *testing.T) {
	dir := ranStoreFor(t, false, 5)
	line := fileHookLine(dir, "/work/app", "/work/app/internal/store/store.go")
	if strings.Contains(line, "go test -tags golden") {
		t.Errorf("a command nobody saw pass is offered as one that did:\n  %s", line)
	}
}

// Below the count bar the line exists only if it carries something the agent can
// act on, and two sessions that both ended cleanly on the same command are that.
// A file with no such pattern stays silent rather than printing the count the
// bar exists to withhold.
func TestBelowTheCountBarTheCommandStandsInForADecision(t *testing.T) {
	dir := ranStoreFor(t, true, 2)
	line := fileHookLine(dir, "/work/app", "/work/app/internal/store/store.go")
	if !strings.Contains(line, "go test -tags golden ./internal/store") {
		t.Errorf("two sessions that agree on a passing command said nothing:\n  %s", line)
	}
	dir = ranStoreFor(t, false, 2)
	if line := fileHookLine(dir, "/work/app", "/work/app/internal/store/store.go"); line != "" {
		t.Errorf("a file with no decision and no evidence still produced a line:\n  %s", line)
	}
}

// The number in the line is sessions, and the manifest is matched on a
// basename — a session that touched two files with the same name comes back
// twice, and counting it twice would put a second session in a sentence that
// has one.
func TestOneSessionIsCountedOnceHoweverOftenItIsHandedOver(t *testing.T) {
	dir := ranStoreFor(t, true, 2)
	metas := index.FileSessions(dir, "/work/app/internal/store/store.go")
	if len(metas) != 2 {
		t.Fatalf("the fixture produced %d sessions", len(metas))
	}
	doubled := append(append([]index.SessionMeta{}, metas...), metas...)
	if got := fileHookRanLine(dir, doubled); !strings.Contains(got, "(2 sessions)") {
		t.Errorf("a doubled list of the same two sessions reads %q", got)
	}
}

// A check that was red in the last two sessions is worth as much as one that
// was green: an agent told nothing runs it, watches it fail, and spends the turn
// working out whether it broke something. What outranks it is a command that
// passed — at any count, because one of the two is a thing to run.
func TestARedCheckIsSaidAndAGreenOneOutranksIt(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	ss := make([]model.Session, 0, 2)
	metas := make([]index.SessionMeta, 0, 2)
	for i, id := range []string{"r1", "r2"} {
		ss = append(ss, model.Session{Harness: "codex", ID: id, Messages: []model.Message{
			{Role: "command", Text: "$ go test ./internal/store  → exit 2"},
		}})
		metas = append(metas, index.SessionMeta{Harness: "codex", ID: id, Updated: at(i)})
	}
	index.BuildSessionFactsForTest(dir, ss)
	got := fileHookRanLine(dir, metas)
	if !strings.Contains(got, "go test ./internal/store") || !strings.Contains(got, "exit 2") {
		t.Errorf("a check red in two sessions is not reported: %q", got)
	}
	if strings.Contains(got, "passed") {
		t.Errorf("a failing command reads as one that passed: %q", got)
	}

	// The same two sessions, both also ending on something that worked: that is
	// the line, and the failure yields to it. Both, because the two-session bar
	// applies to either half — one session's green is still a keystroke.
	for i := range ss {
		ss[i].Messages = append([]model.Message{{Role: "command", Text: "$ make test  → exit 0"}}, ss[i].Messages...)
	}
	index.BuildSessionFactsForTest(dir, ss)
	if got := fileHookRanLine(dir, metas); !strings.Contains(got, "make test") || !strings.Contains(got, "passed") {
		t.Errorf("a command that passed did not outrank the red one: %q", got)
	}
}

// at is a fixed clock for the manifest rows these tests build by hand.
func at(minute int) time.Time {
	return time.Date(2026, 9, 1, 10, minute, 0, 0, time.UTC)
}

func TestFileHookLineCombinesRanCommandAndCoChangeCompanion(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ts := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	for k := range 5 {
		sid := fmt.Sprintf("co%d", k)
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + ts(300-20*k) + `","cwd":"/work/app","message":{"role":"user","content":"update store and schema"}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + ts(299-20*k) + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/internal/store/store.go","old_string":"x","new_string":"y"}},{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/internal/store/schema.go","old_string":"a","new_string":"b"}}]}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + ts(298-20*k) + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"go test ./internal/store"}}]}}`,
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + ts(297-20*k) + `","cwd":"/work/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"ok"}]}}`,
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	line := fileHookLine(dir, "/work/app", "/work/app/internal/store/store.go")
	if !strings.Contains(line, "go test ./internal/store") || !strings.Contains(line, "usually edited alongside schema.go") {
		t.Fatalf("expected fileHookLine to combine ran command and co-change companion, got:\n  %s", line)
	}
}

// #777 taught hook-prompt and hook-context to ask for the rebuild an upgrade
// makes necessary; the action hooks were left out. A spawned subagent gets no
// session start and sends no user prompt — install.go says so where it wires
// Task and Agent — so on a machine that has just upgraded, everything an agent
// does runs against an index nothing has asked to rebuild (#2567).
func TestActionHooksAskForTheRebuildAnUpgradeNeeds(t *testing.T) {
	hermeticEnv(t)
	// hermeticEnv suppresses the request; the question here is whether the
	// hooks ask at all, so let it through and count the spawn.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	spawned := 0
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { spawned++; return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })

	for _, tc := range []struct {
		name    string
		payload string
		run     func(dir string, stdin *strings.Reader, out *bytes.Buffer) error
	}{
		{"hook-tool", `{"session_id":"s","cwd":"/w","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`,
			func(dir string, in *strings.Reader, out *bytes.Buffer) error { return runHookTool(dir, in, out) }},
		{"hook-tool-after", `{"session_id":"s","cwd":"/w","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"stderr":"undefined: snorblefunc"}}`,
			func(dir string, in *strings.Reader, out *bytes.Buffer) error { return runHookToolAfter(dir, in, out) }},
		{"hook-plan", `{"session_id":"s","cwd":"/w","tool_name":"ExitPlanMode","tool_input":{"plan":"Plan: change the pgbouncer pool size."}}`,
			func(dir string, in *strings.Reader, out *bytes.Buffer) error { return runHookPlan(dir, in, out) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "idx")
			writeStaleFormatIndex(t, dir)
			if index.IsCurrentVersion(dir) {
				t.Fatal("fixture claims the current format version")
			}
			spawned = 0
			var out bytes.Buffer
			if err := tc.run(dir, strings.NewReader(tc.payload), &out); err != nil {
				t.Fatal(err)
			}
			// Silence is still the contract: the hook must not answer from an
			// index it cannot read, and must not rebuild inside the action.
			if out.Len() != 0 {
				t.Errorf("answered from a stale-format index: %q", out.String())
			}
			if spawned != 1 {
				t.Errorf("requested %d rebuilds, want 1", spawned)
			}
		})
	}
}

func TestHookToolSymbolLevelDecisionRecallAndPriority(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	repoDir := filepath.Join(tmp, "work")
	targetFile := filepath.Join(repoDir, "pkg", "worker", "pool.go")
	if err := os.MkdirAll(filepath.Dir(targetFile), 0o755); err != nil {
		t.Fatal(err)
	}
	fileSrc := `package worker

func AcquireSlot(n int) error {
	if n <= 0 {
		return nil
	}
	return nil
}

func ClosePool() error {
	drainInFlightQueue()
	return nil
}
`
	if err := os.WriteFile(targetFile, []byte(fileSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Older session s1 recorded a decision about ClosePool without naming pool.go or worker.
	// Newer session s2 recorded a decision about AcquireSlot without naming pool.go or worker.
	sessions := []struct {
		id       string
		day      int
		decision string
	}{
		{"s1", 1, "Decision: ClosePool must drain in-flight jobs before closing the channel."},
		{"s2", 2, "Decision: AcquireSlot rejects non-positive slot counts immediately."},
	}
	for _, s := range sessions {
		ts := time.Date(2026, 9, s.day, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"update worker"}}`, s.id, repoDir, ts),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"u1","name":"Edit","input":{"file_path":%q,"old_string":"a","new_string":"b"}},{"type":"text","text":%q}]}}`, s.id, repoDir, ts, targetFile, s.decision),
		}
		if err := os.WriteFile(filepath.Join(proj, s.id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// 1. Editing inside ClosePool (even when the edit snippet only mentions drainInFlightQueue,
	// which is enclosed by ClosePool on disk) prioritizes the ClosePool decision over the newer AcquireSlot decision.
	payload := map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input": map[string]any{
			"file_path":  targetFile,
			"old_string": "drainInFlightQueue()",
			"new_string": "drainInFlightQueueWithTimeout()",
		},
		"session_id": "live-sym",
		"cwd":        repoDir,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runHookTool(indexDir, strings.NewReader(string(raw)), &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "ClosePool must drain in-flight jobs") {
		t.Fatalf("expected symbol-targeted decision for ClosePool, got: %q", stdout.String())
	}

	// 2. Reading the file without an edit snippet still matches the newest symbol decision declared in the file,
	// while skipping the live session itself.
	line := fileHookLineOutside(indexDir, repoDir, targetFile, func(m index.SessionMeta, _ string) bool {
		return m.ID == "live-sym"
	})
	if !strings.Contains(line, "AcquireSlot rejects non-positive slot counts") {
		t.Fatalf("expected file-declared symbol decision for AcquireSlot, got: %q", line)
	}
}

func TestFileHookRanLinePrefersTargetedTestCommand(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Three sessions ran `make test` (repo-wide sweep), while two sessions ran
	// `go test ./internal/billing -run TestInvoice` (targeted to pkg/stem).
	for i, id := range []string{"r1", "r2", "r3"} {
		ts := time.Date(2026, 9, 10+i, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work","timestamp":"` + ts + `","message":{"role":"user","content":"update internal/billing/invoice.go"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":"/work/internal/billing/invoice.go","old_string":"a","new_string":"b"}},{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"make test"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"PASS\nok\tgithub.com/acme/work\t0.1s"}]}}`,
		}
		if i < 2 {
			lines = append(lines,
				`{"type":"assistant","sessionId":"`+id+`","cwd":"/work","timestamp":"`+ts+`","message":{"role":"assistant","content":[{"type":"tool_use","id":"b2","name":"Bash","input":{"command":"go test ./internal/billing -run TestInvoice"}}]}}`,
				`{"type":"user","sessionId":"`+id+`","cwd":"/work","timestamp":"`+ts+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b2","content":"PASS\nok\tgithub.com/acme/work/internal/billing\t0.02s"}]}}`,
			)
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	got := fileHookLineOutside(indexDir, "/work", "/work/internal/billing/invoice.go", nil)
	if !strings.Contains(got, "go test ./internal/billing -run TestInvoice") {
		t.Fatalf("expected targeted test command to outrank repo-wide make test, got: %q", got)
	}
}

func TestPreFlightCommandFailureIncludesConfirmedRemedy(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)
	t.Setenv("DEJA_COMMAND_HINTS", "on")

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"cf-1", "cf-2"} {
		ts := time.Date(2026, 9, 15+i, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"user","content":"run build"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"make build"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","is_error":true,"content":"panic: sql: database is closed"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"c2","name":"Bash","input":{"command":"make clean && make CGO_ENABLED=0 build"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c2","is_error":false,"content":"ok"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"assistant","content":"that fixed the build"}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	line := commandFailureLine(indexDir, "/work/app", "make build")
	if !strings.Contains(line, "panic: sql: database is closed") {
		t.Fatalf("expected failure warning in commandFailureLine, got: %q", line)
	}
	if !strings.Contains(line, "next command that worked: make clean && make CGO_ENABLED=0 build") {
		t.Fatalf("expected confirmed remedy command in pre-flight commandFailureLine, got: %q", line)
	}
}

func TestLiveSessionStatuslineAndToolReceiptAttribution(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	dir := os.Getenv("DEJA_INDEX_DIR")

	// 1. PostToolUseFailure emits systemMessage receipt for Claude Code, and omits it for Cursor.
	claudePayload := mustMarshal(t, map[string]any{
		"hook_event_name": "PostToolUseFailure",
		"session_id":      "claude-live-1",
		"cwd":             "/work/app",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "make"},
		"error":           "panic: sql: database is closed",
	})
	var claudeStrike1, claudeOut bytes.Buffer
	if err := runHookToolAfterMode(dir, bytes.NewReader(claudePayload), &claudeStrike1, false); err != nil {
		t.Fatal(err)
	}
	if err := runHookToolAfterMode(dir, bytes.NewReader(claudePayload), &claudeOut, false); err != nil {
		t.Fatal(err)
	}
	var claudeResp sessionStartHookResponse
	if err := json.Unmarshal(claudeOut.Bytes(), &claudeResp); err != nil {
		t.Fatalf("unmarshal claude response: %v (%q)", err, claudeOut.String())
	}
	if !strings.Contains(claudeResp.SystemMessage, "deja:") || !strings.Contains(claudeResp.SystemMessage, "make clean && make CGO_ENABLED=0") {
		t.Fatalf("expected visible SystemMessage receipt on Claude PostToolUseFailure, got: %q", claudeResp.SystemMessage)
	}

	cursorPayload := mustMarshal(t, map[string]any{
		"cursor_version":  "1.0.0",
		"conversation_id": "cursor-live-1",
		"hook_event_name": "postToolUseFailure",
		"cwd":             "/work/app",
		"tool_name":       "Shell",
		"tool_input":      map[string]any{"command": "make"},
		"error_message":   "panic: sql: database is closed",
	})
	var cursorStrike1, cursorOut bytes.Buffer
	if err := runHookToolAfterMode(dir, bytes.NewReader(cursorPayload), &cursorStrike1, false); err != nil {
		t.Fatal(err)
	}
	if err := runHookToolAfterMode(dir, bytes.NewReader(cursorPayload), &cursorOut, false); err != nil {
		t.Fatal(err)
	}
	var cursorResp sessionStartHookResponse
	if err := json.Unmarshal(cursorOut.Bytes(), &cursorResp); err != nil {
		t.Fatalf("unmarshal cursor response: %v (%q)", err, cursorOut.String())
	}
	if cursorResp.SystemMessage != "" {
		t.Fatalf("expected empty SystemMessage for Cursor, got: %q", cursorResp.SystemMessage)
	}

	// 2. Brand-new live session that triggers a PreToolUse file injection immediately
	// surfaces file memory on statusline before the new session is indexed in manifest.gob.
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	for i, id := range []string{"hist-1", "hist-2"} {
		ts := time.Date(2026, 9, 20+i, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"user","content":"refine router.go"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"u1","name":"Edit","input":{"file_path":"/work/app/router.go","old_string":"a","new_string":"b"}},{"type":"text","text":"Decision: router.go must reject empty host headers."}]}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	editPayload := mustMarshal(t, map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      "brand-new-live-session",
		"cwd":             "/work/app",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": "/work/app/router.go", "old_string": "a", "new_string": "b"},
	})
	var editOut bytes.Buffer
	if err := runHookTool(dir, bytes.NewReader(editPayload), &editOut); err != nil {
		t.Fatal(err)
	}
	var editResp sessionStartHookResponse
	if err := json.Unmarshal(editOut.Bytes(), &editResp); err != nil {
		t.Fatalf("unmarshal edit response: %v", err)
	}
	if !strings.Contains(editResp.SystemMessage, "deja: router.go has been worked on") {
		t.Fatalf("expected SystemMessage receipt on PreToolUse file injection, got: %q", editResp.SystemMessage)
	}

	mem, ok := statuslineMemory(dir, transcriptSource{SessionID: "brand-new-live-session"})
	if !ok || baseName(mem.Path) != "router.go" || mem.Sessions < 2 {
		t.Fatalf("expected statuslineMemory to resolve router.go for brand-new live session from snapshot, got ok=%v mem=%+v", ok, mem)
	}
}

func toolHookRun(t *testing.T, payload string) string {
	t.Helper()
	var out strings.Builder
	if err := runHookTool(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// commandRunWithAnOutcome seeds sessions that ran a command and say how it
// went — which is the only kind the hook speaks for since #3415.
func commandRunWithAnOutcome(t *testing.T, cmd string, ids ...string) {
	t.Helper()
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range ids {
		writeClaudeFixture(t, filepath.Join(root, "-work-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the suite keeps failing on the shared fixture"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"` + cmd + `"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-01-02T03:06:00Z","message":{"role":"assistant","content":"the suite has to run with -p 1: the shared fixture cannot take parallel packages."}}`,
		})
	}
}

// The hook fires on every action an agent takes, so its default is silence and
// its ceiling is one short line. A command this machine has run before, and
// knows the outcome of, earns that line; anything else does not.
func TestToolHookSpeaksOnlyForACommandWithAHistory(t *testing.T) {
	t.Setenv("DEJA_COMMAND_HINTS", "on")
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	commandRunWithAnOutcome(t, "go test ./... -count=1", "a", "b")
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./... -count=1"},"session_id":"now","cwd":"/work/app"}`)
	if out == "" {
		t.Fatal("a command run in two sessions produced no line")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output is not the PreToolUse shape: %v (%q)", err, out)
	}
	if resp.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("wrong event name: %q", resp.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "2 sessions") {
		t.Errorf("the line does not say how widely it ran: %q", resp.HookSpecificOutput.AdditionalContext)
	}
	if n := len(resp.HookSpecificOutput.AdditionalContext); n > toolHookMaxBytes+recallFrameOverhead {
		t.Errorf("payload is %d bytes, past the per-action budget", n)
	}

	// A command with no history at all is silence, not a "nothing found" line:
	// this is paid once per action.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"terraform apply -auto-approve"},"session_id":"now","cwd":"/work/app"}`); got != "" {
		t.Errorf("spoke about a command it has never seen: %q", got)
	}
	// And an editor action on a file nothing has touched stays silent too.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/srv/app/never.go"},"session_id":"now"}`); got != "" {
		t.Errorf("spoke about a file with no history: %q", got)
	}
	// Malformed input is a miss, never an error.
	if got := toolHookRun(t, "not json"); got != "" {
		t.Errorf("answered malformed input: %q", got)
	}
}

// FileSessions matches on basename, so a same-named file in an unrelated repo
// would inflate the count and point deja blame at the wrong project. The file
// line must count only the project being worked in.
func TestToolHookFileLineIsScopedToTheProject(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	touch := func(proj, cwd, id string) {
		writeClaudeFixture(t, filepath.Join(root, proj, id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"` + cwd + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit config"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"` + cwd + `","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"` + cwd + `/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	// Six sessions in each of two projects, both editing a file named config.go.
	for i := range 6 {
		touch("alpha", "/work/alpha", "al"+string(rune('0'+i)))
		touch("beta", "/work/beta", "be"+string(rune('0'+i)))
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"now","cwd":"/work/alpha"}`)
	if out == "" {
		t.Fatal("the file worked on in this project produced no line")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	ctx := resp.HookSpecificOutput.AdditionalContext
	if strings.Contains(ctx, "12 sessions") {
		t.Errorf("the count included the other project's identically-named file:\n%s", ctx)
	}
	if !strings.Contains(ctx, "6 sessions") {
		t.Errorf("the current project's history was not counted correctly:\n%s", ctx)
	}
}

// The hook must not fire for tools that only read: Read/Glob/NotebookRead carry
// a file_path, and a wide matcher would otherwise pay the cost on every one.
func TestToolHookIgnoresNonEditingTools(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 6 {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	// Read on a file with real history: still silent.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"now","cwd":"/work/alpha"}`); got != "" {
		t.Errorf("the hook fired for a Read: %q", got)
	}
	// Edit on the same file: speaks.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"now","cwd":"/work/alpha"}`); got == "" {
		t.Error("the hook stayed silent for an Edit on a file with history")
	}
}

// pi and omp have no seam that runs before an edit — their one handler whose
// return the model reads holds a finished tool result — so the file's history
// goes out on their lowercase `read`, the step before the edit. Claude Code's
// capitalised "Read" stays silent: its hook fires before the action, so it has
// the edit itself to speak at.
func TestToolHookSpeaksForALowercaseRead(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 6 {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	if got := toolHookRun(t, `{"tool_name":"read","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"pi-1","cwd":"/work/alpha"}`); got == "" {
		t.Error("the hook stayed silent for a read on a file with history")
	}
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"claude-1","cwd":"/work/alpha"}`); got != "" {
		t.Errorf("Claude Code's Read fired: %q", got)
	}
	// --plain is how those two read it: they take a string back from a handler
	// and have no hook stdout to parse.
	var plain bytes.Buffer
	in := strings.NewReader(`{"tool_name":"read","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"pi-2","cwd":"/work/alpha"}`)
	if err := runHookToolMode(os.Getenv("DEJA_INDEX_DIR"), in, &plain, hookToolPlain); err != nil {
		t.Fatal(err)
	}
	got := plain.String()
	if !strings.Contains(got, "config.go has been worked on in") {
		t.Fatalf("--plain did not deliver the file's history:\n%s", got)
	}
	if strings.Contains(got, "hookSpecificOutput") || strings.Contains(got, "PreToolUse") {
		t.Fatalf("--plain kept the hook envelope:\n%s", got)
	}
}

// Isolate the project-scoping branch: query a bare filename so the exact-path
// escape hatch cannot fire, leaving project-scoping as the only thing that can
// admit the session. And a file in no known project stays silent.
func TestToolHookFileScopingIsLoadBearing(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 6 {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// Bare filename: t=="/work/alpha/config.go" != "config.go", so only project
	// scoping (cwd alpha) can admit these sessions.
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"config.go"},"session_id":"now","cwd":"/work/alpha"}`); got == "" {
		t.Error("project scoping did not admit a same-project session on a bare-filename query")
	}
	// Same bare query from an unrelated project: no session matches → silent.
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/gamma")
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"config.go"},"session_id":"now","cwd":"/work/gamma"}`); got != "" {
		t.Errorf("a file in no known project produced a line: %q", got)
	}
}

// Codex and other OpenAI-style agents edit through a single apply_patch tool,
// with the path inside the patch body rather than in file_path. The hook must
// read that path so its file-history line fires on those agents' edits too,
// not only on Claude's Edit/Write.
func TestToolHookReadsTheFileFromAnApplyPatch(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 6 {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	patch := "*** Begin Patch\n*** Update File: /work/alpha/config.go\n@@\n+x\n*** End Patch\n"
	payloadBytes, jerr := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "apply_patch",
		"tool_input":      map[string]any{"command": patch},
		"session_id":      "now",
		"cwd":             "/work/alpha",
	})
	if jerr != nil {
		t.Fatal(jerr)
	}
	out := toolHookRun(t, string(payloadBytes))
	if out == "" {
		t.Fatal("apply_patch on a file with history produced no line")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "6 sessions") {
		t.Errorf("apply_patch file history not surfaced:\n%s", resp.HookSpecificOutput.AdditionalContext)
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "config.go") {
		t.Errorf("the line does not name the file: %q", resp.HookSpecificOutput.AdditionalContext)
	}
	// A patch that only touches a file with no history stays silent.
	noHist := "*** Begin Patch\n*** Add File: /work/alpha/brandnew.go\n@@\n+package p\n*** End Patch\n"
	nb, jerr := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "apply_patch",
		"tool_input": map[string]any{"command": noHist}, "session_id": "now2", "cwd": "/work/alpha",
	})
	if jerr != nil {
		t.Fatal(jerr)
	}
	if got := toolHookRun(t, string(nb)); got != "" {
		t.Errorf("apply_patch on a file with no history spoke: %q", got)
	}
}

// Codex without an apply_patch tool of its own (a provider model with no Codex
// metadata) edits by running apply_patch in its shell, and 0.149 sends that to
// PreToolUse as tool_name Bash with the heredoc in command. The file line has
// to fire there as it does for the apply_patch tool.
func TestToolHookReadsTheFileFromAShellApplyPatch(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 6 {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	run := func(cmd string) string {
		b, jerr := json.Marshal(map[string]any{
			"hook_event_name": "PreToolUse", "tool_name": "Bash",
			"tool_input": map[string]any{"command": cmd}, "session_id": "now", "cwd": "/work/alpha",
		})
		if jerr != nil {
			t.Fatal(jerr)
		}
		return toolHookRun(t, string(b))
	}
	out := run("apply_patch <<'EOF'\n*** Begin Patch\n*** Update File: config.go\n@@\n+x\n*** End Patch\nEOF")
	if !strings.Contains(out, "config.go has been worked on in 6 sessions") {
		t.Fatalf("shell apply_patch on a file with history got no file line: %q", out)
	}
	// A command that only prints a patch header is not an edit.
	if got := run("printf '*** Update File: config.go\\n'"); got != "" {
		t.Errorf("a command that is not apply_patch spoke: %q", got)
	}
}

// The file line carries the prior decision, not just a pointer to it: a
// measured A/B showed a bare "deja blame has the history" changed nothing an
// agent did, while the same moment carrying the decision drove reuse. When a
// session that touched the file recorded a decision, it must appear in the line.
func TestToolHookFileLineCarriesThePriorDecision(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// Five plain edits so the file clears the history threshold.
	for i := range 5 {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	// The deciding session edits config.go and states the decision.
	writeClaudeFixture(t, filepath.Join(root, "alpha", "decide.jsonl"), "decide", []string{
		`{"type":"user","sessionId":"decide","cwd":"/work/alpha","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"settle config.go"}}`,
		`{"type":"assistant","sessionId":"decide","cwd":"/work/alpha","timestamp":"2026-01-03T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"Decision: config.go must load the ARENAGUARD sentinel before any read."},{"type":"tool_use","id":"td","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"x","new_string":"y"}}]}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"now","cwd":"/work/alpha"}`)
	if out == "" {
		t.Fatal("no line for a file with history and a decision")
	}
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	ctx := resp.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "prior decision:") {
		t.Errorf("the line does not carry the decision:\n%s", ctx)
	}
	if !strings.Contains(ctx, "ARENAGUARD") {
		t.Errorf("the decision content is missing:\n%s", ctx)
	}
}

// A decision the session itself took back must not be surfaced at the point of
// an edit — that would push the agent to redo what was undone. When the only
// session with a conclusion says it reverted, there is nothing left to say.
func TestToolHookFileLineSkipsARevertedDecision(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 5 {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	// The only session with a decision also says it reverted it.
	writeClaudeFixture(t, filepath.Join(root, "alpha", "decide.jsonl"), "decide", []string{
		`{"type":"user","sessionId":"decide","cwd":"/work/alpha","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"settle config.go"}}`,
		`{"type":"assistant","sessionId":"decide","cwd":"/work/alpha","timestamp":"2026-01-03T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"Decision: config.go should load the ARENAGUARD sentinel.\nBut we reverted it — it did not help and we backed it out."},{"type":"tool_use","id":"td","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"x","new_string":"y"}}]}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/work/alpha/config.go"},"session_id":"now","cwd":"/work/alpha"}`)
	if out != "" {
		t.Errorf("with no usable decision the line should stay silent:\n%s", out)
	}
}

// The command line honours the trust policy: a command that ran only in a
// withheld project stays silent, and the count excludes withheld sessions.
func TestToolHookCommandHonoursTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// A local session running a distinctive command.
	writeClaudeFixture(t, filepath.Join(root, "m", "l1.jsonl"), "l1", []string{
		`{"type":"user","sessionId":"l1","cwd":"/m","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"deploy"}}`,
		`{"type":"assistant","sessionId":"l1","cwd":"/m","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"terraform apply -auto-approve"}}]}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Import three peer sessions running a different distinctive command.
	in := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 1; i <= 3; i++ {
		id := "pe" + string(rune('0'+i))
		b.WriteString(`{"harness":"claude","session_id":"` + id + `","project":"peerproj","role":"user","text":"peer deploy","time":"2026-07-2` + string(rune('0'+i)) + `T10:00:00Z"}` + "\n")
		b.WriteString(`{"harness":"claude","session_id":"` + id + `","project":"peerproj","role":"command","text":"helmfile sync --peer-only","time":"2026-07-2` + string(rune('0'+i)) + `T10:01:00Z"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(in, "deja-sync-peer.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Import(index.DefaultDir(), in); err != nil {
		t.Fatal(err)
	}
	// Rebuild so commands.gob mines the imported sessions too.
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Deny imported for the auto activation.
	pol := filepath.Join(os.Getenv("HOME"), ".config", "deja", "policy.json")
	if err := os.MkdirAll(filepath.Dir(pol), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pol, []byte(`{"activations":{"auto":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The peer-only command must stay silent under the deny policy.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"helmfile sync --peer-only"},"session_id":"now"}`); got != "" {
		t.Errorf("a command that ran only in a withheld project surfaced: %q", got)
	}
}

// An inspection command carries no reusable signal, and a PreToolUse hook must
// not repeat the same line to the same agent session on every action.
func TestToolHookSkipsInspectionAndDedupes(t *testing.T) {
	t.Setenv("DEJA_COMMAND_HINTS", "on")
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"a", "b", "c"} {
		writeClaudeFixture(t, filepath.Join(root, "-work-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the deploy needs the region set"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"git status --short"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-01-02T03:04:07Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"make deploy-prod REGION=eu"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"2026-01-02T03:04:08Z","message":{"role":"assistant","content":"the deploy has to run with REGION=eu: the eu bucket is the only one the role can write."}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// git status ran in 3 sessions but is inspection — silent.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git status --short"},"session_id":"agent1","cwd":"/work/app"}`); got != "" {
		t.Errorf("an inspection command produced a line: %q", got)
	}
	// A real deploy command speaks once...
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"make deploy-prod REGION=eu"},"session_id":"agent1","cwd":"/work/app"}`); got == "" {
		t.Fatal("a command with real history stayed silent")
	}
	// ...but not a second time in the same agent session.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"make deploy-prod REGION=eu"},"session_id":"agent1","cwd":"/work/app"}`); got != "" {
		t.Errorf("the same line was re-injected to the same session: %q", got)
	}
	// A different agent session still hears it.
	if got := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"make deploy-prod REGION=eu"},"session_id":"agent2","cwd":"/work/app"}`); got == "" {
		t.Error("a fresh agent session was wrongly deduped")
	}
}

// The per-action byte cap must fall on a rune boundary, or a non-ASCII line is
// corrupted into U+FFFD when marshalled.
func TestTruncateToolLineKeepsRunesWhole(t *testing.T) {
	// A run of two-byte runes; cutting at an odd byte would split one.
	s := strings.Repeat("é", 200) // 400 bytes
	got := truncateToolLine(s, 301)
	if !utf8.ValidString(got) {
		t.Fatalf("truncation produced invalid UTF-8: %q", got)
	}
	if len(got) > 301 {
		t.Errorf("over budget: %d bytes", len(got))
	}
	// A short line is returned unchanged.
	if truncateToolLine("ok", 300) != "ok" {
		t.Error("a short line was altered")
	}
}

// The hook-tool injection is recorded so it is visible to stats and the
// receipt, and deduped so it is counted once per fact, not per action.
func TestToolHookRecordsTheInjection(t *testing.T) {
	t.Setenv("DEJA_COMMAND_HINTS", "on")
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	commandRunWithAnOutcome(t, "make deploy-prod REGION=eu", "a", "b")
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	before := usage.Totals(dir).Injections
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"make deploy-prod REGION=eu"},"session_id":"agentX","cwd":"/work/app"}`
	toolHookRun(t, payload)
	if got := usage.Totals(dir).Injections; got != before+1 {
		t.Fatalf("injection not recorded: before=%d after=%d", before, got)
	}
	// The dedup means a repeat in the same session records nothing more.
	toolHookRun(t, payload)
	if got := usage.Totals(dir).Injections; got != before+1 {
		t.Errorf("a deduped repeat still recorded an injection: %d", got)
	}
}

func TestToolHookFileLineSurfacesCoChangedCompanion(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 6 {
		id := "co" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"update parser"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/parser.go","old_string":"a","new_string":"b"}},{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"/work/alpha/parser_test.go","old_string":"x","new_string":"y"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/alpha")
	out := toolHookRun(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/work/alpha/parser.go"},"session_id":"now","cwd":"/work/alpha"}`)
	if out == "" {
		t.Fatal("expected PreToolUse line for co-changed file")
	}
	if !strings.Contains(out, "usually edited alongside parser_test.go (6 of 6 sessions)") {
		t.Errorf("expected co-change companion note in %q", out)
	}
}

func TestToolHookColdStoreHallucinatedLocalImport(t *testing.T) {
	tmp := hermeticEnv(t)
	coldDir := filepath.Join(tmp, "cold-index")
	t.Setenv("DEJA_INDEX_DIR", coldDir)

	repo := filepath.Join(tmp, "repo")
	t.Setenv("CLAUDE_PROJECT_DIR", repo)

	files := map[string]string{
		filepath.Join(repo, ".git", "HEAD"): "ref: refs/heads/main\n",
		filepath.Join(repo, "billing", "tax.py"): `def calculate_vat(amount):
    return amount * 0.2

def format_receipt(entry):
    return str(entry)
`,
		filepath.Join(repo, "web", "format.ts"): `export function formatCurrency(cents: number): string {
  return String(cents);
}

export const DEFAULT_LOCALE = "en-US";
`,
		filepath.Join(repo, "web", "view.ts"): "export const title = 'x';\n",
	}
	for p, content := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	makePayload := func(tool string, input map[string]any) string {
		t.Helper()
		b, err := json.Marshal(map[string]any{
			"hook_event_name": "PreToolUse",
			"tool_name":       tool,
			"tool_input":      input,
			"session_id":      "cold-now",
			"cwd":             repo,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	pyBad := toolHookRun(t, makePayload("Write", map[string]any{
		"file_path": filepath.Join(repo, "billing", "handler.py"),
		"content":   "from .tax import compute_vat\n",
	}))
	if !strings.Contains(pyBad, "handler.py — check import from billing/tax.py") || !strings.Contains(pyBad, "compute_vat") || !strings.Contains(pyBad, "calculate_vat") {
		t.Errorf("expected Python local import typo hint, got %q", pyBad)
	}

	if pyGood := toolHookRun(t, makePayload("Write", map[string]any{
		"file_path": filepath.Join(repo, "billing", "valid_handler.py"),
		"content":   "from .tax import calculate_vat\n",
	})); pyGood != "" {
		t.Errorf("expected empty output for valid Python local import, got %q", pyGood)
	}

	if pyExt := toolHookRun(t, makePayload("Write", map[string]any{
		"file_path": filepath.Join(repo, "billing", "ext_handler.py"),
		"content":   "from requests import get\n",
	})); pyExt != "" {
		t.Errorf("expected empty output for external Python import, got %q", pyExt)
	}

	tsBad := toolHookRun(t, makePayload("Edit", map[string]any{
		"file_path":  filepath.Join(repo, "web", "view.ts"),
		"old_string": "export const title = 'x';\n",
		"new_string": "import { formattCurrency } from './format';\nexport const title = 'x';\n",
	}))
	if !strings.Contains(tsBad, "view.ts — check import from web/format.ts") || !strings.Contains(tsBad, "formattCurrency") || !strings.Contains(tsBad, "formatCurrency") {
		t.Errorf("expected TypeScript local import typo hint, got %q", tsBad)
	}

	if tsGood := toolHookRun(t, makePayload("Edit", map[string]any{
		"file_path":  filepath.Join(repo, "web", "view.ts"),
		"old_string": "export const title = 'x';\n",
		"new_string": "import { formatCurrency } from './format';\nexport const title = 'x';\n",
	})); tsGood != "" {
		t.Errorf("expected empty output for valid TypeScript local import, got %q", tsGood)
	}
}

// traecli 0.208 edits with Claude Code's Edit and Write, and its PreToolUse
// payload names the file only inside command: captured from a live hook,
// {"tool_name":"Edit","tool_input":{"command":"Edit /path"}}. trae-auto has to
// match those tools, and the hook has to find the path there.
func TestTraeEditHookSpeaks(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 6 {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"Edit", "Write"} {
		b, jerr := json.Marshal(map[string]any{
			"hook_event_name": "PreToolUse",
			"tool_name":       tool,
			"tool_input":      map[string]any{"command": tool + " /work/alpha/config.go"},
			"session_id":      "trae-" + tool,
			"cwd":             "/work/alpha",
		})
		if jerr != nil {
			t.Fatal(jerr)
		}
		out := toolHookRun(t, string(b))
		if !strings.Contains(out, "config.go") {
			t.Errorf("%s on a file with history: %q, want the file's line", tool, out)
		}
	}

	var pre string
	for _, h := range traeHookWiring() {
		if h.Event == "PreToolUse" {
			pre = h.Matcher
		}
	}
	re := regexp.MustCompile("^(?:" + pre + ")$")
	for _, tool := range []string{"Bash", "Edit", "Write"} {
		if !re.MatchString(tool) {
			t.Errorf("trae PreToolUse matcher %q misses %s", pre, tool)
		}
	}
	// Codex's own wiring stays as it was: a changed matcher asks every codex
	// user to trust the hook again.
	for _, h := range codexHookWiring {
		if h.Event == "PreToolUse" && h.Matcher != "Bash|apply_patch" {
			t.Errorf("codex PreToolUse matcher changed to %q", h.Matcher)
		}
	}
}

// A file with a broad rule and a narrow exception. The exception is newer
// almost by definition — it is an exception to something — so "newest wins"
// hands the agent the half that reverses the rule, with nothing to say the rule
// exists (#2524).
func TestTheEditLineCarriesBothDecisionsWhenTheyFit(t *testing.T) {
	tmp := hermeticEnv(t)
	_ = tmp
	now := time.Now().UTC()
	if err := sources.AppendPromoted("app", "the general rule",
		"every retry path in retry.go goes through the shared budget",
		"claude:broad", "accepted", now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := sources.AppendPromoted("app", "the exception",
		"the webhook replay is the one exception: it retries twice on its own",
		"claude:narrow", "accepted", now); err != nil {
		t.Fatal(err)
	}
	metas := []index.SessionMeta{
		{ID: "narrow", Harness: "claude", Project: "app", Updated: now},
		{ID: "broad", Harness: "claude", Project: "app", Updated: now.Add(-48 * time.Hour)},
	}

	got := promotedDecisionFor(metas)
	if !strings.Contains(got, "webhook replay") {
		t.Errorf("the newest decision is missing: %q", got)
	}
	if !strings.Contains(got, "shared budget") {
		t.Errorf("the rule the exception qualifies is missing, so the line reverses it: %q", got)
	}
}

// When the second does not fit, the line says one is missing rather than
// leaving the reader with the narrower half alone.
func TestTheEditLineSaysWhenADecisionDidNotFit(t *testing.T) {
	hermeticEnv(t)
	now := time.Now().UTC()
	long := "every retry path in retry.go goes through the shared budget, which the pool owns, " +
		"and nothing in this package may open its own client or set its own deadline for any reason"
	if err := sources.AppendPromoted("app", "the general rule", long, "claude:broad", "accepted", now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := sources.AppendPromoted("app", "the exception",
		"the webhook replay is the one exception: it retries twice on its own",
		"claude:narrow", "accepted", now); err != nil {
		t.Fatal(err)
	}
	metas := []index.SessionMeta{
		{ID: "narrow", Harness: "claude", Project: "app", Updated: now},
		{ID: "broad", Harness: "claude", Project: "app", Updated: now.Add(-48 * time.Hour)},
	}

	got := promotedDecisionFor(metas)
	if !strings.Contains(got, "webhook replay") {
		t.Errorf("the newest decision is missing: %q", got)
	}
	if !strings.Contains(got, "1 more") {
		t.Errorf("the line does not say that another standing decision was left out: %q", got)
	}
	if len(got) > toolHookDecisionBudget+40 {
		t.Errorf("the line is %d bytes, well past its budget: %q", len(got), got)
	}
}

// One decision reads exactly as it did.
func TestTheEditLineWithOneDecisionIsUnchanged(t *testing.T) {
	hermeticEnv(t)
	now := time.Now().UTC()
	if err := sources.AppendPromoted("app", "t", "the retry budget stays at 5", "claude:a", "accepted", now); err != nil {
		t.Fatal(err)
	}
	metas := []index.SessionMeta{{ID: "a", Harness: "claude", Project: "app", Updated: now}}
	if got := promotedDecisionFor(metas); got != "the retry budget stays at 5" {
		t.Errorf("a single decision now reads as %q", got)
	}
}

func TestPromotedDecisionForFileFindsFileNoteBehindUnrelatedLongNote(t *testing.T) {
	hermeticEnv(t)
	now := time.Now().UTC()
	if err := sources.AppendPromoted("app", "retry rule",
		"every retry path in retry.go goes through the shared budget",
		"claude:older", "accepted", now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	longGeneral := "the repository description starts with Search your past AI coding sessions and never uses marketing adjectives or filler words in any README header"
	if err := sources.AppendPromoted("app", "repo desc", longGeneral, "claude:newer", "accepted", now); err != nil {
		t.Fatal(err)
	}
	metas := []index.SessionMeta{
		{ID: "newer", Harness: "claude", Project: "app", Updated: now},
		{ID: "older", Harness: "claude", Project: "app", Updated: now.Add(-24 * time.Hour)},
	}
	got := promotedDecisionForFile("pkg/retry.go", metas)
	if !strings.Contains(got, "retry.go") {
		t.Fatalf("promotedDecisionForFile lost the file-specific note behind a general one: %q", got)
	}
}

// Claude Code's PowerShell runs a command and NotebookEdit changes a file, but
// the hook knew neither: the notebook is named under notebook_path, the shell
// is not Bash, and the installed matcher left PowerShell out (#4489).
func TestHookToolKnowsClaudesPowerShellAndNotebookEdit(t *testing.T) {
	var in toolHookInput
	if err := json.Unmarshal([]byte(`{"tool_name":"NotebookEdit","tool_input":{"notebook_path":"/tmp/proj/retry.ipynb","new_source":"x"}}`), &in); err != nil {
		t.Fatal(err)
	}
	in.adopt()
	if in.ToolInput.FilePath != "/tmp/proj/retry.ipynb" {
		t.Errorf("NotebookEdit file = %q, want the notebook_path", in.ToolInput.FilePath)
	}
	if !isCommandTool("PowerShell") {
		t.Error("PowerShell is not read as a command tool")
	}
	// Both tool events, in settings.json and in the plugin: a failed
	// PowerShell command never reached hook-tool-after (#4489).
	for _, w := range claudeHookWiring {
		if (w.Event == "PreToolUse" || w.Event == "PostToolUse") && !strings.Contains("|"+w.Matcher+"|", "|PowerShell|") {
			t.Errorf("%s matcher %q leaves PowerShell out", w.Event, w.Matcher)
		}
	}
	var plugin struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(repoFile(t, "claude-plugin/.claude-plugin/plugin.json"), &plugin); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"PreToolUse", "PostToolUse"} {
		for _, g := range plugin.Hooks[ev] {
			if !strings.Contains("|"+g.Matcher+"|", "|PowerShell|") {
				t.Errorf("plugin.json %s matcher %q leaves PowerShell out", ev, g.Matcher)
			}
		}
	}
}

// Grok fires PostToolUse for a run_terminal_command that exited non-zero, and
// carries the output as toolResult.output_for_prompt (snake alias
// tool_response); `output` there is bytes, not text. grok-auto wired no
// PostToolUse and hook-tool-after read none of grok's keys (#4499).
func TestGrokFailedCommandGetsTheFixPair(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	for i, payload := range []string{
		`{"hookEventName":"PostToolUse","sessionId":"g-camel","cwd":"/work/app","toolName":"run_terminal_command","toolInput":{"command":"make"},` +
			`"toolResult":{"type":"Bash","exit_code":1,"output_for_prompt":"exit: 1\npanic: sql: database is closed\n","output":[112,97,110]}}`,
		`{"hook_event_name":"PostToolUse","session_id":"g-snake","cwd":"/work/app","tool_name":"run_terminal_command","tool_input":{"command":"make"},` +
			`"tool_response":{"type":"Bash","exit_code":1,"output_for_prompt":"exit: 1\npanic: sql: database is closed\n","output":[112,97,110]}}`,
	} {
		var strike1, out bytes.Buffer
		if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &strike1); err != nil {
			t.Fatal(err)
		}
		if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "CGO_ENABLED=0") {
			t.Errorf("payload %d: no fix pair for grok's failed command:\n%q", i, out.String())
		}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GROK_HOME", filepath.Join(home, ".grok"))
	if _, err := installGrokAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(grokHooksPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "PostToolUse") || !strings.Contains(string(b), "hook-tool-after") {
		t.Errorf("grok-auto wires no PostToolUse hook-tool-after:\n%s", b)
	}
}

// commandcode-auto's SHELL matcher is a case-insensitive regex, so it fires on
// powershell as well as shell_command, and shell_command can carry its
// arguments as a list: the hook ran and looked up `go` alone, or nothing
// (#4540).
func TestHookToolKnowsCommandCodePowershellAndArgs(t *testing.T) {
	if !isCommandTool("powershell") {
		t.Error("powershell is not read as a command tool")
	}
	var in toolHookInput
	if err := json.Unmarshal([]byte(`{"tool_name":"shell_command","tool_input":{"command":"go","args":["test","./retry/..."]}}`), &in); err != nil {
		t.Fatal(err)
	}
	in.adopt()
	if in.ToolInput.Command != "go test ./retry/..." {
		t.Errorf("command = %q, want the arguments after it", in.ToolInput.Command)
	}
}

// Claw Code's PostToolUseFailure payload puts the failing output in
// `tool_error` with `"tool_name":"bash"` and `"tool_result_is_error":true`.
func TestHookToolAfterReadsPostToolUseFailureToolError(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	for i, payload := range []string{
		`{"hook_event_name":"PostToolUseFailure","session_id":"claw-1","cwd":"/work/app","tool_name":"bash","tool_input":{"command":"make"},"tool_input_json":"{\"command\":\"make\"}","tool_error":"exit 1\npanic: sql: database is closed\n","tool_result_is_error":true}`,
		// Truncated mid-string (>1 MiB build output) triggers salvageFromPayload("tool_error").
		`{"hook_event_name":"PostToolUseFailure","session_id":"claw-2","cwd":"/work/app","tool_name":"bash","tool_input":{"command":"make"},"tool_error":"exit 1\npanic: sql: database is closed\n` + strings.Repeat("x", 1024),
		// Truncated tool_response object carrying tool_error triggers salvageToolOutput("tool_error").
		`{"hook_event_name":"PostToolUseFailure","session_id":"claw-3","cwd":"/work/app","tool_name":"bash","tool_input":{"command":"make"},"tool_response":{"tool_error":"exit 1\npanic: sql: database is closed\n` + strings.Repeat("x", 1024),
	} {
		var strike1, out bytes.Buffer
		if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &strike1); err != nil {
			t.Fatal(err)
		}
		if err := runHookToolAfter(os.Getenv("DEJA_INDEX_DIR"), strings.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "CGO_ENABLED=0") {
			t.Errorf("payload %d: no fix pair for claw-code PostToolUseFailure:\n%q", i, out.String())
		}
		if i == 0 && !strings.Contains(out.String(), `"PostToolUseFailure"`) {
			t.Errorf("payload %d: reply lost PostToolUseFailure event name:\n%q", i, out.String())
		}
	}
}

func editStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := func(m int) string { return now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339) }
	for k := range 6 {
		sid := fmt.Sprintf("f%d", k)
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","timestamp":"` + at(200-10*k) + `","cwd":"/work/app","message":{"role":"user","content":"work on the invoice renderer"}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(199-10*k) + `","cwd":"/work/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/render.go","old_string":"x","new_string":"y"}}]}}`,
			`{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at(198-10*k) + `","cwd":"/work/app","message":{"role":"assistant","content":"changed the renderer (` + strconv.Itoa(k) + `)"}}`,
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The line was built on a measurement: an agent follows a decision where it
// ignores a pointer. The last sentence of the last session is not a decision,
// and offered under a weaker label it was never acted on either: a month of real
// sessions, 37 of those lines and no recall, no blame, no edit changed. So the
// line says nothing rather than spend the agent's context on it (#2526).
func TestAScannedConclusionIsNotCalledADecision(t *testing.T) {
	dir := editStore(t)

	if line := fileHookLine(dir, "/work/app", "/work/app/render.go"); line != "" {
		t.Errorf("a session's closing sentence reached the edit:\n  %s", line)
	}
}

// A note the user promoted reaches the file through any session that touched
// it, and one long session touches everything. Read from a real month: the one
// accepted note on the machine, a rule about the repository description, went
// out in front of 56 edits to Go files it says nothing about, and the
// session-start block already carries it. A note that does not name the file
// stays off this line.
func TestAProjectNoteThatIsNotAboutTheFileStaysOff(t *testing.T) {
	dir := editStore(t)
	if _, err := captureRunStderr(t, "promote", "f5", "--state", "accepted",
		"--note", "the repository description leads with the task, not the category"); err != nil {
		t.Fatal(err)
	}

	if line := fileHookLine(dir, "/work/app", "/work/app/render.go"); line != "" {
		t.Errorf("a note about something else went out in front of the edit:\n  %s", line)
	}
}

// A promoted decision keeps the word, since that is what it is — and says
// where it holds. A note is promoted in a session, not against a file, so it
// reaches this line through "a session that worked on this file said it", which
// is not "this was decided about this file". On a real store the one accepted
// note arrived in front of `main.go` reading as that file's decision, where it
// is a rule about the repository description.
func TestAPromotedDecisionKeepsTheWord(t *testing.T) {
	dir := editStore(t)
	if _, err := captureRunStderr(t, "promote", "f5", "--state", "accepted",
		"--note", "the renderer never re-wraps a paragraph that already fits"); err != nil {
		t.Fatal(err)
	}

	line := fileHookLine(dir, "/work/app", "/work/app/render.go")
	if !strings.Contains(line, "decision in this project: the renderer never re-wraps") {
		t.Errorf("a promoted decision is no longer named as one, or claims the file:\n  %s", line)
	}
}

// A config an older deja wrote can name a hook this build no longer has, and
// install leaves that line where it is — so it runs every session. It fell
// through to search, which builds the index if it has to and then reports a
// miss about "hook-session": a session start doing a full index build and
// answering nothing, once per session (#2718).
func TestAnUnknownHookNameIsNotAQuery(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","message":{"role":"user","content":"the zonkoshard retry budget"},` +
		`"timestamp":"2026-08-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	// A harness calling the hook, not a person typing it: `go test` may or may
	// not have a terminal on stdin, and which branch this takes must not
	// depend on how the suite was started.
	restore := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = restore })
	stdinIsTerminal = func() bool { return false }

	var out string
	said := captureStderr(t, func() {
		var err error
		out, err = captureRun(t, "hook-session")
		if err != nil {
			t.Errorf("a hook name a harness still calls must not fail the session: %v", err)
		}
	})
	// Nothing on stdout: a harness reads that as the hook's answer, and on
	// some of them a bare line there lands in the model's context.
	if strings.TrimSpace(out) != "" {
		t.Errorf("a hook that does not exist answered the harness:\n%s", out)
	}
	if !strings.Contains(said, "hook-session") || !strings.Contains(said, "safe to delete") {
		t.Errorf("nothing said what that line in the config is, or how to fix it:\n%s", said)
	}
	if strings.Contains(said, "no matches") || strings.Contains(said, "indexing sessions") {
		t.Errorf("the hook name was read as a query:\n%s", said)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.gob")); err == nil {
		t.Errorf("the index was built by a hook that does not exist")
	}

	// A real mistyped command is still read as a search — the reading #674
	// chose, and the right one for a word somebody typed. It goes to stderr,
	// so what says it ran is the store it built to answer. The exit code says
	// the command was wrong, which is the one thing a script can act on.
	if _, err := captureRun(t, "sarch"); err != nil && !errors.Is(err, errAlreadySaid) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.gob")); err != nil {
		t.Errorf("a mistyped command stopped being read as a search: %v", err)
	}
}

// At a terminal the same word is somebody's typo, not a config: they keep the
// search and the near miss every other mistyped word gets (#674), and telling
// them a config calls it would be a claim about their machine that is false.
func TestATypedHookNameIsStillASearch(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","message":{"role":"user","content":"the zonkoshard retry budget"},` +
		`"timestamp":"2026-08-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = restore })
	stdinIsTerminal = func() bool { return true }

	said := captureStderr(t, func() {
		if _, err := captureRun(t, "hook-contxt"); err != nil {
			t.Error(err)
		}
	})
	if strings.Contains(said, "safe to delete") {
		t.Errorf("a typo was answered with a claim about the reader's config:\n%s", said)
	}
	if _, err := os.Stat(filepath.Join(tmp, "index.db", "manifest.gob")); err != nil {
		t.Errorf("the word somebody typed was not searched for: %v", err)
	}
}

// And a query whose first word happens to be one: no harness passes a retired
// hook extra arguments.
func TestAQueryStartingWithAHookNameIsStillAQuery(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","message":{"role":"user","content":"hook-session timeout in codex"},` +
		`"timestamp":"2026-08-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = restore })
	stdinIsTerminal = func() bool { return false }
	out, err := captureRun(t, "hook-session", "timeout")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hook-session timeout in codex") {
		t.Errorf("a query was swallowed by the guard:\n%s", out)
	}
	_ = tmp
}

// The index lived on a disk that went away between sessions. `deja index` and
// doctor call that unmounted; the hook called it a permission problem and sent
// the reader to check a directory that is not there (#1054).
func TestHookNamesAnUnmountedIndexVolume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not deny writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root creates directories under a read-only parent anyway")
	}
	tmp := hermeticEnv(t)
	// The shape of an external volume: a mount point under a directory this
	// user cannot write, so the path cannot simply be recreated.
	volumes := filepath.Join(tmp, "Volumes")
	dir := filepath.Join(volumes, "ext", "deja", "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", dir)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"zeppelin gasket"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(filepath.Join(volumes, "ext")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(volumes, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(volumes, 0o700) })

	message := func() string {
		out, err := captureRun(t, "hook-context")
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(out) == "" {
			return ""
		}
		var resp struct {
			SystemMessage string `json:"systemMessage"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("hook output is not JSON: %q", out)
		}
		return resp.SystemMessage
	}

	got := message()
	if !strings.Contains(got, "unmounted") {
		t.Errorf("hook did not say the disk is gone: %q", got)
	}
	if strings.Contains(got, "not writable") {
		t.Errorf("hook blamed permissions on a directory that is not there: %q", got)
	}

	// A directory that is there and read-only is a permission problem, and
	// keeps the wording that names one.
	if err := os.Chmod(volumes, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStaleFormatIndex(t, dir)
	parent := filepath.Dir(dir)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	if got := message(); !strings.Contains(got, "not writable") {
		t.Errorf("a read-only index directory lost the permission wording: %q", got)
	}
}

// `deja index` and `deja doctor` both name a store deja could not read, and
// neither is what someone who installed deja and went back to their agent ever
// runs: the project simply had no memory of those sessions and nothing said
// why (#917).
func TestHookNamesAStoreItCouldNotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	open := filepath.Join(root, "-proj")
	if err := os.MkdirAll(open, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-10T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(open, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(root, "-locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "b.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// The hook takes the project from the environment when the payload does
	// not name one; this is the project whose sessions were readable.
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")

	message := func(t *testing.T) string {
		t.Helper()
		out, err := captureRun(t, "hook-context")
		if err != nil {
			t.Fatal(err)
		}
		// Nothing to say prints nothing at all, which is the second call here.
		if strings.TrimSpace(out) == "" {
			return ""
		}
		var resp struct {
			SystemMessage string `json:"systemMessage"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("hook output is not JSON: %q (%v)", out, err)
		}
		return resp.SystemMessage
	}

	got := message(t)
	if !strings.Contains(got, "could not read") || !strings.Contains(got, "deja doctor") {
		t.Errorf("the hook said nothing about the locked store: %q", got)
	}
	// …and it is not repeated every session: the same standing fact is
	// wallpaper by the second time.
	if again := message(t); strings.Contains(again, "could not be read") {
		t.Errorf("the note repeated on the next session: %q", again)
	}
}

const (
	seedGone = "5a1c0de0-1111-4222-8333-444455556666"
	seedLive = "7b2d1ef1-2222-4333-8444-555566667777"
)

// seedClaudeDeleted indexes Claude Code sessions, deletes the first id's
// transcript and its directory of subagent runs the way the cleanupPeriodDays
// sweep does, then lets an index pass find them gone. subagents is how many
// child runs the deleted session launched.
func seedClaudeDeleted(t *testing.T, subagents int, ids ...string) string {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	root := filepath.Join(tmp, "claude", "projects")
	proj := filepath.Join(root, "-work-api")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("CODEX_HOME", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	write := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		write(filepath.Join(proj, id+".jsonl"), `{"type":"user","sessionId":"`+id+`","uuid":"u1","parentUuid":null,"timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"why does the pool exhaust"}}`+"\n")
	}
	for i := range subagents {
		agent := "a76" + strconv.Itoa(i) + "d0f31f74ee0f2"
		line := func(role, text string) string {
			return `{"type":"` + role + `","isSidechain":true,"agentId":"` + agent + `","sessionId":"` + ids[0] + `","timestamp":"2026-09-01T10:00:0` + strconv.Itoa(i) + `Z","cwd":"/work/api","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
		}
		write(filepath.Join(proj, ids[0], "subagents", "agent-"+agent+".jsonl"), line("user", "review the diff")+line("assistant", "two findings"))
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(proj, ids[0]+".jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(proj, ids[0])); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func setWeekStamp(t *testing.T, dir string, at time.Time) {
	t.Helper()
	if err := os.WriteFile(dir+".weeknote", []byte(strconv.FormatInt(at.Unix(), 10)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Claude Code deletes transcripts after cleanupPeriodDays and says nothing,
// and deja keeping them said nothing either. The week note says it, on a week
// with no recall too, with an id the person can paste. It says the files were
// deleted, not who deleted them: a person's own rm is kept the same way.
func TestWeekNoteSaysWhichSessionsWereDeletedFromDisk(t *testing.T) {
	dir := seedClaudeDeleted(t, 0, seedGone, seedLive)
	setWeekStamp(t, dir, time.Now().Add(-8*24*time.Hour))
	// A second on: the window ends before the second the note is written in,
	// and the next one starts there.
	got := weekNoteAt(dir, time.Now().Add(time.Second))
	want := "1 Claude Code session deleted this week; deja kept it — deja resume " + shortID(seedGone) + " --write-back"
	if got != want {
		t.Fatalf("week note = %q\nwant        %q", got, want)
	}
	if termwidth.Columns(got) > noteWidth {
		t.Errorf("the line is %d columns, wider than %d", termwidth.Columns(got), noteWidth)
	}
	// Said once: the next week has nothing new to say.
	if again := weekNoteAt(dir, time.Now().Add(8*24*time.Hour)); again != "" {
		t.Errorf("the same deletion was reported twice: %q", again)
	}
}

// A session that launched subagents is one session, though each child run is
// a transcript of its own under <id>/subagents and goes with it. Counted by
// file, one session with three children read as four, and the id offered could
// be a child's.
func TestWeekNoteCountsADeletedSessionNotItsSubagents(t *testing.T) {
	dir := seedClaudeDeleted(t, 3, seedGone, seedLive)
	setWeekStamp(t, dir, time.Now().Add(-8*24*time.Hour))
	got := weekNoteAt(dir, time.Now().Add(time.Second))
	want := "1 Claude Code session deleted this week; deja kept it — deja resume " + shortID(seedGone) + " --write-back"
	if got != want {
		t.Fatalf("week note = %q\nwant        %q", got, want)
	}
}

// An id whose short form names two sessions is printed whole, and a line too
// wide for it names the screen that lists them instead.
func TestWeekNoteNamesTheDeletedTabWhenTheIDDoesNotFit(t *testing.T) {
	dir := seedClaudeDeleted(t, 0, seedGone, "5a1c0de0-1111-9999-8333-444455556666")
	setWeekStamp(t, dir, time.Now().Add(-8*24*time.Hour))
	got := weekNoteAt(dir, time.Now().Add(time.Second))
	want := "1 Claude Code session deleted this week; deja kept it — the Deleted tab of deja lists them"
	if got != want {
		t.Fatalf("week note = %q\nwant        %q", got, want)
	}
}

// A deletion from before the note's window is old news.
func TestWeekNoteLeavesOutADeletionBeforeItsWindow(t *testing.T) {
	dir := seedClaudeDeleted(t, 0, seedGone, seedLive)
	later := time.Now().Add(time.Hour)
	setWeekStamp(t, dir, later)
	if got := weekNoteAt(dir, later.Add(8*24*time.Hour)); got != "" {
		t.Errorf("a deletion before the window was reported: %q", got)
	}
}

func TestWeekDeletedLineGroupsAgentsAndOffersWhatEachCanDo(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local)
	k := func(h, id string, ago time.Duration) index.KeptSession {
		return index.KeptSession{SessionMeta: index.SessionMeta{Harness: h, ID: id}, At: now.Add(-ago)}
	}
	week := now.Add(-7 * 24 * time.Hour)
	dir := filepath.Join(t.TempDir(), "index")
	cases := []struct {
		name  string
		kept  []index.KeptSession
		since time.Time
		want  string
	}{
		{"none", nil, week, ""},
		{"two agents, newest first", []index.KeptSession{k("amp", "T-1a2b3c4d5e6f7a8b", time.Hour), k("claude", "c1", 2*time.Hour), k("claude", "c2", 3*time.Hour)}, week,
			"3 sessions from Claude Code and Amp deleted this week; deja kept them — deja show T-1a2b3c4d5e"},
		{"too many to name", []index.KeptSession{k("claude", "c1", time.Hour), k("claude", "c2", time.Hour), k("codex", "x1", time.Hour), k("amp", "a1", time.Hour), k("gemini", "g1", time.Hour)}, week,
			"5 sessions deleted this week; deja kept them — deja resume c1 --write-back"},
		{"a longer gap says how long", []index.KeptSession{k("claude", "c1", time.Hour)}, now.Add(-30 * 24 * time.Hour),
			"1 Claude Code session deleted in the last 30 days; deja kept it — deja resume c1 --write-back"},
	}
	for _, c := range cases {
		if got := weekDeletedLine(dir, c.kept, c.since, now); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
	// The widest line a real month gives still carries the command: a
	// three-digit count, the longer window wording, Claude Code's short id.
	many := make([]index.KeptSession, 0, 437)
	for range 437 {
		many = append(many, k("claude", seedGone, time.Hour))
	}
	got := weekDeletedLine(dir, many, now.Add(-30*24*time.Hour), now)
	if termwidth.Columns(got) > noteWidth || !strings.HasSuffix(got, "--write-back") {
		t.Errorf("437 sessions in 30 days: %q (%d columns)", got, termwidth.Columns(got))
	}
}

func TestWeekNoteStartsTheClockThenSpeaksOnceAWeek(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	usage.RecordResult(dir, usage.KindHook, 400, 1, false)
	now := time.Now()
	// The first session after install starts the clock and says nothing, or
	// the note would count the hook that just ran.
	if got := weekNoteAt(dir, now); got != "" {
		t.Fatalf("first session spoke: %q", got)
	}
	if got := weekNoteAt(dir, now.Add(3*24*time.Hour)); got != "" {
		t.Fatalf("mid-week spoke: %q", got)
	}
	got := weekNoteAt(dir, now.Add(8*24*time.Hour))
	if !strings.HasPrefix(got, "deja this week: 2 recalls") || !strings.Contains(got, "deja stats --card") {
		t.Fatalf("week note = %q", got)
	}
	if again := weekNoteAt(dir, now.Add(8*24*time.Hour+time.Hour)); again != "" {
		t.Fatalf("spoke twice in a week: %q", again)
	}
}

func TestWeekNoteKeepsQuietOnAnEmptyWeek(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.WriteFile(dir+".weeknote", []byte(strconv.FormatInt(past.Unix(), 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := weekNoteAt(dir, time.Now()); got != "" {
		t.Fatalf("an empty week was reported: %q", got)
	}
}

// dsh and OpenClaw show a person nothing a hook prints, so their plugins ask
// for the notes on their own and add them to a /deja reply.
func TestHookContextNotesPrintsTheDueWeekNoteOnceAndNoDigest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	week := strconv.FormatInt(time.Now().Add(-8*24*time.Hour).Unix(), 10)
	if err := os.WriteFile(dir+".weeknote", []byte(week), 0o600); err != nil {
		t.Fatal(err)
	}
	got := captureStdout(t, func() { _ = cmdHookContext(dir, []string{"--notes"}) })
	if strings.TrimSpace(got) == "" || !strings.HasPrefix(got, "deja this week: ") {
		t.Fatalf("--notes = %q, want only the week note", got)
	}
	if again := captureStdout(t, func() { _ = cmdHookContext(dir, []string{"--notes"}) }); again != "" {
		t.Fatalf("--notes spoke twice: %q", again)
	}
}

const (
	weekUsedJWT   = "e8b6eefc-fec0-4d1a-9a1b-0000000000a1"
	weekUsedJWTQ  = "fix the JWT clock skew in the auth middleware"
	weekUsedCJK   = "7a1c2d3e-5f60-4a7b-8c9d-0000000000b2"
	weekUsedCJKQ  = "为什么发布脚本在打标签之前会把变更日志的页脚删掉并且在持续集成里面只有周五才会失败"
	weekUsedOther = "4b5c6d7e-8f90-4a1b-2c3d-0000000000c3"
	// weekUsedTwin shares weekUsedJWT's first eight characters, so a credit
	// that names only those names two sessions.
	weekUsedTwin = "e8b6eefc-0000-4000-8000-0000000000e5"
)

// seedWeekUsedStore indexes three old sessions and one from this week whose
// assistant turns carry the given replies, then makes the week note due.
func seedWeekUsedStore(t *testing.T, titles map[string]string, replies ...string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	claude := filepath.Join(tmp, "claude", "-work-app")
	old := time.Now().Add(-20 * 24 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().Add(-2 * 24 * time.Hour).UTC().Format(time.RFC3339)
	user := func(id, at, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + at + `","message":{"role":"user","content":` + mustJSON(t, text) + `}}`
	}
	assistant := func(id, at, text string) string {
		return `{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + at + `","message":{"role":"assistant","content":[{"type":"text","text":` + mustJSON(t, text) + `}]}}`
	}
	for _, id := range []string{weekUsedJWT, weekUsedCJK, weekUsedOther, weekUsedTwin} {
		title := titles[id]
		if title == "" {
			title = "tidy the makefile targets for " + id[:4]
		}
		writeClaudeFixture(t, filepath.Join(claude, id+".jsonl"), id, []string{user(id, old, title), assistant(id, old, "done")})
	}
	const now = "c0ffee00-1111-4222-8333-0000000000d4"
	lines := make([]string, 0, 1+len(replies))
	lines = append(lines, user(now, recent, "the auth tests fail again on token expiry"))
	for _, r := range replies {
		lines = append(lines, assistant(now, recent, r))
	}
	writeClaudeFixture(t, filepath.Join(claude, now+".jsonl"), now, lines)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	// The built note goes first and once; it is not what these tests read.
	if err := os.WriteFile(dir+".builtnote", []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	week := strconv.FormatInt(time.Now().Add(-8*24*time.Hour).Unix(), 10)
	if err := os.WriteFile(dir+".weeknote", []byte(week), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func jwtTitles() map[string]string { return map[string]string{weekUsedJWT: weekUsedJWTQ} }

// The note counted recalls and never said which one helped (#4615).
func TestTheWeekNoteNamesTheSessionAnAgentCredited(t *testing.T) {
	dir := seedWeekUsedStore(t, jwtTitles(),
		"déjà vu: we hit this JWT skew before — reusing it (deja:e8b6eefc-fec)\n\nThe fix is to allow 30s of leeway.")
	got := weekNoteAt(dir, time.Now())
	want := "deja this week: 1 recall — deja stats --card\n" +
		`agents used "` + weekUsedJWTQ + `" (claude) — deja show e8b6eefc-fec`
	if got != want {
		t.Fatalf("week note =\n%s\nwant\n%s", got, want)
	}
	checkPersonLines(t, "the week note", got, 2)
	// The id printed is one `deja show` opens.
	s, ok, err := index.FindByPrefix(dir, "e8b6eefc-fec")
	if err != nil || !ok || s.ID != weekUsedJWT {
		t.Fatalf("deja show e8b6eefc-fec opens %q (ok=%v err=%v)", s.ID, ok, err)
	}
}

// OpenClaw's /deja posts --notes to whatever chat asked, a group included, so
// that path names the agent and the id and never the person's question.
func TestTheWeekNoteOnNotesNamesNoTitle(t *testing.T) {
	dir := seedWeekUsedStore(t, jwtTitles(),
		"déjà vu: we hit this JWT skew before — reusing it (deja:e8b6eefc-fec)")
	got := captureStdout(t, func() { _ = cmdHookContext(dir, []string{"--notes"}) })
	if !strings.Contains(got, "agents used a claude session — deja show e8b6eefc-fec") {
		t.Fatalf("--notes does not name the used session:\n%s", got)
	}
	if strings.Contains(got, "JWT") || strings.Contains(got, `"`) {
		t.Fatalf("--notes quotes the person's own question:\n%s", got)
	}
}

// A second credited session rides along by id; a wide title is cut in
// columns, not runes.
func TestTheWeekNoteStaysInsideTheNoteWidthWithCJK(t *testing.T) {
	dir := seedWeekUsedStore(t, map[string]string{weekUsedJWT: weekUsedJWTQ, weekUsedCJK: weekUsedCJKQ},
		"déjà vu: 页脚 again (claude, Aug 1, deja:7a1c2d3e-5f6) — reusing it.",
		"déjà vu: the same footer drop (deja:7a1c2d3e-5f6) — reusing it",
		"déjà vu: JWT skew (deja:e8b6eefc-fec) — reusing it")
	got := weekNoteAt(dir, time.Now())
	checkPersonLines(t, "the week note", got, 2)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], `agents used "为什么`) || !strings.Contains(lines[1], `…" (claude)`) ||
		!strings.HasSuffix(lines[1], " — deja show 7a1c2d3e-5f6 (also e8b6eefc-fec)") {
		t.Fatalf("week note does not name both sessions, most-credited first:\n%s", got)
	}
}

// Without a credit line, a session recalled twice this week stands in.
func TestTheWeekNoteFallsBackToTheSessionRecalledTwice(t *testing.T) {
	dir := seedWeekUsedStore(t, jwtTitles(), "the leeway is 30s now")
	for range 2 {
		usage.RecordServedSessions(dir, usage.KindRecall, 300, 1, false, 0, []string{weekUsedJWT})
	}
	got := weekNoteAt(dir, time.Now())
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], `agents recalled "fix the JWT`) ||
		!strings.HasSuffix(lines[1], `…" 2 times (claude) — deja show e8b6eefc-fec`) {
		t.Fatalf("week note does not name the session recalled twice:\n%s", got)
	}
	checkPersonLines(t, "the week note", got, 2)
}

// A week with recalls and nothing used says what it said before. An id that
// resolves to nothing, to two sessions, or sits outside a credit line credits
// nothing.
func TestTheWeekNoteWithoutAUsedSessionIsUnchanged(t *testing.T) {
	dir := seedWeekUsedStore(t, jwtTitles(),
		"déjà vu: the skew again (deja:e8b6eefc) — reusing it",
		`tell the user "déjà vu: we hit this JWT skew in March — reusing that fix (deja:a1b2c3d4-e5f)"`,
		"the id deja:e8b6eefc-fec is in the recall block")
	if got := weekNoteAt(dir, time.Now()); got != "deja this week: 1 recall — deja stats --card" {
		t.Fatalf("week note = %q", got)
	}
}

// One repo, two checkouts: sessions recorded in a linked worktree belong to the
// project, not to that directory. #2333 took bare-name suffix matching out of
// the scope rule, and this is the case that rule was written for — the
// candidates carry each worktree root's full name form, so it still holds from
// either side, including a fresh worktree with no history of its own.
func TestAWorktreeSharesTheProjectsHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)

	main := filepath.Join(tmp, "src", "app")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(main, "init", "-q")
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(main, "add", "README.md")
	git(main, "commit", "-qm", "seed")
	feature := filepath.Join(tmp, "wt", "app-feature")
	git(main, "worktree", "add", "-q", feature, "-b", "feature")

	// One session, recorded in the main checkout only.
	dirName := encodedProjectDir(main)
	store := filepath.Join(root, dirName)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-6 * time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"onmain","timestamp":%q,"cwd":%q,`+
		`"message":{"role":"user","content":"the retry budget on main needs a cap"}}`, at, main)
	if err := os.WriteFile(filepath.Join(store, "onmain.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	fromMain := hookContextFor(t, dir, fmt.Sprintf(
		`{"hook_event_name":"SessionStart","cwd":%q,"session_id":"a1"}`, main))
	if !strings.Contains(fromMain, "retry budget on main") {
		t.Fatalf("the checkout's own session is missing, so this measures nothing:\n%s", fromMain)
	}
	fromWorktree := hookContextFor(t, dir, fmt.Sprintf(
		`{"hook_event_name":"SessionStart","cwd":%q,"session_id":"a2"}`, feature))
	if !strings.Contains(fromWorktree, "retry budget on main") {
		t.Errorf("a linked worktree does not see the repo's history:\n%s", fromWorktree)
	}
}

// encodedProjectDir is the directory name a harness gives a project, the way
// sources.resolveEncodedPath reads one back: separators become dashes, and on
// Windows the drive's colon goes with them — a colon is not a filename
// character there, so the old spelling could not be created at all (#2808).
func encodedProjectDir(path string) string {
	name := strings.ReplaceAll(path, string(filepath.Separator), "-")
	return strings.Replace(name, ":", "-", 1)
}

// Work done in a linked worktree is work on the same project, and the session
// start there has to open with the same memory. `ProjectNameCandidates` knows
// that (internal/digest), but nothing held the hook itself to it — and the hook
// is where the project is decided: it reads CLAUDE_PROJECT_DIR, or the payload's
// cwd, and everything downstream follows that one string.
func TestASessionStartedInAWorktreeGetsTheProjectsMemory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	withStatsStores(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "checkout")
	side := filepath.Join(tmp, "checkout-fix")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "seed")
	git("worktree", "add", "-q", side, "-b", "fix")

	// The sessions were worked in the main checkout.
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "checkout", "one.jsonl"), "wtterm", []string{
		strings.TrimSpace(claudeRecord(t, map[string]any{
			"type": "user", "sessionId": "wtterm", "cwd": repo, "timestamp": old,
			"message": map[string]any{"role": "user", "content": "pgbouncer runs in transaction mode and prepared statements are off"},
		})),
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	for _, where := range []struct{ name, dir string }{
		{"the main checkout", repo},
		{"a linked worktree", side},
	} {
		t.Run(where.name, func(t *testing.T) {
			t.Setenv("CLAUDE_PROJECT_DIR", where.dir)
			t.Chdir(where.dir)
			// A Windows path in a JSON string is a run of invalid escapes, and
			// the payload would not parse at all — the same reason
			// hook_cwd_test.go does this.
			withHookStdin(t, hookPayload(t, map[string]string{"source": "startup", "session_id": "ses_wt", "cwd": where.dir}))
			out := captureStdout(t, func() {
				if err := runHookContext(index.DefaultDir(), true); err != nil {
					t.Error(err)
				}
			})
			if !strings.Contains(out, "transaction mode") {
				t.Errorf("a session started in %s opened with no memory of the project:\n%q", where.name, out)
			}
		})
	}
}

// A program of the reader's own that takes deja's subcommand is theirs, even
// when its name starts like deja's: `deja-wrapper.cmd hook-prompt` was read as
// a deja build, so an install rewrote it to deja's line and an uninstall
// deleted it (#4728 review). Builds under other names are still deja's
// (#3681).
func TestHookTokenIsDejasTellsABuildFromAScript(t *testing.T) {
	forgetWrittenExes()
	t.Cleanup(forgetWrittenExes)
	for tok, want := range map[string]bool{
		"/usr/local/bin/deja":                 true,
		`C:\deja\deja.exe`:                    true,
		"/home/me/.config/deja/bin/deja-hook": true,
		"/scratch/deja-cont":                  true,
		`C:/build/deja-prog2.exe`:             true,
		"/tmp/go-build1/b001/deja.test":       true,
		`C:\Temp\deja.test.exe`:               true,
		`"C:/tools/deja-wrapper.cmd"`:         false,
		"/opt/bin/deja-run.sh":                false,
		"/usr/bin/dejavu-notify":              false,
		"/usr/bin/dejagnu":                    false,
	} {
		if got := hookTokenIsDejas(tok); got != want {
			t.Errorf("hookTokenIsDejas(%s) = %v, want %v", tok, got, want)
		}
	}
}

// The same, end to end through every -auto target: after an install, each
// hook line deja wrote is turned into the reader's own wrapper of the same
// shape, then deja installs and uninstalls again. Every wrapper line has to be
// there, unchanged, after both. Run once from a plain home and once from a
// home with a space, where the lines are quoted.
func TestAWrapperNamedLikeDejaSurvivesInstallAndUninstall(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, homeName := range []string{"home", "a home with spaces"} {
		for _, wrapper := range []string{"deja-wrapper.cmd", "deja-run.sh"} {
			for _, target := range installTargetNames() {
				if !strings.HasSuffix(target, "-auto") {
					continue
				}
				t.Run(homeName+"/"+wrapper+"/"+target, func(t *testing.T) {
					home := filepath.Join(t.TempDir(), homeName)
					if err := os.MkdirAll(home, 0o755); err != nil {
						t.Fatal(err)
					}
					spaceyEnv(t, home)
					forgetWrittenExes()
					t.Cleanup(forgetWrittenExes)
					if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
						t.Skipf("install refused here: %v", err)
					}
					// What the lines name: the launcher, or on Windows the
					// binary, written with forward slashes.
					named := filepath.ToSlash(hookCommandExe(self))
					theirs := filepath.ToSlash(filepath.Join(filepath.Dir(named), wrapper))
					seeded := map[string]int{}
					_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
						if err != nil || !fi.Mode().IsRegular() {
							return nil //nolint:nilerr // unreadable entries are skipped
						}
						b, rerr := os.ReadFile(p) //nolint:gosec // the walk is over the test's own temp home
						if rerr != nil {
							return nil //nolint:nilerr // unreadable files are skipped
						}
						text := string(b)
						// A file deja generates whole, or a block it marks as its
						// own, holds no line of the reader's to keep.
						if dejaOwnsFile(home, p) || strings.Contains(text, "managed by `deja install") {
							return nil
						}
						n := 0
						for _, line := range hookCommandLinesIn(text) {
							if firstShellWord(line) == named {
								n++
							}
						}
						if n == 0 {
							return nil
						}
						text = strings.ReplaceAll(text, named+" hook-", theirs+" hook-")
						text = strings.ReplaceAll(text, named+`\" hook-`, theirs+`\" hook-`)
						text = strings.ReplaceAll(text, named+`' hook-`, theirs+`' hook-`)
						if c := strings.Count(text, theirs); c > 0 {
							if err := os.WriteFile(p, []byte(text), fi.Mode()); err != nil { //nolint:gosec // the walk is over the test's own temp home
								t.Fatal(err)
							}
							seeded[p] = c
						}
						return nil
					})
					if len(seeded) == 0 {
						t.Skip("no shell hook line to turn into a wrapper")
					}
					for _, step := range [][]string{{"install", target, "--no-index"}, {"uninstall", target}} {
						_, _ = captureRun(t, step...)
						for p, want := range seeded {
							b, _ := os.ReadFile(p)
							if got := strings.Count(string(b), theirs); got != want {
								t.Errorf("after %s, %s names %s %d times, want %d:\n%s",
									step[0], strings.TrimPrefix(p, home), wrapper, got, want, b)
							}
						}
					}
				})
			}
		}
	}
}

// dejaOwnsFile reports whether deja generated the file whole: it sits in a
// directory named deja, or is named deja itself.
func dejaOwnsFile(home, p string) bool {
	rel, err := filepath.Rel(home, p)
	if err != nil {
		return false
	}
	for part := range strings.SplitSeq(filepath.ToSlash(rel), "/") {
		if part == "deja" || strings.HasPrefix(part, "deja.") {
			return true
		}
	}
	return false
}
