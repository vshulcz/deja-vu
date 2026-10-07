package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Antigravity's PreToolUse reason never reaches the model, so the line the
// pre-tool hook gives before an edit rides the next PreInvocation, read from
// the planner row that made the call — after the edit, with its result.
func TestAntigravityEditLineRidesTheNextInvocation(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := 0; i < 6; i++ {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(tmp, "transcript.jsonl")
	steps := []string{
		`{"type":"USER_INPUT","source":"USER_EXPLICIT","content":"<USER_REQUEST>tidy the loader</USER_REQUEST>","created_at":"2026-10-07T10:00:00Z"}`,
		`{"type":"PLANNER_RESPONSE","source":"MODEL","content":"","created_at":"2026-10-07T10:00:01Z","tool_calls":[{"name":"replace_file_content","args":{"TargetFile":"\"file:///work/alpha/config.go\""}}]}`,
		`{"type":"CODE_ACTION","source":"MODEL","status":"DONE","content":"Edited file:///work/alpha/config.go","created_at":"2026-10-07T10:00:02Z"}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(steps, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"invocationNum": 1, "workspacePaths": []string{"/work/alpha"},
		"transcriptPath": transcript, "conversationId": "agy-1",
	})
	var out bytes.Buffer
	if err := runHookAntigravity(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	var resp antigravityHookResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("reply %q: %v", out.String(), err)
	}
	var said []string
	for _, s := range resp.InjectSteps {
		said = append(said, s.EphemeralMessage)
	}
	if !strings.Contains(strings.Join(said, "\n"), "sentinel") {
		t.Fatalf("the edit's line did not ride the next invocation: %q", said)
	}
	// Once: the next invocation of the same turn says nothing new about it.
	out.Reset()
	if err := runHookAntigravity(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "sentinel") {
		t.Fatalf("the same line came twice: %q", out.String())
	}
}
