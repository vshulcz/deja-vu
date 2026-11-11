package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

func TestTurn0PromptTaskLocalizationAndRecallBrief(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	proj := filepath.Join(tmp, "work", "payments")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/payments\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"settlement_relay.go":  "package payments\n\nfunc DispatchSettlementBatch(batchID string) error { return nil }\n",
		"settlement_relay_test.go": "package payments\n\nimport \"testing\"\n\nfunc TestDispatchSettlementBatch(t *testing.T) { _ = DispatchSettlementBatch(\"b1\") }\n",
		"backoff_jitter.go":    "package payments\n\nfunc ComputeExponentialJitter(attempt int) int { return attempt }\n",
		"ledger_reconcile.go":  "package payments\n\nfunc ReconcileLedgerSnapshot(epoch int64) error { return nil }\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Zero-history natural module phrase ("settlement relay") resolves target + sibling test recipe.
	line, tok := promptTaskTargetsLine(idxDir, proj, "tighten settlement relay batch handling", nil, nil)
	if !strings.Contains(line, "likely target: settlement_relay.go") {
		t.Fatalf("expected settlement_relay.go in localization line, got %q", line)
	}
	if !strings.Contains(line, "TestDispatchSettlementBatch") {
		t.Fatalf("expected sibling test recipe in localization line, got %q", line)
	}
	if tok == "" {
		t.Fatalf("expected non-empty dedup token")
	}

	// 2. Compound identifier phrase ("exponential jitter") resolves ComputeExponentialJitter file.
	compLine, _ := promptTaskTargetsLine(idxDir, proj, "adjust exponential jitter cap on retries", nil, nil)
	if !strings.Contains(compLine, "likely target: backoff_jitter.go") {
		t.Fatalf("expected backoff_jitter.go for compound declaration phrase, got %q", compLine)
	}

	// 3. liveRecallBrief surfaces natural-phrase localization for MCP/CLI recall callers.
	brief := liveRecallBrief(idxDir, proj, "settlement relay")
	if !strings.Contains(brief, "settlement_relay.go") || !strings.Contains(brief, "TestDispatchSettlementBatch") {
		t.Fatalf("expected liveRecallBrief to localize settlement relay + test recipe, got %q", brief)
	}

	// 4. Negative-control guard: conversational filler, generic git/test commands,
	// non-ASCII acknowledgements, and off-topic questions must produce zero targets.
	negatives := []string{
		"thanks, looks good",
		"yes please proceed",
		"run the tests and check git status",
		"commit the changes",
		"продолжай",
		"давай дальше",
		"what is the capital of france",
		"fix bug in main function",
	}
	for _, neg := range negatives {
		if got, _ := promptTaskTargetsLine(idxDir, proj, neg, nil, nil); got != "" {
			t.Errorf("false-positive localization on negative prompt %q: got %q", neg, got)
		}
	}
}

func TestSessionActionMapAndWeakPointerSurfaceEditedFiles(t *testing.T) {
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
	relayTest := filepath.Join(proj, "relay_test.go")
	if err := os.WriteFile(relayTest, []byte("package payments\n\nimport \"testing\"\n\nfunc TestDispatchSettlementBatch(t *testing.T) { _ = DispatchSettlementBatch() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Two independent sessions edited relay.go and verified go test, without naming relay.go in prose.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"prevent duplicate settlement batches under worker contention"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, relayFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test -run TestDispatchSettlementBatch ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"ok  \texample.com/payments\t0.012s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:03:00Z","message":{"role":"assistant","content":"We serialize worker claims with an advisory lease before dispatching."}}`, proj),
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s2.jsonl"), "s2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"tighten lease timeout on settlement batch dispatch"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, relayFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b2","name":"Bash","input":{"command":"go test -run TestDispatchSettlementBatch ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:02:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b2","content":"ok  \texample.com/payments\t0.011s"}]}}`, proj),
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	metas, err := index.AllMeta(idxDir)
	if err != nil || len(metas) != 2 {
		t.Fatalf("expected 2 indexed sessions, got %d (%v)", len(metas), err)
	}
	ss := []model.Session{{
		Harness: metas[0].Harness,
		ID:      metas[0].ID,
		Project: metas[0].Project,
		Title:   metas[0].Title,
	}}

	actLine := sessionActionMapLine(idxDir, proj, ss, nil)
	if !strings.Contains(actLine, "files edited there: relay.go") {
		t.Fatalf("expected relay.go in sessionActionMapLine, got %q", actLine)
	}
	if !strings.Contains(actLine, "go test -run TestDispatchSettlementBatch") {
		t.Fatalf("expected verified test command in sessionActionMapLine, got %q", actLine)
	}

	ptr := weakRecallPointerIn(idxDir, proj, ss, []string{"settlement", "lease"}, nil)
	if !strings.Contains(ptr, "files edited there: relay.go") {
		t.Fatalf("expected weakRecallPointerIn to carry concrete edited files, got %q", ptr)
	}
}
