package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func initPlanGuardGitRepo(t *testing.T, repo string) {
	t.Helper()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/billingapp\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "internal", "billing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "billing", "invoice.go"), []byte("package billing\n\nfunc RoundInvoice(cents int64) int64 { return cents }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "billing", "invoice_test.go"), []byte("package billing\n\nimport \"testing\"\n\nfunc TestInvoiceRoundingCents(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		dir := filepath.Join(repo, "pkg", fmt.Sprintf("hot%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package hot\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package hot\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("commit", "-qm", "seed repo")
}

func TestPlanGuardFindingsSurfaceDirectCoupledFileAndBreakageGuardrail(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "missing-policy.json"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")

	repo := filepath.Join(tmp, "work", "billingapp")
	initPlanGuardGitRepo(t, repo)

	base := time.Now().Add(-48 * time.Hour).UTC()
	// Six hotter unrelated pairs (5 sessions each) saturate orient's top-5 list
	// so internal/billing/invoice.go + invoice_test.go (4 sessions) must be
	// discovered directly from the file named in the plan.
	seq := 0
	for p := range 6 {
		fileA := filepath.Join(repo, "pkg", fmt.Sprintf("hot%d", p), "a.go")
		fileB := filepath.Join(repo, "pkg", fmt.Sprintf("hot%d", p), "b.go")
		for j := range 5 {
			seq++
			id := fmt.Sprintf("hot-%d-%d-%d", p, j, seq)
			ts := base.Add(time.Duration(seq) * time.Minute).Format(time.RFC3339)
			writeClaudeFixture(t, filepath.Join(root, "billingapp", id+".jsonl"), id, []string{
				fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"update hot pair"}}`, id, repo, ts),
				fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"a","new_string":"b"}},{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"a","new_string":"b"}}]}}`, id, repo, ts, fileA, fileB),
			})
		}
	}

	invFile := filepath.Join(repo, "internal", "billing", "invoice.go")
	invTest := filepath.Join(repo, "internal", "billing", "invoice_test.go")
	for i := range 4 {
		seq++
		id := fmt.Sprintf("inv-%d", i)
		ts1 := base.Add(time.Duration(seq*3) * time.Minute).Format(time.RFC3339)
		ts2 := base.Add(time.Duration(seq*3+1) * time.Minute).Format(time.RFC3339)
		ts3 := base.Add(time.Duration(seq*3+2) * time.Minute).Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(root, "billingapp", id+".jsonl"), id, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"adjust invoice rounding"}}`, id, repo, ts1),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test ./internal/billing"}}]}}`, id, repo, ts1),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"--- FAIL: TestInvoiceRoundingCents (0.01s)\nFAIL\texample.com/billingapp/internal/billing\t0.02s"}]}}`, id, repo, ts2),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"cents","new_string":"cents"}},{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"func","new_string":"func"}}]}}`, id, repo, ts3, invFile, invTest),
		})
	}

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	findings := planFindingsIn(dir, repo, "1. Update internal/billing/invoice.go rounding logic\n", "live-plan")
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "invoice_test.go") || !strings.Contains(joined, "invoice.go") {
		t.Fatalf("missing direct coupled-file warning for invoice_test.go:\n%s", joined)
	}
	if !strings.Contains(joined, "TestInvoiceRoundingCents") {
		t.Fatalf("missing per-file guardrail (TestInvoiceRoundingCents breakage) for invoice.go:\n%s", joined)
	}

	withBoth := planFindingsIn(dir, repo, "1. Update internal/billing/invoice.go and internal/billing/invoice_test.go\n", "live-plan")
	if strings.Contains(strings.Join(withBoth, "\n"), "coupled files:") {
		t.Fatalf("coupled-file warning fired even though invoice_test.go is in the plan:\n%s", strings.Join(withBoth, "\n"))
	}
}

func TestPlanGuardScopesProjectAndRespectsPolicyIgnore(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	polPath := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(polPath, []byte(`{"ignore":["*/ignored-dir/*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", polPath)
	root := os.Getenv("DEJA_CLAUDE_ROOT")

	repo := filepath.Join(tmp, "work", "myrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "git", "-C", repo, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/myrepo\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app_helper.go"), []byte("package main\nfunc helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	otherRepo := filepath.Join(tmp, "work", "unrelated-other-repo")
	if err := os.MkdirAll(otherRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	// Forty benign sessions in myrepo satisfy plan IDF floors so only project
	// scoping and policy.Ignore stand between the walls below and plan findings.
	for i := range 40 {
		id := fmt.Sprintf("myrepo-bg-%d", i)
		writeClaudeFixture(t, filepath.Join(root, "myrepo", id+".jsonl"), id, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"bootstrap handler"}}`, id, repo, old),
		})
	}

	// Four sessions in an unrelated repo hitting a wall on pytest_asyncio.
	for i := range 4 {
		id := fmt.Sprintf("other-%d", i)
		toolID := fmt.Sprintf("t-other-%d", i)
		writeClaudeFixture(t, filepath.Join(root, "unrelated-other-repo", id+".jsonl"), id, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"run pytest_asyncio migration"}}`, id, otherRepo, old),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"pip install pytest_asyncio"}}]}}`, id, otherRepo, old, toolID),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"ModuleNotFoundError: No module named 'pytest_asyncio'"}]}}`, id, otherRepo, old, toolID),
		})
	}

	// Four ignored sessions inside myrepo hitting a fatal wall on legacy_token_bug
	// and co-editing app.go + app_helper.go.
	appFile := filepath.Join(repo, "app.go")
	helperFile := filepath.Join(repo, "app_helper.go")
	for i := range 4 {
		id := fmt.Sprintf("ign-%d", i)
		toolID := fmt.Sprintf("t-ign-%d", i)
		writeClaudeFixture(t, filepath.Join(root, "ignored-dir", id+".jsonl"), id, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"debug legacy_token_bug timeout"}}`, id, repo, old),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"go test ./..."}}]}}`, id, repo, old, toolID),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":true,"content":"fatal: legacy_token_bug verification aborted after retries\n--- FAIL: TestLegacyTokenGuard (0.01s)"}]}}`, id, repo, old, toolID),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"main","new_string":"main"}},{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"helper","new_string":"helper"}}]}}`, id, repo, old, appFile, helperFile),
		})
	}

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	if out := planFindingsIn(dir, repo, "1. Run pytest_asyncio migration\n", ""); len(out) != 0 {
		t.Fatalf("unrelated project friction leaked across repo scope: %+v", out)
	}
	if out := planFindingsIn(dir, repo, "1. Update app.go for legacy_token_bug timeout\n", ""); len(out) != 0 {
		t.Fatalf("policy-ignored session friction/files leaked into plan findings: %+v", out)
	}
}

func TestPlanGuardSingularGrammarAndPlanFileFallback(t *testing.T) {
	line := formatPlanFinding(index.PlanCooccurrence{
		Wall: index.Friction{
			Text:     "connection refused fetching relay_worker state",
			Sessions: []index.SessionMeta{{ID: "s1"}, {ID: "s2"}, {ID: "s3"}},
		},
		Command:         "go test ./internal/relay",
		CommandSessions: []index.SessionMeta{{ID: "s1"}},
	})
	if !strings.Contains(line, "(1 session)") || strings.Contains(line, "(1 sessions)") {
		t.Fatalf("expected singular '(1 session)', got %q", line)
	}

	tmp := t.TempDir()
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "missing-policy.json"))
	planPath := filepath.Join(tmp, "plan.md")
	if err := os.WriteFile(planPath, []byte("1. Upgrade pytest_asyncio fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withPlanLookup(t, []index.PlanCooccurrence{{
		Wall: index.Friction{
			Text:     "error: pytest_asyncio event loop mismatch during teardown",
			Sessions: []index.SessionMeta{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}},
		},
		Command:         "pip install pytest_asyncio",
		CommandSessions: []index.SessionMeta{{ID: "p1"}},
	}})

	payload := mustPlanJSON(t, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "ExitPlanMode",
		"cwd":             tmp,
		"tool_input": map[string]any{
			"planFilePath": "plan.md",
		},
	})
	var out bytes.Buffer
	if err := runHookPlan(tmp, bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pytest_asyncio") || !strings.Contains(out.String(), "(1 session)") {
		t.Fatalf("expected planFilePath fallback finding with '(1 session)', got %q", out.String())
	}

	bigPath := filepath.Join(tmp, "huge.md")
	if err := os.WriteFile(bigPath, bytes.Repeat([]byte("pytest_asyncio "), 6000), 0o600); err != nil {
		t.Fatal(err)
	}
	bigPayload := mustPlanJSON(t, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "ExitPlanMode",
		"cwd":             tmp,
		"tool_input": map[string]any{
			"plan_file_path": bigPath,
		},
	})
	out.Reset()
	if err := runHookPlan(tmp, bytes.NewReader(bigPayload), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("oversized plan file >64KB unexpectedly emitted output: %q", out.String())
	}
}

func TestPlanGuardHookPlanModeDialectsAndEnvelopes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "missing-policy.json"))
	withPlanLookup(t, []index.PlanCooccurrence{{
		Wall: index.Friction{
			Text:     "pytest_asyncio fixture loop closed prematurely",
			Sessions: []index.SessionMeta{{ID: "s1"}, {ID: "s2"}, {ID: "s3"}},
		},
		Command:         "pip install pytest_asyncio",
		CommandSessions: []index.SessionMeta{{ID: "s1"}},
	}})

	t.Run("claude single-key json", func(t *testing.T) {
		payload := `{"hook_event_name":"PreToolUse","tool_name":"ExitPlanMode","tool_input":{"plan":"1. Upgrade pytest_asyncio fixture"}}`
		var out bytes.Buffer
		if err := runHookPlanMode(tmp, strings.NewReader(payload), &out, hookToolClaude); err != nil {
			t.Fatal(err)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &top); err != nil {
			t.Fatalf("invalid claude json %q: %v", out.String(), err)
		}
		if len(top) != 1 || top["hookSpecificOutput"] == nil {
			t.Fatalf("claude PreToolUse must keep 1-key hookSpecificOutput contract, got keys %v", top)
		}
		if strings.Contains(out.String(), "permissionDecision") {
			t.Fatalf("unexpected permissionDecision in claude output: %s", out.String())
		}
		if !strings.Contains(out.String(), "pytest_asyncio") || !strings.Contains(out.String(), "(1 session)") {
			t.Fatalf("missing finding or singular session count: %s", out.String())
		}
	})

	t.Run("plain with grok camelCase", func(t *testing.T) {
		payload := `{"hookEventName":"PreToolUse","toolName":"exit_plan_mode","toolInput":{"plan":"1. Upgrade pytest_asyncio fixture"},"sessionId":"","workspaceRoot":"` + filepath.ToSlash(tmp) + `"}`
		var out bytes.Buffer
		if err := runHookPlanMode(tmp, strings.NewReader(payload), &out, hookToolPlain); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(strings.TrimSpace(out.String()), "{") || !strings.Contains(out.String(), "pytest_asyncio") {
			t.Fatalf("expected plain framed recall, got %q", out.String())
		}
	})

	t.Run("crush context json", func(t *testing.T) {
		payload := `{"tool_name":"submit_plan","tool_input":{"content":"1. Upgrade pytest_asyncio fixture"}}`
		var out bytes.Buffer
		if err := runHookPlanMode(tmp, strings.NewReader(payload), &out, hookToolCrush); err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Context string `json:"context"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil || !strings.Contains(resp.Context, "pytest_asyncio") {
			t.Fatalf("unexpected crush envelope %q (err=%v)", out.String(), err)
		}
	})

	t.Run("copilot stringified toolArgs", func(t *testing.T) {
		payload := `{"toolName":"finalize_plan","toolArgs":"{\"plan\":\"1. Upgrade pytest_asyncio fixture\"}"}`
		var out bytes.Buffer
		if err := runHookPlanMode(tmp, strings.NewReader(payload), &out, hookToolCopilot); err != nil {
			t.Fatal(err)
		}
		var resp struct {
			AdditionalContext string `json:"additionalContext"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil || !strings.Contains(resp.AdditionalContext, "pytest_asyncio") {
			t.Fatalf("unexpected copilot envelope %q (err=%v)", out.String(), err)
		}
		if strings.Contains(out.String(), "permissionDecision") {
			t.Fatalf("unexpected permissionDecision in copilot envelope: %s", out.String())
		}
	})

	t.Run("mcode llm_tool_name summary", func(t *testing.T) {
		payload := `{"llm_tool_name":"plan_exit","toolInput":{"summary":"1. Upgrade pytest_asyncio fixture"}}`
		var out bytes.Buffer
		if err := runHookPlanMode(tmp, strings.NewReader(payload), &out, hookToolMcode); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"hookSpecificOutput"`) || !strings.Contains(out.String(), "pytest_asyncio") {
			t.Fatalf("unexpected mcode envelope: %s", out.String())
		}
	})
}

func TestPlanGuardHookToolDelegatesPlanTools(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "missing-policy.json"))
	withPlanLookup(t, []index.PlanCooccurrence{{
		Wall: index.Friction{
			Text:     "pytest_asyncio fixture loop closed prematurely",
			Sessions: []index.SessionMeta{{ID: "s1"}, {ID: "s2"}, {ID: "s3"}},
		},
		Command:         "pip install pytest_asyncio",
		CommandSessions: []index.SessionMeta{{ID: "s1"}},
	}})

	t.Run("claude ExitPlanMode", func(t *testing.T) {
		payload := `{"hook_event_name":"PreToolUse","tool_name":"ExitPlanMode","tool_input":{"plan":"1. Upgrade pytest_asyncio fixture"}}`
		var out bytes.Buffer
		if err := runHookToolMode(tmp, strings.NewReader(payload), &out, hookToolClaude); err != nil {
			t.Fatal(err)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &top); err != nil {
			t.Fatalf("hook-tool claude ExitPlanMode output %q invalid JSON: %v", out.String(), err)
		}
		if len(top) != 1 || top["hookSpecificOutput"] == nil {
			t.Fatalf("expected 1-key hookSpecificOutput JSON, got %q", out.String())
		}
		if strings.Contains(out.String(), "permissionDecision") {
			t.Fatalf("unexpected permissionDecision in hook-tool ExitPlanMode output: %s", out.String())
		}
		if !strings.Contains(out.String(), "pytest_asyncio") || !strings.Contains(out.String(), "(1 session)") {
			t.Fatalf("missing plan finding in hook-tool output: %s", out.String())
		}
	})

	t.Run("copilot exit_plan_mode", func(t *testing.T) {
		payload := `{"toolName":"exit_plan_mode","toolArgs":"{\"plan\":\"1. Upgrade pytest_asyncio fixture\"}"}`
		var out bytes.Buffer
		if err := runHookToolMode(tmp, strings.NewReader(payload), &out, hookToolCopilot); err != nil {
			t.Fatal(err)
		}
		var resp struct {
			AdditionalContext string `json:"additionalContext"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil || !strings.Contains(resp.AdditionalContext, "pytest_asyncio") {
			t.Fatalf("expected copilot additionalContext with plan finding, got %q (err=%v)", out.String(), err)
		}
	})
}

func TestPlanGuardAntigravityPlanStepRidesNextInvocation(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "missing-policy.json"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")

	repo := filepath.Join(tmp, "work", "billingapp")
	initPlanGuardGitRepo(t, repo)

	base := time.Now().Add(-24 * time.Hour).UTC()
	invFile := filepath.Join(repo, "internal", "billing", "invoice.go")
	invTest := filepath.Join(repo, "internal", "billing", "invoice_test.go")
	for i := range 4 {
		id := fmt.Sprintf("ag-inv-%d", i)
		ts := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(root, "billingapp", id+".jsonl"), id, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"update invoice rounding"}}`, id, repo, ts),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"cents","new_string":"cents"}},{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"func","new_string":"func"}}]}}`, id, repo, ts, invFile, invTest),
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	transcript := filepath.Join(tmp, "ag-transcript.jsonl")
	lines := []string{
		`{"type":"USER_INPUT","created_at":"2026-11-22T10:00:00Z","content":"<USER_REQUEST>\nUpdate invoice rounding\n</USER_REQUEST>"}`,
		`{"type":"PLANNER_RESPONSE","created_at":"2026-11-22T10:00:05Z","content":"Submitting plan","tool_calls":[{"name":"exit_plan_mode","args":{"plan":"1. Update internal/billing/invoice.go rounding logic\n"}}]}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	payload := mustPlanJSON(t, map[string]any{
		"invocationNum":  1,
		"workspacePaths": []string{repo},
		"transcriptPath": transcript,
		"conversationId": "ag-plan-conv-1",
	})
	var firstOut bytes.Buffer
	if err := runHookAntigravity(dir, bytes.NewReader(payload), &firstOut); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		InjectSteps []struct {
			EphemeralMessage string `json:"ephemeralMessage"`
		} `json:"injectSteps"`
	}
	if err := json.Unmarshal(firstOut.Bytes(), &resp); err != nil {
		t.Fatalf("bad antigravity JSON %q: %v", firstOut.String(), err)
	}
	var joined strings.Builder
	for _, s := range resp.InjectSteps {
		joined.WriteString(s.EphemeralMessage)
		joined.WriteByte('\n')
	}
	if !strings.Contains(joined.String(), "invoice_test.go") {
		t.Fatalf("expected antigravity injectSteps to carry coupled-file plan warning for invoice_test.go, got:\n%s", firstOut.String())
	}

	var secondOut bytes.Buffer
	if err := runHookAntigravity(dir, bytes.NewReader(payload), &secondOut); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(secondOut.String(), "coupled files:") {
		t.Fatalf("repeated invocation duplicated plan finding: %s", secondOut.String())
	}
}

func TestPlanGuardInstallersWireExitPlanModeWithoutTouchingCodex(t *testing.T) {
	foundClaude := false
	for _, w := range claudeHookWiring {
		if w.Event == "PreToolUse" && w.Sub == "hook-tool" {
			foundClaude = true
			if !strings.Contains(w.Matcher, "ExitPlanMode") {
				t.Fatalf("claude PreToolUse hook-tool matcher missing ExitPlanMode: %q", w.Matcher)
			}
		}
	}
	if !foundClaude {
		t.Fatal("claude PreToolUse hook-tool wiring not found")
	}

	pluginBytes, err := os.ReadFile(filepath.Join("..", "..", "claude-plugin", ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pluginBytes), "ExitPlanMode") {
		t.Fatalf("claude-plugin plugin.json missing ExitPlanMode matcher")
	}

	t.Setenv("HOME", t.TempDir())
	_, _, grokNext, _, err := grokHooksUpdate("/usr/local/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(grokNext), "ExitPlanMode") {
		t.Fatalf("grokHooksUpdate PreToolUse matcher missing ExitPlanMode:\n%s", grokNext)
	}

	grokManifest, err := os.ReadFile(filepath.Join("..", "..", "extensions", "grok", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(grokManifest), "ExitPlanMode") {
		t.Fatalf("extensions/grok/hooks/hooks.json missing ExitPlanMode matcher")
	}

	for _, w := range codexHookWiring {
		if w.Event == "PreToolUse" {
			if w.Matcher != "Bash|apply_patch" || strings.Contains(w.Matcher, "ExitPlanMode") {
				t.Fatalf("codex PreToolUse matcher must remain Bash|apply_patch, got %q", w.Matcher)
			}
		}
	}
}

func mustPlanJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
