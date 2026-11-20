package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Codex VS Code wraps every real input in an IDE-context envelope
// ('# Context from my IDE setup:' … '## My request:'). Only the request body
// is the person's words; the envelope itself must never become the prompt.
// The shapes below are the ones measured on a real store of 19 rollouts /
// 716 wrapped messages (Nwflower/dsh-chat-import#114).

func codexParseLines(t *testing.T, lines []string) model.Session {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "rollout-2026-10-19T10-29-00-abc.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("ParseCodexRollout: err=%v len=%d", err, len(ss))
	}
	return ss[0]
}

func codexCollectUserTexts(s model.Session) []string {
	var out []string
	for _, m := range s.Messages {
		if m.Role == "user" {
			out = append(out, m.Text)
		}
	}
	return out
}

func TestCodexIdeSetupRequestKept(t *testing.T) {
	s := codexParseLines(t, []string{
		`{"timestamp":"2026-10-19T10:29:00.000Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-19T10:29:00.200Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Context from my IDE setup:\n\n## Active file: src/main.cpp\n## Open tabs:\n- a.cpp: src/a.cpp\n\n## My request:\n先看一下入口函数\n"}]}}`,
		`{"timestamp":"2026-10-19T10:29:01.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"入口在 src/main.cpp。"}]}}`,
		`{"timestamp":"2026-10-19T10:31:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Context from my IDE setup:\n\n## Open tabs:\n- a.cpp: src/a.cpp\n\n## My request:\n没有活动文件时的包装\n"}]}}`,
	})
	got := codexCollectUserTexts(s)
	want := []string{"先看一下入口函数", "没有活动文件时的包装"}
	if len(got) != len(want) {
		t.Fatalf("user texts = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("user texts = %#v, want %#v", got, want)
		}
	}
	var gotAssistant bool
	for _, m := range s.Messages {
		if m.Role == "assistant" && m.Text == "入口在 src/main.cpp。" {
			gotAssistant = true
		}
	}
	if !gotAssistant {
		t.Fatalf("assistant message lost: %#v", s.Messages)
	}
}

func TestCodexIdeSetupContextOnlyDropped(t *testing.T) {
	s := codexParseLines(t, []string{
		`{"timestamp":"2026-10-19T10:29:00.000Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-19T10:29:00.200Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Context from my IDE setup:\n\n## Open tabs:\n- a.cpp: src/a.cpp"}]}}`,
		`{"timestamp":"2026-10-19T10:29:01.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"收到。"}]}}`,
	})
	if got := codexCollectUserTexts(s); len(got) != 0 {
		t.Fatalf("context-only wrapper stored as user text: %#v", got)
	}
	var gotAssistant bool
	for _, m := range s.Messages {
		if m.Role == "assistant" && m.Text == "收到。" {
			gotAssistant = true
		}
	}
	if !gotAssistant {
		t.Fatalf("assistant message lost: %#v", s.Messages)
	}
}

func TestCodexIdeSetupVariants(t *testing.T) {
	const meta = `{"timestamp":"2026-10-19T10:29:00.000Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`
	cases := []struct{ name, line, want string }{
		{
			// CRLF rollout: the marker line ends '\r\n' and the body keeps it.
			name: "crlf",
			line: `{"timestamp":"2026-10-19T10:32:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Context from my IDE setup:\r\n\r\n## Active file: src/b.cpp\r\n## Open tabs:\r\n- b.cpp: src/b.cpp\r\n\r\n## My request:\r\nCRLF 换行也要认\r\n"}]}}`,
			want: "CRLF 换行也要认",
		},
		{
			// A pasted PR template: the body carries its own '##' headings and
			// only the line-start '## My request:' marker may split it.
			name: "template body",
			line: `{"timestamp":"2026-10-19T10:33:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Context from my IDE setup:\n\n## Active file: .github/pull_request_template.md\n## Open tabs:\n- pr.md: .github/pull_request_template.md\n\n## My request:\n帮我把这段填一下\n\n## Description\n某个改动\n\n### Validation\n跑过测试\n"}]}}`,
			want: "帮我把这段填一下\n\n## Description\n某个改动\n\n### Validation\n跑过测试",
		},
		{
			// The prefix match is case-insensitive; the marker stays line-start.
			name: "uppercase prefix",
			line: `{"timestamp":"2026-10-19T10:34:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# CONTEXT FROM MY IDE SETUP:\n## My request:\n大小写不敏感"}]}}`,
			want: "大小写不敏感",
		},
		{
			name: "my request for codex",
			line: `{"timestamp":"2026-10-19T10:34:30.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Context from my IDE setup:\n\n## Active file: src/main.go\n\n## My request for Codex:\ncheck the handler\n"}]}}`,
			want: "check the handler",
		},
		{
			// Plain text passes through untouched.
			name: "plain text",
			line: `{"timestamp":"2026-10-19T10:35:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"普通提问"}]}}`,
			want: "普通提问",
		},
		{
			// An envelope that does not open the text is ordinary content.
			name: "mid-text prefix",
			line: `{"timestamp":"2026-10-19T10:36:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"先看这个：# Context from my IDE setup:\n## My request:\n不在开头"}]}}`,
			want: "先看这个：# Context from my IDE setup:\n## My request:\n不在开头",
		},
	}
	for _, tc := range cases {
		s := codexParseLines(t, []string{meta, tc.line})
		got := codexCollectUserTexts(s)
		if len(got) != 1 || got[0] != tc.want {
			t.Fatalf("%s: user texts = %#v, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCodexIdeSetupMixedBlocks(t *testing.T) {
	// An annotation block beside a wrapped request: blocks unwrap one by one,
	// so both survive. Joined first, the annotation header would hide the
	// envelope from the prefix check and the whole wrapper would be stored.
	s := codexParseLines(t, []string{
		`{"timestamp":"2026-10-19T10:29:00.000Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-19T10:29:00.200Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Response annotations:\nEach item contains text selected from an earlier Codex response.\n<response-annotations>[{\"text\":\"这段要改\"}]</response-annotations>"},{"type":"input_text","text":"# Context from my IDE setup:\n\n## My request:\n按批注改"}]}}`,
	})
	got := codexCollectUserTexts(s)
	want := "# Response annotations:\nEach item contains text selected from an earlier Codex response.\n<response-annotations>[{\"text\":\"这段要改\"}]</response-annotations>\n按批注改"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("user texts = %#v, want %q", got, want)
	}
}

// Codex Desktop's imported external-agent sessions flatten the foreign tool
// calls into [external_agent_tool_call: …] / [external_agent_tool_result…]
// text envelopes inside assistant output_text. The parser restores them into
// deja's tool shapes; shapes below mirror the measured store in the reference
// converter (45 rollouts / 18,036 assistant messages, Nwflower/dsh-chat-import).

func codexAssistantTexts(s model.Session) []string {
	var out []string
	for _, m := range s.Messages {
		if m.Role == "assistant" {
			out = append(out, m.Text)
		}
	}
	return out
}

func codexMessagesByRole(s model.Session, role string) []string {
	var out []string
	for _, m := range s.Messages {
		if m.Role == role {
			out = append(out, m.Text)
		}
	}
	return out
}

func TestCodexExternalAgentCallAndResult(t *testing.T) {
	s := codexParseLines(t, []string{
		`{"timestamp":"2026-10-19T10:29:00.000Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-19T10:29:01.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"[external_agent_tool_call: Bash]\ncommand: go test ./...\n[/external_agent_tool_call]\n[external_agent_tool_result]\nok ./...\n[/external_agent_tool_result]"}]}}`,
	})
	if s.ToolCalls["Bash"] != 1 {
		t.Fatalf("Bash tool call not counted: %#v", s.ToolCalls)
	}
	cmds := codexMessagesByRole(s, RoleCommand)
	if len(cmds) != 1 || cmds[0] != "$ go test ./..." {
		t.Fatalf("commands = %#v, want one shell line", cmds)
	}
	outs := codexMessagesByRole(s, RoleToolOutput)
	if len(outs) != 1 || outs[0] != "ok ./..." {
		t.Fatalf("tool outputs = %#v, want one result", outs)
	}
	if got := codexAssistantTexts(s); len(got) != 0 {
		t.Fatalf("assistant prose leaked: %#v", got)
	}
	if s.ToolErrors != 0 {
		t.Fatalf("ToolErrors = %d, want 0", s.ToolErrors)
	}
}

func TestCodexExternalAgentMixedProseAndOrder(t *testing.T) {
	// 776 of the measured blocks mix envelopes with prose, and 102 carry
	// several: prose stays in place, calls and results keep their order.
	s := codexParseLines(t, []string{
		`{"timestamp":"2026-10-19T10:29:00.000Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-19T10:29:01.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"先看一下\n[external_agent_tool_call: Read]\nfile: src/main.go\n[/external_agent_tool_call]\n中间的话\n[external_agent_tool_call: Bash]\ncommand: go vet ./...\n[/external_agent_tool_call]\n[external_agent_tool_result]\nmain.go\n[/external_agent_tool_result]\n[external_agent_tool_result]\npackage main\n[/external_agent_tool_result]"}]}}`,
	})
	// FIFO: the first result (main.go) belongs to the Read, the second (the
	// vet output) to the Bash — results keep their arrival order.
	outs := codexMessagesByRole(s, RoleToolOutput)
	if len(outs) != 2 || outs[0] != "main.go" || outs[1] != "package main" {
		t.Fatalf("tool outputs = %#v", outs)
	}
	cmds := codexMessagesByRole(s, RoleCommand)
	if len(cmds) != 1 || cmds[0] != "$ go vet ./..." {
		t.Fatalf("commands = %#v, want the Bash call only", cmds)
	}
	prose := codexAssistantTexts(s)
	if len(prose) != 2 || prose[0] != "先看一下" || prose[1] != "中间的话" {
		t.Fatalf("assistant prose = %#v", prose)
	}
	if s.ToolCalls["Read"] != 1 || s.ToolCalls["Bash"] != 1 {
		t.Fatalf("tool calls counted wrong: %#v", s.ToolCalls)
	}
}

func TestCodexExternalAgentInputJSONAndError(t *testing.T) {
	// `input: {…}` payload copied verbatim from the foreign tool_use, and an
	// `error` result marker bumping ToolErrors (the only marker in the store).
	s := codexParseLines(t, []string{
		`{"timestamp":"2026-10-19T10:29:00.000Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-19T10:29:01.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"[external_agent_tool_call: Bash]\ninput: {\"command\":\"make test\"}\n[/external_agent_tool_call]\n[external_agent_tool_result: error]\nbuild failed\n[/external_agent_tool_result]"}]}}`,
	})
	cmds := codexMessagesByRole(s, RoleCommand)
	if len(cmds) != 1 || cmds[0] != "$ make test" {
		t.Fatalf("commands = %#v, want the input JSON command", cmds)
	}
	if s.ToolErrors != 1 {
		t.Fatalf("ToolErrors = %d, want 1 from the error marker", s.ToolErrors)
	}
	outs := codexMessagesByRole(s, RoleToolOutput)
	if len(outs) != 1 || outs[0] != "build failed" {
		t.Fatalf("tool outputs = %#v", outs)
	}
}

func TestCodexExternalAgentUnclosedAndOrphan(t *testing.T) {
	// An unclosed envelope stays in the prose; a result with no pending call
	// keeps its raw envelope as assistant text. Nothing is dropped silently.
	s := codexParseLines(t, []string{
		`{"timestamp":"2026-10-19T10:29:00.000Z","type":"session_meta","payload":{"session_id":"abc","cwd":"/w/app"}}`,
		`{"timestamp":"2026-10-19T10:29:01.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"[external_agent_tool_call: Edit]\nfile: a.go\n[external_agent_tool_result]\n没配对的结果\n[/external_agent_tool_result]"}]}}`,
	})
	prose := codexAssistantTexts(s)
	if len(prose) == 0 {
		t.Fatalf("unclosed envelope lost from prose: %#v", s.Messages)
	}
	joined := strings.Join(prose, "\n---\n")
	if !strings.Contains(joined, "[external_agent_tool_result]") || !strings.Contains(joined, "没配对的结果") {
		t.Fatalf("orphan result envelope not kept: %#v", prose)
	}
	if _, counted := s.ToolCalls["Edit"]; counted {
		t.Fatalf("unclosed call counted as a tool call: %#v", s.ToolCalls)
	}
}
