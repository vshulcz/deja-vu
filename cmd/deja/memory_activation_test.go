package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/vault"
)

func TestMCPProjectPathNormalization(t *testing.T) {
	tmp := hermeticEnv(t)
	home := os.Getenv("HOME")

	repoAlpha := filepath.Join(home, "work", "org", "alpha")
	repoBeta := filepath.Join(tmp, "other", "org", "beta")
	unrelated := filepath.Join(tmp, "unrelated")
	for _, d := range []string{
		filepath.Join(repoAlpha, "pkg", "retry"),
		filepath.Join(repoBeta, "pkg", "retry"),
		unrelated,
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alphaFile := filepath.Join(repoAlpha, "pkg", "retry", "backoff.go")
	betaFile := filepath.Join(repoBeta, "pkg", "retry", "backoff.go")
	if err := os.WriteFile(alphaFile, []byte("package retry\n\nfunc NextDelay() int { return 7 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(betaFile, []byte("package retry\n\nfunc NextDelay() int { return 9 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	encAlpha := strings.ReplaceAll(repoAlpha, string(filepath.Separator), "-")
	encBeta := strings.ReplaceAll(repoBeta, string(filepath.Separator), "-")
	writeClaudeFixture(t, filepath.Join(claudeRoot, encAlpha, "sess-alpha-1.jsonl"), "sess-alpha-1", []string{
		`{"type":"user","sessionId":"sess-alpha-1","cwd":` + strconv.Quote(repoAlpha) + `,"timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"why does zorbmax jitter cap fail on pkg/retry/backoff.go"}}`,
		`{"type":"assistant","sessionId":"sess-alpha-1","cwd":` + strconv.Quote(repoAlpha) + `,"timestamp":"2026-08-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":` + strconv.Quote(alphaFile) + `}},{"type":"text","text":"Pinned the zorbmax jitter cap floor inside pkg/retry/backoff.go."}]}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, encBeta, "sess-beta-1.jsonl"), "sess-beta-1", []string{
		`{"type":"user","sessionId":"sess-beta-1","cwd":` + strconv.Quote(repoBeta) + `,"timestamp":"2026-08-02T10:00:00Z","message":{"role":"user","content":"zorbmax jitter cap in other repo"}}`,
		`{"type":"assistant","sessionId":"sess-beta-1","cwd":` + strconv.Quote(repoBeta) + `,"timestamp":"2026-08-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":` + strconv.Quote(betaFile) + `}},{"type":"text","text":"Edited beta backoff for completely unrelated token betaonly991."}]}}`,
	})

	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	t.Chdir(unrelated)

	// Direct MCP blame with filesystem path project and relative file path.
	blameOut, err := callMCPTool(dir, "blame", json.RawMessage(`{"path":"pkg/retry/backoff.go","project":`+strconv.Quote(repoAlpha)+`}`))
	if err != nil {
		t.Fatalf("blame with abs project path failed: %v", err)
	}
	if !strings.Contains(blameOut, "sess-alpha-1") || !strings.Contains(blameOut, "backoff.go") {
		t.Fatalf("blame with abs project path missed alpha session:\n%s", blameOut)
	}
	if strings.Contains(blameOut, "sess-beta-1") || strings.Contains(blameOut, "betaonly991") {
		t.Fatalf("blame with abs project path leaked beta session:\n%s", blameOut)
	}

	// Tilde project path via unified deja(mode=blame) with relative q path.
	tildeOut, err := callMCPTool(dir, "deja", json.RawMessage(`{"mode":"blame","q":"pkg/retry/backoff.go","project":"~/work/org/alpha"}`))
	if err != nil {
		t.Fatalf("deja(blame) with ~/ project path failed: %v", err)
	}
	if !strings.Contains(tildeOut, "sess-alpha-1") {
		t.Fatalf("deja(blame) with ~/ project path missed alpha session:\n%s", tildeOut)
	}
	if strings.Contains(tildeOut, "sess-beta-1") {
		t.Fatalf("deja(blame) with ~/ project path leaked beta session:\n%s", tildeOut)
	}

	// Tilde project path via recall scopes to alpha without leaking beta.
	recallOut, err := callMCPTool(dir, "recall", json.RawMessage(`{"q":"zorbmax jitter cap","project":"~/work/org/alpha"}`))
	if err != nil {
		t.Fatalf("recall with ~/ project path failed: %v", err)
	}
	if !strings.Contains(recallOut, "sess-alpha-1") {
		t.Fatalf("recall with ~/ project path missed alpha session:\n%s", recallOut)
	}
	if strings.Contains(recallOut, "sess-beta-1") || strings.Contains(recallOut, "betaonly991") {
		t.Fatalf("recall with ~/ project path leaked beta session:\n%s", recallOut)
	}

	// auto mode with relative path + abs project path routes to blame in that project.
	autoOut, err := callMCPTool(dir, "deja", json.RawMessage(`{"mode":"auto","q":"pkg/retry/backoff.go","project":`+strconv.Quote(repoAlpha)+`}`))
	if err != nil {
		t.Fatalf("deja(auto) with relative file path + project dir failed: %v", err)
	}
	if !strings.Contains(autoOut, "sess-alpha-1") || strings.Contains(autoOut, "sess-beta-1") {
		t.Fatalf("deja(auto) did not route relative path blame in alpha:\n%s", autoOut)
	}

	// remember normalizes filesystem path project to canonical ClaudeProjectName.
	if _, err := callMCPTool(dir, "remember", json.RawMessage(`{"text":"Always pin backoff seed in tests","project":`+strconv.Quote(repoAlpha)+`}`)); err != nil {
		t.Fatalf("remember with abs project path failed: %v", err)
	}
	wantProject := sources.ClaudeProjectName(repoAlpha)
	notes := sources.LoadNotes()
	foundNote := false
	for _, n := range notes {
		if strings.Contains(n.Title, "backoff seed") || len(n.Messages) > 0 && strings.Contains(n.Messages[0].Text, "Always pin backoff seed in tests") {
			if n.Project != wantProject {
				t.Fatalf("remember filed note under project %q, want %q", n.Project, wantProject)
			}
			foundNote = true
		}
	}
	if !foundNote {
		t.Fatalf("remember note not found in notes store (%d notes)", len(notes))
	}

	// Windows-style drive path project normalizes into canonical candidates without panicking or losing owner/repo.
	winScope, err := resolveQueryScope(QueryScopeInput{
		CWD:        unrelated,
		Project:    `C:\Users\alex\work\org\winapp`,
		Activation: policy.ActivationMCP,
	})
	if err != nil {
		t.Fatalf("resolveQueryScope windows path failed: %v", err)
	}
	if !winScope.MatchesProject("org/winapp") {
		t.Fatalf("windows project path scope %+v did not match org/winapp", winScope.Projects)
	}
}

func TestMCPRecallFilePathPrependsCausalBlame(t *testing.T) {
	tmp := hermeticEnv(t)
	repoDir := filepath.Join(tmp, "work", "org", "svc")
	unrelated := filepath.Join(tmp, "unrelated")
	for _, d := range []string{filepath.Join(repoDir, "pkg", "worker"), unrelated} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	targetFile := filepath.Join(repoDir, "pkg", "worker", "pool.go")
	if err := os.WriteFile(targetFile, []byte("package worker\n\nfunc PoolSize() int { return 4 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	enc := strings.ReplaceAll(repoDir, string(filepath.Separator), "-")
	// Session that edited pkg/worker/pool.go and recorded a decision later retracted.
	writeClaudeFixture(t, filepath.Join(claudeRoot, enc, "sess-causal-1.jsonl"), "sess-causal-1", []string{
		`{"type":"user","sessionId":"sess-causal-1","cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-03T10:00:00Z","message":{"role":"user","content":"adjust worker pool sizing"}}`,
		`{"type":"assistant","sessionId":"sess-causal-1","cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-03T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":` + strconv.Quote(targetFile) + `}},{"type":"text","text":"Changed PoolSize in pkg/worker/pool.go to 4 after queue saturation check."}]}}`,
	})
	// Live session currently touching the same file — must be excluded from causal blame summary.
	writeClaudeFixture(t, filepath.Join(claudeRoot, enc, "sess-asking-live.jsonl"), "sess-asking-live", []string{
		`{"type":"user","sessionId":"sess-asking-live","cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-05T10:00:00Z","message":{"role":"user","content":"inspecting pkg/worker/pool.go right now"}}`,
		`{"type":"assistant","sessionId":"sess-asking-live","cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-05T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":` + strconv.Quote(targetFile) + `}},{"type":"text","text":"Looking at live session change in pkg/worker/pool.go."}]}}`,
	})

	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	markSessionLive(dir, "sess-asking-live")
	notesPath := sources.NotesFile()
	if err := os.MkdirAll(filepath.Dir(notesPath), 0o755); err != nil {
		t.Fatal(err)
	}
	promoRow := `{"kind":"promoted","session":"claude:sess-causal-1","state":"rejected","text":"Reverted unbounded pool growth in pool.go","ts":"2026-08-04T00:00:00Z"}` + "\n"
	if err := os.WriteFile(notesPath, []byte(promoRow), 0o600); err != nil {
		t.Fatalf("write promoted lifecycle note: %v", err)
	}

	t.Chdir(unrelated)

	got, err := callMCPTool(dir, "recall", json.RawMessage(`{"q":"pkg/worker/pool.go","project":`+strconv.Quote(repoDir)+`}`))
	if err != nil {
		t.Fatalf("recall(file path) failed: %v", err)
	}
	if strings.Count(got, "<deja-recall>") != 1 {
		t.Fatalf("expected single <deja-recall> wrapper, got:\n%s", got)
	}
	if len(got) > recallMCPBudget {
		t.Fatalf("recall(file path) exceeded budget: len=%d > %d", len(got), recallMCPBudget)
	}
	if !strings.Contains(got, "Causal blame") || !strings.Contains(got, "sess-causal-1") {
		t.Fatalf("recall(file path) missing prepended causal blame summary:\n%s", got)
	}
	if !strings.Contains(strings.ToLower(got), "rejected") {
		t.Fatalf("recall(file path) causal blame missing lifecycle status:\n%s", got)
	}
	if strings.Contains(got, "sess-asking-live") {
		t.Fatalf("recall(file path) included live session in causal blame:\n%s", got)
	}

	paged, err := callMCPTool(dir, "recall", json.RawMessage(`{"q":"pkg/worker/pool.go","project":`+strconv.Quote(repoDir)+`,"offset":1}`))
	if err != nil {
		t.Fatalf("recall(file path, offset=1) failed: %v", err)
	}
	if strings.Contains(paged, "Causal blame") {
		t.Fatalf("offset > 0 repeated causal blame summary:\n%s", paged)
	}

	var cliBuf bytes.Buffer
	if err := runRecall(dir, []string{"--project", repoDir, "pkg/worker/pool.go"}, &cliBuf); err != nil {
		t.Fatalf("CLI runRecall failed: %v", err)
	}
	cliGot := cliBuf.String()
	if !strings.Contains(cliGot, "Causal blame") || !strings.Contains(cliGot, "sess-causal-1") {
		t.Fatalf("CLI recall(file path) missing prepended causal blame summary:\n%s", cliGot)
	}
	if strings.Contains(cliGot, "sess-asking-live") {
		t.Fatalf("CLI recall(file path) included live session in causal blame:\n%s", cliGot)
	}
}

func TestHookToolExternalMCPWrappersAndToolInputCWD(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_COMMAND_HINTS", "on")
	repoDir := filepath.Join(tmp, "work", "org", "svc")
	unrelated := filepath.Join(tmp, "home-unrelated")
	for _, d := range []string{filepath.Join(repoDir, ".git"), filepath.Join(repoDir, "pkg", "worker"), unrelated} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	poolFile := filepath.Join(repoDir, "pkg", "worker", "pool.go")
	if err := os.WriteFile(poolFile, []byte("package worker\n\nfunc PoolSize(n int) int {\n\treturn n + 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	enc := strings.ReplaceAll(repoDir, string(filepath.Separator), "-")
	for i, id := range []string{"sess-pool-prev-1", "sess-pool-prev-2"} {
		h := string(rune('1' + i))
		writeClaudeFixture(t, filepath.Join(claudeRoot, enc, id+".jsonl"), id, []string{
			`{"type":"user","sessionId":` + strconv.Quote(id) + `,"cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-0` + h + `T10:00:00Z","message":{"role":"user","content":"adjust worker pool limit and run go test ./pkg/worker"}}`,
			`{"type":"assistant","sessionId":` + strconv.Quote(id) + `,"cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-0` + h + `T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":` + strconv.Quote(poolFile) + `}},{"type":"text","text":"Keep PoolSize clamped in pkg/worker/pool.go; run go test ./pkg/worker -race before finishing."}]}}`,
		})
	}
	// Seed repo memory mentioning go test ./pkg/worker so pre-command hint matches only inside repoDir.
	ruleText := "Running go test ./pkg/worker requires -count=1 to bypass cached socket timing"
	if _, err := sources.AddRepoEntries(repoDir, []sources.RepoEntry{{
		ID:    sources.RepoEntryID("gotcha", ruleText),
		Kind:  "gotcha",
		Text:  ruleText,
		Files: []string{"pkg/worker/pool.go"},
		Date:  "2026-08-01",
	}}); err != nil {
		t.Fatalf("AddRepoEntries failed: %v", err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	t.Chdir(unrelated)

	invokePre := func(payload string) string {
		t.Helper()
		var out strings.Builder
		if err := runHookToolMode(dir, strings.NewReader(payload), &out, hookToolClaude); err != nil {
			t.Fatalf("runHookToolMode failed: %v", err)
		}
		return out.String()
	}

	// 1. mcp_runner_run recognized as external MCP command tool AND tool_input.cwd overrides unrelated outer cwd.
	cmdOut := invokePre(`{"hook_event_name":"PreToolUse","session_id":"live-mcp-1","cwd":` + strconv.Quote(unrelated) + `,"tool_name":"mcp_runner_run","tool_input":{"command":"go test ./pkg/worker","cwd":` + strconv.Quote(repoDir) + `}}`)
	if !strings.Contains(cmdOut, "-count=1") {
		t.Fatalf("mcp_runner_run with tool_input.cwd did not surface project command hint:\n%q", cmdOut)
	}

	// 2. mcp_fs_replace triggers search/replace mismatch hint relative to tool_input.cwd.
	replaceOut := invokePre(`{"hook_event_name":"PreToolUse","session_id":"live-mcp-2","cwd":` + strconv.Quote(unrelated) + `,"tool_name":"mcp_fs_replace","tool_input":{"file_path":"pkg/worker/pool.go","cwd":` + strconv.Quote(repoDir) + `,"old_string":"func PoolSize(n int) int {\n\treturn n + 999\n}","new_string":"func PoolSize(n int) int {\n\treturn n + 2\n}"}}`)
	if !strings.Contains(replaceOut, "sess-pool-prev") && !strings.Contains(replaceOut, "not verbatim") {
		t.Fatalf("mcp_fs_replace did not emit file history / mismatch hint:\n%q", replaceOut)
	}
	if !strings.Contains(replaceOut, "return n + 1") {
		t.Fatalf("mcp_fs_replace did not surface editTargetMismatchLine closest disk line:\n%q", replaceOut)
	}

	// 3. mcp_fs_patch parses patchText against tool_input.cwd.
	patchText := "*** Begin Patch\n*** Update File: pkg/worker/pool.go\n@@\n-return n + 1\n+return n + 2\n*** End Patch"
	patchOut := invokePre(`{"hook_event_name":"PreToolUse","session_id":"live-mcp-3","cwd":` + strconv.Quote(unrelated) + `,"tool_name":"mcp_fs_patch","tool_input":{"cwd":` + strconv.Quote(repoDir) + `,"patchText":` + strconv.Quote(patchText) + `}}`)
	if !strings.Contains(patchOut, "pool.go") {
		t.Fatalf("mcp_fs_patch did not route through applyPatchLine:\n%q", patchOut)
	}

	// 4. mcp_fs_open strips :line selector relative to tool_input.cwd and surfaces file history.
	openOut := invokePre(`{"hook_event_name":"PreToolUse","session_id":"live-mcp-4","cwd":` + strconv.Quote(unrelated) + `,"tool_name":"mcp_fs_open","tool_input":{"file_path":"pkg/worker/pool.go:1-4","cwd":` + strconv.Quote(repoDir) + `}}`)
	if !strings.Contains(openOut, "pool.go") {
		t.Fatalf("mcp_fs_open did not strip selector and emit file history:\n%q", openOut)
	}
}

func TestHookToolAfterExternalMCPContentArrayAndWrappedHeaders(t *testing.T) {
	tmp := hermeticEnv(t)
	repoDir := filepath.Join(tmp, "work", "org", "svc")
	unrelated := filepath.Join(tmp, "home-unrelated")
	for _, d := range []string{filepath.Join(repoDir, "pkg", "worker"), unrelated} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	poolFile := filepath.Join(repoDir, "pkg", "worker", "pool.go")
	if err := os.WriteFile(poolFile, []byte("package worker\n\nfunc PoolSize() int { return 4 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	enc := strings.ReplaceAll(repoDir, string(filepath.Separator), "-")
	// Seed two prior sessions hitting the exact compiler error and resolving it so a confirmed FixPair exists.
	for i, id := range []string{"sess-fix-1", "sess-fix-2"} {
		ts1 := "2026-08-01T1" + string(rune('0'+i)) + ":00:00Z"
		ts2 := "2026-08-01T1" + string(rune('0'+i)) + ":01:00Z"
		ts3 := "2026-08-01T1" + string(rune('0'+i)) + ":02:00Z"
		writeClaudeFixture(t, filepath.Join(claudeRoot, enc, id+".jsonl"), id, []string{
			`{"type":"assistant","sessionId":` + strconv.Quote(id) + `,"cwd":` + strconv.Quote(repoDir) + `,"timestamp":` + strconv.Quote(ts1) + `,"message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"go test ./pkg/worker"}}]}}`,
			`{"type":"user","sessionId":` + strconv.Quote(id) + `,"cwd":` + strconv.Quote(repoDir) + `,"timestamp":` + strconv.Quote(ts2) + `,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","is_error":true,"content":"./pkg/worker/pool.go:14:2: undefined: missingHelper"}]}}`,
			`{"type":"assistant","sessionId":` + strconv.Quote(id) + `,"cwd":` + strconv.Quote(repoDir) + `,"timestamp":` + strconv.Quote(ts3) + `,"message":{"role":"assistant","content":[{"type":"tool_use","id":"c2","name":"Bash","input":{"command":"go test -tags=workerfix ./pkg/worker"}}]}}`,
			`{"type":"user","sessionId":` + strconv.Quote(id) + `,"cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-01T1` + string(rune('0'+i)) + `:03:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c2","is_error":false,"content":"ok  github.com/example/svc/pkg/worker 0.01s"}]}}`,
		})
	}

	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(unrelated)

	payload := `{"hook_event_name":"PostToolUse","session_id":"live-after-1","cwd":` + strconv.Quote(unrelated) + `,"tool_name":"mcp__runner__exec","tool_input":{"command":"go test ./pkg/worker","cwd":` + strconv.Quote(repoDir) + `},"tool_response":{"content":[{"type":"text","text":"A pre-tool hook changed the arguments of this tool call before it ran. Changed: mode.\n\nCreated At: 2026-11-26T10:00:00Z\nCompleted At: 2026-11-26T10:00:02Z\n[cmd direct | exit=1 | 12ms]\n./pkg/worker/pool.go:14:2: undefined: missingHelper"}]}}`
	var out strings.Builder
	if err := runHookToolAfterMode(dir, strings.NewReader(payload), &out, true); err != nil {
		t.Fatalf("runHookToolAfterMode failed: %v", err)
	}
	if !strings.Contains(out.String(), "go test -tags=workerfix ./pkg/worker") {
		t.Fatalf("PostToolUse on mcp__runner__exec with content[] + header wrapper missed FixPair:\n%q", out.String())
	}

	stripped := latestToolFailureFromSteps([]transcriptStep{{
		Type:    "GENERIC",
		Content: "A pre-tool hook changed the arguments of this tool call before it ran. Changed: mode.\n\nCreated At: 2026-11-26T10:00:00Z\nCompleted At: 2026-11-26T10:00:02Z\n[cmd direct | exit=1 | 12ms]\n./pkg/worker/pool.go:14:2: undefined: missingHelper",
	}})
	if strings.Contains(stripped, "A pre-tool hook changed") || strings.Contains(stripped, "Created At:") || !strings.HasPrefix(stripped, "./pkg/worker/pool.go:14:2: undefined: missingHelper") {
		t.Fatalf("latestToolFailureFromSteps kept MCP wrapper header: %q", stripped)
	}
}

func TestSessionEndResolvesAntigravityWorkspacePaths(t *testing.T) {
	tmp := hermeticEnv(t)
	homeDir := os.Getenv("HOME")
	repoDir := filepath.Join(tmp, "work", "org", "svc")
	plainDir := filepath.Join(tmp, "plain-non-git")
	otherPlain := filepath.Join(tmp, "other-non-git")
	for _, d := range []string{filepath.Join(repoDir, ".git"), plainDir, otherPlain} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	decode := func(payload string) precompactHookInput {
		t.Helper()
		var in precompactHookInput
		if err := json.Unmarshal([]byte(payload), &in); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		in.adopt()
		return in
	}

	// Payload A: Antigravity Stop/SessionEnd sends conversationId + workspacePaths with empty cwd.
	inA := decode(`{"conversationId":"agy-stop-1","workspacePaths":[` + strconv.Quote(homeDir) + `,` + strconv.Quote(repoDir) + `]}`)
	if inA.SessionID != "agy-stop-1" {
		t.Fatalf("adopted SessionID = %q, want %q", inA.SessionID, "agy-stop-1")
	}
	if got := sessionEndProject(inA); got != repoDir {
		t.Fatalf("Payload A sessionEndProject = %q, want repo %q", got, repoDir)
	}

	// Payload B: Non-repo HOME cwd alongside repoDir in workspacePaths resolves to repoDir.
	inB := decode(`{"conversationId":"agy-stop-2","cwd":` + strconv.Quote(homeDir) + `,"workspacePaths":[` + strconv.Quote(homeDir) + `,` + strconv.Quote(repoDir) + `]}`)
	if got := sessionEndProject(inB); got != repoDir {
		t.Fatalf("Payload B sessionEndProject = %q, want repo %q", got, repoDir)
	}

	// Payload C: Explicit non-home cwd is preserved even when workspace_roots also has non-git dir.
	inC := decode(`{"session_id":"explicit-1","cwd":` + strconv.Quote(plainDir) + `,"workspace_roots":[` + strconv.Quote(otherPlain) + `]}`)
	if got := sessionEndProject(inC); got != plainDir {
		t.Fatalf("Payload C sessionEndProject = %q, want %q", got, plainDir)
	}
}

func TestSessionEndRefreshCapturesConfirmedFixAndRuleCandidates(t *testing.T) {
	tmp := hermeticEnv(t)
	repoDir := filepath.Join(tmp, "work", "org", "svc")
	for _, d := range []string{filepath.Join(repoDir, ".git"), filepath.Join(repoDir, "pkg", "worker")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	poolFile := filepath.Join(repoDir, "pkg", "worker", "pool.go")
	if err := os.WriteFile(poolFile, []byte("package worker\n\nfunc PoolSize(n int) int { return n + 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	enc := strings.ReplaceAll(repoDir, string(filepath.Separator), "-")
	for i, id := range []string{"sess-se-fix-1", "sess-se-fix-2"} {
		h := string(rune('1' + i))
		writeClaudeFixture(t, filepath.Join(claudeRoot, enc, id+".jsonl"), id, []string{
			`{"type":"user","entrypoint":"cli","sessionId":` + strconv.Quote(id) + `,"cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-1` + h + `T10:00:00Z","message":{"role":"user","content":"Always run go test -tags=workerfix ./pkg/worker before calling pkg/worker/pool.go builds green"}}`,
			`{"type":"assistant","sessionId":` + strconv.Quote(id) + `,"cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-1` + h + `T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"go test ./pkg/worker"}}]}}`,
			`{"type":"user","sessionId":` + strconv.Quote(id) + `,"cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-1` + h + `T10:02:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","is_error":true,"content":"./pkg/worker/pool.go:14: cannot find package workerfix_shim in build context"}]}}`,
			`{"type":"assistant","sessionId":` + strconv.Quote(id) + `,"cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-1` + h + `T10:03:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"c2","name":"Bash","input":{"command":"go test -tags=workerfix ./pkg/worker"}}]}}`,
			`{"type":"user","sessionId":` + strconv.Quote(id) + `,"cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-1` + h + `T10:04:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c2","is_error":false,"content":"ok  github.com/example/svc/pkg/worker 0.01s"}]}}`,
		})
	}
	// Single-session unconfirmed sighting (Candidate == true) must NOT be captured as an auto-candidate.
	writeClaudeFixture(t, filepath.Join(claudeRoot, enc, "sess-se-once.jsonl"), "sess-se-once", []string{
		`{"type":"user","sessionId":"sess-se-once","cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-15T10:00:00Z","message":{"role":"user","content":"investigate worker db check"}}`,
		`{"type":"assistant","sessionId":"sess-se-once","cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-15T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"make integration-worker"}}]}}`,
		`{"type":"user","sessionId":"sess-se-once","cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-15T10:02:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","is_error":true,"content":"psql: error: connection to server on localhost failed: connection refused"}]}}`,
		`{"type":"assistant","sessionId":"sess-se-once","cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-15T10:03:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"c2","name":"Bash","input":{"command":"docker compose up -d db"}}]}}`,
		`{"type":"user","sessionId":"sess-se-once","cwd":` + strconv.Quote(repoDir) + `,"timestamp":"2026-08-15T10:04:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c2","is_error":false,"content":"container db started"}]}}`,
	})

	dir := os.Getenv("DEJA_INDEX_DIR")
	t.Setenv("CLAUDE_PROJECT_DIR", repoDir)
	runHookRefresh(dir)

	if _, err := os.Stat(sources.RepoMemoryPath(repoDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runHookRefresh unexpectedly created .deja/memory.jsonl in untouched repo: err=%v", err)
	}
	if notes := sources.LoadNotes(); len(notes) != 0 {
		t.Fatalf("runHookRefresh auto-wrote global notes without approval: %+v", notes)
	}

	sidecarPath := filepath.Join(dir, "remember_candidates.jsonl")
	rawSidecar, err := vault.ReadSealedFile(dir, sidecarPath, vault.DomainSidecar)
	if err != nil || len(rawSidecar) == 0 {
		t.Fatalf("remember_candidates.jsonl sidecar missing or unreadable: err=%v len=%d", err, len(rawSidecar))
	}
	sidecarStr := string(rawSidecar)
	if !strings.Contains(sidecarStr, "workerfix_shim") || !strings.Contains(sidecarStr, "workerfix") {
		t.Fatalf("remember_candidates.jsonl missing confirmed fix candidate:\n%s", sidecarStr)
	}
	if !strings.Contains(sidecarStr, "Always run go test -tags=workerfix") {
		t.Fatalf("remember_candidates.jsonl missing standing user rule candidate:\n%s", sidecarStr)
	}
	if strings.Contains(sidecarStr, "connection refused") || strings.Contains(sidecarStr, "docker compose") {
		t.Fatalf("remember_candidates.jsonl captured unconfirmed Candidate=true FixPair:\n%s", sidecarStr)
	}

	// Running refresh a second time stays idempotent and merges existing sealed candidates.
	runHookRefresh(dir)

	cands, err := collectRepoCandidates(dir, repoDir, 0, 20)
	if err != nil {
		t.Fatalf("collectRepoCandidates failed: %v", err)
	}
	var sawConfirmedFix, sawRule bool
	for _, c := range cands {
		if strings.Contains(c.Text, "docker compose") || strings.Contains(c.Text, "connection refused") {
			t.Fatalf("collectRepoCandidates surfaced unconfirmed Candidate=true FixPair: %+v", c)
		}
		if c.Kind == "gotcha" && strings.Contains(c.Text, "workerfix_shim") && strings.Contains(c.Text, "go test -tags=workerfix ./pkg/worker") && slices.Contains(c.Files, "pkg/worker/pool.go") {
			sawConfirmedFix = true
		}
		if strings.Contains(c.Text, "Always run go test -tags=workerfix ./pkg/worker") {
			if c.Sessions != 2 {
				t.Fatalf("standing user rule candidate Sessions = %d, want 2: %+v", c.Sessions, c)
			}
			sawRule = true
		}
	}
	if !sawConfirmedFix {
		t.Fatalf("collectRepoCandidates missing confirmed session-end fix candidate: %+v", cands)
	}
	if !sawRule {
		t.Fatalf("collectRepoCandidates missing standing user rule candidate: %+v", cands)
	}
}

func TestRememberCandidatesAndMCPHelperBranches(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home-norm")
	repoDir := filepath.Join(home, "work", "org", "svc")
	workerDir := filepath.Join(repoDir, "pkg", "worker")
	if err := os.MkdirAll(workerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	poolFile := filepath.Join(workerDir, "pool.go")
	if err := os.WriteFile(poolFile, []byte("package worker\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := normalizeProjectName("", repoDir); got != "" {
		t.Fatalf("normalizeProjectName empty = %q, want empty", got)
	}
	if got := normalizeProjectName("org/svc", repoDir); got != "org/svc" {
		t.Fatalf("normalizeProjectName named = %q, want org/svc", got)
	}
	wantProj := sources.ClaudeProjectName(repoDir)
	if got := normalizeProjectName("~/work/org/svc", ""); got != wantProj {
		t.Fatalf("normalizeProjectName tilde = %q, want %q", got, wantProj)
	}

	if got := mcpContentArrayText("not-a-slice"); got != "" {
		t.Fatalf("mcpContentArrayText non-slice = %q, want empty", got)
	}
	mixed := []any{
		"ignore-non-map",
		map[string]any{"type": "image", "text": "skip"},
		map[string]any{"type": "text", "text": "   "},
		map[string]any{"type": "text", "text": "first line"},
		map[string]any{"type": "", "text": "second line"},
	}
	if got := mcpContentArrayText(mixed); got != "first line\nsecond line" {
		t.Fatalf("mcpContentArrayText mixed = %q, want two lines", got)
	}

	if got := candidateErrorRelFile(repoDir, poolFile, ""); got != "pkg/worker/pool.go" {
		t.Fatalf("candidateErrorRelFile edit = %q, want pkg/worker/pool.go", got)
	}
	if got := candidateErrorRelFile(repoDir, "", "pkg/worker/pool.go:14:2: boom"); got != "pkg/worker/pool.go" {
		t.Fatalf("candidateErrorRelFile errText = %q, want pkg/worker/pool.go", got)
	}
	if got := candidateErrorRelFile(repoDir, "", "   "); got != "" {
		t.Fatalf("candidateErrorRelFile blank = %q, want empty", got)
	}
	if got := candidateErrorRelFile(repoDir, "", "pkg/worker/missing.go:9: boom"); got != "" {
		t.Fatalf("candidateErrorRelFile missing = %q, want empty", got)
	}

	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := readRememberCandidates(""); got != nil {
		t.Fatalf("readRememberCandidates empty dir = %v, want nil", got)
	}
	t0 := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	rows := []storedRememberCandidate{
		{ID: "b", Project: wantProj, Kind: "rule", Text: "Always keep b", Sessions: 1, Source: "user rule", At: t0},
		{ID: "z", Project: wantProj, Kind: "rule", Text: "Always keep z", Sessions: 3, Source: "user rule", At: t0},
		{ID: "a", Project: wantProj, Kind: "rule", Text: "Always keep a", Sessions: 1, Source: "user rule", At: t0},
	}
	writeRememberCandidates("", rows)
	writeRememberCandidates(dir, rows)
	gotRows := readRememberCandidates(dir)
	if len(gotRows) != 3 || gotRows[0].ID != "z" || gotRows[1].ID != "a" || gotRows[2].ID != "b" {
		t.Fatalf("readRememberCandidates tie-break order = %+v, want [z, a, b]", gotRows)
	}

	// Verify blank/malformed lines are skipped and missing IDs are backfilled on read.
	rawSidecar := "\n{not-json}\n{\"project\":\"org/svc\",\"kind\":\"rule\",\"text\":\"Always backfill id\",\"source\":\"user rule\",\"at\":\"2026-08-21T00:00:00Z\"}\n"
	if err := vault.WriteSealedFile(dir, rememberCandidatesPath(dir), vault.DomainSidecar, []byte(rawSidecar), 0o600); err != nil {
		t.Fatal(err)
	}
	backfilled := readRememberCandidates(dir)
	if len(backfilled) != 1 || backfilled[0].ID == "" || backfilled[0].Text != "Always backfill id" {
		t.Fatalf("readRememberCandidates malformed/backfill = %+v", backfilled)
	}

	existing := []storedRememberCandidate{
		{ID: "dup", Project: wantProj, Kind: "gotcha", Text: "Old note", Sessions: 1, Source: "session-end fix", At: t0},
		{ID: "dup", Project: wantProj, Kind: "gotcha", Text: "Old note", Sessions: 1, Source: "session-end fix", At: t0},
	}
	if unchanged := mergeRememberCandidates(existing, nil); len(unchanged) != 2 {
		t.Fatalf("mergeRememberCandidates(nil) altered len=%d", len(unchanged))
	}
	incoming := []storedRememberCandidate{
		{
			ID:       "dup",
			Project:  wantProj,
			Kind:     "gotcha",
			Text:     "Old note",
			Files:    []string{"pkg/worker/pool.go"},
			Sessions: 2,
			From:     "deja (2026-08-21)",
			Source:   "session-end fix",
			Session:  "sess-merged",
			At:       t0.Add(time.Hour),
		},
		{ID: "new-id", Project: wantProj, Kind: "rule", Text: "New rule", Source: "user rule", At: t0},
	}
	merged := mergeRememberCandidates(existing, incoming)
	if len(merged) != 2 || merged[0].Sessions != 2 || merged[0].From == "" || merged[0].Session != "sess-merged" || !slices.Contains(merged[0].Files, "pkg/worker/pool.go") {
		t.Fatalf("mergeRememberCandidates result unexpected: %+v", merged)
	}
}
