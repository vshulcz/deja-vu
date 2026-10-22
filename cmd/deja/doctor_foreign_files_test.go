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
			// Read by default now, as Claude's are (#5229), and the row says how.
			want: "1 subagent transcripts read as task, answer and what they changed",
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
			// Read by default now, as Claude's are (#5223), and the row says how.
			want: "1 subagent transcripts read as task, answer and what they changed",
		},
		{
			// Muse keeps cron.db, goals.db and spilled tool output in each
			// session directory, and its subagents' logs one level down.
			row: "muse", env: "DEJA_MUSE_ROOTS",
			session: "2026/09/18/u1/session.jsonl",
			own: []string{"2026/09/18/u1/cron.db", "2026/09/18/u1/goals.db",
				"2026/09/18/u1/tool-outputs/call_1.txt", "2026/09/18/u1/subagent/c1/session.jsonl"},
			// Read by default now, as Claude's are (#5301), and the row says how.
			want: "1 subagent transcripts read as task, answer and what they changed",
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
			for line := range strings.SplitSeq(out.String(), "\n") {
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

// A Kimi /btw fork is read, so doctor counts it as indexed; a fork that an
// Agent call with fork: true made is a sub-agent run and stays a skipped one
// (#4484).
func TestDoctorCountsAKimiBtwForkAsRead(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	root := filepath.Join(tmp, "kimi-store")
	t.Setenv("DEJA_KIMI_ROOT", root)
	session := filepath.Join(root, "sessions", "wd_api", "s1")
	for rel, body := range map[string]string{
		"state.json": `{"workDir":"/w/api","agents":{"main":{"type":"main"},` +
			`"agent-1":{"type":"sub","parentAgentId":"main","forkedFrom":"main"},` +
			`"agent-2":{"type":"sub","parentAgentId":"main","forkedFrom":"main"}}}`,
		"agents/main/wire.jsonl": `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"why is the retry loop slow"}]},"time":1782295300001}` + "\n",
		"agents/agent-1/wire.jsonl": `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"side"}],"origin":{"kind":"injection","variant":"btw"}},"time":1782295300002}` + "\n" +
			`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"what is jitter"}],"origin":{"kind":"user"}},"time":1782295300003}` + "\n",
		"agents/agent-2/wire.jsonl": `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"audit it"}],"origin":{"kind":"system_trigger","name":"subagent"}},"time":1782295300004}` + "\n",
	} {
		p := filepath.Join(session, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	doctorHarnesses(&out, t.TempDir())
	var row string
	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "kimi ") && strings.Contains(line, "file") {
			row = strings.TrimSpace(line)
		}
	}
	for _, want := range []string{"(2 files", "1 subagent transcripts skipped"} {
		if !strings.Contains(row, want) {
			t.Errorf("kimi row lacks %q: %s", want, row)
		}
	}
}
