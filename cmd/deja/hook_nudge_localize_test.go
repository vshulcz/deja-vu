package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
		"settlement_relay.go":      "package payments\n\nfunc DispatchSettlementBatch(batchID string) error { return nil }\n",
		"settlement_relay_test.go": "package payments\n\nimport \"testing\"\n\nfunc TestDispatchSettlementBatch(t *testing.T) { _ = DispatchSettlementBatch(\"b1\") }\n",
		"backoff_jitter.go":        "package payments\n\nfunc ComputeExponentialJitter(attempt int) int { return attempt }\n",
		"ledger_reconcile.go":      "package payments\n\nfunc ReconcileLedgerSnapshot(epoch int64) error { return nil }\n",
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
	if len(tok) == 0 {
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

	actLine := sessionActionMapLineForPrompt(idxDir, proj, "", ss, nil)
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
	sweepFiles := make([]string, 0, 10)
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
	s4Lines := make([]string, 0, len(sweepFiles)+2)
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

	// 5. Ranked touched consensus recovers worker_lease.go when >=2 strongly matching sessions edited it.
	ranked := []model.Session{
		{Harness: "claude", ID: "s2", Project: "work/payments"},
		{Harness: "claude", ID: "s3", Project: "work/payments"},
		{Harness: "claude", ID: "s4", Project: "work/payments"},
		{Harness: "claude", ID: "skip1", Project: "work/payments"},
	}
	rcLine, rcTok := rankedTouchedConsensusLine(idxDir, proj, ranked, []int{2, 2, 2, 2}, []int{1, 1, 1, 1}, nil, map[string]bool{"skip1": true})
	if !strings.Contains(rcLine, "files edited in related sessions: worker_lease.go") || rcTok == "" {
		t.Fatalf("expected rankedTouchedConsensusLine to recover worker_lease.go, got line=%q tok=%q", rcLine, rcTok)
	}
	if seenLine, _ := rankedTouchedConsensusLine(idxDir, proj, ranked, []int{2, 2}, []int{1, 1}, map[string]bool{rcTok: true}, nil); seenLine != "" {
		t.Fatalf("expected seen token to suppress duplicate consensus line, got %q", seenLine)
	}
}

func TestTurn0StructuredPromptDiagnosticGuardrailAndDedup(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	proj := filepath.Join(tmp, "work", "archiver")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/archiver\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(proj, "manifest_verifier.go")
	quorumPath := filepath.Join(proj, "quorum_fencing.go")
	readmePath := filepath.Join(proj, "CHANGELOG.md")
	files := map[string]string{
		manifestPath: "package archiver\n\nimport \"errors\"\n\nfunc VerifyManifestDigest(raw []byte) error {\n\tif len(raw) == 0 {\n\t\treturn errors.New(\"snapshot manifest checksum mismatch\")\n\t}\n\treturn nil\n}\n",
		filepath.Join(proj, "manifest_verifier_test.go"): "package archiver\n\nimport \"testing\"\n\nfunc TestVerifyManifestDigest(t *testing.T) { _ = VerifyManifestDigest([]byte(\"ok\")) }\n",
		quorumPath: "package archiver\n\ntype Change struct{ Epoch int }\n\nfunc AcquireQuorumEpoch(epoch int) error { return nil }\n",
		readmePath: "# Changelog\n\nInitial release.\n",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)

	// Past sessions on manifest_verifier.go: two sessions touching it, including a
	// verified FixPair breakage so Turn 0 has a concrete guardrail to attach.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "archiver", "m1.jsonl"), "m1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"verify manifest digest handling"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"em1","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, manifestPath),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"bm1","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:02:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"bm1","is_error":true,"content":"--- FAIL: TestVerifyManifestDigest (0.01s)\n    manifest_verifier_test.go:6: empty trailer header\nFAIL\tarchiver\t0.02s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:03:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"em2","name":"Edit","input":{"file_path":%q,"old_string":"len(raw) == 0","new_string":"len(raw) < 4"}}]}}`, proj, manifestPath),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:04:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"bm2","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:04:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"bm2","content":"ok  \texample.com/archiver\t0.01s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","cwd":%q,"timestamp":"2026-03-01T10:04:30Z","message":{"role":"assistant","content":"Fixed TestVerifyManifestDigest in manifest_verifier.go because empty trailer headers were not rejected before slicing."}}`, proj),
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "archiver", "m2.jsonl"), "m2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"m2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"tighten manifest verifier bounds"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"em3","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, manifestPath),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m2","cwd":%q,"timestamp":"2026-03-02T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"bm3","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"m2","cwd":%q,"timestamp":"2026-03-02T10:02:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"bm3","content":"ok  \texample.com/archiver\t0.01s"}]}}`, proj),
	})

	// Past sessions bridging external domain vocabulary ("splitbrain", "heartbeat")
	// onto rare repo stem "quorum" / "fencing" via cooccur.gob.
	for i := 1; i <= 3; i++ {
		sid := fmt.Sprintf("q%d", i)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "archiver", sid+".jsonl"), sid, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-03-0%dT11:00:00Z","message":{"role":"user","content":"investigate splitbrain stalledleasedetector during quorum fencing"}}`, sid, proj, i+2),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-03-0%dT11:01:00Z","message":{"role":"assistant","content":"Updated quorum fencing AcquireQuorumEpoch to fence splitbrain stalledleasedetector."}}`, sid, proj, i+2),
		})
	}

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	// 1. Multi-paragraph structured spec with fork boilerplate, doc mention (CHANGELOG.md),
	// sentence-initial Titlecase word ("Change"), quoted diagnostic error string, and
	// closing git/PR workflow instructions localizes manifest_verifier.go + guardrail!
	structuredPrompt := "Fork started — processing in background\n<fork-boilerplate>\nWorker fork instructions.\n</fork-boilerplate>\n\nYour directive: Change in example.com/archiver (also note CHANGELOG.md).\nUsers see \"snapshot manifest checksum mismatch\" when restoring empty archives.\n\nWork in your own worktree from origin/main (`git fetch origin`).\nDo NOT push or open a PR.\nReport: branch + commit SHA, root cause, and `go test ./...` output."

	turn0 := promptDirectContextIn(idxDir, proj, "live-sess-1", structuredPrompt, false, nil)
	if !strings.Contains(turn0, "likely target: manifest_verifier.go") {
		t.Fatalf("expected structured prompt + diagnostic literal to localize manifest_verifier.go, got:\n%s", turn0)
	}
	if strings.Contains(turn0, "defined in quorum_fencing.go") {
		t.Fatalf("expected sentence-initial Titlecase 'Change' not to trigger symbol brief hijack, got:\n%s", turn0)
	}
	if !strings.Contains(turn0, "past breakage here:") && !strings.Contains(turn0, "prior decision:") {
		t.Fatalf("expected Turn-0 localization to attach verified guardrail for manifest_verifier.go, got:\n%s", turn0)
	}

	// 2. Temporal skip: skipping m1 hides m1's FixPair breakage from Turn-0 guardrail.
	skipped := promptDirectContextIn(idxDir, proj, "", structuredPrompt, true, map[string]bool{"m1": true})
	if strings.Contains(skipped, "TestVerifyManifestDigest") && strings.Contains(skipped, "past breakage here:") {
		t.Fatalf("expected skip[m1] to suppress m1 FixPair guardrail (zero future leakage), got:\n%s", skipped)
	}

	// 3. Cross-surface .seen deduplication: after live-sess-1 already saw the
	// guardrail at Turn 0, PreToolUse on manifest_verifier.go must not re-inject the same guardrail.
	toolPayload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":%q},"cwd":%q,"session_id":"live-sess-1"}`, manifestPath, proj)
	var toolOut bytes.Buffer
	if err := runHookTool(idxDir, strings.NewReader(toolPayload), &toolOut); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(toolOut.String(), "past breakage here:") {
		t.Fatalf("expected .seen deduplication to suppress duplicate PreToolUse guardrail in same session, got:\n%s", toolOut.String())
	}

	// 4. Co-occurrence bridge: domain words ("splitbrain", "stalledleasedetector") absent from
	// source files bridge via cooccur.gob onto quorum_fencing.go, while vague prompts stay silent.
	if len(index.CooccurNeighbors(idxDir, "splitbrain")) > 0 {
		coLoc, _ := promptTaskTargetsLine(idxDir, proj, "prevent splitbrain stalledleasedetector regression", nil, nil)
		if !strings.Contains(coLoc, "quorum_fencing.go") {
			t.Fatalf("expected cooccur.gob bridge to localize quorum_fencing.go, got %q", coLoc)
		}
	}
	if negLoc, _ := promptTaskTargetsLine(idxDir, proj, "please clean up comments and formatting", nil, nil); negLoc != "" {
		t.Fatalf("expected vague negative prompt to stay silent, got %q", negLoc)
	}

	// 5. Direct helper coverage for reversal clipping, breakage ranking, and workflow meta spans.
	if !isWorkflowMetaSpan("git fetch origin main") || isWorkflowMetaSpan("AcquireQuorumEpoch") {
		t.Errorf("unexpected isWorkflowMetaSpan behavior")
	}
	if !usableReversalLine("Откатил кэширование сокета, потому что оно ломало переподключение") || usableReversalLine("Verified: with the hunk reverted, test fails") {
		t.Errorf("unexpected usableReversalLine behavior")
	}
	longRev := "Confirmed: First we inspected the pool stats in detail across several worker Goroutines; abandoned per-request mutex locking in LeaseStore because it deadlocked under retry storms."
	if clipped := clipReversalDecision(longRev, 120); !strings.Contains(clipped, "abandoned") {
		t.Errorf("expected clipReversalDecision to preserve reversal tail, got %q", clipped)
	}
	if !mentionsFileStrictForReversal("internal/lease_workers.go", "reverted worker retry loop") || mentionsFileStrictForReversal("internal/search/query.go", "search package cleanup") {
		t.Errorf("unexpected mentionsFileStrictForReversal behavior")
	}
	if !mentionsDistinctiveSymbolForReversal([]string{"AcquireQuorumEpoch"}, "reverted AcquireQuorumEpoch lock") || mentionsDistinctiveSymbolForReversal([]string{"short"}, "reverted short") {
		t.Errorf("unexpected mentionsDistinctiveSymbolForReversal behavior")
	}
	if !repoEntryMentionsFile(proj, filepath.Join(proj, "quorum_fencing.go"), "always check quorum_fencing invariants") || repoEntryMentionsFile(proj, filepath.Join(proj, "io.go"), "unrelated note") {
		t.Errorf("unexpected repoEntryMentionsFile behavior")
	}
	if !isGenericDirPart("internal") || isGenericDirPart("quorum") {
		t.Errorf("unexpected isGenericDirPart behavior")
	}
	fp := index.FixPair{Symbol: "LeaseStore.RenewQuorum", Why: "RenewQuorum skipped epoch check", Candidate: false}
	if breakageContextRank(fp, []string{"RenewQuorum"}) < 7 || !whyMentionsSymbol(fp.Why, fp.Symbol) {
		t.Errorf("unexpected breakageContextRank / whyMentionsSymbol")
	}
	if !decisionDuplicatesBreakageWhy("Root cause: RenewQuorum skipped epoch check during failover", "TestRenew — RenewQuorum skipped epoch check during failover") {
		t.Errorf("expected decisionDuplicatesBreakageWhy = true")
	}
	if extractBrokenTestName("--- FAIL: TestVerifyManifestDigest (0.01s)") != "TestVerifyManifestDigest" || !isSpecificBreakageError("panic: nil pointer dereference") || isSpecificBreakageError("- bullet item") {
		t.Errorf("unexpected breakage error classification")
	}
}

func TestTurn0StemStopwordMemoryAndSingleSessionConsensus(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	proj := filepath.Join(tmp, "payservice")
	files := map[string]string{
		filepath.Join(proj, ".git", "HEAD"):          "ref: refs/heads/main\n",
		filepath.Join(proj, "batch_after_hook.go"):   "package payservice\n\nfunc RunSettlementWindow() {}\n",
		filepath.Join(proj, "mcp_router.go"):         "package payservice\n\nfunc DispatchMcpFrame() {}\n",
		filepath.Join(proj, "localize_cache.go"):     "package payservice\n\nfunc LocalizeTargetCache() {}\n",
		filepath.Join(proj, "quorum_fence.go"):       "package payservice\n\nfunc FenceEpoch() {}\n",
		filepath.Join(proj, "worker_retry_one.go"):   "package payservice\n\nfunc RetryWorkerOne() {}\n",
		filepath.Join(proj, "worker_retry_two.go"):   "package payservice\n\nfunc RetryWorkerTwo() {}\n",
		filepath.Join(proj, "worker_retry_three.go"): "package payservice\n\nfunc RetryWorkerThree() {}\n",
		filepath.Join(proj, "worker_retry_four.go"):  "package payservice\n\nfunc RetryWorkerFour() {}\n",
		filepath.Join(proj, ".deja", "memory.jsonl"): `{"id":"aa11bb22cc","kind":"gotcha","text":"Settlement rollback fails during reconciliation window","files":["batch_after_hook.go"],"date":"2026-03-05"}` + "\n",
	}
	for p, content := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Generic stopword stem tokens ("after hook") must not pass isTechnicalStemStopWord or spuriously match batch_after_hook.go.
	if !isTechnicalStemStopWord("after") || !isTechnicalStemStopWord("hook") {
		t.Fatalf("expected after/hook to be filtered as technical stem stopwords")
	}
	if stopOut, _ := promptTaskTargetsLine(idxDir, proj, "check behavior after hook runs", nil, nil); strings.Contains(stopOut, "batch_after_hook.go") {
		t.Errorf("generic stopwords unexpectedly localized batch_after_hook.go: %q", stopOut)
	}

	// 2. Cold repo without an index still resolves 3-letter package/stem tokens ("mcp"), ASCII inflections ("localizing"), and .deja/memory.jsonl.
	if mcpOut, _ := promptTaskTargetsLine(idxDir, proj, "fix broken mcp dispatch router", nil, nil); !strings.Contains(mcpOut, "mcp_router.go") {
		t.Errorf("expected 3-letter token to localize mcp_router.go, got %q", mcpOut)
	}
	if inflectOut, _ := promptTaskTargetsLine(idxDir, proj, "fix localizing target cache bug", nil, nil); !strings.Contains(inflectOut, "localize_cache.go") {
		t.Errorf("expected ASCII inflection to localize localize_cache.go, got %q", inflectOut)
	}
	memBoost := repoMemoryTargetBoost(proj, "settlement rollback failure during reconciliation window", selfProjectWordSet(proj))
	if memBoost["batch_after_hook.go"] <= 0 {
		t.Errorf("expected .deja/memory.jsonl boost for batch_after_hook.go, got %v", memBoost)
	}

	// 3. Single strong session fallback in rankedTouchedConsensusLine works when contributingSessions == 1,
	// while weak or cross-project single sessions stay silent.
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	fenceFile := filepath.Join(proj, "quorum_fence.go")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payservice", "strong1.jsonl"), "strong1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"strong1","cwd":%q,"timestamp":"2026-03-06T10:00:00Z","message":{"role":"user","content":"fix quorum epoch fencing drift during snapshot handoff"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"strong1","cwd":%q,"timestamp":"2026-03-06T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"func FenceEpoch() {}","new_string":"func FenceEpoch() {}"}}]}}`, proj, fenceFile),
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	metas, err := index.AllMeta(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	var projName string
	for _, m := range metas {
		if m.ID == "strong1" {
			projName = m.Project
			break
		}
	}
	if projName == "" {
		t.Fatalf("expected strong1 metadata in index, got %+v", metas)
	}

	ranked := []model.Session{{Harness: "claude", ID: "strong1", Project: projName}}
	strongLine, strongTok := rankedTouchedConsensusLine(idxDir, proj, ranked, []int{3}, []int{2}, nil, nil)
	if !strings.Contains(strongLine, "files edited in related session: quorum_fence.go") || strongTok == "" {
		t.Errorf("expected single-strong-session fallback to recover quorum_fence.go, got line=%q tok=%q", strongLine, strongTok)
	}
	if weakLine, _ := rankedTouchedConsensusLine(idxDir, proj, ranked, []int{2}, []int{0}, nil, nil); weakLine != "" {
		t.Errorf("expected weak single session to stay silent, got %q", weakLine)
	}
	crossRanked := []model.Session{{Harness: "claude", ID: "strong1", Project: "other/unrelated"}}
	if crossLine, _ := rankedTouchedConsensusLine(idxDir, proj, crossRanked, []int{3}, []int{2}, nil, nil); crossLine != "" {
		t.Errorf("expected cross-project single session to be filtered out, got %q", crossLine)
	}
}

func TestTurn0FourCharMorphBodyAndCrossScriptLocalize(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	proj := filepath.Join(tmp, "work", "gateway")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":             "module example.com/gateway\n\ngo 1.24\n",
		"subnet_pool.go":     "package gateway\n\n// Allocates cidr ranges and normalizes ingress headers.\nfunc AssignRouteSlot(slot int) int { return slot }\n",
		"localize_filter.go": "package gateway\n\nfunc FilterLocalizedSnippets() {}\n",
		"alpha_worker.go":    "package gateway\n\nfunc RunAlphaWorker() {}\n",
		"beta_worker.go":     "package gateway\n\nfunc RunBetaWorker() {}\n",
		"gamma_worker.go":    "package gateway\n\nfunc RunGammaWorker() {}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// (a) 4-char standalone body token ("cidr") + morphological root ("normalizing" -> "normaliz" matching "normalizes").
	bodyLine, _ := promptTaskTargetsLine(idxDir, proj, "fix normalizing cidr allocations", nil, nil)
	if !strings.Contains(bodyLine, "subnet_pool.go") {
		t.Fatalf("expected subnet_pool.go from 4-char + morph body match, got %q", bodyLine)
	}

	// (b) Russian prompt localizes English repo file via CrossScriptTerms without English tokens.
	ruLine, _ := promptTaskTargetsLine(idxDir, proj, "поправь локализацию и фильтрацию сниппетов", nil, nil)
	if !strings.Contains(ruLine, "localize_filter.go") {
		t.Fatalf("expected localize_filter.go from Russian CrossScriptTerms prompt, got %q", ruLine)
	}
}

func TestSingleStrongSessionScansPastNotesAndRanksWideTouched(t *testing.T) {
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
	noiseNames := []string{
		"noise_one.go", "noise_two.go", "noise_three.go", "noise_four.go", "noise_five.go",
		"noise_six.go", "noise_seven.go", "noise_eight.go", "noise_nine.go",
	}
	for _, name := range noiseNames {
		if err := os.WriteFile(filepath.Join(proj, name), []byte("package payments\n\nfunc NoopTask() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	quorumFile := filepath.Join(proj, "quorum_lease.go")
	if err := os.WriteFile(quorumFile, []byte("package payments\n\nfunc RenewQuorumLease() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Three zero-Touched note sessions ahead of one 10-file session where quorum_lease.go was edited last.
	for i, sid := range []string{"note1", "note2", "note3"} {
		writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", sid+".jsonl"), sid, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-03-0%dT10:00:00Z","message":{"role":"user","content":"discuss quorum lease architecture notes"}}`, sid, proj, i+1),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-03-0%dT10:01:00Z","message":{"role":"assistant","content":"Reviewed design tradeoffs without editing files."}}`, sid, proj, i+1),
		})
	}
	s4Lines := make([]string, 0, 2+len(noiseNames))
	s4Lines = append(s4Lines,
		fmt.Sprintf(`{"type":"user","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:00:00Z","message":{"role":"user","content":"tighten quorum lease fencing across workers"}}`, proj),
	)
	for i, name := range noiseNames {
		p := filepath.Join(proj, name)
		s4Lines = append(s4Lines, fmt.Sprintf(`{"type":"assistant","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:01:%02dZ","message":{"role":"assistant","content":[{"type":"tool_use","id":"n%d","name":"Edit","input":{"file_path":%q,"old_string":"NoopTask()","new_string":"NoopTask()"}}]}}`, proj, i+1, i+1, p))
	}
	s4Lines = append(s4Lines, fmt.Sprintf(`{"type":"assistant","sessionId":"s4","cwd":%q,"timestamp":"2026-03-04T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"q1","name":"Edit","input":{"file_path":%q,"old_string":"return nil","new_string":"return nil"}}]}}`, proj, quorumFile))
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s4.jsonl"), "s4", s4Lines)

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	metas, err := index.AllMeta(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]index.SessionMeta{}
	for _, m := range metas {
		byID[m.ID] = m
	}
	toSess := func(id string) model.Session {
		m := byID[id]
		return model.Session{Harness: m.Harness, ID: m.ID, Project: m.Project, Title: m.Title}
	}
	ranked := []model.Session{toSess("note1"), toSess("note2"), toSess("note3"), toSess("s4")}
	line, tok := rankedTouchedConsensusLineForPrompt(idxDir, proj, "quorum lease fencing bug", ranked, []int{4, 4, 4, 3}, []int{0, 0, 0, 0}, nil, nil)
	if !strings.HasPrefix(line, "files edited in related session: quorum_lease.go") {
		t.Fatalf("expected quorum_lease.go ranked first after scanning past note sessions and >8 touched files, got %q (tok=%q)", line, tok)
	}
}

func TestSelectLowDFTouchedTargets(t *testing.T) {
	sweep := []string{"f01.go", "f02.go", "f03.go", "f04.go", "f05.go", "f06.go", "f07.go", "f08.go", "f09.go", "f10.go"}
	sweepDF := make(map[string]int, len(sweep))
	for _, f := range sweep {
		sweepDF[f] = 1
	}
	if got := selectLowDFTouchedTargets(sweep, sweepDF, 4); len(got) != 0 {
		t.Fatalf("expected pure low-DF sweep to return nil, got %v", got)
	}

	mixed := []string{
		"hub1.go", "hub2.go", "hub3.go", "hub4.go", "hub5.go", "hub6.go", "hub7.go",
		"leaf_alpha.go", "leaf_beta.go",
	}
	mixedDF := map[string]int{
		"hub1.go":       18,
		"hub2.go":       15,
		"hub3.go":       12,
		"hub4.go":       9,
		"hub5.go":       8,
		"hub6.go":       7,
		"hub7.go":       6,
		"leaf_alpha.go": 2,
		"leaf_beta.go":  3,
	}
	got := selectLowDFTouchedTargets(mixed, mixedDF, 4)
	want := []string{"leaf_alpha.go", "leaf_beta.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("expected mixed hub+leaf session to isolate %v, got %v", want, got)
	}
}

func TestEvalPromptTurn0ReadOnlyOmitsLeadAndStaysConcise(t *testing.T) {
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
	relayFile := filepath.Join(proj, "settlement_relay.go")
	if err := os.WriteFile(relayFile, []byte("package payments\n\nfunc DispatchSettlementBatch() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"investigate backoff scheduling jitter"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, relayFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:00Z","message":{"role":"assistant","content":"Settled on epoch fencing tokens in settlement_relay.go so workers abort stale dispatches."}}`, proj),
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s2.jsonl"), "s2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"check coordinator standby lease"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, relayFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:02:00Z","message":{"role":"assistant","content":"Confirmed epoch fencing validates settlement_relay dispatches."}}`, proj),
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out := evalPromptTurn0ReadOnly(idxDir, proj, "settlement relay epoch fencing", []string{"payments"}, nil)
	if out == "" {
		t.Fatalf("expected non-empty Turn-0 read-only recall output")
	}
	if strings.Contains(out, promptHookLead) {
		t.Fatalf("expected evalPromptTurn0ReadOnly to omit promptHookLead preamble, got %q", out)
	}
	if len(out) > 900 {
		t.Fatalf("expected steady-state Turn-0 output <= 900 bytes, got %d: %q", len(out), out)
	}
}

func TestLongSpecMegaFileDampeningAndDirScope(t *testing.T) {
	idxDir := hermeticEnv(t)
	proj := t.TempDir()
	for _, rel := range []string{"internal/reconcile", "cmd/ledger"} {
		if err := os.MkdirAll(filepath.Join(proj, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	reconcileSrc := `package reconcile

// ReconcileBatchOffsets commits verified offset watermarks per partition.
func ReconcileBatchOffsets(
	partition int,
	watermark int64,
) error {
	return commitOffsetWatermark(partition, watermark)
}

func SharedEntry() {}

func commitOffsetWatermark(partition int, watermark int64) error {
	if watermark < 0 {
		return nil
	}
	_ = partition
	return nil
}
`
	if err := os.WriteFile(filepath.Join(proj, "internal", "reconcile", "offset_table.go"), []byte(reconcileSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	var mega strings.Builder
	mega.WriteString("package main\n\nfunc SharedEntry() {}\n\n")
	for i := range 220 {
		fmt.Fprintf(&mega, "func dispatchLedgerRoute%03d(offset, watermark, batch, partition int) int {\n", i)
		mega.WriteString("\t// reconcile dispatch helper in cmd/ledger router\n")
		mega.WriteString("\treturn offset + watermark + batch + partition\n}\n\n")
	}
	if err := os.WriteFile(filepath.Join(proj, "cmd", "ledger", "router_dispatch.go"), []byte(mega.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := strings.Join([]string{
		"Fix partition watermark persistence bug in internal/reconcile.",
		"Lines below describe the failure symptom and constraints:",
		"When offset watermarks advance, batch commits must stay atomic.",
		"Audit partition watermark handling under internal/reconcile.",
		"Check commitOffsetWatermark bounds before advancing.",
		"Constraints: keep cmd/ledger router dispatch behavior unchanged.",
		"Verification: gofmt -w cmd/ledger and run golangci-lint before report.",
		"Deliverables: branch report only, do not push.",
	}, "\n")

	line, toks := promptTaskTargetsLine(idxDir, proj, spec, nil, nil)
	if line == "" || len(toks) == 0 {
		t.Fatalf("expected non-empty localization targets, got line=%q toks=%v", line, toks)
	}
	offIdx := strings.Index(line, "internal/reconcile/offset_table.go")
	if offIdx < 0 {
		t.Fatalf("expected internal/reconcile/offset_table.go in %q", line)
	}
	if routerIdx := strings.Index(line, "cmd/ledger/router_dispatch.go"); routerIdx >= 0 && routerIdx < offIdx {
		t.Fatalf("expected internal/reconcile/offset_table.go before mega-file, got %q", line)
	}

	idx := loadRepoSymbolMap(proj)
	loc, ok := idx.firstDecl["reconcilebatchoffsets"]
	if !ok {
		t.Fatalf("expected ReconcileBatchOffsets in idx.firstDecl")
	}
	if loc.rel != "internal/reconcile/offset_table.go" || loc.line != 4 {
		t.Fatalf("unexpected decl location: %+v", loc)
	}
	if !strings.Contains(loc.sig, "watermark int64") || strings.Contains(loc.sig, "\n") {
		t.Fatalf("expected collapsed multi-line signature with watermark int64, got %q", loc.sig)
	}

	rel, lineNo, sig := findSymbolDeclarationInRepo(proj, "ReconcileBatchOffsets")
	if rel != "internal/reconcile/offset_table.go" || lineNo != 4 || !strings.Contains(sig, "watermark int64") {
		t.Fatalf("findSymbolDeclarationInRepo got (%q, %d, %q)", rel, lineNo, sig)
	}

	symLine, symTok := promptSymbolBriefLine(proj, "audit SharedEntry vs ReconcileBatchOffsets", nil)
	if !strings.HasPrefix(symTok, "prompt:sb:") {
		t.Fatalf("expected prompt:sb token from promptSymbolBriefLine, got %q (%q)", symTok, symLine)
	}
	if !strings.Contains(symLine, "internal/reconcile/offset_table.go:4") || !strings.Contains(symLine, "watermark int64") {
		t.Fatalf("expected df=1 ReconcileBatchOffsets signature in promptSymbolBriefLine output, got %q", symLine)
	}
	if strings.Contains(symLine, "SharedEntry") || strings.Contains(symLine, "router_dispatch.go") {
		t.Fatalf("expected ReconcileBatchOffsets (df=1) to beat SharedEntry (df=2), got %q", symLine)
	}
}

func TestStage2ImplicitLocalizationAndCompactDigestBudget(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	proj := filepath.Join(tmp, "work", "myapp")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/myapp\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"internal/search/fork_copy.go":     "package search\n\nfunc NormalizeForkCopyGroup(id string) string { return id }\n",
		"cmd/app/fork_copies.go":           "package main\n\nfunc ListForkCopies() int { return 0 }\n",
		"extensions/widget/index.js":       "export function registerWidget() { return true }\n",
		"extensions/widget/lib.js":         "export const WIDGET_VERSION = 1\n",
		"cmd/app/install_widget_plugin.go": "package main\n\nfunc InstallWidgetPlugin() error { return nil }\n",
		"cmd/app/release_notice.go":        "package main\n\nimport \"os\"\n\nfunc ReleaseNoticeDisabled() bool { return os.Getenv(\"MYAPP_NO_RELEASE_NOTICE\") == \"1\" }\n",
		"cmd/app/notice_caller.go":         "package main\n\nfunc checkNotice() bool { return ReleaseNoticeDisabled() }\n",
	}
	for i := range 20 {
		files[fmt.Sprintf("internal/misc/mod_%02d.go", i)] = fmt.Sprintf("package misc\n\nfunc Helper%02d() int { return %d }\n", i, i)
	}
	for rel, body := range files {
		full := filepath.Join(proj, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Rules: paragraph preserves technical copy/fork grouping scope and -y/-ies stem plural match.
	forkPrompt := "Audit deduplication in recent sessions.\nRules: keep your change inside the copy/fork grouping logic."
	forkLine, _ := promptTaskTargetsLine(idxDir, proj, forkPrompt, nil, nil)
	if !strings.Contains(forkLine, "internal/search/fork_copy.go") || !strings.Contains(forkLine, "cmd/app/fork_copies.go") {
		t.Fatalf("expected both fork_copy.go and fork_copies.go in %q", forkLine)
	}

	// 2. Explicit small directory scope (`extensions/widget/`) prepends directory files ahead of suffix bigrams.
	dirPrompt := "Update the widget plugin (`extensions/widget/`) startup payload."
	dirLine, _ := promptTaskTargetsLine(idxDir, proj, dirPrompt, nil, nil)
	idxIdx := strings.Index(dirLine, "extensions/widget/index.js")
	plugIdx := strings.Index(dirLine, "cmd/app/install_widget_plugin.go")
	if !strings.Contains(dirLine, "extensions/widget/lib.js") || idxIdx < 0 || plugIdx < 0 || plugIdx < idxIdx {
		t.Fatalf("expected explicit small dir targets before widget plugin file, got %q", dirLine)
	}

	// 3. Unique compound env/config identifier resolves release_notice.go.
	envPrompt := "Fix startup update check when MYAPP_NO_RELEASE_NOTICE=1 is set."
	envLine, _ := promptTaskTargetsLine(idxDir, proj, envPrompt, nil, nil)
	if !strings.Contains(envLine, "cmd/app/release_notice.go") {
		t.Fatalf("expected cmd/app/release_notice.go in %q", envLine)
	}

	// 4. Guidance detection, compact target supplement, and index-backed caller resolution.
	if !nudgeHasLocalizedGuidance("likely target: a.go") ||
		!nudgeHasLocalizedGuidance("defined in a.go:10: func Foo()") ||
		!nudgeHasLocalizedGuidance("touch together: a.go") {
		t.Fatalf("expected nudgeHasLocalizedGuidance to recognize target/symbol/touch prefixes")
	}
	supp := compactTargetSupplementLine(
		"likely target: a.go — run go test ./pkg -run '^TestA$'",
		"touch together: a.go, b.go — run go test ./pkg -run '^TestA$'",
	)
	if supp != "files edited there: b.go" {
		t.Fatalf("compactTargetSupplementLine = %q, want %q", supp, "files edited there: b.go")
	}
	idx := loadRepoSymbolMap(proj)
	callers := crossFileCallersFromIndexLine(proj, &idx, "cmd/app/release_notice.go", "ReleaseNoticeDisabled", "func ReleaseNoticeDisabled() bool")
	if callers != " — callers of ReleaseNoticeDisabled: cmd/app/notice_caller.go:3" {
		t.Fatalf("crossFileCallersFromIndexLine = %q", callers)
	}
}
