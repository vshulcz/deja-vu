package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func coordRunToolHook(t *testing.T, dir string, payload map[string]any) string {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if runErr := runHookTool(dir, bytes.NewReader(b), &out); runErr != nil {
		t.Fatalf("runHookTool failed: %v", runErr)
	}
	return strings.TrimSpace(out.String())
}

func coordRunAfterHook(t *testing.T, dir string, payload map[string]any) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if runErr := runHookToolAfter(dir, bytes.NewReader(b), &out); runErr != nil {
		t.Fatalf("runHookToolAfter failed: %v", runErr)
	}
}

func coordRunContextHook(t *testing.T, dir string, payload map[string]any) string {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var outBytes []byte
	runErr := withStdin(b, func() error {
		var capErr error
		outBytes, capErr = captureDeferredStdout(func() error {
			return runHookContextMode(dir, false, false)
		})
		return capErr
	})
	if runErr != nil {
		t.Fatalf("runHookContextMode failed: %v", runErr)
	}
	return strings.TrimSpace(string(outBytes))
}

func withFrozenCoordClock(t *testing.T, start time.Time) *time.Time {
	t.Helper()
	cur := start
	prev := coordNow
	coordNow = func() time.Time { return cur }
	t.Cleanup(func() { coordNow = prev })
	return &cur
}

func makeFakeGitRepo(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func makeFakeLinkedWorktree(t *testing.T, mainRepo, wtDir, name string) string {
	t.Helper()
	gitDir := filepath.Join(mainRepo, ".git", "worktrees", name)
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitFile := "gitdir: " + gitDir + "\n"
	if err := os.WriteFile(filepath.Join(wtDir, ".git"), []byte(gitFile), 0o644); err != nil {
		t.Fatal(err)
	}
	return gitDir
}

func TestCoordCanonicalPathAndGitWorktree(t *testing.T) {
	tmp := t.TempDir()
	realRepo := filepath.Join(tmp, "real-repo")
	makeFakeGitRepo(t, realRepo)
	linkRepo := filepath.Join(tmp, "link-repo")
	if err := os.Symlink(realRepo, linkRepo); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink requires elevated privileges on Windows")
		}
		t.Fatal(err)
	}

	pReal := coordCanonicalPath(realRepo, "sub/new_file.go")
	pLink := coordCanonicalPath(linkRepo, filepath.Join(linkRepo, "sub", "new_file.go"))
	if pReal == "" || pReal != pLink {
		t.Fatalf("expected symlinked missing file paths to match: real=%q link=%q", pReal, pLink)
	}

	wtMain, gdMain := resolveCoordGitWorktree(linkRepo)
	expectedMain := coordCanonicalPath("", realRepo)
	if wtMain != expectedMain || gdMain != filepath.Join(expectedMain, ".git") {
		t.Fatalf("unexpected main worktree resolution: wt=%q gd=%q want=%q", wtMain, gdMain, expectedMain)
	}

	wtDir := filepath.Join(tmp, "wt-feature")
	expectedGitDir := makeFakeLinkedWorktree(t, realRepo, wtDir, "wt-feature")
	subDir := filepath.Join(wtDir, "nested", "pkg")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gotWT, gotGD := resolveCoordGitWorktree(subDir)
	if gotWT != coordCanonicalPath("", wtDir) || gotGD != coordCanonicalPath("", expectedGitDir) {
		t.Fatalf("unexpected linked worktree resolution: wt=%q gd=%q", gotWT, gotGD)
	}
}

func TestActiveFileRadarCrossHarnessAndTTL(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	dir := index.DefaultDir()
	now := withFrozenCoordClock(t, time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))

	repo := filepath.Join(t.TempDir(), "repo")
	makeFakeGitRepo(t, repo)
	target := filepath.Join(repo, "pkg", "auth.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Session A edits pkg/auth.go via PreToolUse Edit (cold index: no deja index needed).
	if out := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": target},
		"session_id":      "sess-alpha-001",
		"cwd":             repo,
	}); out != "" {
		t.Fatalf("first editor should not warn, got %q", out)
	}

	// Session B runs PreToolUse Read: stays quiet (reads do not interrupt PreToolUse).
	if out := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Read",
		"tool_input":      map[string]any{"file_path": target},
		"session_id":      "sess-beta-002",
		"cwd":             repo,
	}); out != "" {
		t.Fatalf("PreToolUse Read should stay silent, got %q", out)
	}

	// Session B attempts PreToolUse Edit: must warn about sess-alpha-001.
	warn := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": "pkg/auth.go"},
		"session_id":      "sess-beta-002",
		"cwd":             repo,
	})
	if !strings.Contains(warn, "parallel edit radar: pkg/auth.go was edited") || !strings.Contains(warn, "sess-alpha-0") {
		t.Fatalf("expected parallel edit radar warning for sess-beta-002, got %q", warn)
	}

	// Session B edits again with no newer external edit: deduped.
	if out := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Write",
		"tool_input":      map[string]any{"file_path": "pkg/auth.go"},
		"session_id":      "sess-beta-002",
		"cwd":             repo,
	}); out != "" {
		t.Fatalf("unchanged external edit should be deduped, got %q", out)
	}

	// Session A makes a newer edit via OpenCode/Codex apply_patch (+15s).
	*now = now.Add(15 * time.Second)
	patch := "*** Begin Patch\n*** Update File: pkg/auth.go\n@@\n*** End Patch\n"
	patchWarn := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "apply_patch",
		"tool_input":      map[string]any{"patchText": patch},
		"session_id":      "sess-alpha-001",
		"cwd":             repo,
	})
	if !strings.Contains(patchWarn, "sess-beta-00") {
		t.Fatalf("Session A should see Session B's intervening edit, got %q", patchWarn)
	}

	// Session C (OpenCode/Gemini post-only host) reads via PostToolUse read_file: DOES warn!
	postReadWarn := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "read_file",
		"tool_input":      map[string]any{"filePath": target},
		"session_id":      "sess-gamma-003",
		"cwd":             repo,
	})
	if !strings.Contains(postReadWarn, "parallel edit radar: pkg/auth.go was edited") {
		t.Fatalf("PostToolUse read_file on post-only host should warn, got %q", postReadWarn)
	}

	// Session B re-warns because Session A touched pkg/auth.go again at +15s.
	reWarn := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": target},
		"session_id":      "sess-beta-002",
		"cwd":             repo,
	})
	if !strings.Contains(reWarn, "sess-alpha-0") {
		t.Fatalf("expected re-warn after newer external edit, got %q", reWarn)
	}

	// Advance past 30m TTL: Session D edits quietly.
	*now = now.Add(31 * time.Minute)
	if out := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": target},
		"session_id":      "sess-delta-004",
		"cwd":             repo,
	}); out != "" {
		t.Fatalf("expired >30m edits must not warn, got %q", out)
	}
}

func TestActiveFileRadarLineageAndWorktreeIsolation(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	dir := index.DefaultDir()
	_ = withFrozenCoordClock(t, time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC))

	tmp := t.TempDir()
	repo1 := filepath.Join(tmp, "repo-main")
	makeFakeGitRepo(t, repo1)
	file1 := filepath.Join(repo1, "shared.go")
	if err := os.WriteFile(file1, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wt2 := filepath.Join(tmp, "repo-wt2")
	_ = makeFakeLinkedWorktree(t, repo1, wt2, "wt2")
	file2 := filepath.Join(wt2, "shared.go")
	if err := os.WriteFile(file2, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Parent P edits repo1/shared.go.
	_ = coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": file1},
		"session_id":      "parent-p",
		"cwd":             repo1,
	})

	// Link Child C and Sibling S under Parent P.
	coordLinkParent(dir, "child-c", "parent-p", repo1)
	coordLinkParent(dir, "sibling-s", "parent-p", repo1)

	// Child C editing parent's file does NOT warn.
	if out := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": file1},
		"session_id":      "child-c",
		"cwd":             repo1,
	}); out != "" {
		t.Fatalf("child should not collide with parent edit, got %q", out)
	}

	// Separate linked git worktree editing its own shared.go does NOT warn either.
	if out := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": file2},
		"session_id":      "worker-wt2",
		"cwd":             wt2,
	}); out != "" {
		t.Fatalf("separate git worktree should not false-alarm, got %q", out)
	}

	// Parallel sibling S DOES warn on Child C's edit in the same worktree.
	sibWarn := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": file1},
		"session_id":      "sibling-s",
		"cwd":             repo1,
	})
	if !strings.Contains(sibWarn, "child-c") {
		t.Fatalf("parallel sibling subagents should still warn each other, got %q", sibWarn)
	}
}

func TestGitRadarIndexLockAndConcurrentEdits(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	dir := index.DefaultDir()
	now := withFrozenCoordClock(t, time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC))

	tmp := t.TempDir()
	repo := filepath.Join(tmp, "alpha-svc")
	makeFakeGitRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Read-only git commands never trigger git radar, even if index.lock exists.
	lockPath := filepath.Join(repo, ".git", "index.lock")
	if err := os.WriteFile(lockPath, []byte("lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(lockPath, now.Add(-20*time.Second), now.Add(-20*time.Second))

	for _, roCmd := range []string{"git status", "git diff --stat", "git log -n 3", "git branch --show-current", "git stash list", "git worktree list"} {
		if out := coordRunToolHook(t, dir, map[string]any{
			"hook_event_name": "PreToolUse",
			"tool_name":       "Bash",
			"tool_input":      map[string]any{"command": roCmd},
			"session_id":      "sess-git-ro",
			"cwd":             repo,
		}); out != "" {
			t.Fatalf("read-only git command %q triggered radar: %q", roCmd, out)
		}
	}

	// Active index.lock (<10m) warns on mutating git command.
	activeWarn := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "cd " + repo + " && git commit -m wip"},
		"session_id":      "sess-git-1",
		"cwd":             tmp,
	})
	if !strings.Contains(activeWarn, "git radar: index.lock active") || !strings.Contains(activeWarn, "in alpha-svc before git commit") {
		t.Fatalf("expected active index.lock warning, got %q", activeWarn)
	}

	// Stale index.lock (>=10m) updates ModTime and warns specifically as stale.
	staleTime := now.Add(-15 * time.Minute)
	if err := os.Chtimes(lockPath, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	staleWarn := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "git -C " + repo + " add main.go"},
		"session_id":      "sess-git-1",
		"cwd":             tmp,
	})
	if !strings.Contains(staleWarn, "git radar: stale index.lock") || !strings.Contains(staleWarn, "before git add") {
		t.Fatalf("expected stale index.lock warning, got %q", staleWarn)
	}

	_ = os.Remove(lockPath)

	// Session A edits main.go; Session B running git push in same worktree warns.
	_ = coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": filepath.Join(repo, "main.go")},
		"session_id":      "sess-editor",
		"cwd":             repo,
	})
	wtWarn := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "git push origin main"},
		"session_id":      "sess-pusher",
		"cwd":             repo,
	})
	if !strings.Contains(wtWarn, "1 file(s) in this worktree recently edited by active session sess-editor") || !strings.Contains(wtWarn, "main.go") {
		t.Fatalf("expected concurrent worktree edit warning before git push, got %q", wtWarn)
	}
}

func TestSubagentParentBriefSpawnAndStartParity(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	dir := index.DefaultDir()
	now := withFrozenCoordClock(t, time.Date(2026, 5, 1, 15, 0, 0, 0, time.UTC))

	repo := filepath.Join(t.TempDir(), "svc")
	makeFakeGitRepo(t, repo)
	for _, rel := range []string{"read_only.go", "edited_main.go", "extra.go"} {
		if err := os.WriteFile(filepath.Join(repo, rel), []byte("package svc\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Parent reads both read_only.go and edited_main.go, then edits edited_main.go.
	_ = coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Read",
		"tool_input":      map[string]any{"file_path": filepath.Join(repo, "read_only.go")},
		"session_id":      "parent-1",
		"cwd":             repo,
	})
	_ = coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Read",
		"tool_input":      map[string]any{"file_path": filepath.Join(repo, "edited_main.go")},
		"session_id":      "parent-1",
		"cwd":             repo,
	})
	_ = coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": filepath.Join(repo, "edited_main.go")},
		"session_id":      "parent-1",
		"cwd":             repo,
	})

	// Silent exit-0 verify command (empty output, cd matching repo stripped, PATH= preserved!).
	coordRunAfterHook(t, dir, map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "cd " + repo + " && PATH=/opt/venv/bin:$PATH go vet ./..."},
		"tool_response":   map[string]any{"output": ""},
		"session_id":      "parent-1",
		"cwd":             repo,
	})

	// Failing verify command afterwards must NOT overwrite the passing verify command!
	coordRunAfterHook(t, dir, map[string]any{
		"hook_event_name": "PostToolUseFailure",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "go test ./..."},
		"tool_response":   map[string]any{"output": "FAIL pkg/svc [build failed]"},
		"session_id":      "parent-1",
		"cwd":             repo,
	})

	// Parent transcript JSONL tail provides additional file reads & defused @trigger check.
	transcript := filepath.Join(t.TempDir(), "parent.jsonl")
	transcriptLines := []string{
		fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"r1","name":"Read","input":{"file_path":%q}}]}}`, filepath.Join(repo, "extra.go")),
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(transcriptLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// PreToolUse Task on cold store (!planIndexReady): preserves model & subagent_type, delivers brief!
	spawnOut := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Task",
		"tool_input": map[string]any{
			"prompt":        "Investigate auth failure",
			"subagent_type": "explore",
			"model":         "haiku",
		},
		"session_id":      "parent-1",
		"transcript_path": transcript,
		"cwd":             repo,
	})
	if spawnOut == "" {
		t.Fatalf("expected non-empty spawn updatedInput with parent brief")
	}
	var spawnResp struct {
		HookSpecificOutput struct {
			UpdatedInput struct {
				Prompt       string `json:"prompt"`
				SubagentType string `json:"subagent_type"`
				Model        string `json:"model"`
			} `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(spawnOut), &spawnResp); err != nil {
		t.Fatalf("invalid spawn JSON %q: %v", spawnOut, err)
	}
	if spawnResp.HookSpecificOutput.UpdatedInput.SubagentType != "explore" || spawnResp.HookSpecificOutput.UpdatedInput.Model != "haiku" {
		t.Fatalf("spawn hook dropped fields: %+v", spawnResp.HookSpecificOutput.UpdatedInput)
	}
	prompt := spawnResp.HookSpecificOutput.UpdatedInput.Prompt
	if !strings.Contains(prompt, "Parent session context:") ||
		!strings.Contains(prompt, "- edited: edited_main.go") ||
		!strings.Contains(prompt, "- verified: PATH=/opt/venv/bin:$PATH go vet ./...") ||
		strings.Contains(prompt, "- read: edited_main.go") ||
		!strings.Contains(prompt, "read_only.go") ||
		!strings.Contains(prompt, "extra.go") {
		t.Fatalf("unexpected spawn brief content:\n%s", prompt)
	}
	// Check byte bound of inner brief block inside <deja-recall>...</deja-recall>.
	_, afterBrief, hasBrief := strings.Cut(prompt, "Parent session context:")
	if !hasBrief {
		t.Fatalf("missing parent session context marker in prompt:\n%s", prompt)
	}
	inner := "Parent session context:" + afterBrief
	inner = strings.TrimSpace(strings.TrimSuffix(inner, recallFrameFooter))
	if len(inner) > coordBriefMaxBytes {
		t.Fatalf("parent brief exceeded %d bytes (%d): %q", coordBriefMaxBytes, len(inner), inner)
	}

	// Immediate OpenCode SessionStart for spawned child (+1s, <= 5s dedup window): must be deduplicated!
	*now = now.Add(1 * time.Second)
	dedupedStart := coordRunContextHook(t, dir, map[string]any{
		"hook_event_name":   "SessionStart",
		"session_id":        "child-oc-1",
		"parent_session_id": "parent-1",
		"cwd":               repo,
	})
	if strings.Contains(dedupedStart, "Parent session context:") {
		t.Fatalf("child start immediately after spawn brief should deduplicate, got %q", dedupedStart)
	}

	// Standalone child start outside 5s spawn window (e.g. Claude SubagentStart): delivers once under SubagentStart!
	*now = now.Add(10 * time.Second)
	subStart := coordRunContextHook(t, dir, map[string]any{
		"hook_event_name": "SubagentStart",
		"session_id":      "parent-1",
		"agent_id":        "agent-sub-2",
		"cwd":             repo,
	})
	var ctxResp sessionStartHookResponse
	if err := json.Unmarshal([]byte(subStart), &ctxResp); err != nil {
		t.Fatalf("expected valid SubagentStart JSON, got %q (%v)", subStart, err)
	}
	if ctxResp.HookSpecificOutput.HookEventName != "SubagentStart" ||
		!strings.Contains(ctxResp.HookSpecificOutput.AdditionalContext, "Parent session context:") {
		t.Fatalf("unexpected SubagentStart response: %+v", ctxResp)
	}

	// Repeating SubagentStart / SessionStart for the same child never double-briefs.
	repeatStart := coordRunContextHook(t, dir, map[string]any{
		"hook_event_name":   "SessionStart",
		"session_id":        "agent-sub-2",
		"parent_session_id": "parent-1",
		"cwd":               repo,
	})
	if strings.Contains(repeatStart, "Parent session context:") {
		t.Fatalf("child session should never be briefed twice, got %q", repeatStart)
	}
}

func TestCoordBriefByteCapAndDefusal(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	_ = withFrozenCoordClock(t, time.Date(2026, 5, 1, 16, 0, 0, 0, time.UTC))

	repo := filepath.Join(t.TempDir(), "mono")
	makeFakeGitRepo(t, repo)

	data := coordBriefData{
		Edits: []string{
			filepath.Join(repo, "pkg", "very_long_service_module_one_with_long_name.go"),
			filepath.Join(repo, "pkg", "very_long_service_module_two_with_long_name.go"),
			filepath.Join(repo, "pkg", "very_long_service_module_three_with_long_name.go"),
			filepath.Join(repo, "pkg", "very_long_service_module_four_with_long_name.go"),
			filepath.Join(repo, "pkg", "very_long_service_module_five_with_long_name.go"),
			filepath.Join(repo, "pkg", "very_long_service_module_six_with_long_name.go"),
		},
		Reads: []string{
			filepath.Join(repo, "internal", "alpha_beta_gamma_delta_file.go"),
			filepath.Join(repo, "internal", "epsilon_zeta_eta_theta_file.go"),
		},
		VerifyCmd: "pytest -k '@smoke /cmd' pkg/",
	}
	brief := formatParentSubagentBrief(repo, data)
	if len(brief) > coordBriefMaxBytes {
		t.Fatalf("brief exceeded %d bytes (%d):\n%s", coordBriefMaxBytes, len(brief), brief)
	}
	if strings.Contains(brief, "@smoke") || !strings.Contains(brief, "Parent session context:") {
		t.Fatalf("expected @trigger defusal inside brief, got:\n%s", brief)
	}
	_ = dir
}

func TestCoordAntigravityAndRecallOff(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	dir := index.DefaultDir()
	_ = withFrozenCoordClock(t, time.Date(2026, 5, 1, 17, 0, 0, 0, time.UTC))

	repo := filepath.Join(t.TempDir(), "ag-repo")
	makeFakeGitRepo(t, repo)
	oldFile := filepath.Join(repo, "old.go")
	curFile := filepath.Join(repo, "current.go")

	steps := []transcriptStep{
		// Index 0: newest step (handled by editFileCallLines -> runHookTool).
		{
			Type: "PLANNER_RESPONSE",
			ToolCalls: []any{
				map[string]any{"name": "read_file", "args": map[string]any{"AbsolutePath": curFile}},
			},
		},
		// Older step: verified command + prior read.
		{
			Type:    "PLANNER_RESPONSE",
			Content: "All checks passed",
			ToolCalls: []any{
				map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": "go test ./pkg/..."}},
				map[string]any{"name": "read_file", "args": map[string]any{"AbsolutePath": oldFile}},
			},
		},
	}

	// Another session edited current.go externally.
	_ = coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": curFile},
		"session_id":      "other-ag-sess",
		"cwd":             repo,
	})

	coordRecordAntigravitySteps(dir, repo, "ag-conv-1", steps)

	// Newest step's read_file sent by editFileCallLines MUST still detect external collision!
	agWarn := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "read_file",
		"tool_input":      map[string]any{"file_path": curFile},
		"session_id":      "ag-conv-1",
		"cwd":             repo,
	})
	if !strings.Contains(agWarn, "parallel edit radar: current.go was edited") {
		t.Fatalf("Antigravity newest step should warn on external edit, got %q", agWarn)
	}

	// Verify older step's read & command populated ag-conv-1's brief.
	briefData := gatherParentBriefData(dir, "ag-conv-1", "", repo)
	if briefData.VerifyCmd != "go test ./pkg/..." || len(briefData.Reads) < 2 {
		t.Fatalf("expected Antigravity older step read+verify in brief, got %+v", briefData)
	}

	// DEJA_RECALL=off suppresses all coordination completely.
	t.Setenv("DEJA_RECALL", "off")
	offDir := filepath.Join(t.TempDir(), "index.db")
	_ = coordRunToolHook(t, offDir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": curFile},
		"session_id":      "off-1",
		"cwd":             repo,
	})
	if out := coordRunToolHook(t, offDir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": curFile},
		"session_id":      "off-2",
		"cwd":             repo,
	}); out != "" {
		t.Fatalf("DEJA_RECALL=off must silence radar, got %q", out)
	}
	if _, statErr := os.Stat(coordPath(offDir)); !os.IsNotExist(statErr) {
		t.Fatalf("DEJA_RECALL=off must not write .coord, got err=%v", statErr)
	}
}

func TestCoordComposesWithFileRecallAndRespectsPolicy(t *testing.T) {
	dir := seedEditHistory(t)
	hostsNoWarmup(t)
	_ = withFrozenCoordClock(t, time.Date(2026, 5, 1, 18, 0, 0, 0, time.UTC))

	// Session 1 edits config.go.
	_ = coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": "config.go"},
		"session_id":      "sess_1",
		"cwd":             "/work/alpha",
	})

	// Session 2 editing config.go gets BOTH historical recall ("sentinel") and live parallel edit radar.
	got := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": "config.go"},
		"session_id":      "sess_2",
		"cwd":             "/work/alpha",
	})
	if !strings.Contains(got, "sentinel") {
		t.Fatalf("expected historical file decision in hook-tool output, got %q", got)
	}
	if !strings.Contains(got, "parallel edit radar:") {
		t.Fatalf("expected parallel edit radar alongside file decision, got %q", got)
	}

	// Policy disabling auto local activation silences .coord radar and writes.
	polDir := t.TempDir()
	polFile := filepath.Join(polDir, "policy.json")
	if err := os.WriteFile(polFile, []byte(`{"activations":{"auto":{"local":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", polFile)
	deniedStore := filepath.Join(t.TempDir(), "index.db")
	_ = coordRunToolHook(t, deniedStore, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": "/work/alpha/main.go"},
		"session_id":      "pol-1",
		"cwd":             "/work/alpha",
	})
	if warn := coordRunToolHook(t, deniedStore, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": "/work/alpha/main.go"},
		"session_id":      "pol-2",
		"cwd":             "/work/alpha",
	}); warn != "" {
		t.Fatalf("auto local=false policy must silence radar, got %q", warn)
	}
	if _, statErr := os.Stat(coordPath(deniedStore)); !os.IsNotExist(statErr) {
		t.Fatalf("auto local=false policy must not write .coord, got err=%v", statErr)
	}

	// Ignored directory policy silences .coord radar for paths under the ignored pattern.
	if err := os.WriteFile(polFile, []byte(`{"ignore":["*/scratch-eval/*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ignoredStore := filepath.Join(t.TempDir(), "index.db")
	_ = coordRunToolHook(t, ignoredStore, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": "/work/scratch-eval/main.go"},
		"session_id":      "ign-1",
		"cwd":             "/work/scratch-eval",
	})
	if warn := coordRunToolHook(t, ignoredStore, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": "/work/scratch-eval/main.go"},
		"session_id":      "ign-2",
		"cwd":             "/work/scratch-eval",
	}); warn != "" {
		t.Fatalf("ignored directory policy must silence radar, got %q", warn)
	}
	if _, statErr := os.Stat(coordPath(ignoredStore)); !os.IsNotExist(statErr) {
		t.Fatalf("ignored directory policy must not write .coord, got err=%v", statErr)
	}
}

func TestSplitShellAndGitInvocationWindowsPaths(t *testing.T) {
	segs := splitShellSegments(`cd C:\Users\alex\alpha-svc && git commit -m wip`)
	if len(segs) != 2 || segs[0] != `cd C:\Users\alex\alpha-svc` || segs[1] != "git commit -m wip" {
		t.Fatalf("unexpected segments: %q", segs)
	}
	cdFields := splitShellFields(segs[0])
	if len(cdFields) != 2 || cdFields[0] != "cd" || cdFields[1] != `C:\Users\alex\alpha-svc` {
		t.Fatalf("lost Windows backslashes in cd fields: %q", cdFields)
	}

	cFields := splitShellFields(`git -C C:\Users\alex\alpha-svc add main.go`)
	dirFlag, sub, _, ok := parseGitInvocation(cFields)
	if !ok || dirFlag != `C:\Users\alex\alpha-svc` || sub != "add" {
		t.Fatalf("parseGitInvocation -C Windows path failed: ok=%v dir=%q sub=%q fields=%q", ok, dirFlag, sub, cFields)
	}

	quotedFields := splitShellFields(`"C:\Program Files\Git\bin\git.exe" -C "C:\Users\alex\alpha svc" push origin main`)
	dirFlag, sub, _, ok = parseGitInvocation(quotedFields)
	if !ok || dirFlag != `C:\Users\alex\alpha svc` || sub != "push" {
		t.Fatalf("parseGitInvocation git.exe quoted Windows path failed: ok=%v dir=%q sub=%q fields=%q", ok, dirFlag, sub, quotedFields)
	}

	uncFields := splitShellFields(`git -C \\203.0.113.10\share\alpha-svc checkout -b fix`)
	dirFlag, sub, _, ok = parseGitInvocation(uncFields)
	if !ok || dirFlag != `\\203.0.113.10\share\alpha-svc` || sub != "checkout" {
		t.Fatalf("parseGitInvocation UNC path failed: ok=%v dir=%q sub=%q fields=%q", ok, dirFlag, sub, uncFields)
	}
}

func TestCoordSkipsMCPHelperSessionsAndPlainInputField(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	hostsNoWarmup(t)
	_ = withFrozenCoordClock(t, time.Date(2026, 5, 1, 18, 0, 0, 0, time.UTC))

	// Ephemeral helper sessions (*-mcp-*) must never write .coord or fire radar.
	_ = coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": "config.go"},
		"session_id":      "helper-mcp-001",
		"cwd":             "/work/alpha",
	})
	if got := coordRunToolHook(t, dir, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": "config.go"},
		"session_id":      "sess_main",
		"cwd":             "/work/alpha",
	}); got != "" {
		t.Fatalf("expected no radar warning from helper-mcp session, got %q", got)
	}

	// Non-patch tools carrying "input" must not be treated as patches.
	searchStore := filepath.Join(t.TempDir(), "index2.db")
	_ = coordRunToolHook(t, searchStore, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "search",
		"tool_input":      map[string]any{"input": "plain search query for config.go", "file_path": "config.go"},
		"session_id":      "sess_search",
		"cwd":             "/work/alpha",
	})
	if _, err := os.Stat(coordPath(searchStore)); !os.IsNotExist(err) {
		t.Fatalf("plain input search tool must not write .coord, got err=%v", err)
	}

	// Real apply_patch carrying patch text in "input" still records the edited path.
	_ = coordRunToolHook(t, searchStore, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "apply_patch",
		"tool_input":      map[string]any{"input": "*** Begin Patch\n*** Update File: config.go\n@@ -1 +1 @@\n-old\n+new\n*** End Patch\n"},
		"session_id":      "sess_patch_1",
		"cwd":             "/work/alpha",
	})
	if warn := coordRunToolHook(t, searchStore, map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": "config.go"},
		"session_id":      "sess_patch_2",
		"cwd":             "/work/alpha",
	}); !strings.Contains(warn, "parallel edit radar:") {
		t.Fatalf("expected parallel edit radar after apply_patch input, got %q", warn)
	}
}
