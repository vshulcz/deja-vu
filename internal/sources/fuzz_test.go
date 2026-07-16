package sources

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// fuzzEnv points every source root at a throwaway temp dir so parsers that
// resolve project names against the filesystem (claude/cursor encoded dirs,
// grok cwd markers, gemini registry) never touch the real home directory.
func fuzzEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(root, "gemini"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(root, "grok"))
	t.Setenv("GROK_HOME", filepath.Join(root, "grok"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(root, "antigravity"))
	return root
}

// checkSessions asserts the invariants every parser must uphold: it only
// returns sessions with at least one message and a non-empty ID (each format
// derives an ID from the filename or transcript, never leaving it blank).
func checkSessions(t *testing.T, ss []model.Session) {
	t.Helper()
	for i := range ss {
		if ss[i].Messages == nil {
			t.Fatalf("session %d has nil Messages: %#v", i, ss[i])
		}
		if ss[i].ID == "" {
			t.Fatalf("session %d has empty ID: %#v", i, ss[i])
		}
	}
}

func writeFuzzFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const claudeSeed = `{"type":"user","sessionId":"claude-abc","cwd":"/tmp/demo","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"Find frobnicator bug"}}
{"type":"assistant","sessionId":"claude-abc","cwd":"/tmp/demo","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"in parser.go"}]}}
`

func FuzzParseClaudeFile(f *testing.F) {
	f.Add([]byte(claudeSeed))
	f.Add([]byte(`{"type":"user","sessionId":"c","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hi"}}` + "\n"))
	f.Add([]byte(`{"type":"summary","summary":"x"}` + "\n"))
	f.Add([]byte(claudeSeed[:len(claudeSeed)/2])) // torn tail
	f.Add([]byte("{\x00not json\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzEnv(t)
		p := writeFuzzFile(t, t.TempDir(), "s.jsonl", data)
		ss, _ := ParseClaudeFile(p)
		checkSessions(t, ss)
	})
}

const codexRolloutSeed = `{"timestamp":"2026-01-02T03:04:05Z","type":"session_meta","payload":{"session_id":"codex-abc","cwd":"/tmp/codemo"}}
{"timestamp":"2026-01-02T03:04:06Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello codex"}]}}
{"timestamp":"2026-01-02T03:04:07Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixed"}]}}
`

func FuzzParseCodexRollout(f *testing.F) {
	f.Add([]byte(codexRolloutSeed))
	f.Add([]byte(`{"timestamp":"2026-01-02T03:04:06Z","payload":{"role":"assistant","content":"x"}}` + "\n"))
	f.Add([]byte(`{"payload":{"message":"bare user text"}}` + "\n"))
	f.Add([]byte(codexRolloutSeed[:len(codexRolloutSeed)/2]))
	f.Add([]byte(`{"payload":null}` + "\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzEnv(t)
		p := writeFuzzFile(t, t.TempDir(), "rollout-fuzz.jsonl", data)
		ss, _ := ParseCodexRollout(p)
		checkSessions(t, ss)
	})
}

func FuzzParseCodexHistory(f *testing.F) {
	f.Add([]byte(`{"session_id":"hist-abc","ts":1767323045,"text":"history needle"}` + "\n"))
	f.Add([]byte(`{"session_id":"","text":"no id"}` + "\n"))
	f.Add([]byte(`{"session_id":"h","text":""}` + "\n"))
	f.Add([]byte(`{"session_id":"h","ts":"2026-01-02T03:04:05Z","text":"a"}` + "\n{broken"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzEnv(t)
		p := writeFuzzFile(t, t.TempDir(), "history.jsonl", data)
		ss, _ := ParseCodexHistory(p)
		checkSessions(t, ss)
	})
}

const geminiJSONSeed = `{"sessionId":"session-old-1","startTime":"2026-01-16T18:34:00.000Z","lastUpdated":"2026-01-16T18:35:00.000Z","messages":[{"id":"m1","timestamp":"2026-01-16T18:34:01.000Z","type":"user","content":"Hello"},{"id":"m2","timestamp":"2026-01-16T18:34:02.000Z","type":"gemini","content":"Hi!"}]}`

const geminiJSONLSeed = `{"sessionId":"sess-new-1","startTime":"2026-07-01T10:00:00.000Z","lastUpdated":"2026-07-01T10:00:00.000Z","kind":"main"}
{"id":"u1","timestamp":"2026-07-01T10:00:01.000Z","type":"user","content":[{"text":"first question"}]}
{"id":"a1","timestamp":"2026-07-01T10:00:02.000Z","type":"gemini","content":"wrong answer"}
{"$rewindTo":"a1"}
{"id":"a2","timestamp":"2026-07-01T10:00:05.000Z","type":"gemini","content":"right answer"}
{"$set":{"lastUpdated":"2026-07-01T10:00:06.000Z"}}
`

// ParseGeminiFile dispatches on extension, so each input is parsed as both a
// whole-session .json and a streaming .jsonl.
func FuzzParseGeminiFile(f *testing.F) {
	f.Add([]byte(geminiJSONSeed))
	f.Add([]byte(geminiJSONLSeed))
	f.Add([]byte(`{"$rewindTo":"missing"}` + "\n"))
	f.Add([]byte(geminiJSONLSeed[:len(geminiJSONLSeed)/2]))
	f.Add([]byte(`{"sessionId":"s","messages":[{"type":"user","content":[{"text":"x"}]}]}`))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzEnv(t)
		chats := filepath.Join(t.TempDir(), "tmp", "proj", "chats")
		for _, ext := range []string{".json", ".jsonl"} {
			p := writeFuzzFile(t, chats, "session-fuzz"+ext, data)
			ss, _ := ParseGeminiFile(p)
			checkSessions(t, ss)
		}
	})
}

const aiderSeed = "# aider chat started at 2026-07-16 10:32:01\n\n> aider v0.86.1\n\n#### fix the off-by-one\n#### keep tests green\n\nI'll fix the loop:\n\n```go\nfor i := 0; i < n; i++ {\n#### not a user line\n```\n\n> Applied edit\n\n# aider chat started at 2026-07-16 11:00:00\n\n#### second question\n\nSecond answer.\n"

func FuzzParseAiderFile(f *testing.F) {
	f.Add([]byte(aiderSeed))
	f.Add([]byte("# aider chat started at 2026-01-01 00:00:00\n#### q\nans\n"))
	f.Add([]byte("#### orphan user line with no session header\n"))
	f.Add([]byte("# aider chat started at not-a-timestamp\n#### q\n```\nunclosed fence"))
	f.Add([]byte(aiderSeed[:len(aiderSeed)/2]))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzEnv(t)
		p := writeFuzzFile(t, t.TempDir(), ".aider.chat.history.md", data)
		ss, _ := ParseAiderFile(p)
		checkSessions(t, ss)
	})
}

const antigravitySeed = `{"step_index":1,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nBuild this\n</USER_REQUEST>"}
{"step_index":2,"source":"SYSTEM","type":"SYSTEM","created_at":"2026-07-08T14:18:28Z","content":"noise"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-08T14:18:29Z","content":"I can help."}
`

func FuzzParseAntigravityFile(f *testing.F) {
	f.Add([]byte(antigravitySeed))
	f.Add([]byte(`{"source":"USER_EXPLICIT","created_at":"2026-07-08T14:18:27Z","content":"hi"}` + "\n"))
	f.Add([]byte(`{"source":"MODEL","content":"<USER_REQUEST></USER_REQUEST>"}` + "\n"))
	f.Add([]byte(antigravitySeed[:len(antigravitySeed)/2]))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		root := fuzzEnv(t)
		dir := filepath.Join(root, "antigravity", "brain", "traj-1", ".system_generated", "logs")
		p := writeFuzzFile(t, dir, "transcript.jsonl", data)
		ss, _ := ParseAntigravityFile(p)
		checkSessions(t, ss)
	})
}

const grokSeed = `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"find grokneedle"},"_meta":{"promptIndex":0}}}}
{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"first "}},"_meta":{"promptId":"p1"}}}
{"timestamp":1782900005,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer"}},"_meta":{"promptId":"p1"}}}
`

func FuzzParseGrokFile(f *testing.F) {
	f.Add([]byte(grokSeed))
	f.Add([]byte(`{"timestamp":1,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"x"}}}}` + "\n"))
	f.Add([]byte(`{"params":{"update":{"sessionUpdate":"user_message_chunk","content":"bare string"}}}` + "\n"))
	f.Add([]byte(grokSeed + "{malformed\n"))
	f.Add([]byte(grokSeed[:len(grokSeed)/2]))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		root := fuzzEnv(t)
		group := url.PathEscape("/work/cool-app")
		dir := filepath.Join(root, "grok", "sessions", group, "sess-1")
		summary := `{"info":{"id":"sess-1","cwd":"/work/cool-app"},"generated_title":"t","created_at":"2026-07-01T10:00:00Z","updated_at":"2026-07-01T10:00:06Z"}`
		writeFuzzFile(t, dir, "summary.json", []byte(summary))
		p := writeFuzzFile(t, dir, "updates.jsonl", data)
		ss, _ := ParseGrokFile(p)
		checkSessions(t, ss)
	})
}

const cursorTranscriptSeed = `{"role":"user","message":{"content":[{"type":"text","text":"transcriptneedle question"}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"Running ls."},{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}
{"type":"turn_ended","status":"success"}
`

func FuzzParseCursorTranscript(f *testing.F) {
	f.Add([]byte(cursorTranscriptSeed))
	f.Add([]byte(`{"role":"user","message":{"content":"hi"}}` + "\n"))
	f.Add([]byte(`{"role":"assistant","message":null}` + "\n"))
	f.Add([]byte(cursorTranscriptSeed[:len(cursorTranscriptSeed)/2]))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzEnv(t)
		dir := filepath.Join(t.TempDir(), "projects", "Users-x-app", "agent-transcripts", "sess-1")
		p := writeFuzzFile(t, dir, "sess-1.jsonl", data)
		ss, _ := ParseCursorTranscript(p)
		checkSessions(t, ss)
	})
}
