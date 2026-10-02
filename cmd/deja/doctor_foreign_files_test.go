package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each client keeps files of its own beside the transcripts: goal queues,
// saved chats, workflow runs, cost caches, sub-agent metadata. None is a
// transcript deja failed to read, and a sub-agent's log is a skipped sub-agent,
// not an unknown file (#4473, #4474, #4475, #4476, #4477, #4478).
func TestAClientsOwnFilesAreNotUnreadTranscripts(t *testing.T) {
	cases := []struct {
		row     string
		env     string
		sub     string // the root below the env var's directory
		session string
		own     []string
		want    string // a fragment the row must still carry
		avoid   string // and one it must not
	}{
		{
			row: "kimi", env: "DEJA_KIMI_ROOT", sub: "sessions",
			session: "wd_api/s1/agents/main/wire.jsonl",
			own: []string{"wd_api/s1/state.json", "wd_api/s1/upcoming-goals.json", "wd_api/s1/agents/agent-1/wire.jsonl",
				"wd_api/s1/agents/main/tasks/bash-a1b2c3d4.json", "wd_api/s1/tasks/bash-e5f6a7b8.json"},
			want:  "1 subagent transcripts skipped",
			avoid: "DEJA_INCLUDE_SUBAGENTS",
		},
		{
			row: "gemini", env: "DEJA_GEMINI_ROOT", sub: "tmp",
			session: "demo/chats/session-1.json",
			own: []string{"demo/logs.json", "demo/checkpoint-retry-loop.json",
				"demo/checkpoints/2026-10-01T10-00-00_000Z-main.go-replace.json", "demo/s1/tasks/1.json"},
			want: "(1 file",
		},
		{
			row: "qwen", env: "DEJA_QWEN_ROOT", sub: "projects",
			session: "-w-api/chats/a.jsonl",
			own: []string{"-w-api/session-organization.v1.json", "-w-api/workflows/run-1.json",
				"-w-api/workflows/run-1/journal.jsonl", "-w-api/subagents/s1/agent-a1.jsonl",
				"-w-api/subagents/s1/agent-a1.meta.json"},
			want:  "1 subagent transcripts skipped",
			avoid: "DEJA_INCLUDE_SUBAGENTS",
		},
		{
			row: "copilot", env: "DEJA_COPILOT_ROOT",
			session: "s1/events.jsonl",
			own:     []string{"s1/autopilot-objective.json"},
			want:    "(1 file",
		},
		{
			row: "openclaw", env: "DEJA_OPENCLAW_ROOT",
			session: "main/sessions/s1.jsonl",
			own:     []string{"main/sessions/.usage-cost-cache.json", "main/sessions/s1.trajectory.jsonl"},
			want:    "(1 file",
		},
		{
			row: "claude", env: "DEJA_CLAUDE_ROOT",
			session: "-tmp-proj/s1.jsonl",
			own:     []string{"-tmp-proj/s1/subagents/agent-a1.jsonl", "-tmp-proj/s1/subagents/agent-a1.meta.json"},
			want:    "1 subagent transcripts read as task",
			avoid:   "skipped",
		},
	}
	for _, tc := range cases {
		t.Run(tc.row, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
			root := filepath.Join(tmp, tc.row+"-store")
			t.Setenv(tc.env, root)
			base := filepath.Join(root, tc.sub)
			for _, rel := range append([]string{tc.session}, tc.own...) {
				p := filepath.Join(base, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(`{"type":"user","text":"why is the retry loop slow"}`+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			doctorHarnesses(&out, t.TempDir())
			var row string
			for _, line := range strings.Split(out.String(), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), tc.row+" ") {
					row = strings.TrimSpace(line)
				}
			}
			if row == "" {
				t.Fatalf("no %s row in:\n%s", tc.row, out.String())
			}
			if strings.Contains(row, "not recognised") {
				t.Errorf("the client's own files are counted as unread transcripts: %s", row)
			}
			if !strings.Contains(row, tc.want) {
				t.Errorf("row lacks %q: %s", tc.want, row)
			}
			if tc.avoid != "" && strings.Contains(row, tc.avoid) {
				t.Errorf("row says %q: %s", tc.avoid, row)
			}
		})
	}
}
