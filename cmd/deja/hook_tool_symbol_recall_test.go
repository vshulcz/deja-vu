package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestWave3HookToolSymbolLevelDecisionRecallAndPriority(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	repoDir := filepath.Join(tmp, "work")
	targetFile := filepath.Join(repoDir, "pkg", "worker", "pool.go")
	if err := os.MkdirAll(filepath.Dir(targetFile), 0o755); err != nil {
		t.Fatal(err)
	}
	fileSrc := `package worker

func AcquireSlot(n int) error {
	if n <= 0 {
		return nil
	}
	return nil
}

func ClosePool() error {
	drainInFlightQueue()
	return nil
}
`
	if err := os.WriteFile(targetFile, []byte(fileSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Older session s1 recorded a decision about ClosePool without naming pool.go or worker.
	// Newer session s2 recorded a decision about AcquireSlot without naming pool.go or worker.
	sessions := []struct {
		id       string
		day      int
		decision string
	}{
		{"s1", 1, "Decision: ClosePool must drain in-flight jobs before closing the channel."},
		{"s2", 2, "Decision: AcquireSlot rejects non-positive slot counts immediately."},
	}
	for _, s := range sessions {
		ts := time.Date(2026, 9, s.day, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		lines := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"update worker"}}`, s.id, repoDir, ts),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"u1","name":"Edit","input":{"file_path":%q,"old_string":"a","new_string":"b"}},{"type":"text","text":%q}]}}`, s.id, repoDir, ts, targetFile, s.decision),
		}
		if err := os.WriteFile(filepath.Join(proj, s.id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// 1. Editing inside ClosePool (even when the edit snippet only mentions drainInFlightQueue,
	// which is enclosed by ClosePool on disk) prioritizes the ClosePool decision over the newer AcquireSlot decision.
	payload := map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_input": map[string]any{
			"file_path":  targetFile,
			"old_string": "drainInFlightQueue()",
			"new_string": "drainInFlightQueueWithTimeout()",
		},
		"session_id": "live-sym",
		"cwd":        repoDir,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runHookTool(indexDir, strings.NewReader(string(raw)), &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "ClosePool must drain in-flight jobs") {
		t.Fatalf("expected symbol-targeted decision for ClosePool, got: %q", stdout.String())
	}

	// 2. Reading the file without an edit snippet still matches the newest symbol decision declared in the file,
	// while skipping the live session itself.
	line := fileHookLineOutside(indexDir, repoDir, targetFile, func(m index.SessionMeta, _ string) bool {
		return m.ID == "live-sym"
	})
	if !strings.Contains(line, "AcquireSlot rejects non-positive slot counts") {
		t.Fatalf("expected file-declared symbol decision for AcquireSlot, got: %q", line)
	}
}

func TestWave3FileHookRanLinePrefersTargetedTestCommand(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Three sessions ran `make test` (repo-wide sweep), while two sessions ran
	// `go test ./internal/billing -run TestInvoice` (targeted to pkg/stem).
	for i, id := range []string{"r1", "r2", "r3"} {
		ts := time.Date(2026, 9, 10+i, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work","timestamp":"` + ts + `","message":{"role":"user","content":"update internal/billing/invoice.go"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":"/work/internal/billing/invoice.go","old_string":"a","new_string":"b"}},{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"make test"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"PASS\nok\tgithub.com/acme/work\t0.1s"}]}}`,
		}
		if i < 2 {
			lines = append(lines,
				`{"type":"assistant","sessionId":"`+id+`","cwd":"/work","timestamp":"`+ts+`","message":{"role":"assistant","content":[{"type":"tool_use","id":"b2","name":"Bash","input":{"command":"go test ./internal/billing -run TestInvoice"}}]}}`,
				`{"type":"user","sessionId":"`+id+`","cwd":"/work","timestamp":"`+ts+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b2","content":"PASS\nok\tgithub.com/acme/work/internal/billing\t0.02s"}]}}`,
			)
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	got := fileHookLineOutside(indexDir, "/work", "/work/internal/billing/invoice.go", nil)
	if !strings.Contains(got, "go test ./internal/billing -run TestInvoice") {
		t.Fatalf("expected targeted test command to outrank repo-wide make test, got: %q", got)
	}
}

func TestWave3PreFlightCommandFailureIncludesConfirmedRemedy(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)
	t.Setenv("DEJA_COMMAND_HINTS", "on")

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"cf-1", "cf-2"} {
		ts := time.Date(2026, 9, 15+i, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"user","content":"run build"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"make build"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","is_error":true,"content":"panic: sql: database is closed"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"c2","name":"Bash","input":{"command":"make clean && make CGO_ENABLED=0 build"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c2","is_error":false,"content":"ok"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"assistant","content":"that fixed the build"}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(indexDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	line := commandFailureLine(indexDir, "/work/app", "make build")
	if !strings.Contains(line, "panic: sql: database is closed") {
		t.Fatalf("expected failure warning in commandFailureLine, got: %q", line)
	}
	if !strings.Contains(line, "next command that worked: make clean && make CGO_ENABLED=0 build") {
		t.Fatalf("expected confirmed remedy command in pre-flight commandFailureLine, got: %q", line)
	}
}

func TestWave3LiveSessionStatuslineAndToolReceiptAttribution(t *testing.T) {
	seedFixPair(t, "panic: sql: database is closed", "make clean && make CGO_ENABLED=0")
	dir := os.Getenv("DEJA_INDEX_DIR")

	// 1. PostToolUseFailure emits systemMessage receipt for Claude Code, and omits it for Cursor.
	claudePayload := mustMarshal(t, map[string]any{
		"hook_event_name": "PostToolUseFailure",
		"session_id":      "claude-live-1",
		"cwd":             "/work/app",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "make"},
		"error":           "panic: sql: database is closed",
	})
	var claudeOut bytes.Buffer
	if err := runHookToolAfterMode(dir, bytes.NewReader(claudePayload), &claudeOut, false); err != nil {
		t.Fatal(err)
	}
	var claudeResp sessionStartHookResponse
	if err := json.Unmarshal(claudeOut.Bytes(), &claudeResp); err != nil {
		t.Fatalf("unmarshal claude response: %v (%q)", err, claudeOut.String())
	}
	if !strings.Contains(claudeResp.SystemMessage, "deja:") || !strings.Contains(claudeResp.SystemMessage, "make clean && make CGO_ENABLED=0") {
		t.Fatalf("expected visible SystemMessage receipt on Claude PostToolUseFailure, got: %q", claudeResp.SystemMessage)
	}

	cursorPayload := mustMarshal(t, map[string]any{
		"cursor_version":  "1.0.0",
		"conversation_id": "cursor-live-1",
		"hook_event_name": "postToolUseFailure",
		"cwd":             "/work/app",
		"tool_name":       "Shell",
		"tool_input":      map[string]any{"command": "make"},
		"error_message":   "panic: sql: database is closed",
	})
	var cursorOut bytes.Buffer
	if err := runHookToolAfterMode(dir, bytes.NewReader(cursorPayload), &cursorOut, false); err != nil {
		t.Fatal(err)
	}
	var cursorResp sessionStartHookResponse
	if err := json.Unmarshal(cursorOut.Bytes(), &cursorResp); err != nil {
		t.Fatalf("unmarshal cursor response: %v (%q)", err, cursorOut.String())
	}
	if cursorResp.SystemMessage != "" {
		t.Fatalf("expected empty SystemMessage for Cursor, got: %q", cursorResp.SystemMessage)
	}

	// 2. Brand-new live session that triggers a PreToolUse file injection immediately
	// surfaces file memory on statusline before the new session is indexed in manifest.gob.
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	for i, id := range []string{"hist-1", "hist-2"} {
		ts := time.Date(2026, 9, 20+i, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		lines := []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"user","content":"refine router.go"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"u1","name":"Edit","input":{"file_path":"/work/app/router.go","old_string":"a","new_string":"b"}},{"type":"text","text":"Decision: router.go must reject empty host headers."}]}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	editPayload := mustMarshal(t, map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      "brand-new-live-session",
		"cwd":             "/work/app",
		"tool_name":       "Edit",
		"tool_input":      map[string]any{"file_path": "/work/app/router.go", "old_string": "a", "new_string": "b"},
	})
	var editOut bytes.Buffer
	if err := runHookTool(dir, bytes.NewReader(editPayload), &editOut); err != nil {
		t.Fatal(err)
	}
	var editResp sessionStartHookResponse
	if err := json.Unmarshal(editOut.Bytes(), &editResp); err != nil {
		t.Fatalf("unmarshal edit response: %v", err)
	}
	if !strings.Contains(editResp.SystemMessage, "deja: router.go has been worked on") {
		t.Fatalf("expected SystemMessage receipt on PreToolUse file injection, got: %q", editResp.SystemMessage)
	}

	mem, ok := statuslineMemory(dir, transcriptSource{SessionID: "brand-new-live-session"})
	if !ok || baseName(mem.Path) != "router.go" || mem.Sessions < 2 {
		t.Fatalf("expected statuslineMemory to resolve router.go for brand-new live session from snapshot, got ok=%v mem=%+v", ok, mem)
	}
}
