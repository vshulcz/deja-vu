package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func writeClaudeFixSession(t *testing.T, claudeRoot, projSlug, cwd, sid, failCmd, errText, fixCmd, fixOut string, offsetMin int) {
	t.Helper()
	dir := filepath.Join(claudeRoot, projSlug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ts := func(delta int) string {
		return now.Add(-time.Duration(offsetMin-delta) * time.Minute).Format(time.RFC3339)
	}
	rec := func(role string, content any, delta int) string {
		b := mustMarshal(t, map[string]any{
			"type":      role,
			"sessionId": sid,
			"cwd":       cwd,
			"timestamp": ts(delta),
			"message":   map[string]any{"role": role, "content": content},
		})
		return string(b)
	}
	lines := []string{
		rec("user", "fix failing step in "+filepath.Base(cwd), 0),
		rec("assistant", []any{map[string]any{
			"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": failCmd},
		}}, 1),
		rec("user", []any{map[string]any{
			"type": "tool_result", "tool_use_id": "t1", "is_error": true, "content": errText,
		}}, 2),
		rec("assistant", []any{map[string]any{
			"type": "tool_use", "id": "t2", "name": "Bash", "input": map[string]any{"command": fixCmd},
		}}, 3),
		rec("user", []any{map[string]any{
			"type": "tool_result", "tool_use_id": "t2", "is_error": false, "content": fixOut,
		}}, 4),
	}
	if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runPostToolOnce(t *testing.T, dir, sessionID, cwd, cmd, stderr, stdout string, isErr bool) string {
	t.Helper()
	payload := map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": cmd},
		"tool_response":   map[string]any{"stdout": stdout, "stderr": stderr},
		"session_id":      sessionID,
		"cwd":             cwd,
	}
	if isErr {
		payload["hook_event_name"] = "PostToolUseFailure"
		payload["tool_result_is_error"] = true
	}
	var out bytes.Buffer
	if err := runHookToolAfter(dir, bytes.NewReader(mustMarshal(t, payload)), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// Stage 4: Candidate pairs (single-session sightings) are excluded from every
// auto-hook surface (post-tool, near-error probe, and pre-tool trap warning),
// while remaining available on explicit `deja fix` CLI and MCP `fix` queries.
func TestSniperGateDropsCandidatesFromAutoHooksAndKeepsExplicitFix(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	const exactErr = "dial tcp 203.0.113.10:5432: connect: connection refused"
	const nearRecordedErr = "pkg/billing.go:42: undefined: missingLedgerHelper"
	const nearProbeErr = "pkg/billing.go:99: undefined: missingLedgerHelper"

	writeClaudeFixSession(t, claude, "-work-app", "/work/app", "cand-exact",
		"make migrate-app", exactErr, "docker compose up -d db", "started", 120)
	writeClaudeFixSession(t, claude, "-work-app", "/work/app", "cand-near",
		"go test ./...", nearRecordedErr, "glimwraxctl --sync && go test ./...", "ok", 90)

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Post-tool exact match stays silent on both Strike 1 and Strike 2.
	for strike := 1; strike <= 2; strike++ {
		if got := runPostToolOnce(t, dir, "s-cand-exact", "/work/app", "make migrate-app", exactErr, "", true); got != "" {
			t.Fatalf("strike %d leaked Candidate exact fix into hook-tool-after:\n%s", strike, got)
		}
		if got := runPostToolOnce(t, dir, "s-cand-near", "/work/app", "go test ./...", nearProbeErr, "", true); got != "" {
			t.Fatalf("strike %d leaked Candidate near-probe fix into hook-tool-after:\n%s", strike, got)
		}
	}

	// Pre-tool trap warning stays silent for Candidate fixes.
	if trap := confirmedCommandTrapLine(dir, "/work/app", "make migrate-app"); trap != "" {
		t.Fatalf("confirmedCommandTrapLine leaked Candidate fix:\n%s", trap)
	}
	prePayload := mustMarshal(t, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "make migrate-app"},
		"cwd":             "/work/app",
		"session_id":      "s-cand-pre",
	})
	var preOut bytes.Buffer
	if err := runHookTool(dir, bytes.NewReader(prePayload), &preOut); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preOut.String(), "docker compose up -d db") {
		t.Fatalf("hook-tool leaked Candidate remedy:\n%s", preOut.String())
	}

	// Explicit `deja fix` CLI and MCP `fix` still surface the Candidate with unconfirmed framing.
	cliOut, err := captureRun(t, "fix", exactErr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cliOut, "unconfirmed") || !strings.Contains(cliOut, "docker compose up -d db") {
		t.Fatalf("explicit `deja fix` dropped Candidate pair:\n%s", cliOut)
	}
	mcpOut, _, merr := mcpFix(dir, "fix", mustMarshal(t, map[string]any{"error": exactErr}))
	if merr != nil {
		t.Fatal(merr)
	}
	if !strings.Contains(mcpOut, "unconfirmed") || !strings.Contains(mcpOut, "docker compose up -d db") {
		t.Fatalf("explicit MCP `fix` dropped Candidate pair:\n%s", mcpOut)
	}
}

// Stage 4: Auto-hooks strictly scope fixes to the current project — even for
// MachineFact (`command not found`) and IsShellDiagnostic errors — and stay
// silent for empty cwd or $HOME, while explicit `deja fix` / MCP `fix` still
// answer across projects.
func TestSniperGateStrictSameProjectScopingInAutoHooks(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	setTestHome(t, home)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	const ordinaryErr = "dial tcp 203.0.113.10:5432: connect: connection refused"
	const missingErr = "zsh:1: command not found: quibtool"
	const shellDiagErr = "(eval):1: no matches found: --include=*.py"

	for k := range 2 {
		sid := fmt.Sprintf("alpha-%d", k)
		writeClaudeFixSession(t, claude, "-work-alpha", "/work/alpha", sid+"-ord",
			"make migrate-alpha", ordinaryErr, "docker compose up -d alpha-db", "ok", 200-10*k)
		writeClaudeFixSession(t, claude, "-work-alpha", "/work/alpha", sid+"-miss",
			"quibtool check ./...", missingErr, "make check-alpha", "ok", 180-10*k)
		writeClaudeFixSession(t, claude, "-work-alpha", "/work/alpha", sid+"-sh",
			"git grep --include=*.py pat", shellDiagErr, "git grep --include='*.py' pat", "a.py:1:pat", 160-10*k)
	}

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Same project answers (Strike 1 for detEnv missing binary / shell diag; Strike 2 for ordinary).
	if got := runPostToolOnce(t, dir, "a-det-1", "/work/alpha", "quibtool check ./...", missingErr, "", true); !strings.Contains(got, "make check-alpha") {
		t.Fatalf("same-project MachineFact missing binary failed on Strike 1:\n%s", got)
	}
	if got := runPostToolOnce(t, dir, "a-det-2", "/work/alpha", "git grep --include=*.py pat", shellDiagErr, "", true); !strings.Contains(got, "git grep --include='*.py' pat") {
		t.Fatalf("same-project IsShellDiagnostic failed on Strike 1:\n%s", got)
	}

	// Cross-project (/work/beta), empty cwd, and $HOME stay silent in auto-hooks even at Strike 2.
	for _, foreignCWD := range []string{"/work/beta", "", home} {
		label := foreignCWD
		switch label {
		case "":
			label = "<empty>"
		case home:
			label = "$HOME"
		}
		for _, tc := range []struct {
			name string
			cmd  string
			err  string
		}{
			{"ordinary", "make migrate-alpha", ordinaryErr},
			{"missing-bin", "quibtool check ./...", missingErr},
			{"shell-diag", "git grep --include=*.py pat", shellDiagErr},
		} {
			for strike := 1; strike <= 2; strike++ {
				if line := fixPairLineForStreak(dir, foreignCWD, tc.cmd, tc.err, 2); line != "" {
					t.Fatalf("fixPairLineForStreak(%s, %s) leaked cross-project pair:\n%s", label, tc.name, line)
				}
				if got := runPostToolOnce(t, dir, "foreign-"+tc.name+"-"+label, foreignCWD, tc.cmd, tc.err, "", true); got != "" {
					t.Fatalf("hook-tool-after(%s, %s) strike %d leaked pair:\n%s", label, tc.name, strike, got)
				}
			}
		}
	}

	// missingProgramLineWithRemedy only appends a next-command remedy in the same project.
	sameMiss := missingProgramLineWithRemedy(dir, "/work/alpha", "quibtool check ./...")
	if !strings.Contains(sameMiss, "quibtool is not on this machine") || !strings.Contains(sameMiss, "next command that worked: make check-alpha") {
		t.Fatalf("same-project missingProgramLineWithRemedy missing expected warning/remedy: %q", sameMiss)
	}
	for _, foreignCWD := range []string{"/work/beta", home, ""} {
		line := missingProgramLineWithRemedy(dir, foreignCWD, "quibtool check ./...")
		if strings.Contains(line, "make check-alpha") || strings.Contains(line, "next command that worked") {
			t.Fatalf("missingProgramLineWithRemedy(%q) leaked cross-project remedy: %q", foreignCWD, line)
		}
	}

	// Explicit `deja fix` CLI and MCP `fix` still answer across projects and from $HOME.
	t.Chdir(home)
	cliOut, err := captureRun(t, "fix", ordinaryErr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cliOut, "docker compose up -d alpha-db") {
		t.Fatalf("explicit `deja fix` from $HOME dropped cross-project pair:\n%s", cliOut)
	}
	mcpOut, _, merr := mcpFix(dir, "fix", mustMarshal(t, map[string]any{"error": shellDiagErr}))
	if merr != nil {
		t.Fatal(merr)
	}
	if !strings.Contains(mcpOut, "git grep --include='*.py' pat") {
		t.Fatalf("explicit MCP `fix` dropped cross-project shell diagnostic pair:\n%s", mcpOut)
	}
}

// Stage 4: Non-detEnv confirmed pairs obey the 2-strike gate and reset their
// consecutive failure counter whenever a command succeeds (PostToolUse exit 0
// or `deja shell postexec 0`).
func TestSniperGateTwoStrikeAndCleanReset(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	dir := os.Getenv("DEJA_INDEX_DIR")

	const errOut = "goroutine 1 [running]:\npanic: sql: database is closed"

	// PostToolUse flow: Strike 1 silent -> clean command resets -> next failure silent -> consecutive failure delivers.
	if got := runPostToolOnce(t, dir, "s-streak", "/work/app", "make build", errOut, "", false); got != "" {
		t.Fatalf("strike 1 delivered prematurely:\n%s", got)
	}
	// Clean command (no error, clean output) resets FailStreak.
	if got := runPostToolOnce(t, dir, "s-streak", "/work/app", "go version", "", "go version go1.24", false); got != "" {
		t.Fatalf("clean command unexpectedly emitted hook output:\n%s", got)
	}
	if got := runPostToolOnce(t, dir, "s-streak", "/work/app", "make build", errOut, "", false); got != "" {
		t.Fatalf("failure after reset was treated as strike 2:\n%s", got)
	}
	got2 := runPostToolOnce(t, dir, "s-streak", "/work/app", "make build", errOut, "", false)
	if !strings.Contains(got2, "make clean") || !strings.Contains(got2, "CGO_ENABLED=0") {
		t.Fatalf("consecutive strike 2 did not deliver fix pair:\n%s", got2)
	}
	// Strike 3 is deduped per session.
	if got3 := runPostToolOnce(t, dir, "s-streak", "/work/app", "make build", errOut, "", false); got3 != "" {
		t.Fatalf("strike 3 was not deduped:\n%s", got3)
	}

	// Shell hook postexec flow: Strike 1 silent -> postexec exit 0 resets -> Strike 1 silent -> Strike 2 prints.
	t.Setenv("CLAUDE_PROJECT_DIR", "/work/app")
	var out, errBuf bytes.Buffer
	if err := runShell(dir, []string{"postexec", "sh-streak", "2", "make", errOut}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), "CGO_ENABLED=0") {
		t.Fatalf("shell postexec strike 1 printed fix prematurely: %q", errBuf.String())
	}
	out.Reset()
	errBuf.Reset()
	if err := runShell(dir, []string{"postexec", "sh-streak", "0", "true", "ok"}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errBuf.Reset()
	if err := runShell(dir, []string{"postexec", "sh-streak", "2", "make", errOut}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), "CGO_ENABLED=0") {
		t.Fatalf("shell postexec after exit 0 reset was treated as strike 2: %q", errBuf.String())
	}
	out.Reset()
	errBuf.Reset()
	if err := runShell(dir, []string{"postexec", "sh-streak", "2", "make", errOut}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "make clean && make CGO_ENABLED=0") {
		t.Fatalf("shell postexec strike 2 did not deliver fix pair: %q", errBuf.String())
	}
}

// Stage 4: First-strike bypass fires immediately in the same project for
// Repaired, Substitute, IsShellDiagnostic, missing binary/module, unknown flag,
// usage errors, and auth/permission errors.
func TestSniperGateFirstStrikeBypass(t *testing.T) {
	// Unit contract for fixPairAllowedOnFirstStrike.
	if !fixPairAllowedOnFirstStrike(index.FixPair{Repaired: true}, "arbitrary build error") {
		t.Error("expected Repaired pair to bypass first-strike gate")
	}
	if !fixPairAllowedOnFirstStrike(index.FixPair{Substitute: true}, "arbitrary build error") {
		t.Error("expected Substitute pair to bypass first-strike gate")
	}
	for _, out := range []string{
		"(eval):1: no matches found: *.py",
		"zsh:1: command not found: shellcheck",
		"ModuleNotFoundError: No module named 'pydantic'",
		"Error: Cannot find module 'zod'",
		"unknown flag: --frobnicate",
		"Usage: mytool [flags]",
		"open /etc/secret: permission denied",
		"HTTP 401 Unauthorized: API key missing",
		"auth token expired, re-login required",
	} {
		if !fixPairAllowedOnFirstStrike(index.FixPair{}, out) {
			t.Errorf("expected first-strike bypass for deterministic/env failure: %q", out)
		}
	}
	if fixPairAllowedOnFirstStrike(index.FixPair{}, "panic: sql: database is closed") {
		t.Error("unexpected first-strike bypass for ordinary non-detEnv panic")
	}

	// End-to-end: Repaired pair + missing binary + unknown flag deliver on Strike 1 in same project.
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	for k := range 2 {
		sid := fmt.Sprintf("rep-%d", k)
		if k == 0 {
			writeClaudeFixSession(t, claude, "-work-app", "/work/app", sid+"-rep",
				"go test -tags=badflag ./pkg/orders ./pkg/billing", "fatal: build tag badflag rejected by workspace config",
				"go test -tags=goodflag ./pkg/orders ./pkg/billing", "PASS", 150-10*k)
		}
		writeClaudeFixSession(t, claude, "-work-app", "/work/app", sid+"-flag",
			"lintctl --bogus ./...", "fatal: unknown flag: --bogus",
			"make lint-fix && true", "clean", 130-10*k)
		writeClaudeFixSession(t, claude, "-work-app", "/work/app", sid+"-perm",
			"docker ps", "permission denied while trying to connect to the Docker daemon socket",
			"make docker-login", "ok", 110-10*k)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	if got := runPostToolOnce(t, dir, "s1-rep", "/work/app", "go test -tags=badflag ./pkg/orders ./pkg/billing", "fatal: build tag badflag rejected by workspace config", "", true); !strings.Contains(got, "go test -tags=goodflag ./pkg/orders ./pkg/billing") {
		t.Fatalf("Repaired pair did not bypass first-strike gate:\n%s", got)
	}
	if got := runPostToolOnce(t, dir, "s1-flag", "/work/app", "lintctl --bogus ./...", "fatal: unknown flag: --bogus", "", true); !strings.Contains(got, "make lint-fix") {
		t.Fatalf("unknown flag detEnv did not bypass first-strike gate:\n%s", got)
	}
	if got := runPostToolOnce(t, dir, "s1-perm", "/work/app", "docker ps", "permission denied while trying to connect to the Docker daemon socket", "", true); !strings.Contains(got, "make docker-login") {
		t.Fatalf("permission denied detEnv did not bypass first-strike gate:\n%s", got)
	}
}

// Stage 4: Multi-line command output containing a failure (>200 bytes) must
// trigger coordHasFriction so it increments FailStreak and is not recorded as
// a passing VerifyCmd in .coord.
func TestSniperGateMultilineCoordHasFrictionAndVerifySuppression(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	multiFail := strings.Repeat("build step: compiling module components safely without issues\n", 5) +
		"FAIL: TestOrderSettlement unexpected timeout after 30s\n" +
		"FAIL\tgithub.com/example/app/orders\t30.012s\n"
	if len(multiFail) <= 220 {
		t.Fatalf("test fixture too short (%d bytes)", len(multiFail))
	}
	if !coordHasFriction(multiFail) {
		t.Fatalf("coordHasFriction missed multi-line error >200 bytes")
	}

	rawFail := mustMarshal(t, map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "go test ./..."},
		"tool_response":   map[string]any{"stdout": multiFail},
		"session_id":      "ml-sess",
		"cwd":             repo,
	})
	streak := coordRecordAfterCommand(dir, repo, "go test ./...", multiFail, rawFail, "ml-sess")
	if streak != 1 {
		t.Fatalf("expected streak=1 on multi-line failure, got %d", streak)
	}
	st := loadCoordState(dir)
	if sess := st.Sessions["ml-sess"]; sess != nil && sess.VerifyCmd != "" {
		t.Fatalf("failed multi-line test output was falsely recorded as VerifyCmd=%q", sess.VerifyCmd)
	}

	// A genuinely clean test run clears FailStreak and records VerifyCmd.
	cleanOut := "ok  \tgithub.com/example/app/orders\t0.042s\n"
	rawClean := mustMarshal(t, map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "go test ./..."},
		"tool_response":   map[string]any{"stdout": cleanOut},
		"session_id":      "ml-sess",
		"cwd":             repo,
	})
	if streak = coordRecordAfterCommand(dir, repo, "go test ./...", cleanOut, rawClean, "ml-sess"); streak != 0 {
		t.Fatalf("expected clean run to reset streak to 0, got %d", streak)
	}
	st = loadCoordState(dir)
	if sess := st.Sessions["ml-sess"]; sess == nil || sess.VerifyCmd == "" {
		t.Fatalf("clean run did not record VerifyCmd in .coord: %+v", st.Sessions["ml-sess"])
	}
}

// Stage 4: Index-free local repository diagnostics (.deja/memory.jsonl,
// compiler symbol signatures, and failing test assertion lines) still fire
// immediately on Strike 1 even on an empty/cold index directory.
func TestSniperGateIndexFreeRepoDiagnosticsFireOnFirstStrike(t *testing.T) {
	hermeticEnv(t)
	coldDir := filepath.Join(t.TempDir(), "cold-idx")
	t.Setenv("DEJA_INDEX_DIR", coldDir)

	repo := filepath.Join(t.TempDir(), "repo")
	files := map[string]string{
		filepath.Join(repo, ".git", "HEAD"): "ref: refs/heads/main\n",
		filepath.Join(repo, "billing", "ledger.py"): "class InvoiceLedger:\n" +
			"    def compute_total(self, rows):\n" +
			"        return sum(rows)\n",
		filepath.Join(repo, "calc", "calc_test.go"): "package calc\n\n" +
			"import \"testing\"\n\n" +
			"func TestSum(t *testing.T) {\n" +
			"\tif got := 1 + 1; got != 3 { t.Fatalf(\"got %d\", got) }\n" +
			"}\n",
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := sources.RepoEntry{
		ID:    sources.RepoEntryID("rejected", "never invoke deploy/prod.sh without --dry-run"),
		Kind:  "rejected",
		Text:  "never invoke deploy/prod.sh without --dry-run",
		Files: []string{"deploy/prod.sh"},
		Date:  "2026-10-15",
	}
	memPath := sources.RepoMemoryPath(repo)
	if err := os.MkdirAll(filepath.Dir(memPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memPath, append(mustMarshal(t, entry), '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := runPostToolOnce(t, coldDir, "s1-mem", repo, "bash deploy/prod.sh", "Error: deploy/prod.sh preflight check failed", "", true); !strings.Contains(got, "in .deja/memory.jsonl") || !strings.Contains(got, "--dry-run") {
		t.Fatalf("cold Strike 1 missed .deja/memory.jsonl guardrail:\n%s", got)
	}
	if got := runPostToolOnce(t, coldDir, "s1-ast", repo, "pytest billing/test_ledger.py", "AttributeError: 'InvoiceLedger' object has no attribute 'comput_total'", "", true); !strings.Contains(got, "attribute hint for InvoiceLedger") || !strings.Contains(got, "compute_total") {
		t.Fatalf("cold Strike 1 missed local attribute hint:\n%s", got)
	}
	if got := runPostToolOnce(t, coldDir, "s1-test", repo, "go test ./calc", "--- FAIL: TestSum (0.00s)\n    calc_test.go:6: got 2\nFAIL", "", true); !strings.Contains(got, "calc/calc_test.go:6 (TestSum):") || !strings.Contains(got, "got != 3") {
		t.Fatalf("cold Strike 1 missed failing test source line:\n%s", got)
	}
}
