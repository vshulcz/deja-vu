package sources

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

func FuzzStreamingJSONLAndParsers(f *testing.F) {
	f.Add([]byte(
		`{"type":"user","sessionId":"fuzz-cl","cwd":"/work/app","timestamp":"2026-10-01T08:00:00Z","message":{"role":"user","content":"Fix 日本語 Привет 🦊"}}`+"\n"+
			`{"type":"assistant","sessionId":"fuzz-cl","requestId":"req_1","timestamp":"2026-10-01T08:00:01Z","message":{"id":"msg_1","role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":1200,"output_tokens":50},"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/app/a.go","old_string":"old","new_string":"new 日本語 🦊"}}]}}`+"\n",
	), uint16(80))
	f.Add([]byte(
		"\xef\xbb\xbf"+
			`{"timestamp":"2026-10-01T08:00:00Z","type":"session_meta","payload":{"id":"fuzz-cx","cwd":"/work/app","model_provider":"custom-gw"}}`+"\n"+
			`{"timestamp":"2026-10-01T08:00:01Z","type":"turn_context","payload":{"model":"gpt-5-codex"}}`+"\n"+
			`{"timestamp":"2026-10-01T08:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":100},"last_token_usage":{"input_tokens":70,"output_tokens":30}}}}`+"\n"+
			fmt.Sprintf(`{"timestamp":"2026-10-01T08:00:03Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"c1","input":%q}}`, `exec_command({cmd: "go test ./..."})`)+"\n",
	), uint16(140))
	f.Add([]byte(
		`{"step_index":0,"source":"SYSTEM","type":"EPHEMERAL_MESSAGE","created_at":"2026-10-01T07:00:00Z","content":"init"}`+"\r\n"+
			`{"step_index":1,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-01T07:00:01Z","content":"check 🦊"}`+"\r\n"+
			`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-01T07:00:02Z","content":"ok"}`+"\r\n"+
			`{"step_index":3,"source":"MODEL","type":"VIEW_FILE","created_at":"2026-10-01T07:00:03Z","content":"File Path: file:///work/app/a.go"}`+"\r\n",
	), uint16(120))
	f.Add([]byte(
		fmt.Sprintf(`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-01T07:00:00Z","content":%q}`, strings.Repeat("a", 5000))+"\n"+
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","content":"torn fragment without newline`,
	), uint16(200))

	f.Fuzz(func(t *testing.T, raw []byte, cut uint16) {
		if len(raw) > 128<<10 {
			return
		}
		cappedSpan := capEditSpan(string(raw))
		if len(cappedSpan) > editSpanMax {
			t.Fatalf("capEditSpan exceeded editSpanMax: %d > %d", len(cappedSpan), editSpanMax)
		}
		if !utf8.ValidString(cappedSpan) {
			t.Fatalf("capEditSpan returned invalid UTF-8")
		}
		cappedMsg := capParsedMessage(string(raw))
		if utf8.Valid(raw) && !utf8.ValidString(cappedMsg) {
			t.Fatalf("capParsedMessage broke UTF-8 validity on valid UTF-8 input")
		}

		tmp := t.TempDir()
		t.Setenv("DEJA_ANTIGRAVITY_ROOT", tmp)

		clPath := filepath.Join(tmp, "proj", "fuzz.jsonl")
		cxPath := filepath.Join(tmp, "sessions", "rollout-2026-10-01T00-00-00-fuzz.jsonl")
		agPath := filepath.Join(tmp, "brain", "fuzz-id", ".system_generated", "logs", "transcript.jsonl")
		for _, p := range []string{clPath, cxPath, agPath} {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}

		off := int64(int(cut) % (len(raw) + 1))

		checkSessions := func(ctx string, fn func() ([]model.Session, error)) {
			ss, _ := fn()
			for i, s := range ss {
				assertValidSessionUTF8(t, fmt.Sprintf("%s[%d]", ctx, i), s)
			}
		}

		checkSessions("claude-full", func() ([]model.Session, error) { return ParseClaudeFile(clPath) })
		checkSessions("claude-off", func() ([]model.Session, error) { return ParseClaudeFileFromOffset(clPath, off) })
		checkSessions("codex-full", func() ([]model.Session, error) { return ParseCodexRollout(cxPath) })
		checkSessions("codex-off", func() ([]model.Session, error) { return ParseCodexRolloutFromOffset(cxPath, off) })
		_ = codexResumes(cxPath, off)
		checkSessions("ag-full", func() ([]model.Session, error) { return ParseAntigravityFile(agPath) })
		checkSessions("ag-off", func() ([]model.Session, error) { return ParseAntigravityFileFromOffset(agPath, off) })
		_ = antigravityResumes(agPath, off)
	})
}
