package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// Antigravity has no compaction event. It writes a CHECKPOINT step and keeps
// the steps before it, so the next PreInvocation finds the compaction in the
// transcript, captures the conversation as it stood, and hands the packet back
// once, the way Gemini's next BeforeAgent does.
func TestAntigravityCompactionIsCaughtUpAtTheNextInvocation(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	logs := filepath.Join(t.TempDir(), "brain", "agy-1", ".system_generated", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(logs, "transcript.jsonl")
	steps := []string{
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-07T10:00:00Z","content":"<USER_REQUEST>fix the parser test</USER_REQUEST>"}`,
		`{"source":"MODEL","type":"RUN_COMMAND","created_at":"2026-10-07T10:00:01Z","content":"Task Description: go test ./parser/...\nOutput:\n--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-07T10:00:02Z","content":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}`,
	}
	write := func(extra ...string) {
		if err := os.WriteFile(transcript, []byte(strings.Join(append(append([]string{}, steps...), extra...), "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	invoke := func() string {
		payload := mustMarshal(t, map[string]any{
			"invocationNum": 1, "workspacePaths": []string{workspace},
			"transcriptPath": transcript, "conversationId": "agy-1",
		})
		var out bytes.Buffer
		if err := runHookAntigravity(dir, bytes.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	write()
	if out := invoke(); strings.Contains(out, "Compaction context") {
		t.Fatalf("a conversation that never compacted got a packet: %s", out)
	}
	write(
		`{"source":"SYSTEM","type":"CHECKPOINT","created_at":"2026-10-07T10:05:00Z","content":"summary of the work so far"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-07T10:05:01Z","content":"carrying on"}`,
	)
	out := invoke()
	for _, want := range []string{"Compaction context", "go test ./parser/...", "fix the parser test"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "carrying on") {
		t.Errorf("the packet was built from steps after the checkpoint: %s", out)
	}
	if again := invoke(); strings.Contains(again, "Compaction context") {
		t.Fatalf("the same compaction was handed back twice: %s", again)
	}
	editPath := filepath.ToSlash(filepath.Join(workspace, "parser", "parse.go"))
	write(
		`{"source":"SYSTEM","type":"CHECKPOINT","created_at":"2026-10-07T10:05:00Z","content":"summary of the work so far"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-07T10:05:01Z","content":"reading file","tool_calls":[{"name":"read_file","args":{"Path":"`+editPath+`"}}]}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-07T10:05:02Z","content":"fixing file","tool_calls":[{"name":"edit_file","args":{"Path":"`+editPath+`","Target":"a","Replacement":"b"}}]}`,
	)
	_ = invoke()
	sum := usage.CompactionRecoverySummary(dir)
	if sum == nil || sum.Measured != 1 || sum.MedianActions != 1 {
		t.Fatalf("expected 1 measured Antigravity compaction recovery with 1 action before edit, got %+v", sum)
	}
}

func TestLatestToolFailureFromStepsParsesAntigravityAndMCPFormats(t *testing.T) {
	steps := []transcriptStep{
		{
			Type:    "GENERIC",
			Content: "Command exited with code 1.\nCommand output:\ninternal/parser/parse.go:42:2: assignment mismatch: 3 variables but ParseToken returns 2 values",
		},
	}
	got := latestToolFailureFromSteps(steps)
	if !strings.Contains(got, "assignment mismatch: 3 variables but ParseToken returns 2 values") {
		t.Fatalf("expected Command exited with code 1 format to extract output, got %q", got)
	}

	mcpSteps := []transcriptStep{
		{
			Type:    "GENERIC",
			Content: "[cmd direct | exit=1 | 120ms]\n--- FAIL: TestParseToken (0.01s)",
		},
	}
	if gotMCP := latestToolFailureFromSteps(mcpSteps); !strings.Contains(gotMCP, "--- FAIL: TestParseToken") {
		t.Fatalf("expected MCP exit=1 format to extract output, got %q", gotMCP)
	}

	okSteps := []transcriptStep{
		{
			Type:    "GENERIC",
			Content: "Command exited with code 0.\nCommand output:\nPASS",
		},
	}
	if gotOK := latestToolFailureFromSteps(okSteps); gotOK != "" {
		t.Fatalf("expected exit 0 to return empty failure, got %q", gotOK)
	}
}
