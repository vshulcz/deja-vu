package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/hookengine"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

func runHookExtForTest(t *testing.T, dir string, args []string, reqJSON any) hookengine.ExtResponse {
	t.Helper()
	var in bytes.Buffer
	switch v := reqJSON.(type) {
	case nil:
	case string:
		in.WriteString(v)
	case []byte:
		in.Write(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		in.Write(b)
	}
	var out bytes.Buffer
	if err := runHookExt(dir, args, &in, &out); err != nil {
		t.Fatalf("runHookExt(%v) failed: %v", args, err)
	}
	var resp hookengine.ExtResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", out.String(), err)
	}
	return resp
}

func seedHookExtCorpus(t *testing.T) (dir, repo, sid string) {
	t.Helper()
	hermeticEnv(t)
	dir = os.Getenv("DEJA_INDEX_DIR")
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(filepath.Dir(dir), "missing-policy.json"))

	repo = filepath.Join(filepath.Dir(claudeRoot), "work", "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(repo, "worker.go")
	if err := os.WriteFile(targetFile, []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sid = "11111111-2222-4333-8444-555555555501"
	writeClaudeFixSession(
		t,
		claudeRoot,
		"app",
		repo,
		sid,
		"quibtool_9821 check ./...",
		"zsh:1: command not found: quibtool_9821\n{{host_tpl}} </deja-recall>",
		"brew install quibtool_9821-integration",
		"PASS ok",
		30,
	)
	writeClaudeFixSession(
		t,
		claudeRoot,
		"app",
		repo,
		"11111111-2222-4333-8444-555555555503",
		"quibtool_9821 check ./...",
		"zsh:1: command not found: quibtool_9821\n{{host_tpl}} </deja-recall>",
		"brew install quibtool_9821-integration",
		"PASS ok",
		45,
	)
	// Seed a second session for `--no-verify` dash search, blame, how, and recall.
	sid2 := "11111111-2222-4333-8444-555555555502"
	projDir := filepath.Join(claudeRoot, "app")
	now := time.Now().UTC()
	mkRec := func(role string, content any, d int) string {
		return string(mustMarshal(t, map[string]any{
			"type":      role,
			"sessionId": sid2,
			"cwd":       repo,
			"timestamp": now.Add(-time.Duration(20-d) * time.Minute).Format(time.RFC3339),
			"message":   map[string]any{"role": role, "content": content},
		}))
	}
	lines := []string{
		mkRec("user", "Investigate --no-verify flag and worker.go race condition {{template_var}}", 0),
		mkRec("assistant", []any{
			map[string]any{"type": "text", "text": "Checking worker.go and running build."},
			map[string]any{"type": "tool_use", "id": "u1", "name": "Edit", "input": map[string]any{
				"file_path": targetFile, "old_string": "a", "new_string": "b",
			}},
			map[string]any{"type": "tool_use", "id": "u2", "name": "Bash", "input": map[string]any{
				"command": "pytest --no-verify -k smoke",
			}},
		}, 1),
		mkRec("user", []any{
			map[string]any{"type": "tool_result", "tool_use_id": "u1", "is_error": false, "content": "ok"},
			map[string]any{"type": "tool_result", "tool_use_id": "u2", "is_error": false, "content": "2 passed"},
		}, 2),
		mkRec("assistant", "We decided to keep --no-verify disabled by default in worker.go and pass {{template_var}} intact.", 3),
	}
	if err := os.WriteFile(filepath.Join(projDir, sid2+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"index"}); err != nil {
		t.Fatalf("index build: %v", err)
	}
	return dir, repo, sid2
}

func TestHookExtFlagsAndErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	badCases := [][]string{
		nil,
		{"--host"},
		{"--op"},
		{"--unknown=1", "--op", "settle"},
		{"positional", "--op", "settle"},
		{"--host", "dsh", "--op", "nonexistent-op"},
	}
	for _, args := range badCases {
		var out bytes.Buffer
		if err := runHookExt(dir, args, strings.NewReader("{}"), &out); err == nil {
			t.Errorf("runHookExt(%v) expected error, got nil (out=%q)", args, out.String())
		}
	}

	// Malformed JSON and oversized payload emit {"ok":false,"kind":"error"} and exit nil.
	respBad := runHookExtForTest(t, dir, []string{"--op=settle", "--host=opencode"}, "{not-json")
	if respBad.Ok || respBad.Kind != "error" || respBad.Error == "" {
		t.Fatalf("malformed JSON resp = %+v", respBad)
	}

	oversize := bytes.Repeat([]byte("x"), hookengine.MaxExtRequestBytes+5)
	respBig := runHookExtForTest(t, dir, []string{"--op", "settle"}, oversize)
	if respBig.Ok || respBig.Kind != "error" || !strings.Contains(respBig.Error, "exceeds") {
		t.Fatalf("oversize resp = %+v", respBig)
	}
}

func TestHookExtAccessControl(t *testing.T) {
	dir, _, _ := seedHookExtCorpus(t)
	polPath := filepath.Join(filepath.Dir(dir), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", polPath)
	t.Setenv("DEJA_CLIENT", "rogue-client")
	polJSON := `{"require_clients": true, "clients": {"trusted": {"read": true}}}`
	if err := os.WriteFile(polPath, []byte(polJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// Hook ops fail open cleanly (`ok: true`, empty context).
	for _, op := range []string{"context", "prompt", "warmup-status"} {
		resp := runHookExtForTest(t, dir, []string{"--host", "opencode", "--op", op}, hookengine.ExtRequest{SessionID: "s-deny", Prompt: "worker"})
		if !resp.Ok || resp.Context != "" || resp.Output != "" {
			t.Fatalf("denied hook op %s resp = %+v", op, resp)
		}
	}

	// CLI ops (`tool`, `slash`) surface an explicit error envelope without falling back.
	toolResp := runHookExtForTest(t, dir, []string{"--host", "openclaw", "--op", "tool"}, hookengine.ExtRequest{
		Tool:  "deja_recall",
		Query: "worker",
	})
	if toolResp.Ok || toolResp.Kind != "error" || toolResp.Error == "" {
		t.Fatalf("denied tool resp = %+v", toolResp)
	}

	slashResp := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "slash"}, hookengine.ExtRequest{
		Args: "worker",
	})
	if slashResp.Ok || slashResp.Kind != "error" || slashResp.Error == "" {
		t.Fatalf("denied slash resp = %+v", slashResp)
	}
}

func TestHookExtHookOperations(t *testing.T) {
	dir, repo, sid := seedHookExtCorpus(t)
	if err := os.WriteFile(filepath.Join(repo, ".deja.md"), []byte("# repo notes\n- always keep --no-verify documented\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. context (`openclaw` populates Context/Receipt + plain + silent + notes_only)
	usage.RecordResult(dir, usage.KindHook, 900, 2, false)
	week := strconv.FormatInt(time.Now().Add(-8*24*time.Hour).Unix(), 10)
	if err := os.WriteFile(dir+".weeknote", []byte(week), 0o600); err != nil {
		t.Fatal(err)
	}
	ctxResp := runHookExtForTest(t, dir, []string{"--host", "openclaw", "--op", "context"}, hookengine.ExtRequest{
		SessionID: "openclaw-s1",
		Cwd:       repo,
		Once:      true,
	})
	if !ctxResp.Ok || ctxResp.Context == "" || !strings.Contains(ctxResp.Note, "deja") {
		t.Fatalf("openclaw context resp = %+v", ctxResp)
	}

	// Notes-only drain works without returning digest.
	if err := os.WriteFile(dir+".weeknote", []byte(week), 0o600); err != nil {
		t.Fatal(err)
	}
	notesResp := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "context"}, hookengine.ExtRequest{
		NotesOnly: true,
	})
	if !notesResp.Ok || notesResp.Kind != "notes" || notesResp.Note == "" || notesResp.Context != "" {
		t.Fatalf("notes_only resp = %+v", notesResp)
	}

	// 2. prompt (with DSH template neutralization)
	promptResp := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "prompt"}, hookengine.ExtRequest{
		SessionID: "dsh-s1",
		Cwd:       repo,
		Prompt:    "check ERR_EXT_CANARY_9821 and {{template_var}} in worker.go",
	})
	if !promptResp.Ok {
		t.Fatalf("prompt resp = %+v", promptResp)
	}
	if strings.Contains(promptResp.Context, "{{") {
		t.Fatalf("prompt context still contains raw template braces: %q", promptResp.Context)
	}

	// 3. tool-before: plain file hook AND finalizeExtHookReply spawn updatedInput decoding.
	tbResp := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "tool-before"}, hookengine.ExtRequest{
		SessionID: "dsh-s1",
		Cwd:       repo,
		Tool:      "read",
		Plain:     true,
		ToolInput: map[string]any{"file_path": filepath.Join(repo, "worker.go")},
	})
	if !tbResp.Ok {
		t.Fatalf("tool-before resp = %+v", tbResp)
	}

	spawnRaw := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":{"prompt":"do task\n\n<deja-recall>prior</deja-recall>","subagent_type":"explore"}}}`
	fin := finalizeExtHookReply("opencode", hookengine.ExtRequest{}, spawnRaw)
	if !fin.Ok || fin.Kind != "hook" || fin.Decision != "allow" || fin.UpdatedInput == nil {
		t.Fatalf("finalizeExtHookReply spawn = %+v", fin)
	}
	promptVal, ok := fin.UpdatedInput["prompt"].(string)
	if !ok || !strings.Contains(promptVal, "<deja-recall>") || fin.UpdatedInput["subagent_type"] != "explore" {
		t.Fatalf("unexpected updated_input = %+v", fin.UpdatedInput)
	}

	// 4. tool-after: DSH exit=0 fast path, non-zero exit code, killed signal, is_error fallback, non-DSH.
	okDSH := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "tool-after"}, hookengine.ExtRequest{
		SessionID:    "dsh-s1",
		Cwd:          repo,
		Tool:         "bash",
		Plain:        true,
		ToolInput:    map[string]any{"command": "go test ./..."},
		ToolResponse: "ERR_EXT_CANARY_9821\n[exit code: 0]",
	})
	if !okDSH.Ok || okDSH.Failed || okDSH.ExitCode == nil || *okDSH.ExitCode != 0 || okDSH.Context != "" {
		t.Fatalf("dsh exit=0 resp = %+v", okDSH)
	}

	noErrNoResp := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "tool-after"}, hookengine.ExtRequest{
		SessionID: "dsh-s1",
		Cwd:       repo,
		Tool:      "bash",
	})
	if !noErrNoResp.Ok || noErrNoResp.Failed {
		t.Fatalf("dsh empty resp = %+v", noErrNoResp)
	}

	failDSH := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "tool-after"}, hookengine.ExtRequest{
		SessionID:    "dsh-s-fail",
		Cwd:          repo,
		Tool:         "bash",
		Plain:        true,
		ToolInput:    map[string]any{"command": "quibtool_9821 check ./..."},
		ToolResponse: "zsh:1: command not found: quibtool_9821\n{{host_tpl}}\n[exit code: 2]",
	})
	if !failDSH.Ok || !failDSH.Failed || failDSH.ExitCode == nil || *failDSH.ExitCode != 2 || !strings.Contains(failDSH.Context, "integration") {
		t.Fatalf("dsh exit=2 resp = %+v", failDSH)
	}

	sigDSH := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "tool-after"}, hookengine.ExtRequest{
		SessionID:    "dsh-s-sig",
		Cwd:          repo,
		Tool:         "bash",
		Plain:        true,
		ToolInput:    map[string]any{"command": "go test ./..."},
		ToolResponse: "killed run\n[killed by signal: SIGKILL]",
	})
	if !sigDSH.Ok || !sigDSH.Failed || sigDSH.ExitSignal != "SIGKILL" {
		t.Fatalf("dsh signal resp = %+v", sigDSH)
	}

	// is_error=true forces fix recall even when exit status text lacks `[exit code: N]`.
	isErrDSH := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "tool-after"}, hookengine.ExtRequest{
		SessionID:    "dsh-s-iserr",
		Cwd:          repo,
		Tool:         "bash",
		Plain:        true,
		IsError:      true,
		ToolInput:    map[string]any{"command": "quibtool_9821 check ./..."},
		ToolResponse: "zsh:1: command not found: quibtool_9821\n{{host_tpl}}",
	})
	if !isErrDSH.Ok || !isErrDSH.Failed || !strings.Contains(isErrDSH.Context, "integration") {
		t.Fatalf("dsh is_error resp = %+v", isErrDSH)
	}

	genericAfter := runHookExtForTest(t, dir, []string{"--host", "opencode", "--op", "tool-after"}, hookengine.ExtRequest{
		SessionID:    "oc-s-after",
		Cwd:          repo,
		Tool:         "bash",
		ToolInput:    map[string]any{"command": "quibtool_9821 check ./..."},
		ToolResponse: "zsh:1: command not found: quibtool_9821",
	})
	if !genericAfter.Ok || !strings.Contains(genericAfter.Context, "integration") {
		t.Fatalf("generic tool-after resp = %+v", genericAfter)
	}

	// 5. precompact preserves transcript_path and bounds multi-byte runes safely.
	longBody := strings.Repeat("🦀", 4005)
	trFile := filepath.Join(repo, "transcript-marker.jsonl")
	rawPre := `{"session_id":"oc-compact","cwd":` + string(mustMarshal(t, repo)) + `,"transcript_path":` + string(mustMarshal(t, trFile)) + `,"source":"openclaw","messages":[{"role":"user","content":` + string(mustMarshal(t, longBody)) + `}]}`
	preResp := runHookExtForTest(t, dir, []string{"--host", "openclaw", "--op", "precompact"}, rawPre)
	if !preResp.Ok || len(preResp.Messages) != 1 || preResp.Dropped != 0 {
		t.Fatalf("precompact resp = %+v", preResp)
	}
	msgMap, ok := preResp.Messages[0].(map[string]any)
	if !ok {
		t.Fatalf("expected map message, got %T", preResp.Messages[0])
	}
	msgContent, ok := msgMap["content"].(string)
	if !ok || len([]rune(msgContent)) != 4000 {
		t.Fatalf("precompact content = %T (runes=%d), want 4000 runes", msgMap["content"], len([]rune(msgContent)))
	}

	// 6. session-end clears cleanly.
	seResp := runHookExtForTest(t, dir, []string{"--host", "opencode", "--op", "session-end"}, hookengine.ExtRequest{
		SessionID: sid,
		Cwd:       repo,
		Reason:    "complete",
	})
	if !seResp.Ok {
		t.Fatalf("session-end resp = %+v", seResp)
	}

	// 7. statusline + warmup-status
	stResp := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "statusline"}, hookengine.ExtRequest{})
	if !stResp.Ok || stResp.Kind != "status" || stResp.Output == "" {
		t.Fatalf("statusline resp = %+v", stResp)
	}

	wsEmpty := runHookExtForTest(t, dir, []string{"--host", "opencode", "--op", "warmup-status"}, hookengine.ExtRequest{})
	if !wsEmpty.Ok || wsEmpty.Output != "" {
		t.Fatalf("empty warmup-status resp = %+v", wsEmpty)
	}
	wsData := warmupStatus{Phase: "indexing", Done: 3, Total: 10, Started: time.Now().UnixNano(), Updated: time.Now().UnixNano()}
	if err := os.WriteFile(warmupStatusPath(dir), mustMarshal(t, wsData), 0o600); err != nil {
		t.Fatal(err)
	}
	wsActive := runHookExtForTest(t, dir, []string{"--host", "opencode", "--op", "warmup-status"}, hookengine.ExtRequest{})
	if !wsActive.Ok || !strings.Contains(wsActive.Output, "indexing 30%") {
		t.Fatalf("active warmup-status resp = %+v", wsActive)
	}
	_ = os.Remove(warmupStatusPath(dir))
	_ = index.HasManifest(dir)
}

func TestHookExtToolsRememberSlashSettle(t *testing.T) {
	dir, repo, sid := seedHookExtCorpus(t)
	workerFile := filepath.Join(repo, "worker.go")

	// Empty remember errors with ok:false.
	remEmpty := runHookExtForTest(t, dir, []string{"--op", "remember"}, hookengine.ExtRequest{})
	if remEmpty.Ok || remEmpty.Kind != "error" {
		t.Fatalf("empty remember resp = %+v", remEmpty)
	}

	remOK := runHookExtForTest(t, dir, []string{"--op", "remember"}, hookengine.ExtRequest{
		Content: "Decision: always pin worker cache TTL to 300s",
		Tags:    []string{"cache"},
		Project: "app",
	})
	if !remOK.Ok || remOK.Kind != "success" || remOK.Output == "" {
		t.Fatalf("remember resp = %+v", remOK)
	}

	// recall: dash-prefixed query, DSH neutralization, empty error.
	recDash := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "tool"}, hookengine.ExtRequest{
		Tool:  "deja_recall",
		Query: "--no-verify",
		Limit: 25,
	})
	if !recDash.Ok || recDash.Kind != "success" || !strings.Contains(recDash.Output, "worker.go") {
		t.Fatalf("recall dash resp = %+v", recDash)
	}
	if strings.Contains(recDash.Output, "{{") {
		t.Fatalf("dsh recall kept double braces: %q", recDash.Output)
	}

	recEmpty := runHookExtForTest(t, dir, []string{"--host", "opencode", "--op", "tool"}, hookengine.ExtRequest{
		Tool: "recall",
	})
	if recEmpty.Ok || recEmpty.Kind != "error" {
		t.Fatalf("empty recall resp = %+v", recEmpty)
	}

	// fix, blame (normal + json), how, ctx, remember via tool, unknown tool.
	fixResp := runHookExtForTest(t, dir, []string{"--host", "openclaw", "--op", "tool"}, hookengine.ExtRequest{
		Tool:      "deja_fix",
		ToolInput: map[string]any{"error": "quibtool_9821"},
	})
	if !fixResp.Ok || fixResp.Kind != "success" {
		t.Fatalf("fix resp = %+v", fixResp)
	}

	blameResp := runHookExtForTest(t, dir, []string{"--host", "openclaw", "--op", "tool"}, hookengine.ExtRequest{
		Tool:  "deja_blame",
		Mode:  "json",
		Query: workerFile,
	})
	if !blameResp.Ok || blameResp.Kind != "success" || blameResp.Output == "" {
		t.Fatalf("blame json resp = %+v", blameResp)
	}

	howResp := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "tool"}, hookengine.ExtRequest{
		Tool:  "deja_how",
		Query: "pytest",
	})
	if !howResp.Ok || howResp.Kind != "success" {
		t.Fatalf("how resp = %+v", howResp)
	}

	ctxResp := runHookExtForTest(t, dir, []string{"--host", "hermes", "--op", "tool"}, hookengine.ExtRequest{
		Tool:  "recall_context",
		Query: sid,
	})
	if !ctxResp.Ok || ctxResp.Kind != "success" || !strings.Contains(ctxResp.Output, "worker.go") {
		t.Fatalf("ctx resp = %+v", ctxResp)
	}

	toolRem := runHookExtForTest(t, dir, []string{"--host", "hermes", "--op", "tool"}, hookengine.ExtRequest{
		Tool:      "deja_remember",
		ToolInput: map[string]any{"text": "Always inspect stderr tail first"},
	})
	if !toolRem.Ok || toolRem.Kind != "success" {
		t.Fatalf("tool remember resp = %+v", toolRem)
	}

	toolUnk := runHookExtForTest(t, dir, []string{"--op", "tool"}, hookengine.ExtRequest{Tool: "deja_nope", Query: "x"})
	if toolUnk.Ok || toolUnk.Kind != "error" {
		t.Fatalf("unknown tool resp = %+v", toolUnk)
	}

	// slash: bad syntax + valid dash query scoped to session.
	slashBad := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "slash"}, hookengine.ExtRequest{Args: "--session"})
	if slashBad.Ok || slashBad.Kind != "error" {
		t.Fatalf("slash bad resp = %+v", slashBad)
	}
	slashOK := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "slash"}, hookengine.ExtRequest{
		Args: "--session " + sid + " -- --no-verify",
	})
	if !slashOK.Ok || slashOK.Kind != "success" || !strings.Contains(slashOK.Output, "worker.go") {
		t.Fatalf("slash ok resp = %+v", slashOK)
	}

	// settle: opencode / dsh / hermes
	settleDSH := runHookExtForTest(t, dir, []string{"--host", "dsh", "--op", "settle"}, hookengine.ExtRequest{
		Text:   "<deja-recall>\"foo\" {{x}}</deja-recall>",
		Prompt: "already mentioned foo",
	})
	if !settleDSH.Ok || strings.Contains(settleDSH.Text, "{{") {
		t.Fatalf("settle dsh resp = %+v", settleDSH)
	}

	settleHermesHold := runHookExtForTest(t, dir, []string{"--host", "hermes", "--op", "settle"}, hookengine.ExtRequest{
		Notes: []string{"deja-vu — 2 sessions failed: `deja doctor`"},
	})
	if !settleHermesHold.Ok || settleHermesHold.Note != "" || len(settleHermesHold.HeldNotes) != 1 {
		t.Fatalf("settle hermes hold resp = %+v", settleHermesHold)
	}
	settleHermesFlush := runHookExtForTest(t, dir, []string{"--host", "hermes", "--op", "settle"}, hookengine.ExtRequest{
		Text:      "recalled block",
		HeldNotes: settleHermesHold.HeldNotes,
		Notes:     []string{`deja — opencode: "work"`},
	})
	if !settleHermesFlush.Ok || settleHermesFlush.Note == "" || len(settleHermesFlush.HeldNotes) != 0 || !strings.Contains(settleHermesFlush.IndicatorLabel, "deja") {
		t.Fatalf("settle hermes flush resp = %+v", settleHermesFlush)
	}
}
