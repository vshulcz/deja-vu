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
)

// The hooks half of the property #2848 pins for the MCP tools: anything deja
// puts into an agent's context says the text came out of a transcript.
//
// It matters more here than there. A tool answer was asked for; a hook speaks
// unasked, at session start, on every prompt, and after a failing command — and
// the last of those hands over a command to run.
func TestEveryHookInjectionSaysItIsUntrusted(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(claudeRoot, "beta")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const errLine = "ld: symbol(s) not found for architecture arm64"
	base := time.Now().Add(-72 * time.Hour).UTC()
	// Two sessions holding the same error and the command that followed it, so
	// the failure hook has a confirmed pair, and prose the other hooks recall.
	for i, sid := range []string{"h1", "h2"} {
		at := base.Add(time.Duration(i) * time.Hour)
		rows := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/work/api","timestamp":%q,"message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
				sid, at.Format(time.RFC3339)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/work/api","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","content":%q}]}}`,
				sid, at.Add(time.Minute).Format(time.RFC3339), errLine),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":"/work/api","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go build -ldflags=-w ./internal/index"}}]}}`,
				sid, at.Add(2*time.Minute).Format(time.RFC3339)),
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The pre-action hook speaks about a file several sessions have worked on:
	// one edit is not a history worth interrupting for, so the fixture has to
	// carry the shape that surface actually answers to.
	for k := 0; k < 6; k++ {
		sid := fmt.Sprintf("e%d", k)
		at := base.Add(time.Duration(k) * time.Hour)
		rows := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/work/api","timestamp":%q,"message":{"role":"user","content":"work on the ingest watermark"}}`,
				sid, at.Format(time.RFC3339)),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":"/work/api","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/api/internal/index/ingest.go","old_string":"x","new_string":"y"}}]}}`,
				sid, at.Add(time.Minute).Format(time.RFC3339)),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":"/work/api","timestamp":%q,"message":{"role":"assistant","content":"the watermark stays grok-only (%d)"}}`,
				sid, at.Add(2*time.Minute).Format(time.RFC3339), k),
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)

	check := func(surface, out, must string) {
		t.Helper()
		// A hook that injected nothing proves nothing — the fixture has to
		// make each one speak, or this passes for the wrong reason.
		if !strings.Contains(out, must) {
			t.Errorf("%s: injected nothing from the fixture, so its framing is untested:\n%s", surface, firstLines(out, 3))
			return
		}
		if !strings.Contains(out, "untrusted reference data") {
			t.Errorf("%s: puts transcript text in the context with nothing saying where it came from:\n%s", surface, firstLines(out, 3))
		}
	}

	withHookStdin(t, hookPayload(t, map[string]string{"source": "startup", "session_id": "ses_hooks", "cwd": cwd}))
	// The block leads with what the project has been busy with, which the
	// edit sessions above now are — either half of the fixture proves the
	// text came from it.
	check("hook-context", captureStdout(t, func() { runHookContextPlain(t) }), "watermark")

	var prompt bytes.Buffer
	if err := runHookPromptMode(dir, strings.NewReader(`{"prompt":"what did we settle about pgbouncer prepared statements","session_id":"ses_hooks"}`), &prompt, true); err != nil {
		t.Fatal(err)
	}
	check("hook-prompt", prompt.String(), "transaction mode")

	// hook-tool speaks before an action rather than after one: an Edit of a
	// file the store has seen brings back what was decided about it.
	var before bytes.Buffer
	editPayload := fmt.Sprintf(`{"hook_event_name":"PreToolUse","tool_name":"Edit",`+
		`"tool_input":{"file_path":%q},"cwd":%q,"session_id":"ses_hooks_edit"}`,
		"/work/api/internal/index/ingest.go", "/work/api")
	if err := runHookTool(dir, strings.NewReader(editPayload), &before); err != nil {
		t.Fatal(err)
	}
	check("hook-tool", before.String(), "ingest.go")

	var after bytes.Buffer
	payload := fmt.Sprintf(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go build ./..."},`+
		`"tool_response":{"stderr":%q},"cwd":%q,"session_id":"ses_hooks"}`, errLine, cwd)
	if err := runHookToolAfter(dir, strings.NewReader(payload), &after); err != nil {
		t.Fatal(err)
	}
	check("hook-tool-after", after.String(), "go build -ldflags=-w")
}
