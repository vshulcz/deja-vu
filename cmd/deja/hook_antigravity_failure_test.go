package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Antigravity hands PostToolUse the error and then allows it no answer — its
// contract is an empty object. The moment a command fails is reachable only
// from the invocation that follows it, and the failure is in the transcript:
// a step that says which code the command exited with, and the output under it.
func TestAFailedCommandIsReadFromTheTranscript(t *testing.T) {
	dir := t.TempDir()

	write := func(name string, steps ...string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Join(steps, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	failed := write("failed.jsonl",
		`{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nbuild it\n</USER_REQUEST>"}`,
		`{"step_index":1,"type":"GENERIC","status":"DONE","content":"Created At: now\nCompleted At: now\n\nThe command exited with code 1.\nOutput:\n./main.go:9:2: undefined: glimwraxHelper\n"}`)
	got := latestToolFailure(failed)
	if !strings.Contains(got, "undefined: glimwraxHelper") {
		t.Errorf("the error was not read out of the step: %q", got)
	}
	// The harness's own framing is not part of the error.
	if strings.Contains(got, "Created At") || strings.Contains(got, "exited with code") {
		t.Errorf("the step's framing came back as the error: %q", got)
	}

	// A command that worked is not a failure.
	ok := write("ok.jsonl",
		`{"step_index":0,"type":"GENERIC","status":"DONE","content":"Created At: now\n\nThe command exited with code 0.\nOutput:\nhi\n"}`)
	if got := latestToolFailure(ok); got != "" {
		t.Errorf("a successful command read as a failure: %q", got)
	}

	// And a failure the agent has already moved past is not the moment either:
	// only the newest tool step counts.
	stale := write("stale.jsonl",
		`{"step_index":0,"type":"GENERIC","status":"DONE","content":"The command exited with code 1.\nOutput:\nthe old error\n"}`,
		`{"step_index":1,"type":"GENERIC","status":"DONE","content":"The command exited with code 0.\nOutput:\nfine now\n"}`)
	if got := latestToolFailure(stale); got != "" {
		t.Errorf("a failure that was already worked past came back: %q", got)
	}

	// Nothing to read is silence, not a guess.
	if got := latestToolFailure(filepath.Join(dir, "missing.jsonl")); got != "" {
		t.Errorf("a missing transcript answered %q", got)
	}
}

// And the hook speaks it: after a failed command, the next invocation carries
// what this machine ran the last time the same error came up.
func TestTheInvocationAfterAFailureCarriesTheRepair(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	seedClaudeFixPair(t, claude, "agyfix")
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	transcript := filepath.Join(tmp, "transcript.jsonl")
	steps := []string{
		`{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nbuild it\n</USER_REQUEST>"}`,
		`{"step_index":1,"type":"GENERIC","status":"DONE","content":"The command exited with code 1.\nOutput:\n./main.go:9:2: undefined: glimwraxHelper\n"}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(steps, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(map[string]any{
		"invocationNum":  1,
		"conversationId": "conv-fx",
		"transcriptPath": transcript,
		"workspacePaths": []string{filepath.Join(os.TempDir(), "agyfix")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runHookAntigravity(dir, bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "glimwraxctl") {
		t.Errorf("the repair did not arrive at the failure:\n%s", out.String())
	}
}

// seedClaudeFixPair writes a session that hit an error and then ran the command
// that settled it — the shape a fix pair is built from.
func seedClaudeFixPair(t *testing.T, root, project string) {
	t.Helper()
	dir := filepath.Join(root, "-"+project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(os.TempDir(), project)
	rec := func(kind string, content any, at string) string {
		b, _ := json.Marshal(map[string]any{
			"type": kind, "sessionId": "fix1", "cwd": cwd, "timestamp": at,
			"message": map[string]any{"role": kind, "content": content},
		})
		return string(b)
	}
	lines := []string{
		rec("user", "the build stops on an undefined symbol", "2026-09-01T10:00:00Z"),
		rec("assistant", []any{map[string]any{"type": "tool_use", "id": "t1", "name": "Bash",
			"input": map[string]string{"command": "go build ./..."}}}, "2026-09-01T10:00:10Z"),
		rec("user", []any{map[string]any{"type": "tool_result", "tool_use_id": "t1",
			"content": "./main.go:9:2: undefined: glimwraxHelper"}}, "2026-09-01T10:00:20Z"),
		rec("assistant", []any{map[string]any{"type": "tool_use", "id": "t2", "name": "Bash",
			"input": map[string]string{"command": "glimwraxctl --sync && go build ./..."}}}, "2026-09-01T10:00:40Z"),
		rec("user", []any{map[string]any{"type": "tool_result", "tool_use_id": "t2",
			"content": "ok"}}, "2026-09-01T10:01:00Z"),
	}
	if err := os.WriteFile(filepath.Join(dir, "fix1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
