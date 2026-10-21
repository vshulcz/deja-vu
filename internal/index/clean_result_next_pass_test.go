package index

import (
	"path/filepath"
	"strings"
	"testing"
)

// A clean result that lands in the pass after its call gives the command the
// exit 0 a rebuild stamps on it, in every harness whose reader stamps one:
// goose's structured exit_code, kiro-cli's exit_status and the pi family's
// clean run, as Claude's does since #5527. The others stamp no exit 0 and are
// here as controls.
func TestACleanResultInTheNextPassStampsItsExit(t *testing.T) {
	cases := []struct {
		name, env, file string
		head, call, res string
	}{
		{
			name: "codex", env: "DEJA_CODEX_ROOT",
			file: "sessions/2026/07/17/rollout-2026-07-17T09-00-00-0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001.jsonl",
			head: `{"timestamp":"2026-07-17T09:00:00.000Z","type":"session_meta","payload":{"id":"0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001","cwd":"/tmp/proj"}}` + "\n" +
				`{"timestamp":"2026-07-17T09:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the retry loop"}]}}` + "\n",
			call: `{"timestamp":"2026-07-17T09:02:00.000Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"go vet ./retry\"}","call_id":"call_3"}}` + "\n",
			res:  `{"timestamp":"2026-07-17T09:02:01.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_3","output":"Process exited with code 0\nOutput:\nok"}}` + "\n",
		},
		{
			name: "copilot", env: "DEJA_COPILOT_ROOT",
			file: "session-state/0199cccc-0000-7000-8000-000000000001/events.jsonl",
			head: `{"type":"session.start","timestamp":"2026-07-17T09:00:00Z","data":{"sessionId":"0199cccc-0000-7000-8000-000000000001","startTime":"2026-07-17T09:00:00Z","context":{"cwd":"/tmp/proj"}}}` + "\n" +
				`{"type":"user.message","timestamp":"2026-07-17T09:00:01Z","data":{"content":"fix the retry loop"}}` + "\n",
			call: `{"type":"tool.execution_start","timestamp":"2026-07-17T09:02:00Z","data":{"toolCallId":"tc1","toolName":"bash","arguments":{"command":"go vet ./retry"}}}` + "\n",
			res:  `{"type":"tool.execution_complete","timestamp":"2026-07-17T09:02:01Z","data":{"toolCallId":"tc1","success":true,"result":{"content":"ok"},"toolTelemetry":{"metrics":{"exit_code":0}}}}` + "\n",
		},
		{
			name: "kimi", env: "DEJA_KIMI_ROOT",
			file: "sessions/wd_proj/session_t01/agents/main/wire.jsonl",
			head: `{"type":"metadata","protocol_version":"1.4","created_at":1790870000000}` + "\n" +
				`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]},"time":1790870004000}` + "\n",
			call: `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"c1","name":"Bash","args":{"command":"go vet ./retry"}},"time":1790870004786}` + "\n",
			res:  `{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"c1","result":{"output":"ok"}},"time":1790870004853}` + "\n",
		},
		{
			name: "claude", env: "DEJA_CLAUDE_ROOT",
			file: "-tmp-proj/c1.jsonl",
			head: `{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:00Z","cwd":"/tmp/proj","message":{"role":"user","content":"fix the retry loop"}}` + "\n",
			call: `{"type":"assistant","sessionId":"c1","timestamp":"2026-10-01T10:00:01Z","cwd":"/tmp/proj","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go vet ./retry"}}]}}` + "\n",
			res:  `{"type":"user","sessionId":"c1","timestamp":"2026-10-01T10:00:02Z","cwd":"/tmp/proj","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}` + "\n",
		},
		{
			name: "commandcode", env: "DEJA_COMMANDCODE_ROOT",
			file: "-tmp-proj/s1.jsonl",
			head: `{"type":"session","version":3,"id":"s1","timestamp":"2026-10-01T20:09:14.839Z","cwd":"/tmp/proj"}` + "\n" +
				`{"type":"message","id":"u1","timestamp":"2026-10-01T20:09:15.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}` + "\n",
			call: `{"type":"message","id":"a1","timestamp":"2026-10-01T20:09:16.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"shell_command","input":{"command":"go vet ./retry"}}]}}` + "\n",
			res:  `{"type":"message","id":"r1","timestamp":"2026-10-01T20:09:17.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"Exit code: 0\nok"}]}]}}` + "\n",
		},
		{
			name: "goose", env: "DEJA_GOOSE_ROOT",
			file: "sessions/20261001_1.jsonl",
			head: `{"working_dir":"/tmp/proj","description":"fix the retry loop","message_count":0}` + "\n" +
				`{"role":"user","created":1790848800,"content":[{"type":"text","text":"fix the retry loop"}]}` + "\n",
			call: `{"role":"assistant","created":1790848801,"content":[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"developer__shell","arguments":{"command":"go vet ./retry"}}}}]}` + "\n",
			res:  `{"role":"user","created":1790848802,"content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"ok"}],"structuredContent":{"exit_code":0}}}}]}` + "\n",
		},
		{
			name: "kiro-cli", env: "DEJA_KIRO_ROOT",
			file: "cli/11111111-2222-4333-8444-555555555555.jsonl",
			head: `{"version":"v1","kind":"Prompt","data":{"message_id":"m0","content":[{"kind":"text","data":"fix the retry loop"}],"meta":{"timestamp":1790848800}}}` + "\n",
			call: `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m1","content":[{"kind":"toolUse","data":{"toolUseId":"t1","name":"shell","input":{"command":"go vet ./retry"}}}],"meta":{"timestamp":1790848801}}}` + "\n",
			res:  `{"version":"v1","kind":"ToolResults","data":{"message_id":"m2","content":[{"kind":"toolResult","data":{"toolUseId":"t1","content":[{"kind":"json","data":{"exit_status":"exit status: 0","stdout":"ok","stderr":""}}],"status":"success"}}],"meta":{"timestamp":1790848802}}}` + "\n",
		},
		{
			name: "pi", env: "DEJA_PI_ROOT",
			file: "--tmp-proj--/s-retry.jsonl",
			head: `{"type":"session","version":3,"id":"s-retry","timestamp":"2026-09-01T09:00:00Z","cwd":"/tmp/proj"}` + "\n" +
				`{"type":"message","id":"u1","timestamp":"2026-09-01T09:00:01Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}` + "\n",
			call: `{"type":"message","id":"a1","timestamp":"2026-09-01T09:02:00Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-vet","name":"bash","arguments":{"command":"go vet ./retry"}}]}}` + "\n",
			res:  `{"type":"message","id":"r1","timestamp":"2026-09-01T09:02:01Z","message":{"role":"toolResult","toolCallId":"c-vet","toolName":"bash","content":[{"type":"text","text":"ok"}],"details":{},"isError":false}}` + "\n",
		},
		{
			name: "omp", env: "DEJA_OMP_ROOT",
			file: "--tmp-proj--/2026-10-01T10-00-00-000Z_o1.jsonl",
			head: `{"type":"session","version":3,"id":"omp-s1","timestamp":"2026-10-01T10:00:00.000Z","cwd":"/tmp/proj"}` + "\n" +
				`{"type":"message","id":"u1","timestamp":"2026-10-01T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}` + "\n",
			call: `{"type":"message","id":"a1","timestamp":"2026-10-01T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"go vet ./retry"}}]}}` + "\n",
			res:  `{"type":"message","id":"r1","timestamp":"2026-10-01T10:00:02.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"ok"}],"details":{"exitCode":0},"isError":false}}` + "\n",
		},
		{
			name: "qwen", env: "DEJA_QWEN_ROOT",
			file: "projects/-tmp-proj/chats/q1.jsonl",
			head: `{"type":"user","sessionId":"q1","timestamp":"2026-07-17T09:00:00Z","cwd":"/tmp/proj","message":{"role":"user","parts":[{"text":"fix the retry loop"}]}}` + "\n",
			call: `{"type":"assistant","sessionId":"q1","timestamp":"2026-07-17T09:00:01Z","cwd":"/tmp/proj","message":{"role":"model","parts":[{"functionCall":{"id":"f1","name":"run_shell_command","args":{"command":"go vet ./retry"}}}]}}` + "\n",
			res:  `{"type":"tool_result","sessionId":"q1","timestamp":"2026-07-17T09:00:02Z","cwd":"/tmp/proj","message":{"role":"user","parts":[{"functionResponse":{"id":"f1","name":"run_shell_command","response":{"output":"Command: go vet ./retry\nExit Code: 0\nok"}}}]}}` + "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := parityEnv(t, map[string]string{tc.env: "store"})
			f := filepath.Join(tmp, "store", filepath.FromSlash(tc.file))
			parityWrite(t, f, tc.head, false)
			inc := filepath.Join(tmp, "inc")
			parityPass(t, inc, false)
			parityWrite(t, f, tc.call, true)
			parityPass(t, inc, false)
			parityWrite(t, f, tc.res, true)
			parityPass(t, inc, false)
			sameAsRebuild(t, inc)
			held := false
			for _, v := range paritySnapshot(t, inc) {
				held = held || strings.Contains(v, "$ go vet ./retry")
			}
			if !held {
				t.Error("control: the command was never indexed")
			}
		})
	}
}
