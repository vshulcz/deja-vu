package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestTelemetryParityAcrossParsers(t *testing.T) {
	writeFile := func(t *testing.T, p, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	assertTelemetry := func(t *testing.T, ss []model.Session, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 {
			t.Fatalf("sessions = %d, want 1", len(ss))
		}
		s := ss[0]
		if s.Model == nil || s.Model.ID == "" {
			t.Errorf("Model = %+v, want non-empty ID", s.Model)
		}
		if s.Tokens == nil || s.Tokens.Input <= 0 || s.Tokens.Output <= 0 {
			t.Errorf("Tokens = %+v, want Input > 0 and Output > 0", s.Tokens)
		}
		if len(s.ToolCalls) == 0 {
			t.Errorf("ToolCalls = %+v, want non-empty", s.ToolCalls)
		}
		if s.ToolErrors != 1 {
			t.Errorf("ToolErrors = %d, want 1", s.ToolErrors)
		}
	}

	cases := []struct {
		name string
		run  func(t *testing.T) ([]model.Session, error)
	}{
		{
			name: "antigravity",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				root := t.TempDir()
				t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
				p := filepath.Join(root, "brain", "conv-tel", ".system_generated", "logs", "transcript.jsonl")
				body := strings.Join([]string{
					`{"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-01T10:00:00Z","content":"run test"}`,
					`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-01T10:00:01Z","model":"gemini-2.5-pro","usage":{"input_tokens":120,"output_tokens":45},"content":"running","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test ./..."}}]}`,
					`{"source":"MODEL","type":"RUN_COMMAND","status":"ERROR","created_at":"2026-07-01T10:00:02Z","content":"Command exited with code 1.\nCommand output:\nFAIL"}`,
				}, "\n") + "\n"
				return ParseAntigravityFile(writeFile(t, p, body))
			},
		},
		{
			name: "hermes",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				db := writeHermesDB(t, filepath.Join(t.TempDir(), "p"), `
					CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT, model TEXT, billing_provider TEXT, input_tokens INTEGER, output_tokens INTEGER, reasoning_tokens INTEGER, cache_read_tokens INTEGER, model_config TEXT);
					INSERT INTO sessions VALUES ('20261018_012943_14524c','cli',NULL,'/tmp/proj','claude-sonnet-4-5','anthropic',100,40,0,0,'{}');
					INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
					 ('20261018_012943_14524c','user','run test',NULL,NULL,NULL,1792200000),
					 ('20261018_012943_14524c','assistant','',NULL,'[{"id":"c1","type":"function","function":{"name":"terminal","arguments":"{\"command\":\"go test ./...\"}"}}]',NULL,1792200001),
					 ('20261018_012943_14524c','tool','{"output":"FAIL","exit_code":1}','c1',NULL,'terminal',1792200002);`)
				return ParseHermesDB(db)
			},
		},
		{
			name: "goose",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				p := filepath.Join(t.TempDir(), "g1.jsonl")
				body := strings.Join([]string{
					`{"id":"g1","working_dir":"/tmp/proj","provider_name":"anthropic","model":"claude-sonnet-4-5","input_tokens":110,"output_tokens":35,"created_at":"2026-07-01T10:00:00Z"}`,
					`{"role":"user","created":1782900000,"content":[{"type":"text","text":"test"}]}`,
					`{"role":"assistant","created":1782900001,"content":[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"shell","arguments":{"command":"go test ./..."}}}}]}`,
					`{"role":"user","created":1782900002,"content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"error","error":"failed","value":{"content":[{"type":"text","text":"FAIL"}],"isError":true}}}]}`,
				}, "\n") + "\n"
				return ParseGooseFile(writeFile(t, p, body))
			},
		},
		{
			name: "pi",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				p := filepath.Join(t.TempDir(), "p1.jsonl")
				body := strings.Join([]string{
					`{"type":"session","id":"p1","cwd":"/tmp/proj","timestamp":"2026-07-01T10:00:00Z"}`,
					`{"type":"message","timestamp":"2026-07-01T10:00:01Z","message":{"role":"user","content":"test"}}`,
					`{"type":"message","timestamp":"2026-07-01T10:00:02Z","message":{"role":"assistant","provider":"anthropic","model":"claude-sonnet-4-5","usage":{"input":90,"output":30},"content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"go test ./..."}}]}}`,
					`{"type":"message","timestamp":"2026-07-01T10:00:03Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","isError":true,"details":{"exitCode":1},"content":"FAIL"}}`,
				}, "\n") + "\n"
				return ParsePiFile(writeFile(t, p, body))
			},
		},
		{
			name: "gemini",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				p := filepath.Join(t.TempDir(), "session-1.json")
				body := `{"sessionId":"gem1","startTime":"2026-07-01T10:00:00Z","lastUpdated":"2026-07-01T10:00:05Z","messages":[{"type":"user","timestamp":"2026-07-01T10:00:00Z","content":[{"text":"test"}]},{"type":"gemini","timestamp":"2026-07-01T10:00:01Z","model":"gemini-2.5-pro","tokens":{"input":100,"output":25},"content":"checking","toolCalls":[{"id":"c1","name":"run_shell_command","args":{"command":"go test ./..."},"status":"error","result":[{"functionResponse":{"id":"c1","response":{"output":"Command: go test ./...\nDirectory: (root)\nOutput: FAIL\nError: exit status 1\nExit Code: 1\nSignal: (none)\nBackground PIDs: (none)\nProcess Group PGID: 100","error":"exit status 1"}}}]}]}]}`
				return ParseGeminiFile(writeFile(t, p, body))
			},
		},
		{
			name: "qwen",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				p := filepath.Join(t.TempDir(), "qw1.jsonl")
				body := strings.Join([]string{
					`{"type":"user","sessionId":"qw1","cwd":"/tmp/proj","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","parts":[{"text":"test"}]}}`,
					`{"type":"assistant","sessionId":"qw1","cwd":"/tmp/proj","timestamp":"2026-07-01T10:00:01Z","model":"qwen3-coder-plus","usageMetadata":{"promptTokenCount":140,"candidatesTokenCount":50},"message":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"run_shell_command","args":{"command":"go test ./..."}}}]}}`,
					`{"type":"user","sessionId":"qw1","cwd":"/tmp/proj","timestamp":"2026-07-01T10:00:02Z","message":{"role":"user","parts":[{"functionResponse":{"id":"c1","name":"run_shell_command","response":{"output":"Command: go test ./...\nDirectory: (root)\nOutput: FAIL\nError: exit status 1\nExit Code: 1\nSignal: (none)\nBackground PIDs: (none)\nProcess Group PGID: 101","error":"exit status 1"}}}]}}`,
				}, "\n") + "\n"
				return ParseQwenFile(writeFile(t, p, body))
			},
		},
		{
			name: "copilot",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				p := filepath.Join(t.TempDir(), "events.jsonl")
				body := strings.Join([]string{
					`{"type":"session.start","timestamp":"2026-07-01T10:00:00Z","data":{"sessionId":"cp1","selectedModel":"gpt-5","context":{"cwd":"/tmp/proj"}}}`,
					`{"type":"user.message","timestamp":"2026-07-01T10:00:01Z","data":{"content":"test"}}`,
					`{"type":"assistant.message","timestamp":"2026-07-01T10:00:02Z","data":{"content":"running","model":"gpt-5","usage":{"inputTokens":80,"outputTokens":20}}}`,
					`{"type":"tool.execution_start","timestamp":"2026-07-01T10:00:03Z","data":{"toolCallId":"c1","toolName":"bash","arguments":{"command":"go test ./..."}}}`,
					`{"type":"tool.execution_complete","timestamp":"2026-07-01T10:00:04Z","data":{"toolCallId":"c1","success":false,"result":{"content":"FAIL\n<shellId: 0 completed with exit code 1>"}}}`,
				}, "\n") + "\n"
				return ParseCopilotFile(writeFile(t, p, body))
			},
		},
		{
			name: "clawcode",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				p := filepath.Join(t.TempDir(), "cc1.jsonl")
				body := strings.Join([]string{
					`{"type":"session_meta","session_id":"cc1","cwd":"/tmp/proj","created_at_ms":1782900000000}`,
					`{"type":"message","message":{"role":"user","content":"test"}}`,
					`{"type":"message","model":"claude-sonnet-4-5","input_tokens":70,"output_tokens":25,"message":{"role":"assistant","blocks":[{"type":"text","text":"running"},{"type":"tool_use","id":"c1","name":"bash","input":{"command":"go test ./..."}}]}}`,
					`{"type":"message","message":{"role":"tool","blocks":[{"type":"tool_result","tool_use_id":"c1","is_error":true,"exit_code":1,"content":"FAIL"}]}}`,
				}, "\n") + "\n"
				return ParseClawCodeFile(writeFile(t, p, body))
			},
		},
		{
			name: "crush",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				sql := "insert into sessions values ('s1',null,'t',3,150,45,0.0,1784282403,1784282400,null,null);\n" +
					crushInsert(t, "m1", "s1", "user", 1784282400, []any{map[string]any{"type": "text", "data": map[string]any{"text": "test"}}}) +
					crushInsert(t, "m2", "s1", "assistant", 1784282401, []any{map[string]any{"type": "tool_call", "data": map[string]any{"id": "c1", "name": "bash", "input": `{"command":"go test ./..."}`}}}) +
					crushInsert(t, "m3", "s1", "tool", 1784282402, []any{map[string]any{"type": "tool_result", "data": map[string]any{"tool_call_id": "c1", "name": "bash", "content": "FAIL\nExit code 1", "is_error": true}}})
				return ParseCrushDB(crushStore(t, "proj", sql))
			},
		},
		{
			name: "zed",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				if !ZstdAvailable() || !SQLite3Available() {
					t.Skip("zstd or sqlite3 not installed")
				}
				zedHome(t)
				body := `{"version":"0.3.0","title":"t","updated_at":"2026-07-19T09:00:02Z","model":{"provider":"anthropic","model":"claude-sonnet-4-5"},"cumulative_token_usage":{"input_tokens":130,"output_tokens":40},"messages":[{"User":{"id":"u1","content":[{"Text":"test"}]}},{"Agent":{"content":[{"ToolUse":{"id":"c1","name":"terminal","input":{"command":"go test ./..."}}}],"tool_results":{"c1":{"tool_use_id":"c1","is_error":true,"content":{"Text":"Command \"go test ./...\" failed with exit code 1.\n\nFAIL"}}}}}]}`
				hex := zedZstdHex(t, body)
				sql := fmt.Sprintf("%s\ninsert into threads (id, summary, updated_at, data_type, data, folder_paths, created_at) values ('z1', 't', '2026-07-19T09:00:02Z', 'zstd', x'%s', '[\"/tmp/proj\"]', '2026-07-19T08:00:00Z');", zedSchema, hex)
				return ParseZedDB(zedTestDB(t, sql))
			},
		},
		{
			name: "kimi",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				p := filepath.Join(t.TempDir(), "sess1", "wire.jsonl")
				body := strings.Join([]string{
					`{"type":"context.append_message","time":"2026-07-01T10:00:00Z","message":{"role":"user","content":"test"}}`,
					`{"type":"context.append_loop_event","time":"2026-07-01T10:00:01Z","event":{"type":"step.begin","model":"kimi-k2","provider":"moonshot","usage":{"input_tokens":95,"output_tokens":30}}}`,
					`{"type":"context.append_loop_event","time":"2026-07-01T10:00:02Z","event":{"type":"tool.call","toolCallId":"c1","name":"Bash","args":{"command":"go test ./..."}}}`,
					`{"type":"context.append_loop_event","time":"2026-07-01T10:00:03Z","event":{"type":"tool.result","toolCallId":"c1","result":{"output":"FAIL","isError":true,"exitCode":1}}}`,
				}, "\n") + "\n"
				return ParseKimiFile(writeFile(t, p, body))
			},
		},
		{
			name: "kiro_cli",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				p := filepath.Join(t.TempDir(), "kiro1.jsonl")
				body := strings.Join([]string{
					`{"kind":"Prompt","data":{"message_id":"m1","meta":{"timestamp":"2026-07-01T10:00:00Z"},"content":[{"kind":"text","data":"test"}]}}`,
					`{"kind":"AssistantMessage","data":{"message_id":"m2","model":"claude-sonnet-4-5","usage":{"input_tokens":85,"output_tokens":20},"content":[{"kind":"text","data":"running"},{"kind":"toolUse","data":{"toolUseId":"c1","name":"execute_bash","input":{"command":"go test ./..."}}}]}}`,
					`{"kind":"ToolResults","data":{"content":[{"kind":"toolResult","data":{"toolUseId":"c1","status":"error","content":[{"kind":"json","data":{"exit_status":"1","stdout":"FAIL"}}]}}]}}`,
				}, "\n") + "\n"
				return ParseKiroCLIFile(writeFile(t, p, body))
			},
		},
		{
			name: "ante",
			run: func(t *testing.T) ([]model.Session, error) {
				t.Helper()
				dir := filepath.Join(t.TempDir(), "ses_01TELEM")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(dir, "meta.json"), `{"id":"ses_01TELEM","provider":"anthropic","model":"claude-sonnet-4-6","dir":"/Users/alex/payments-api","started_time":"2026-08-14T10:00:00Z","last_active_time":"2026-08-14T10:01:00Z"}`)
				body := strings.Join([]string{
					`{"timestamp":"2026-08-14T10:00:00Z","id":"e1","event":{"UserInput":"test"}}`,
					`{"timestamp":"2026-08-14T10:00:05Z","id":"e2","event":{"ToolStart":{"id":"c1","name":"Bash","args":{"command":"go test ./..."}}}}`,
					`{"timestamp":"2026-08-14T10:00:10Z","id":"e3","event":{"ToolEnd":{"tool_use_id":"c1","tool_name":"Bash","status":"Failed","result_json":{"Completed":{"exit_code":1,"stdout":"FAIL"}}}}}`,
					`{"timestamp":"2026-08-14T10:00:15Z","id":"e4","event":{"UsageUpdate":{"usage":{"input_tokens":85,"output_tokens":20}}}}`,
				}, "\n") + "\n"
				return ParseAnteFile(writeFile(t, filepath.Join(dir, "events.jsonl"), body))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ss, err := tc.run(t)
			assertTelemetry(t, ss, err)
		})
	}
}
