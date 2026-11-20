package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
)

func writeStage3ClaudeSubagent(t *testing.T, claudeRoot, parentID, agentID, cwd string, start time.Time, calls []struct {
	cmd     string
	isError bool
	out     string
}) {
	t.Helper()
	path := filepath.Join(claudeRoot, "-work-app", parentID, "subagents", agentID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for i, c := range calls {
		t1 := start.Add(time.Duration(i*2) * time.Minute).Format(time.RFC3339)
		t2 := start.Add(time.Duration(i*2+1) * time.Minute).Format(time.RFC3339)
		tid := fmt.Sprintf("tu-%s-%d", agentID, i)
		fmt.Fprintf(&sb, `{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":%q}}]}}`+"\n",
			agentID, t1, cwd, tid, c.cmd)
		fmt.Fprintf(&sb, `{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%q,"is_error":%v}]}}`+"\n",
			agentID, t2, cwd, tid, c.out, c.isError)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOrientAggregatesWorktreeSubagentVerifyAndDropsHighFailRate(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	t0 := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	// Two subagents running from different dot-worktree checkouts with viewer pipes.
	// Also run a >=50%-failing verify command across two sessions and a <50%-failing command across three.
	writeStage3ClaudeSubagent(t, claudeRoot, "p1", "sub-1", "/work/app/.worktrees/wt1", t0, []struct {
		cmd     string
		isError bool
		out     string
	}{
		{cmd: "cd /work/app/.worktrees/wt1 && go test ./pkg/... 2>&1 | tail -50", isError: false, out: "ok"},
		{cmd: "pytest -k flaky", isError: true, out: "Exit code 1\nfailed"},
		{cmd: "make lint", isError: false, out: "ok"},
	})
	writeStage3ClaudeSubagent(t, claudeRoot, "p2", "sub-2", "/work/app/.claude/worktrees/wt2", t0.Add(time.Hour), []struct {
		cmd     string
		isError bool
		out     string
	}{
		{cmd: "cd /work/app/.claude/worktrees/wt2 && go test ./pkg/... | head -40", isError: false, out: "ok"},
		{cmd: "pytest -k flaky", isError: false, out: "ok"},
		{cmd: "make lint", isError: true, out: "Exit code 2\nwarn"},
	})
	writeStage3ClaudeSubagent(t, claudeRoot, "p3", "sub-3", "/work/app", t0.Add(2*time.Hour), []struct {
		cmd     string
		isError bool
		out     string
	}{
		{cmd: "make lint", isError: false, out: "ok"},
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatalf("deja index: %v", err)
	}
	dir := index.DefaultDir()

	// mcpOrient should aggregate subagent worktree commands and drop >=50%-failing pytest -k flaky.
	raw, err := json.Marshal(map[string]string{"project": "work/app", "cwd": "/work/app"})
	if err != nil {
		t.Fatal(err)
	}
	orientOut, _, err := mcpOrient(dir, "orient", raw)
	if err != nil {
		t.Fatalf("mcpOrient: %v", err)
	}
	if !strings.Contains(orientOut, "go test ./pkg/...") || !strings.Contains(orientOut, "2 sessions") {
		t.Errorf("mcpOrient missing aggregated worktree verify command:\n%s", orientOut)
	}
	if !strings.Contains(orientOut, "make lint") {
		t.Errorf("mcpOrient dropped <50%% failing command:\n%s", orientOut)
	}
	if strings.Contains(orientOut, "pytest -k flaky") {
		t.Errorf("mcpOrient kept >=50%% failing command:\n%s", orientOut)
	}

	// orientDigestBlock should agree: worktree go test included, pytest -k flaky excluded.
	digest := orientDigestBlock(dir, "/work/app", []string{"work/app"}, policy.ActivationAuto)
	if !strings.Contains(digest, "go test ./pkg/...") || !strings.Contains(digest, "2 sessions") {
		t.Errorf("orientDigestBlock missing aggregated worktree verify command:\n%s", digest)
	}
	if !strings.Contains(digest, "make lint") {
		t.Errorf("orientDigestBlock dropped <50%% failing command:\n%s", digest)
	}
	if strings.Contains(digest, "pytest -k flaky") {
		t.Errorf("orientDigestBlock kept >=50%% failing command:\n%s", digest)
	}

	// projectXRayLine should surface a healthy recurring command, never the >=50% failing one.
	xray := projectXRayLine(dir, "/work/app")
	if (!strings.Contains(xray, "make lint") && !strings.Contains(xray, "go test ./pkg/...")) || strings.Contains(xray, "pytest -k flaky") {
		t.Errorf("projectXRayLine = %q", xray)
	}
}

func TestOrientPreservesEnvPrefixedVerifyCommandsAndFileHookMatchesLongOne(t *testing.T) {
	envCmd := `PATH="/opt/toolchain/custom/bin:$PATH" .venv/bin/pytest tests/integration/suite --maxfail=1`
	if len(envCmd) <= orientCommandMax || len(envCmd) > orientEnvCommandMax {
		t.Fatalf("test fixture assumption failed: len(envCmd)=%d", len(envCmd))
	}
	if got := orientCommand("cd /work/app && " + envCmd); got != envCmd {
		t.Errorf("orientCommand truncated env-prefixed command: got %q, want %q", got, envCmd)
	}
	plainLong := "go test ./... -run 'TestSomethingWithAVeryLongNameIndeed|TestAnotherOne' -count=1 -v"
	if got := orientCommand(plainLong); len([]rune(got)) > orientCommandMax+len("…") {
		t.Errorf("regular long command was not truncated: %q", got)
	}

	dir := t.TempDir()
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	metas := []index.SessionMeta{
		{Harness: "claude", ID: "m1", Updated: at},
		{Harness: "claude", ID: "m2", Updated: at.Add(time.Minute)},
	}
	ss := []model.Session{
		{
			ID: "m1", Harness: "claude", Project: "work/app", Updated: at,
			Messages: []model.Message{
				{Role: "command", Text: "$ " + envCmd + "  → exit 0", Time: at},
			},
		},
		{
			ID: "m2", Harness: "claude", Project: "work/app", Updated: at.Add(time.Minute),
			Messages: []model.Message{
				{Role: "command", Text: "$ " + envCmd + "  → exit 0", Time: at.Add(time.Minute)},
			},
		},
	}
	index.BuildSessionFactsForTest(dir, ss)
	got := fileHookRanLineFor(dir, "", metas)
	if !strings.Contains(got, ".venv/bin/pytest") || !strings.Contains(got, "passed") {
		t.Errorf("fileHookRanLineFor did not preserve long env-prefixed verify command: %q", got)
	}
}

func TestHowDemotesHighFailRateKeepsOnlyMatchAndDirectMatchesSetupPrefixes(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	t0 := time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)

	// Healthy pytest variant ran in 1 session, high-fail pytest variant ran in 2 sessions.
	writeStage3ClaudeSubagent(t, claudeRoot, "h1", "s-ok", "/work/app", t0, []struct {
		cmd     string
		isError bool
		out     string
	}{
		{cmd: "pytest tests/unit", isError: false, out: "ok"},
		{cmd: "pytest tests/broken", isError: true, out: "Exit code 1\nfail"},
		{cmd: "pytest tests/broken", isError: false, out: "ok"},
	})
	writeStage3ClaudeSubagent(t, claudeRoot, "h2", "s-bad", "/work/app", t0.Add(time.Hour), []struct {
		cmd     string
		isError bool
		out     string
	}{
		{cmd: "pytest tests/broken", isError: true, out: "Exit code 1\nfail"},
	})
	writeStage3ClaudeSubagent(t, claudeRoot, "h3", "s-only", "/work/app", t0.Add(2*time.Hour), []struct {
		cmd     string
		isError bool
		out     string
	}{
		{cmd: "cargo test --doc", isError: true, out: "Exit code 1\nfail"},
		{cmd: "cargo test --doc", isError: false, out: "ok"},
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatalf("deja index: %v", err)
	}
	dir := index.DefaultDir()

	// Healthy `pytest tests/unit` (1 session) must rank above high-fail `pytest tests/broken` (2 sessions, 2/3 fails).
	entries, _, _, _, err := howEntriesWithFallback(dir, []string{"pytest"}, []string{"work/app"}, false, policy.ActivationAuto)
	if err != nil {
		t.Fatalf("howEntriesWithFallback: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected >= 2 pytest entries, got %+v", entries)
	}
	if !strings.Contains(entries[0].Command, "pytest tests/unit") {
		t.Errorf("expected healthy command ranked first, got %+v", entries)
	}

	// Only match with >=50% fail rate must still be returned by `deja how`.
	out, err := captureRun(t, "how", "cargo test")
	if err != nil {
		t.Fatalf("deja how cargo test: %v", err)
	}
	if !strings.Contains(out, "cargo test --doc") {
		t.Errorf("deja how dropped only matching >=50%% failing command:\n%s", out)
	}

	// howDirectMatch skips leading cd, export, source, and dot setup segments.
	for _, cmd := range []string{
		"$ cd /work/app && pytest -q",
		"$ export PATH=/opt/bin:$PATH && pytest -q",
		"$ source .venv/bin/activate && pytest -q",
		"$ . .venv/bin/activate && pytest -q",
	} {
		if !howDirectMatch(cmd, []string{"pytest"}) {
			t.Errorf("howDirectMatch(%q, [pytest]) = false, want true", cmd)
		}
	}
}
