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
	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	shellReportErr = "pattern ./...: directory prefix . does not contain main module or its selected dependencies"
	shellReportFix = "go mod init example.com/vetdemo"
)

// qwenShellReport is the result qwen-code 0.20.0 writes for every shell call:
// `Error: (none)` is there when the command worked too.
func qwenShellReport(cmd, out string, code int) string {
	return fmt.Sprintf("Command: %s\nDirectory: (root)\nOutput: %s\nError: (none)\nExit Code: %d\nSignal: (none)\nBackground PIDs: (none)\nProcess Group PGID: 4242", cmd, out, code)
}

// assertShellReportPair checks both ends of #4256: the pair is mined under the
// error the command printed, and the failure hook finds it from the harness's
// own payload.
func assertShellReportPair(t *testing.T, errLine string, response map[string]any) {
	t.Helper()
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	fixes := index.FixesFor(dir, errLine, 5, nil)
	if len(fixes) == 0 || strings.TrimPrefix(fixes[0].Command, "$ ") != shellReportFix {
		t.Fatalf("no pair mined under the bare error: %+v", fixes)
	}
	payload := map[string]any{
		"session_id": "live", "cwd": "/work/app",
		"tool_name": "run_shell_command", "tool_input": map[string]any{"command": "go vet ./..."},
	}
	for k, v := range response {
		payload[k] = v
	}
	raw, _ := json.Marshal(payload)
	var out bytes.Buffer
	if err := runHookToolAfterMode(dir, bytes.NewReader(raw), &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), shellReportFix) {
		t.Fatalf("the failure hook said nothing for a pair in the index:\n%q", out.String())
	}
}

// qwenShellSessions writes two Qwen Code sessions that each hit shellReportErr
// and then run shellReportFix, which prints fixOut and exits fixCode.
func qwenShellSessions(t *testing.T, fixOut string, fixCode int) {
	t.Helper()
	hermeticEnv(t)
	chats := filepath.Join(os.Getenv("DEJA_QWEN_ROOT"), "projects", "-work-app", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-72 * time.Hour)
	ts := func(i int) string { return at.Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339) }
	call := func(id, sid, cmd string, i int) string {
		return fmt.Sprintf(`{"sessionId":%q,"timestamp":%q,"type":"assistant","cwd":"/work/app","message":{"role":"model","parts":[{"functionCall":{"id":%q,"name":"run_shell_command","args":{"command":%q}}}]}}`, sid, ts(i), id, cmd)
	}
	result := func(id, sid, report string, i int) string {
		return fmt.Sprintf(`{"sessionId":%q,"timestamp":%q,"type":"tool_result","cwd":"/work/app","message":{"role":"user","parts":[{"functionResponse":{"id":%q,"name":"run_shell_command","response":{"output":%q}}}]}}`, sid, ts(i), id, report)
	}
	for n := 0; n < 2; n++ {
		sid := fmt.Sprintf("qwen-%d", n)
		rows := []string{
			fmt.Sprintf(`{"sessionId":%q,"timestamp":%q,"type":"user","cwd":"/work/app","message":{"role":"user","parts":[{"text":"vet the project"}]}}`, sid, ts(0)),
			call("c1", sid, "go vet ./...", 1),
			result("c1", sid, qwenShellReport("go vet ./...", shellReportErr, 1), 2),
			call("c2", sid, shellReportFix, 3),
			result("c2", sid, qwenShellReport(shellReportFix, fixOut, fixCode), 4),
		}
		if err := os.WriteFile(filepath.Join(chats, sid+".jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Qwen labels every shell result `Error: (none)`, so a command that worked
// read as one that failed and no qwen session ever yielded a pair; and the
// pair was stored as `Output: <error>`, which the hook never looks up (#4256).
func TestFixPairMinedFromQwenShellReports(t *testing.T) {
	qwenShellSessions(t, "go: creating new go.mod: module example.com/vetdemo", 0)
	// Qwen's PostToolUseFailure carries the report under `error`.
	assertShellReportPair(t, shellReportErr, map[string]any{"error": qwenShellReport("go vet ./...", shellReportErr, 1)})
}

// geminiShellSessions writes two Gemini CLI sessions that each hit a command
// printing errOut and then run shellReportFix, and returns the failing
// result as Gemini wraps it.
func geminiShellSessions(t *testing.T, errOut string) string {
	t.Helper()
	hermeticEnv(t)
	root := os.Getenv("DEJA_GEMINI_ROOT")
	chats := filepath.Join(root, "tmp", "work-app", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte(`{"projects":{"/work/app":"work-app"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-72 * time.Hour)
	ts := func(i int) string { return at.Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339) }
	// The call on the gemini record with its result inline, and the result
	// again on the user record that follows, as Gemini CLI writes them.
	gemini := func(id, cmd, out string, i int) string {
		resp := fmt.Sprintf(`{"functionResponse":{"id":%q,"name":"run_shell_command","response":{"output":%q}}}`, id, out)
		return fmt.Sprintf(`{"id":"g%d","timestamp":%q,"type":"gemini","content":"","toolCalls":[{"id":%q,"name":"run_shell_command","args":{"command":%q},"result":[%s]}]}`, i, ts(i), id, cmd, resp) + "\n" +
			fmt.Sprintf(`{"id":"u%d","timestamp":%q,"type":"user","content":[%s]}`, i, ts(i), resp)
	}
	failed := "<untrusted_context>\nOutput: " + errOut + "\nExit Code: 1\nProcess Group PGID: 98713\n</untrusted_context>"
	for n := 0; n < 2; n++ {
		sid := fmt.Sprintf("gemini-%d", n)
		rows := []string{
			fmt.Sprintf(`{"sessionId":%q,"projectHash":"abc","startTime":%q,"lastUpdated":%q,"kind":"main"}`, sid, ts(0), ts(5)),
			fmt.Sprintf(`{"id":"u0","timestamp":%q,"type":"user","content":"vet the project"}`, ts(0)),
			gemini("run_shell_command__1", "go vet ./...", failed, 1),
			gemini("run_shell_command__2", shellReportFix, "<untrusted_context>\nOutput: go: creating new go.mod: module example.com/vetdemo\nProcess Group PGID: 98714\n</untrusted_context>", 3),
		}
		name := fmt.Sprintf("session-2026-09-28T10-00-%s.jsonl", sid)
		if err := os.WriteFile(filepath.Join(chats, name), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return failed
}

// Gemini wraps the output in the same `Output:` label; the pair was stored with
// it and the hook, which strips it, never found the pair (#4256).
func TestFixPairMinedFromGeminiShellReports(t *testing.T) {
	failed := geminiShellSessions(t, shellReportErr)
	// Gemini's AfterTool carries it under tool_response.llmContent.
	assertShellReportPair(t, shellReportErr, map[string]any{"tool_response": map[string]any{"llmContent": failed}})
}

// Only the report comes off: `Error: (none)` goes, a line the command printed
// that starts with "Error: " stays as it was, so it still reads as the failure.
func TestShellReportKeepsTheCommandsOwnErrorLine(t *testing.T) {
	report := "Command: npm start\nDirectory: (root)\nOutput: npm ERR! code ELIFECYCLE\nError: Cannot find module 'glimwrax'\nError: (none)\nExit Code: 1"
	want := "npm ERR! code ELIFECYCLE\nError: Cannot find module 'glimwrax'"
	if got := sources.UnwrapShellReport(report); got != want {
		t.Fatalf("unwrapped to %q, want %q", got, want)
	}
}

// The hook unwrapped gemini's llmContent twice, once reading the response and
// again before the lookup, so a command whose own output starts with
// "Output: " lost that too and no longer matched what the index, which
// unwraps once, stored for it.
func TestGeminiReportIsUnwrappedOnce(t *testing.T) {
	errOut := "Output: " + shellReportErr
	failed := geminiShellSessions(t, errOut)
	assertShellReportPair(t, errOut, map[string]any{"tool_response": map[string]any{"llmContent": failed}})
}

// The other half of reading the report: a remedy that exits non-zero without
// printing an error is a failure too, read off the exit status Qwen reports
// (#4255), so it is not served as the fix.
func TestQwenRemedyThatExitsNonZeroIsNotAFix(t *testing.T) {
	qwenShellSessions(t, "go: creating new go.mod: module example.com/vetdemo", 1)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	if fixes := index.FixesFor(os.Getenv("DEJA_INDEX_DIR"), shellReportErr, 5, nil); len(fixes) != 0 {
		t.Fatalf("a remedy that exited 1 was kept as the fix: %+v", fixes)
	}
}
