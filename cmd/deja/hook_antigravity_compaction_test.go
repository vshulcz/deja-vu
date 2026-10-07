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
		payload, _ := json.Marshal(map[string]any{
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
}
