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

func TestTurn0MultilingualFixPairBridgeAndSelfProjectNoiseGuard(t *testing.T) {
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
	storeFile := filepath.Join(proj, "store_codec.go")
	if err := os.WriteFile(storeFile, []byte("package payments\n\nfunc DecodeFrameHeader(buf []byte) error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loopFile := filepath.Join(proj, "worker_lease.go")
	if err := os.WriteFile(loopFile, []byte("package payments\n\nfunc RenewWorkerLease() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var sweepFiles []string
	for i := range 10 {
		p := filepath.Join(proj, fmt.Sprintf("extra_%02d.go", i))
		if err := os.WriteFile(p, []byte(fmt.Sprintf("package payments\n\nfunc ExtraHandler%02d() {}\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		sweepFiles = append(sweepFiles, p)
	}

	// s1: confirmed self-corrected FixPair on store_codec.go ("makeslice: len out of range").
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"investigate corrupt frame decode crash"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:30Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e0","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, storeFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b0","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b0","is_error":true,"content":"panic: runtime error: makeslice: len out of range\nFAIL\texample.com/payments\t0.02s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:30Z","message":{"role":"assistant","content":"The panic happened because untrusted frame length overflowed makeslice bounds."}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:45Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, storeFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"ok  \texample.com/payments\t0.01s"}]}}`, proj),
	})

	// s2 & s3: Cyrillic sessions editing worker_lease.go with inflected domain words.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s2.jsonl"), "s2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"воркер зацикливается при потере аренды очереди"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, loopFile),
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s3.jsonl"), "s3", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s3","cwd":%q,"timestamp":"2026-03-03T10:00:00Z","message":{"role":"user","content":"убрать повторное зацикливание аренды очереди"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s3","cwd":%q,"timestamp":"2026-03-03T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e3","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, loopFile),
	})

	// s4: broad 10-file marathon sweep mentioning the project's own name ("payments").
	var s4Lines []string
	s4Lines = append(s4Lines, fmt.Sprintf(`{"type":"user","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:00:00Z","message":{"role":"user","content":"wide cleanup across payments handlers"}}`, proj))
	for i, sf := range sweepFiles {
		s4Lines = append(s4Lines, fmt.Sprintf(`{"type":"assistant","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:01:%02dZ","message":{"role":"assistant","content":[{"type":"tool_use","id":"es%d","name":"Edit","input":{"file_path":%q,"old_string":"{}","new_string":"{}"}}]}}`, proj, i, i, sf))
	}
	s4Lines = append(s4Lines, fmt.Sprintf(`{"type":"assistant","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:05:00Z","message":{"role":"assistant","content":"Completed wide cleanup across payments."}}`, proj))
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s4.jsonl"), "s4", s4Lines)

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	// 1. FixPair memory bridges "makeslice panic" in natural prose -> store_codec.go without pasted stderr.
	fxLoc, _ := promptTaskTargetsLine(idxDir, proj, "harden makeslice panic bounds in payments", nil, nil)
	if !strings.Contains(fxLoc, "store_codec.go") {
		t.Fatalf("expected FixPair bridge to localize store_codec.go, got %q", fxLoc)
	}

	// 2. Pure Cyrillic prompt with inflected stem ("зацикливания" vs "зацикливается") localizes worker_lease.go.
	cyrLoc, _ := promptTaskTargetsLine(idxDir, proj, "проверить защиту от зацикливания аренды очереди", nil, nil)
	if !strings.Contains(cyrLoc, "worker_lease.go") {
		t.Fatalf("expected Cyrillic stem bridge to localize worker_lease.go, got %q", cyrLoc)
	}

	// 3. Broad >8-file sweep session s4 is suppressed in sessionActionMapLine (zero sweep noise).
	sweepSess := []model.Session{{Harness: "claude", ID: "s4", Project: "work/payments", Title: "wide cleanup"}}
	if got := sessionActionMapLineForPrompt(idxDir, proj, "check payments status", sweepSess, nil); got != "" {
		t.Fatalf("expected >8-file sweep session to stay silent in sessionActionMapLineForPrompt, got %q", got)
	}

	// 4. Self-project name ("payments") inside cwd must NOT bypass the 2-term quote gate in promptDigestBodyIn.
	sweepFull := []model.Session{{
		Harness: "claude",
		ID:      "s4",
		Project: "work/payments",
		Title:   "wide cleanup",
		Messages: []model.Message{
			{Role: "user", Text: "wide cleanup across payments handlers"},
			{Role: "assistant", Text: "Completed wide cleanup across payments."},
		},
	}}
	idf := map[string]float64{"payments": 1.2, "unseen_xyz_token": 4.8}
	if body, ok := promptDigestBodyIn(idxDir, proj, "live", sweepFull, true, []string{"payments", "unseen_xyz_token"}, idf, "check payments unseen_xyz_token", "", "", nil, nil, nil, nil); ok {
		t.Fatalf("expected self-project name not to relax 2-term quote gate inside cwd, got:\n%s", body)
	}
}
