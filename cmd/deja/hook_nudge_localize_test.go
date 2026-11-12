package main

import (
	"bytes"
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
}
